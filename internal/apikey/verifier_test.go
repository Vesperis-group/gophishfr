package apikey

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testService(t *testing.T) *Service {
	t.Helper()
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyring, err := NewKeyring("active", map[string][]byte{"active": key})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(keyring)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestVerifierProtocol(t *testing.T) {
	service := testService(t)
	first, err := service.Compute("active", []byte("synthetic-token"))
	if err != nil {
		t.Fatal(err)
	}

	second, _ := service.Compute("active", []byte("synthetic-token"))
	changed, _ := service.Compute("active", []byte("synthetic-token-changed"))
	if first != second || first == changed || len(first) != VerifierSize {
		t.Fatal("verifier determinism, separation, or size contract failed")
	}

	empty, _ := service.Compute("active", nil)
	arbitrary, _ := service.Compute("active", []byte{0, 0xff, '\n'})
	if empty == arbitrary {
		t.Fatal("distinct arbitrary token bytes produced the same verifier")
	}
	if err := service.Verify("active", []byte("synthetic-token"), first[:]); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(service.Verify("active", []byte("wrong"), first[:]), ErrVerification) {
		t.Fatal("wrong token did not return typed verification failure")
	}
	if !errors.Is(service.Verify("active", nil, first[:31]), ErrInvalidState) {
		t.Fatal("wrong verifier length did not return typed state failure")
	}
}

func TestKeyringStrictParsingAndRedaction(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	valid := fmt.Sprintf(`{"version":1,"active_key_id":"new","keys":[{"id":"old","key":%q},{"id":"new","key":%q}]}`, encoded, encoded)
	keyring, err := ParseKeyringJSON([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	rendered := fmt.Sprintf("%v %#v", keyring, keyring)
	if bytes.Contains([]byte(rendered), []byte(encoded)) {
		t.Fatal("formatted keyring exposed key material")
	}
	for name, document := range map[string]string{
		"unknown field":     `{"version":1,"active_key_id":"x","keys":[],"extra":true}`,
		"unknown version":   `{"version":2,"active_key_id":"x","keys":[]}`,
		"empty keys":        `{"version":1,"active_key_id":"x","keys":[]}`,
		"duplicate":         fmt.Sprintf(`{"version":1,"active_key_id":"x","keys":[{"id":"x","key":%q},{"id":"x","key":%q}]}`, encoded, encoded),
		"unknown active":    fmt.Sprintf(`{"version":1,"active_key_id":"y","keys":[{"id":"x","key":%q}]}`, encoded),
		"noncanonical":      `{"version":1,"active_key_id":"x","keys":[{"id":"x","key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]}`,
		"invalid id":        fmt.Sprintf(`{"version":1,"active_key_id":"bad id","keys":[{"id":"bad id","key":%q}]}`, encoded),
		"trailing document": valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseKeyringJSON([]byte(document)); !errors.Is(err, ErrMalformedKeyring) {
				t.Fatalf("expected typed malformed keyring error, got %v", err)
			}
		})
	}
}

func TestKeyringFileBoundsPermissionsAndSymlink(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(keyringDocument{
		Version: 1, ActiveKeyID: "active",
		Keys: []keyEntryDocument{{ID: "active", Key: base64.StdEncoding.EncodeToString(key)}},
	})
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, document, 0o400); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "mounted-secret")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyringFile(link); err != nil {
		t.Fatalf("read-only symlink mount rejected: %v", err)
	}
	if err := os.Chmod(target, 0o620); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyringFile(link); !errors.Is(err, ErrMalformedKeyring) {
		t.Fatal("group-writable keyring accepted")
	}
	oversized := filepath.Join(directory, "oversized")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), maxKeyringFileBytes+1), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyringFile(oversized); !errors.Is(err, ErrMalformedKeyring) {
		t.Fatal("oversized keyring accepted")
	}
}

func TestConcurrentVerifierReads(t *testing.T) {
	service := testService(t)
	want, _ := service.Compute("active", []byte("concurrent-token"))
	var group sync.WaitGroup
	for i := 0; i < 64; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 100; j++ {
				got, err := service.Compute("active", []byte("concurrent-token"))
				if err != nil || got != want {
					t.Errorf("concurrent compute mismatch: %v", err)
					return
				}
			}
		}()
	}
	group.Wait()
}

func FuzzVerifier(f *testing.F) {
	key := make([]byte, keySize)
	for index := range key {
		key[index] = byte(index + 1)
	}

	keyring, _ := NewKeyring("fuzz", map[string][]byte{"fuzz": key})
	service, _ := New(keyring)
	f.Add([]byte("seed"))
	f.Add([]byte{})
	f.Add([]byte{0, 0xff})
	f.Fuzz(func(t *testing.T, token []byte) {
		verifier, err := service.Compute("fuzz", token)
		if err != nil || len(verifier) != VerifierSize {
			t.Fatalf("compute failed: %v", err)
		}
		if err := service.Verify("fuzz", token, verifier[:]); err != nil {
			t.Fatal(err)
		}
	})
}

func FuzzParseKeyring(f *testing.F) {
	f.Add([]byte(`{"version":1,"active_key_id":"x","keys":[]}`))
	f.Add([]byte{0xff, 0x00})
	f.Fuzz(func(t *testing.T, document []byte) {
		keyring, err := ParseKeyringJSON(document)
		if err == nil {
			if keyring == nil || len(keyring.ids) == 0 || len(keyring.ids) > maxKeyringKeys {
				t.Fatal("parser returned invalid keyring state")
			}
			return
		}
		if !errors.Is(err, ErrMalformedKeyring) {
			t.Fatalf("parser returned a sensitive/untyped error: %v", err)
		}
	})
}

func TestVerifierProtocolVectorAndDomainSeparation(t *testing.T) {
	key := make([]byte, keySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	keyring, err := NewKeyring("vector", map[string][]byte{"vector": key})
	if err != nil {
		t.Fatal(err)
	}
	service, _ := New(keyring)
	got, err := service.Compute("vector", []byte("synthetic-vector-token"))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got[:]) != "a23e66cff0d5fe17d9d4a69a79c5b30374f79aca6ac72442eb5f6497f0c08869" {
		t.Fatal("immutable protocol vector changed")
	}
	var raw [keySize]byte
	copy(raw[:], key)
	otherDomain := computeVerifier(raw, Domain+"-different", []byte("synthetic-vector-token"))
	if got == otherDomain {
		t.Fatal("domain separation did not change verifier")
	}
	key[0] ^= 0xff
	otherKeyring, _ := NewKeyring("vector", map[string][]byte{"vector": key})
	otherService, _ := New(otherKeyring)
	otherKey, _ := otherService.Compute("vector", []byte("synthetic-vector-token"))
	if got == otherKey {
		t.Fatal("changed key did not change verifier")
	}
}
