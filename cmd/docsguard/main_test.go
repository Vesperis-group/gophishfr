package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validDocs holds compliant fixture content for every canonical document
// docsguard.canonicalDocs registers, keyed by its repository-root-relative
// path. It is a small, hand-written model of this PR's real documents --
// not a copy of them -- sufficient to satisfy every required fact and
// forbidden-literal check so TestRun's "all documents compliant" case has
// a true, deliberate baseline to mutate away from in the failure cases.
var validDocs = map[string]string{
	"docs/API_AUTHENTICATION.md": "" +
		"Authorization: Bearer TESTTOKEN is the only recommended and canonical transport.\n" +
		"- the api_key query parameter is deprecated, removal targeted for 0.13.0;\n" +
		"- the api_key form parameter is deprecated, removal targeted for 0.13.0;\n" +
		"- a raw Authorization header with no Bearer prefix is deprecated, with no removal version announced.\n",
	"docs/GROUP_IMPORT_LIMITS.md": "" +
		"| Authentication | API key, as the canonical Authorization: Bearer header (a deprecated api_key query parameter is also still accepted, targeted for removal in 0.13.0) |\n",
	"docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md": "" +
		"Authorization: Bearer TESTTOKEN is, and remains, the only recommended and canonical transport.\n" +
		"- the api_key query parameter is deprecated, removal targeted for 0.13.0.\n" +
		"- the api_key form parameter is deprecated, removal targeted for 0.13.0.\n" +
		"- a raw Authorization header is deprecated, with no removal version announced.\n" +
		"Rotate any key ever sent as a query parameter.\n" +
		"Review and remove historical copies of that key from access logs under your retention policy.\n",
	"docs/API_KEY_TRANSPORT_DEPRECATION.md": "" +
		"Authorization: Bearer TESTTOKEN is the only recommended and canonical transport.\n" +
		"| api_key query parameter | Deprecated legacy | 0.13.0 |\n" +
		"| api_key form parameter | Deprecated legacy | 0.13.0 |\n" +
		"A raw Authorization header is deprecated legacy, with no removal version announced.\n" +
		"Rotate any key ever sent as a query parameter.\n" +
		"Review and remove historical copies of that key from access logs under your retention policy.\n" +
		"Before (deprecated):\n" +
		"```bash\n" +
		"curl \"https://gophishfr.example/api/campaigns/42/complete?api_key=REPLACE_WITH_YOUR_TOKEN\"\n" +
		"```\n" +
		"After (canonical):\n" +
		"```bash\n" +
		"curl -H \"Authorization: Bearer REPLACE_WITH_YOUR_TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
		"```\n",
}

// writeDocSet materializes docs (a path -> content map, normally a copy of
// validDocs with one entry mutated) under a fresh temp directory and
// returns that directory's path, suitable for docsguard's -root flag.
func writeDocSet(t *testing.T, docs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range docs {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// cloneDocs returns a shallow copy of validDocs a test can mutate one entry
// of without affecting other tests.
func cloneDocs() map[string]string {
	out := make(map[string]string, len(validDocs))
	for k, v := range validDocs {
		out[k] = v
	}
	return out
}

func TestRunAllDocumentsCompliant(t *testing.T) {
	root := writeDocSet(t, cloneDocs())
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "FORBIDDEN") {
		t.Fatalf("expected no FORBIDDEN line, got %q", stdout.String())
	}
}

func TestRunMissingRequiredFactFails(t *testing.T) {
	docs := cloneDocs()
	docs["docs/API_AUTHENTICATION.md"] = "This document no longer mentions the canonical transport at all.\n"
	root := writeDocSet(t, docs)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "FORBIDDEN: docs/API_AUTHENTICATION.md:") {
		t.Fatalf("expected a FORBIDDEN line for the mutated file, got %q", stdout.String())
	}
}

func TestRunForbiddenParameterCredentialFails(t *testing.T) {
	docs := cloneDocs()
	docs["docs/API_AUTHENTICATION.md"] += "curl \"https://gophishfr.example/api/campaigns/42/complete?api_key=TOKEN\"\n"
	root := writeDocSet(t, docs)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "api_key=") {
		t.Fatalf("expected the forbidden-literal message to name api_key=, got %q", stdout.String())
	}
}

func TestRunForbiddenNonBearerAuthorizationFails(t *testing.T) {
	docs := cloneDocs()
	docs["docs/GROUP_IMPORT_LIMITS.md"] += "curl -H \"Authorization: TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n"
	root := writeDocSet(t, docs)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "FORBIDDEN: docs/GROUP_IMPORT_LIMITS.md:") {
		t.Fatalf("expected a FORBIDDEN line for the mutated file, got %q", stdout.String())
	}
}

func TestRunCanonicalExampleMissingBearerFails(t *testing.T) {
	docs := cloneDocs()
	docs["docs/API_KEY_TRANSPORT_DEPRECATION.md"] = strings.Replace(
		docs["docs/API_KEY_TRANSPORT_DEPRECATION.md"],
		"curl -H \"Authorization: Bearer REPLACE_WITH_YOUR_TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n",
		"curl \"https://gophishfr.example/api/campaigns/42/complete?api_key=REPLACE_WITH_YOUR_TOKEN\"\n",
		1,
	)
	root := writeDocSet(t, docs)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "does not contain the literal") {
		t.Fatalf("expected the canonical-example message, got %q", stdout.String())
	}
}

func TestRunMissingCanonicalDocumentFailsClosed(t *testing.T) {
	docs := cloneDocs()
	delete(docs, "docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md")
	root := writeDocSet(t, docs)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for a missing canonical document, got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md") {
		t.Fatalf("expected the missing file to be named in stderr, got %q", stderr.String())
	}
}

func TestRunUnexpectedArgumentIsUsageError(t *testing.T) {
	root := writeDocSet(t, cloneDocs())
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root, "docs/API_AUTHENTICATION.md"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for an unexpected positional argument, got %d", code)
	}
}

// A forbidden example can carry a real secret value -- e.g. an operator
// pasted a live token into a doc by mistake. The CLI's diagnostics must
// never echo it: only file, an optional line, and a fixed message.
func TestRunSecretBearingForbiddenExampleIsNeverEchoed(t *testing.T) {
	const distinctiveSecret = "sk-SYNTH7f3c9a1b-not-a-real-credential-9e2d4f"
	docs := cloneDocs()
	docs["docs/API_AUTHENTICATION.md"] += "curl -d \"api_key=" + distinctiveSecret + "\" https://gophishfr.example/api/campaigns/42/complete\n"
	root := writeDocSet(t, docs)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 (violations found), got %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), distinctiveSecret) {
		t.Fatalf("stdout leaked the secret-bearing example text: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), distinctiveSecret) {
		t.Fatalf("stderr leaked the secret-bearing example text: %q", stderr.String())
	}
}
