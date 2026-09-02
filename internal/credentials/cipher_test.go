package credentials_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
)

func testContext() credentials.Context {
	return credentials.Context{Kind: "test-kind", Table: "test_table", Column: "test_column", OwnerID: "1", RecordID: "42"}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	kr := testKeyring(t, "active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()

	cases := map[string][]byte{
		"empty":       {},
		"ascii":       []byte("hunter2"),
		"unicode":     []byte("mot de passe: éàü 密码 🔒"),
		"binary":      {0x00, 0x01, 0x02, 0xff, 0xfe, 0x7f, 0x80},
		"large":       bytes.Repeat([]byte("A large-but-reasonable secret payload. "), 4096), // ~160 KiB
		"nul-in-body": append([]byte("before\x00after"), 0),
	}

	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			env, err := c.Encrypt(ctx, plaintext)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			got, err := c.Decrypt(ctx, env)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
			}
		})
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	kr := testKeyring(t, "active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()
	plaintext := []byte("same plaintext every time")

	env1, err := c.Encrypt(ctx, plaintext)
	if err != nil {
		t.Fatalf("Encrypt (1): %v", err)
	}
	env2, err := c.Encrypt(ctx, plaintext)
	if err != nil {
		t.Fatalf("Encrypt (2): %v", err)
	}

	if env1 == env2 {
		t.Fatalf("two encryptions of identical plaintext/context produced identical envelopes: %q", env1)
	}

	got1, err := c.Decrypt(ctx, env1)
	if err != nil {
		t.Fatalf("Decrypt(env1): %v", err)
	}
	got2, err := c.Decrypt(ctx, env2)
	if err != nil {
		t.Fatalf("Decrypt(env2): %v", err)
	}
	if !bytes.Equal(got1, plaintext) || !bytes.Equal(got2, plaintext) {
		t.Fatalf("both envelopes must still decrypt to the original plaintext")
	}
}

func TestPlaintextNotPresentInEnvelope(t *testing.T) {
	kr := testKeyring(t, "active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()

	marker := "THIS-DISTINCTIVE-MARKER-MUST-NEVER-APPEAR-CIPHERTEXT-SIDE-0x9F3A"
	plaintext := []byte(strings.Repeat(marker+" ", 64))

	env, err := c.Encrypt(ctx, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	raw := string(env)
	if strings.Contains(raw, marker) {
		t.Fatalf("marker plaintext leaked directly into serialized envelope")
	}
	if strings.Contains(strings.ToUpper(raw), strings.ToUpper(marker)) {
		t.Fatalf("marker plaintext leaked into serialized envelope (case-insensitive)")
	}

	// Also check every base64-decodable field of the envelope, in case the
	// marker survived encoding in a way a raw substring search on the
	// envelope text would miss.
	parts := strings.Split(raw, ":")
	for _, part := range parts {
		if decoded, err := base64.StdEncoding.DecodeString(part); err == nil {
			if bytes.Contains(decoded, []byte(marker)) {
				t.Fatalf("marker plaintext leaked into a base64-decoded envelope field")
			}
		}
	}

	got, err := c.Decrypt(ctx, env)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch after leak check")
	}
}

func TestDecryptTamperDetection(t *testing.T) {
	kr := testKeyring(t, "active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()

	env, err := c.Encrypt(ctx, []byte("tamper me if you can"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	fields := strings.Split(string(env), ":")
	if len(fields) != 5 {
		t.Fatalf("unexpected envelope shape: %d fields", len(fields))
	}
	magic, version, keyID, nonceB64, ciphertextB64 := fields[0], fields[1], fields[2], fields[3], fields[4]

	flipLastByte := func(b64 string) string {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		decoded[len(decoded)-1] ^= 0xFF
		return base64.StdEncoding.EncodeToString(decoded)
	}
	flipFirstByte := func(b64 string) string {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		decoded[0] ^= 0xFF
		return base64.StdEncoding.EncodeToString(decoded)
	}

	rebuild := func(magic, version, keyID, nonceB64, ciphertextB64 string) credentials.Envelope {
		return credentials.Envelope(strings.Join([]string{magic, version, keyID, nonceB64, ciphertextB64}, ":"))
	}

	tampered := map[string]credentials.Envelope{
		"nonce":      rebuild(magic, version, keyID, flipFirstByte(nonceB64), ciphertextB64),
		"ciphertext": rebuild(magic, version, keyID, nonceB64, flipFirstByte(ciphertextB64)),
		"tag":        rebuild(magic, version, keyID, nonceB64, flipLastByte(ciphertextB64)), // Seal appends the tag at the end.
		"key-id":     rebuild(magic, version, keyID+"-tampered", nonceB64, ciphertextB64),
	}

	for name, env := range tampered {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Decrypt(ctx, env); err == nil {
				t.Fatalf("tampered %s unexpectedly decrypted successfully", name)
			}
		})
	}
}

func TestDecryptUnknownKey(t *testing.T) {
	kr := testKeyring(t, "active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()

	env, err := c.Encrypt(ctx, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	fields := strings.Split(string(env), ":")
	fields[2] = "totally-unknown-key-id"
	tampered := credentials.Envelope(strings.Join(fields, ":"))

	_, err = c.Decrypt(ctx, tampered)
	if err == nil {
		t.Fatal("expected error decrypting with an unknown key id")
	}
	if !errors.Is(err, credentials.ErrUnknownKey) {
		t.Fatalf("expected ErrUnknownKey, got %v", err)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	// Two keyrings sharing a key ID but with different underlying bytes: a
	// stand-in for an operator loading the wrong keyring, or a keyring that
	// was corrupted/regenerated with the same ID pointing at different
	// material.
	krA, err := credentials.NewKeyring("shared-id", map[string][]byte{"shared-id": synthKey(0x11)})
	if err != nil {
		t.Fatalf("NewKeyring A: %v", err)
	}
	krB, err := credentials.NewKeyring("shared-id", map[string][]byte{"shared-id": synthKey(0x22)})
	if err != nil {
		t.Fatalf("NewKeyring B: %v", err)
	}

	cA, err := credentials.New(krA)
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	cB, err := credentials.New(krB)
	if err != nil {
		t.Fatalf("New B: %v", err)
	}

	ctx := testContext()
	env, err := cA.Encrypt(ctx, []byte("secret payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = cB.Decrypt(ctx, env)
	if err == nil {
		t.Fatal("expected error decrypting with the wrong key")
	}
	if !errors.Is(err, credentials.ErrAuthenticationFailed) {
		t.Fatalf("expected ErrAuthenticationFailed, got %v", err)
	}
}

func TestDecryptWrongAAD(t *testing.T) {
	kr := testKeyring(t, "active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	env, err := c.Encrypt(credentials.Context{Kind: "k1"}, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = c.Decrypt(credentials.Context{Kind: "k2"}, env)
	if err == nil {
		t.Fatal("expected error decrypting with the wrong AAD/context")
	}
	if !errors.Is(err, credentials.ErrAuthenticationFailed) {
		t.Fatalf("expected ErrAuthenticationFailed, got %v", err)
	}
}

func TestOldKeyDecryptActiveKeyEncrypt(t *testing.T) {
	oldKey := synthKey(0x33)
	activeKey := synthKey(0x44)

	// Encrypt something under an old keyring where "key-old" was active.
	oldKr, err := credentials.NewKeyring("key-old", map[string][]byte{"key-old": oldKey})
	if err != nil {
		t.Fatalf("NewKeyring old: %v", err)
	}
	oldCipher, err := credentials.New(oldKr)
	if err != nil {
		t.Fatalf("New old: %v", err)
	}
	ctx := testContext()
	oldEnv, err := oldCipher.Encrypt(ctx, []byte("encrypted under the old key"))
	if err != nil {
		t.Fatalf("Encrypt under old key: %v", err)
	}

	// Rotate: the keyring now has a new active key, but keeps the old one
	// around for decrypting envelopes that predate the rotation.
	rotatedKr, err := credentials.NewKeyring("key-new", map[string][]byte{
		"key-new": activeKey,
		"key-old": oldKey,
	})
	if err != nil {
		t.Fatalf("NewKeyring rotated: %v", err)
	}
	rotatedCipher, err := credentials.New(rotatedKr)
	if err != nil {
		t.Fatalf("New rotated: %v", err)
	}

	// The pre-rotation envelope still decrypts using the retired key.
	got, err := rotatedCipher.Decrypt(ctx, oldEnv)
	if err != nil {
		t.Fatalf("Decrypt(oldEnv) after rotation: %v", err)
	}
	if string(got) != "encrypted under the old key" {
		t.Fatalf("unexpected plaintext: %q", got)
	}

	// New encryptions use the new active key.
	newEnv, err := rotatedCipher.Encrypt(ctx, []byte("encrypted under the new key"))
	if err != nil {
		t.Fatalf("Encrypt after rotation: %v", err)
	}
	if rotatedKr.ActiveKeyID() != "key-new" {
		t.Fatalf("unexpected active key id: %q", rotatedKr.ActiveKeyID())
	}
	fields := strings.Split(string(newEnv), ":")
	if fields[2] != "key-new" {
		t.Fatalf("new envelope should carry the active key id, got %q", fields[2])
	}

	got2, err := rotatedCipher.Decrypt(ctx, newEnv)
	if err != nil {
		t.Fatalf("Decrypt(newEnv): %v", err)
	}
	if string(got2) != "encrypted under the new key" {
		t.Fatalf("unexpected plaintext: %q", got2)
	}
}

func TestDecryptMalformedUnsupportedEnvelopes(t *testing.T) {
	kr := testKeyring(t, "active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()

	valid, err := c.Encrypt(ctx, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	fields := strings.Split(string(valid), ":")

	cases := map[string]credentials.Envelope{
		"empty":                 credentials.Envelope(""),
		"not-an-envelope":       credentials.Envelope("just some random text"),
		"missing-fields":        credentials.Envelope("gophishfr-cred:v1:only-three-fields"),
		"wrong-magic":           credentials.Envelope(strings.Join(append([]string{"totally-wrong-magic"}, fields[1:]...), ":")),
		"unsupported-version":   credentials.Envelope(strings.Join(append([]string{fields[0], "v99"}, fields[2:]...), ":")),
		"bad-key-id-chars":      credentials.Envelope(strings.Join(append([]string{fields[0], fields[1], "not:a:valid:id"}, fields[3:]...), ":")),
		"bad-nonce-base64":      credentials.Envelope(strings.Join([]string{fields[0], fields[1], fields[2], "not-base64!!", fields[4]}, ":")),
		"bad-ciphertext-base64": credentials.Envelope(strings.Join([]string{fields[0], fields[1], fields[2], fields[3], "not-base64!!"}, ":")),
		"truncated":             credentials.Envelope(string(valid)[:len(valid)/2]),
	}

	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Decrypt(ctx, env); err == nil {
				t.Fatalf("expected error decrypting %s", name)
			}
		})
	}
}

func TestConcurrentEncryptDecrypt(t *testing.T) {
	kr := testKeyring(t, "active", map[string][32]byte{"retired": func() [32]byte {
		var k [32]byte
		copy(k[:], synthKey(0x55))
		return k
	}()})
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()

	const goroutines = 32
	const perGoroutine = 50

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*perGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				plaintext := []byte(strings.Repeat("x", (id+i)%37+1))
				env, err := c.Encrypt(ctx, plaintext)
				if err != nil {
					errCh <- err
					return
				}
				got, err := c.Decrypt(ctx, env)
				if err != nil {
					errCh <- err
					return
				}
				if !bytes.Equal(got, plaintext) {
					errCh <- errors.New("plaintext mismatch under concurrency")
					return
				}
				// Also exercise concurrent reads of ActiveKeyID.
				_ = kr.ActiveKeyID()
			}
		}(g)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrency failure: %v", err)
	}
}

func TestCipherRejectsNilKeyring(t *testing.T) {
	if _, err := credentials.New(nil); err == nil {
		t.Fatal("expected error constructing a Cipher with a nil keyring")
	}
}
