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

	t.Run("oversized line followed by a forbidden example fails closed", func(t *testing.T) {
		oversized := strings.Repeat("a", 2*1024*1024)
		content := oversized + "\n?api_key=TOKEN\n"
		path := writeTempFile(t, dir, "oversized.md", content)

		var stdout, stderr bytes.Buffer
		code := run([]string{path}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("expected exit 2 for a scan error (fail closed), got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "scanning") {
			t.Fatalf("expected a scanning-error message, got stderr=%q", stderr.String())
		}
	})

	// A forbidden example can carry a real secret value -- e.g. an operator
	// pasted a live token into a doc by mistake. The CLI's diagnostics must
	// never echo it: only file, line range, kind, and a fixed explanation.
	// This uses a distinctive synthetic string that would never appear in
	// legitimate diagnostic output, so any leak is unambiguous.
	t.Run("a secret-bearing forbidden example is never echoed", func(t *testing.T) {
		const distinctiveSecret = "sk-SYNTH7f3c9a1b-not-a-real-credential-9e2d4f"
		content := "curl -d \"api_key=" + distinctiveSecret + "\" https://gophishfr.example/api/campaigns/42/complete\n" +
			"curl -H \"Authorization: " + distinctiveSecret + "\" https://gophishfr.example/api/campaigns/42/complete\n"
		path := writeTempFile(t, dir, "secret-bearing.md", content)

		var stdout, stderr bytes.Buffer
		code := run([]string{path}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit 1 (violations found), got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String(), distinctiveSecret) {
			t.Fatalf("stdout leaked the secret-bearing example text: %q", stdout.String())
		}
		if strings.Contains(stderr.String(), distinctiveSecret) {
			t.Fatalf("stderr leaked the secret-bearing example text: %q", stderr.String())
		}
		if !strings.Contains(stdout.String(), "FORBIDDEN (parameter_credential)") {
			t.Fatalf("expected a parameter_credential FORBIDDEN line with useful location, got stdout=%q", stdout.String())
		}
		if !strings.Contains(stdout.String(), "FORBIDDEN (raw_authorization)") {
			t.Fatalf("expected a raw_authorization FORBIDDEN line with useful location, got stdout=%q", stdout.String())
		}
		if !strings.Contains(stdout.String(), path+":1") {
			t.Fatalf("expected the file:line location for the first violation, got stdout=%q", stdout.String())
		}
		if !strings.Contains(stdout.String(), path+":2") {
			t.Fatalf("expected the file:line location for the second violation, got stdout=%q", stdout.String())
		}
	})
}
