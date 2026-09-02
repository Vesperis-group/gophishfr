package credentials_test

import (
	"bytes"
	"testing"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
)

func testKeyring(t *testing.T, activeID string, extra map[string][32]byte) *credentials.Keyring {
	t.Helper()
	keys := map[string][]byte{
		activeID: synthKey(0xA1),
	}
	for id, k := range extra {
		kk := k
		keys[id] = kk[:]
	}
	kr, err := credentials.NewKeyring(activeID, keys)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return kr
}

// synthKey builds a deterministic, obviously-synthetic 32-byte test key from
// a single seed byte. It is never anything that could pass for a real
// production secret.
func synthKey(seed byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed ^ byte(i)
	}
	return k
}

func TestContextCollisionFraming(t *testing.T) {
	kr := testKeyring(t, "test-active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plaintext := []byte("identical-plaintext")

	ctxA := credentials.Context{Kind: "test-kind", OwnerID: "1", RecordID: "23"}
	ctxB := credentials.Context{Kind: "test-kind", OwnerID: "12", RecordID: "3"}

	envA, err := c.Encrypt(ctxA, plaintext)
	if err != nil {
		t.Fatalf("Encrypt(ctxA): %v", err)
	}
	envB, err := c.Encrypt(ctxB, plaintext)
	if err != nil {
		t.Fatalf("Encrypt(ctxB): %v", err)
	}

	// Each context can only decrypt its own envelope.
	if _, err := c.Decrypt(ctxA, envA); err != nil {
		t.Fatalf("Decrypt(ctxA, envA): %v", err)
	}
	if _, err := c.Decrypt(ctxB, envB); err != nil {
		t.Fatalf("Decrypt(ctxB, envB): %v", err)
	}

	// Cross-decrypting must fail: if OwnerID/RecordID collided under naive
	// delimiter concatenation ("1"+":"+"23" == "12"+":"+"3" would NOT be
	// true, but this guards the property the length-prefixed framing is
	// meant to guarantee generally), decrypting envA under ctxB (or vice
	// versa) must never succeed.
	if _, err := c.Decrypt(ctxB, envA); err == nil {
		t.Fatalf("Decrypt(ctxB, envA) unexpectedly succeeded: distinct contexts produced colliding AAD")
	}
	if _, err := c.Decrypt(ctxA, envB); err == nil {
		t.Fatalf("Decrypt(ctxA, envB) unexpectedly succeeded: distinct contexts produced colliding AAD")
	}
}

func TestContextAllFieldsDistinguish(t *testing.T) {
	kr := testKeyring(t, "test-active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plaintext := []byte("payload")

	base := credentials.Context{Kind: "k", Table: "t", Column: "col", OwnerID: "1", RecordID: "1"}
	env, err := c.Encrypt(base, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	variants := []credentials.Context{
		{Kind: "other", Table: "t", Column: "col", OwnerID: "1", RecordID: "1"},
		{Kind: "k", Table: "other", Column: "col", OwnerID: "1", RecordID: "1"},
		{Kind: "k", Table: "t", Column: "other", OwnerID: "1", RecordID: "1"},
		{Kind: "k", Table: "t", Column: "col", OwnerID: "other", RecordID: "1"},
		{Kind: "k", Table: "t", Column: "col", OwnerID: "1", RecordID: "other"},
	}
	for i, variant := range variants {
		if _, err := c.Decrypt(variant, env); err == nil {
			t.Fatalf("variant %d: modified context unexpectedly decrypted successfully", i)
		}
	}

	// The exact original context still works.
	got, err := c.Decrypt(base, env)
	if err != nil {
		t.Fatalf("Decrypt(base, env): %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", got, plaintext)
	}
}

func TestContextValidation(t *testing.T) {
	kr := testKeyring(t, "test-active", nil)
	c, err := credentials.New(kr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("empty kind rejected", func(t *testing.T) {
		_, err := c.Encrypt(credentials.Context{}, []byte("x"))
		if err == nil {
			t.Fatal("expected error for empty Kind")
		}
	})

	t.Run("oversized kind rejected", func(t *testing.T) {
		huge := make([]byte, 4096)
		for i := range huge {
			huge[i] = 'a'
		}
		_, err := c.Encrypt(credentials.Context{Kind: string(huge)}, []byte("x"))
		if err == nil {
			t.Fatal("expected error for oversized Kind")
		}
	})

	t.Run("oversized field rejected", func(t *testing.T) {
		huge := make([]byte, 4096)
		for i := range huge {
			huge[i] = 'a'
		}
		_, err := c.Encrypt(credentials.Context{Kind: "k", OwnerID: string(huge)}, []byte("x"))
		if err == nil {
			t.Fatal("expected error for oversized OwnerID")
		}
	})
}
