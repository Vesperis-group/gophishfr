package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestRunFlagsAndExitCodes(t *testing.T) {
	dir := t.TempDir()
	clean := writeTempFile(t, dir, "clean.md", "Authorization: Bearer TOKEN\n")
	dirty := writeTempFile(t, dir, "dirty.md", "?api_key=TOKEN\n")

	t.Run("clean file passes", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{clean}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
		}
	})

	t.Run("dirty file fails", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{dirty}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit 1, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "FORBIDDEN") {
			t.Fatalf("expected a FORBIDDEN line, got %q", stdout.String())
		}
	})

	t.Run("exempted dirty file passes", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"-exempt", dirty, dirty}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit 0 for an exempted file, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
		}
	})

	t.Run("no files is a usage error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run(nil, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("expected exit 2 for no files, got %d", code)
		}
	})

	t.Run("missing file is reported", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{filepath.Join(dir, "does-not-exist.md")}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("expected exit 2 for a missing file, got %d", code)
		}
	})
}
