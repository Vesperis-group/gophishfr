package credentials_test

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
)

func writeTempKeyringFile(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	doc := `{
		"version": 1,
		"active_key_id": "test-active",
		"keys": [{"id": "test-active", "key": "` + base64.StdEncoding.EncodeToString(synthKey(0x77)) + `"}]
	}`
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(doc), mode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// WriteFile applies the umask; force the exact mode the test wants.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	return path
}

func TestLoadKeyringFileValid(t *testing.T) {
	dir := t.TempDir()
	path := writeTempKeyringFile(t, dir, "keyring.json", 0o400)

	kr, err := credentials.LoadKeyringFile(path)
	if err != nil {
		t.Fatalf("LoadKeyringFile: %v", err)
	}
	if kr.ActiveKeyID() != "test-active" {
		t.Fatalf("unexpected active key id: %q", kr.ActiveKeyID())
	}
}

func TestLoadKeyringFileMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := credentials.LoadKeyringFile(filepath.Join(dir, "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected error for a missing keyring file")
	}
	if !errors.Is(err, credentials.ErrInvalidKeyring) {
		t.Fatalf("expected ErrInvalidKeyring, got %v", err)
	}
}

func TestLoadKeyringFileDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := credentials.LoadKeyringFile(dir)
	if err == nil {
		t.Fatal("expected error when the path is a directory")
	}
}

func TestLoadKeyringFileMalformedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.json")
	if err := os.WriteFile(path, []byte("not json"), 0o400); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := credentials.LoadKeyringFile(path); err == nil {
		t.Fatal("expected error for malformed keyring content")
	}
}

func TestLoadKeyringFileRejectsGroupWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on windows")
	}
	dir := t.TempDir()
	path := writeTempKeyringFile(t, dir, "keyring.json", 0o660) // rw-rw----: group write set.

	_, err := credentials.LoadKeyringFile(path)
	if err == nil {
		t.Fatal("expected error for a group-writable keyring file")
	}
	if !errors.Is(err, credentials.ErrInvalidKeyring) {
		t.Fatalf("expected ErrInvalidKeyring, got %v", err)
	}
}

func TestLoadKeyringFileRejectsWorldWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on windows")
	}
	dir := t.TempDir()
	path := writeTempKeyringFile(t, dir, "keyring.json", 0o406)

	_, err := credentials.LoadKeyringFile(path)
	if err == nil {
		t.Fatal("expected error for a world-writable keyring file")
	}
}

func TestLoadKeyringFileAllowsGroupAndWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on windows")
	}
	dir := t.TempDir()
	// 0444: a shape commonly seen on Docker/Kubernetes secret mounts, world
	// and group readable but not writable by anyone.
	path := writeTempKeyringFile(t, dir, "keyring.json", 0o444)

	if _, err := credentials.LoadKeyringFile(path); err != nil {
		t.Fatalf("a read-only-for-everyone keyring file must be accepted: %v", err)
	}
}

func TestLoadKeyringFileFollowsLegitimateSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	dir := t.TempDir()
	target := writeTempKeyringFile(t, dir, "real-keyring.json", 0o440)

	// Simulate a Docker/Kubernetes secret mount: the path the application
	// opens is a symlink into a separately-mounted location.
	linkPath := filepath.Join(dir, "keyring-link.json")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	kr, err := credentials.LoadKeyringFile(linkPath)
	if err != nil {
		t.Fatalf("LoadKeyringFile via symlink: %v", err)
	}
	if kr.ActiveKeyID() != "test-active" {
		t.Fatalf("unexpected active key id via symlink: %q", kr.ActiveKeyID())
	}
}

func TestLoadKeyringFileChecksResolvedSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	dir := t.TempDir()
	// The symlink itself commonly has broad permissions (symlinks on Linux
	// are effectively always 0777 and the mode is not meaningful); what must
	// be enforced is the permission of the resolved target.
	target := writeTempKeyringFile(t, dir, "real-keyring.json", 0o660) // group-writable target: must be rejected.
	linkPath := filepath.Join(dir, "keyring-link.json")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if _, err := credentials.LoadKeyringFile(linkPath); err == nil {
		t.Fatal("expected the resolved target's insecure permissions to be rejected through the symlink")
	}
}

func TestLoadKeyringFileNeverCreatesOrModifies(t *testing.T) {
	dir := t.TempDir()
	path := writeTempKeyringFile(t, dir, "keyring.json", 0o400)

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if _, err := credentials.LoadKeyringFile(path); err != nil {
		t.Fatalf("LoadKeyringFile: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if before.Mode() != after.Mode() {
		t.Fatalf("LoadKeyringFile must never chmod the keyring file: before=%v after=%v", before.Mode(), after.Mode())
	}

	// And a missing path must never be created as a side effect.
	missing := filepath.Join(dir, "should-never-exist.json")
	if _, err := credentials.LoadKeyringFile(missing); err == nil {
		t.Fatal("expected error for a missing keyring file")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("LoadKeyringFile must never create a default/fallback keyring file, stat err=%v", err)
	}
}
