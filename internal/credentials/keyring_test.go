package credentials_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
)

// prettyPrintf renders v through every common fmt verb an accidental
// log/debug statement might use, so a redaction regression is caught
// regardless of which verb was used. v is `any` rather than *credentials.
// Keyring so the same helper exercises a *Keyring, a dereferenced Keyring
// value, and a nil *Keyring alike.
func prettyPrintf(v any) string {
	return fmt.Sprintf("%v %+v %#v %s", v, v, v, v)
}

// synthB64Key returns the standard-encoding base64 of a deterministic,
// obviously-synthetic 32-byte test key.
func synthB64Key(seed byte) string {
	return base64.StdEncoding.EncodeToString(synthKey(seed))
}

func TestParseKeyringJSONValid(t *testing.T) {
	doc := `{
		"version": 1,
		"active_key_id": "test-active-2026-01",
		"keys": [
			{"id": "test-active-2026-01", "key": "` + synthB64Key(0x01) + `"},
			{"id": "test-retired-2025-06", "key": "` + synthB64Key(0x02) + `"}
		]
	}`
	kr, err := credentials.ParseKeyringJSON([]byte(doc))
	if err != nil {
		t.Fatalf("ParseKeyringJSON: %v", err)
	}
	if kr.ActiveKeyID() != "test-active-2026-01" {
		t.Fatalf("unexpected active key id: %q", kr.ActiveKeyID())
	}
}

func TestParseKeyringJSONRejections(t *testing.T) {
	valid := func() string {
		return `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [
				{"id": "k1", "key": "` + synthB64Key(0x01) + `"}
			]
		}`
	}

	cases := map[string]string{
		"empty document":   "",
		"not json":         "this is not json at all",
		"trailing garbage": valid() + `{"extra":true}`,
		"unknown field": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `"}],
			"unexpected_field": "surprise"
		}`,
		"unsupported version": `{
			"version": 2,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `"}]
		}`,
		"missing version": `{
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `"}]
		}`,
		"no keys": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": []
		}`,
		"missing active key id": `{
			"version": 1,
			"active_key_id": "",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `"}]
		}`,
		"active key id absent from keys": `{
			"version": 1,
			"active_key_id": "does-not-exist",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `"}]
		}`,
		"bad base64": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "not-valid-base64!!!"}]
		}`,
		"non-canonical base64 padding": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01)[:len(synthB64Key(0x01))-1] + `A"}]
		}`,
		"key encoding trailing LF": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `\n"}]
		}`,
		"key encoding trailing CR": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `\r"}]
		}`,
		"key encoding trailing CRLF": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `\r\n"}]
		}`,
		"key encoding embedded LF": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01)[:4] + `\n` + synthB64Key(0x01)[4:] + `"}]
		}`,
		"key encoding embedded CRLF": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + synthB64Key(0x01)[:4] + `\r\n` + synthB64Key(0x01)[4:] + `"}]
		}`,
		"wrong key size (too short)": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + base64.StdEncoding.EncodeToString([]byte("too-short")) + `"}]
		}`,
		"wrong key size (too long)": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1", "key": "` + base64.StdEncoding.EncodeToString(append(synthKey(0x01), 0xFF)) + `"}]
		}`,
		"empty key id": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "", "key": "` + synthB64Key(0x01) + `"}]
		}`,
		"oversized key id": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "` + strings.Repeat("a", 65) + `", "key": "` + synthB64Key(0x01) + `"}]
		}`,
		"invalid key id characters": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [{"id": "k1:with:colons", "key": "` + synthB64Key(0x01) + `"}]
		}`,
		"duplicate key id": `{
			"version": 1,
			"active_key_id": "k1",
			"keys": [
				{"id": "k1", "key": "` + synthB64Key(0x01) + `"},
				{"id": "k1", "key": "` + synthB64Key(0x02) + `"}
			]
		}`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := credentials.ParseKeyringJSON([]byte(doc))
			if err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestParseKeyringJSONMalformedVariants(t *testing.T) {
	// A grab-bag of structurally malformed documents that must all be
	// rejected without panicking.
	cases := []string{
		`{`,
		`[]`,
		`null`,
		`"just a string"`,
		`42`,
		`{"version": "one", "active_key_id": "k1", "keys": []}`,
		`{"version": 1, "active_key_id": 5, "keys": []}`,
		`{"version": 1, "active_key_id": "k1", "keys": "not-an-array"}`,
		`{"version": 1, "active_key_id": "k1", "keys": [{"id": 5, "key": "x"}]}`,
	}
	for i, doc := range cases {
		t.Run(strings.ReplaceAll(doc, "\n", " "), func(t *testing.T) {
			if _, err := credentials.ParseKeyringJSON([]byte(doc)); err == nil {
				t.Fatalf("case %d: expected error, doc=%s", i, doc)
			}
		})
	}
}

func TestParseKeyringJSONUnsupportedVersionIsDistinguishable(t *testing.T) {
	doc := `{
		"version": 7,
		"active_key_id": "k1",
		"keys": [{"id": "k1", "key": "` + synthB64Key(0x01) + `"}]
	}`
	_, err := credentials.ParseKeyringJSON([]byte(doc))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, credentials.ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
}

func TestNewKeyringRejections(t *testing.T) {
	t.Run("no keys", func(t *testing.T) {
		if _, err := credentials.NewKeyring("k1", map[string][]byte{}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("wrong key size", func(t *testing.T) {
		if _, err := credentials.NewKeyring("k1", map[string][]byte{"k1": []byte("short")}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid key id", func(t *testing.T) {
		if _, err := credentials.NewKeyring("bad:id", map[string][]byte{"bad:id": synthKey(0x01)}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("active id missing from keys", func(t *testing.T) {
		if _, err := credentials.NewKeyring("missing", map[string][]byte{"k1": synthKey(0x01)}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("empty active id", func(t *testing.T) {
		if _, err := credentials.NewKeyring("", map[string][]byte{"k1": synthKey(0x01)}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestKeyringImmutability(t *testing.T) {
	original := synthKey(0x01)
	callerCopy := make([]byte, len(original))
	copy(callerCopy, original)

	kr, err := credentials.NewKeyring("k1", map[string][]byte{"k1": callerCopy})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	// Mutate the caller's slice after construction: it must not affect the
	// keyring, since NewKeyring must have copied it.
	for i := range callerCopy {
		callerCopy[i] = 0
	}

	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := testContext()
	env, err := c.Encrypt(ctx, []byte("still using the original key"))
	if err != nil {
		t.Fatalf("Encrypt after caller mutated its slice: %v", err)
	}
	if _, err := c.Decrypt(ctx, env); err != nil {
		t.Fatalf("Decrypt after caller mutated its slice: %v", err)
	}
}

func TestKeyringStringDoesNotExposeKeyMaterial(t *testing.T) {
	secret := synthKey(0xAB)
	kr, err := credentials.NewKeyring("k1", map[string][]byte{"k1": secret})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(secret)

	rendered := kr.String()
	if strings.Contains(rendered, b64) {
		t.Fatalf("Keyring.String() leaked key material: %s", rendered)
	}

	goRendered := kr.GoString()
	if strings.Contains(goRendered, b64) {
		t.Fatalf("Keyring.GoString() leaked key material: %s", goRendered)
	}

	printed := prettyPrintf(kr)
	if strings.Contains(printed, b64) {
		t.Fatalf("fmt printing of *Keyring leaked key material: %s", printed)
	}
}

// TestKeyringFormattingValueDoesNotExposeKeyMaterial covers the gap the
// crypto Inspector flagged: String/GoString were previously defined only on
// *Keyring, so a dereferenced Keyring value (for example fmt.Sprintf("%v",
// *kr)) did not implement fmt.Stringer at all and fell back to fmt's default
// struct dump, which prints every field, including the keys map's raw key
// bytes. String/GoString must now be implemented so that a plain Keyring
// value is exactly as redacted as a *Keyring.
func TestKeyringFormattingValueDoesNotExposeKeyMaterial(t *testing.T) {
	secret := synthKey(0xCD)
	kr, err := credentials.NewKeyring("k1", map[string][]byte{"k1": secret})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	value := *kr
	b64 := base64.StdEncoding.EncodeToString(secret)

	rendered := value.String()
	if strings.Contains(rendered, b64) {
		t.Fatalf("Keyring.String() (value receiver) leaked key material: %s", rendered)
	}
	goRendered := value.GoString()
	if strings.Contains(goRendered, b64) {
		t.Fatalf("Keyring.GoString() (value receiver) leaked key material: %s", goRendered)
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run(verb, func(t *testing.T) {
			printed := fmt.Sprintf(verb, value)
			if strings.Contains(printed, b64) {
				t.Fatalf("fmt %s of a dereferenced Keyring value leaked key material: %s", verb, printed)
			}
			if strings.Contains(printed, "keys:map[") {
				t.Fatalf("fmt %s of a dereferenced Keyring value printed the raw keys map: %s", verb, printed)
			}
		})
	}
}

// TestKeyringFormattingNilPointerDoesNotPanic covers the pointer half of the
// same requirement: formatting a nil *Keyring through fmt must never panic
// and must never print key material (there is none to print, but a
// regression could plausibly reintroduce a nil-unsafe accessor call).
func TestKeyringFormattingNilPointerDoesNotPanic(t *testing.T) {
	var kr *credentials.Keyring

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("formatting a nil *Keyring panicked: %v", r)
		}
	}()

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run(verb, func(t *testing.T) {
			printed := fmt.Sprintf(verb, kr)
			if strings.Contains(printed, "map[") {
				t.Fatalf("fmt %s of a nil *Keyring unexpectedly printed map contents: %s", verb, printed)
			}
		})
	}

	printed := prettyPrintf(kr)
	if strings.Contains(printed, "map[") {
		t.Fatalf("fmt printing of a nil *Keyring unexpectedly printed map contents: %s", printed)
	}
}
