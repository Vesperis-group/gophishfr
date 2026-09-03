//go:build windows

package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLogFileWindowsAppendsWithoutPOSIXModeCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gophish.log")
	if err := os.WriteFile(path, []byte("old marker\n"), 0600); err != nil {
		t.Fatal(err)
	}

	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("open writable Windows log: %v", err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0666 {
		t.Fatalf("Windows writable mode precondition = %04o, want 0666", info.Mode().Perm())
	}
	if err := secureLogFile(path, f, info); err != nil {
		t.Fatalf("Windows helper asserted POSIX permissions: %v", err)
	}
	if _, err := f.WriteString("new marker\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old marker\nnew marker\n" {
		t.Fatalf("content was truncated or not appended: %q", content)
	}
}

func TestOpenLogFileWindowsRejectsUnsafeObjects(t *testing.T) {
	t.Run("read only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "gophish.log")
		if err := os.WriteFile(path, []byte("read-only marker"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0444); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0666); err != nil && !os.IsNotExist(err) {
				t.Errorf("restore writable attribute: %v", err)
			}
		})
		if _, err := openLogFile(path); err == nil {
			t.Fatal("expected read-only file to fail writable open")
		}
	})

	t.Run("directory", func(t *testing.T) {
		if _, err := openLogFile(t.TempDir()); err == nil ||
			!strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("expected regular-file error, got %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.log")
		path := filepath.Join(dir, "gophish.log")
		if err := os.WriteFile(target, []byte("target marker"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("Windows symlink privilege unavailable: %v", err)
		}
		if _, err := openLogFile(path); err == nil ||
			!strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink error, got %v", err)
		}
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "target marker" {
			t.Fatalf("symlink target changed: %q", content)
		}
	})
}
