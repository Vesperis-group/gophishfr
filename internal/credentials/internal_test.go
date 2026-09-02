package credentials

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// failingReader always fails to read. It exists only to exercise the
// entropy-failure path (ErrEntropyUnavailable) in-process, without adding
// any exported way to swap the production random source.
type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) {
	return 0, errors.New("synthetic test entropy failure")
}

func fixedTestKey() []byte {
	k := make([]byte, aeadKeySize)
	for i := range k {
		k[i] = byte(0x5A ^ i)
	}
	return k
}

// TestEncryptEntropyFailure overrides the package-private randReader for the
// duration of this test only, then restores it. Because this mutates
// package-level state, this test must never run with t.Parallel(), and by
// convention no other test in this package does either — go test runs
// non-parallel tests within a package sequentially, so this is safe under
// both `go test` and `go test -race`.
func TestEncryptEntropyFailure(t *testing.T) {
	original := randReader
	defer func() { randReader = original }()
	randReader = failingReader{}

	kr, err := NewKeyring("entropy-test", map[string][]byte{"entropy-test": fixedTestKey()})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	c, err := New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.Encrypt(Context{Kind: "entropy-test"}, []byte("payload"))
	if err == nil {
		t.Fatal("expected an error when the entropy source fails")
	}
	if !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("expected ErrEntropyUnavailable, got %v", err)
	}
}

// TestEncryptEntropyFailureDoesNotLeakKeyMaterial confirms that the error
// path above does not turn a random-source failure into a message that
// includes key bytes.
func TestEncryptEntropyFailureDoesNotLeakKeyMaterial(t *testing.T) {
	original := randReader
	defer func() { randReader = original }()
	randReader = failingReader{}

	key := fixedTestKey()
	kr, err := NewKeyring("entropy-test", map[string][]byte{"entropy-test": key})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	c, err := New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.Encrypt(Context{Kind: "entropy-test"}, []byte("payload"))
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for i := 0; i+4 <= len(key); i++ {
		if strings.Contains(msg, string(key[i:i+4])) {
			t.Fatalf("error message appears to contain key material: %v", err)
		}
	}
}

// TestDecodeCanonicalBase64RejectsNonCanonicalForms exercises
// decodeCanonicalBase64 directly. base64.StdEncoding.Strict() alone accepts
// an embedded or trailing CR/LF (its own documentation says exactly that:
// "the input is still malleable, as new line characters (CR and LF) are
// still ignored"), which would let two textually different envelope/keyring
// strings decode to the same bytes. decodeCanonicalBase64 must reject every
// such non-canonical representation and accept only the one true canonical
// encoding of any given byte string.
func TestDecodeCanonicalBase64RejectsNonCanonicalForms(t *testing.T) {
	canonical := base64.StdEncoding.EncodeToString([]byte("a canonical base64 payload"))
	if _, err := decodeCanonicalBase64(canonical); err != nil {
		t.Fatalf("canonical input unexpectedly rejected: %v", err)
	}

	cases := map[string]string{
		"trailing LF":       canonical + "\n",
		"trailing CR":       canonical + "\r",
		"trailing CRLF":     canonical + "\r\n",
		"leading LF":        "\n" + canonical,
		"leading CR":        "\r" + canonical,
		"embedded LF":       canonical[:4] + "\n" + canonical[4:],
		"embedded CR":       canonical[:4] + "\r" + canonical[4:],
		"embedded CRLF":     canonical[:4] + "\r\n" + canonical[4:],
		"doubled CRLF":      canonical[:4] + "\r\n\r\n" + canonical[4:],
		"CR before padding": canonical[:len(canonical)-1] + "\r" + canonical[len(canonical)-1:],
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			decoded, err := decodeCanonicalBase64(input)
			if err == nil {
				t.Fatalf("expected rejection of non-canonical base64 (%s): %q decoded to %x", name, input, decoded)
			}
		})
	}
}

// FuzzParseEnvelope exercises the strict envelope parser directly with
// arbitrary input. The only invariant under test is that it never panics:
// any input that is not a well-formed envelope must return an error.
func FuzzParseEnvelope(f *testing.F) {
	seeds := []string{
		"",
		"gophishfr-cred",
		"gophishfr-cred:v1",
		"gophishfr-cred:v1:test-key:AAAAAAAAAAAAAAAA:AAAAAAAAAAAAAAAAAAAAAAAAAAAA==",
		"gophishfr-cred:v2:test-key:AAAA:AAAA",
		"wrong-magic:v1:test-key:AAAA:AAAA",
		"gophishfr-cred:v1:not a valid key id:AAAA:AAAA",
		"gophishfr-cred:v1:test-key:not-base64!!:AAAA",
		"gophishfr-cred:v1:test-key:AAAA:not-base64!!",
		"not an envelope at all",
		":::::",
		"gophishfr-cred:v1:" + string(make([]byte, 200)),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseEnvelope(Envelope(s))
	})
}

// FuzzDecrypt exercises Decrypt end-to-end (parsing plus AEAD authentication)
// against a real, fixed Cipher, with arbitrary envelope strings. It must
// never panic, regardless of input.
func FuzzDecrypt(f *testing.F) {
	kr, err := NewKeyring("fuzz-active", map[string][]byte{"fuzz-active": fixedTestKey()})
	if err != nil {
		f.Fatalf("NewKeyring: %v", err)
	}
	c, err := New(kr)
	if err != nil {
		f.Fatalf("New: %v", err)
	}
	ctx := Context{Kind: "fuzz-kind"}

	valid, err := c.Encrypt(ctx, []byte("fuzz seed plaintext"))
	if err != nil {
		f.Fatalf("Encrypt: %v", err)
	}

	f.Add(string(valid))
	f.Add("")
	f.Add("gophishfr-cred:v1:fuzz-active:not-base64:not-base64")
	f.Add("gophishfr-cred:v1:unknown-key-id:AAAA:AAAA")
	f.Add(string(valid) + "trailing-garbage")

	f.Fuzz(func(t *testing.T, s string) {
		_, _ = c.Decrypt(ctx, Envelope(s))
	})
}
