package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/Vesperis-group/gophishfr/models"
)

func TestLoadCredentialCipher(t *testing.T) {
	t.Setenv(models.IMAPCredentialKeyringEnvironment, "")
	credentialCipher, err := loadCredentialCipher()
	if err != nil || credentialCipher != nil {
		t.Fatalf("unset keyring returned cipher=%v err=%v", credentialCipher, err)
	}

	keyringPath := filepath.Join(t.TempDir(), "keyring.json")
	key := bytes.Repeat([]byte{0x29}, 32)
	document := fmt.Sprintf(
		`{"version":1,"active_key_id":"native-test-key","keys":[{"id":"native-test-key","key":"%s"}]}`,
		base64.StdEncoding.EncodeToString(key),
	)
	if err := os.WriteFile(keyringPath, []byte(document), 0o400); err != nil {
		t.Fatalf("write keyring: %v", err)
	}
	t.Setenv(models.IMAPCredentialKeyringEnvironment, keyringPath)
	credentialCipher, err = loadCredentialCipher()
	if err != nil {
		t.Fatalf("load read-only keyring: %v", err)
	}

	// The bootstrap loads once. Removing the file cannot affect the injected,
	// immutable cipher.
	if err := os.Remove(keyringPath); err != nil {
		t.Fatalf("remove loaded keyring: %v", err)
	}
	context := credentials.Context{
		Kind: "imap-password", Table: "imap", Column: "password", OwnerID: "1",
	}
	envelope, err := credentialCipher.Encrypt(context, []byte("synthetic-bootstrap-secret"))
	if err != nil {
		t.Fatalf("encrypt after keyring removal: %v", err)
	}
	if _, err := credentialCipher.Decrypt(context, envelope); err != nil {
		t.Fatalf("decrypt after keyring removal: %v", err)
	}
}

func TestLoadCredentialCipherRejectsUnsafeFile(t *testing.T) {
	keyringPath := filepath.Join(t.TempDir(), "unsafe-keyring.json")
	if err := os.WriteFile(keyringPath, []byte(`{}`), 0o622); err != nil {
		t.Fatalf("write unsafe keyring: %v", err)
	}
	if err := os.Chmod(keyringPath, 0o622); err != nil {
		t.Fatalf("set unsafe keyring permissions: %v", err)
	}
	t.Setenv(models.IMAPCredentialKeyringEnvironment, keyringPath)
	if _, err := loadCredentialCipher(); err == nil {
		t.Fatal("unsafe keyring permissions were accepted")
	}
}
