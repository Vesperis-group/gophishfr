package docsguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func messagesContain(violations []Violation, substr string) bool {
	for _, v := range violations {
		if strings.Contains(v.Message, substr) {
			return true
		}
	}
	return false
}

// goodAPIAuthentication is a compliant fixture for
// docs/API_AUTHENTICATION.md: it satisfies every required fact that
// document is registered for and contains none of the forbidden literals.
const goodAPIAuthentication = "" +
	"Authorization: Bearer <token> is the only recommended and canonical transport.\n" +
	"- the api_key query parameter was removed in 0.13.0;\n" +
	"- the api_key form parameter was removed in 0.13.0;\n" +
	"- a raw Authorization header with no Bearer prefix is deprecated, with no removal version announced.\n"

func TestCheckFileAPIAuthenticationCompliant(t *testing.T) {
	violations := CheckFile("docs/API_AUTHENTICATION.md", goodAPIAuthentication)
	if len(violations) != 0 {
		t.Fatalf("expected no violations for a compliant document, got %v", violations)
	}
}

func TestCheckFileAPIAuthenticationMissingEachRequiredFact(t *testing.T) {
	cases := map[string]string{
		"bearer canonical":       "- the api_key query parameter was removed in 0.13.0;\n- the api_key form parameter was removed in 0.13.0;\n- a raw Authorization header with no Bearer prefix is deprecated, with no removal version announced.\n",
		"query removed":          "Authorization: Bearer <token> is the only recommended and canonical transport.\n- the api_key form parameter was removed in 0.13.0;\n- a raw Authorization header with no Bearer prefix is deprecated, with no removal version announced.\n",
		"form removed":           "Authorization: Bearer <token> is the only recommended and canonical transport.\n- the api_key query parameter was removed in 0.13.0;\n- a raw Authorization header with no Bearer prefix is deprecated, with no removal version announced.\n",
		"raw no removal version": "Authorization: Bearer <token> is the only recommended and canonical transport.\n- the api_key query parameter was removed in 0.13.0;\n- the api_key form parameter was removed in 0.13.0;\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			violations := CheckFile("docs/API_AUTHENTICATION.md", content)
			if len(violations) == 0 {
				t.Fatalf("expected at least one missing-required-fact violation when %s is absent", name)
			}
			if !messagesContain(violations, "missing required fact") {
				t.Fatalf("expected a missing required fact violation, got %v", violations)
			}
		})
	}
}

// Soft-wrapped sentences: flattenWhitespace must still let a required fact
// whose matching text crosses a Markdown soft line break be found.
func TestCheckFileRequiredFactAcrossSoftWrap(t *testing.T) {
	content := "" +
		"Authorization: Bearer <token> is the only recommended\nand canonical transport.\n" +
		"- the api_key query\nparameter was removed in\n0.13.0;\n" +
		"- the api_key form\nparameter was removed in\n0.13.0;\n" +
		"- a raw\nAuthorization header with no Bearer prefix is deprecated, with no removal\nversion announced.\n"
	violations := CheckFile("docs/API_AUTHENTICATION.md", content)
	if len(violations) != 0 {
		t.Fatalf("expected a soft-wrapped sentence to still satisfy every required fact, got %v", violations)
	}
}

func TestCheckFileForbiddenParameterCredential(t *testing.T) {
	content := goodAPIAuthentication + "curl \"https://gophishfr.example/api/campaigns/42/complete?api_key=TOKEN\"\n"
	violations := CheckFile("docs/API_AUTHENTICATION.md", content)
	if !messagesContain(violations, "api_key=") {
		t.Fatalf("expected a forbidden api_key= violation, got %v", violations)
	}
}

func TestCheckFileForbiddenNonBearerAuthorization(t *testing.T) {
	cases := []string{
		"curl -H \"Authorization: TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n",
		"curl -H \"Authorization: bearer TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n",
		"Authorization:\n",
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			violations := CheckFile("docs/API_AUTHENTICATION.md", goodAPIAuthentication+line)
			if !messagesContain(violations, "not immediately followed by the literal") {
				t.Fatalf("expected a forbidden non-Bearer violation for %q, got %v", line, violations)
			}
		})
	}
}

func TestCheckFileLegitimateBearerAuthorizationNotFlagged(t *testing.T) {
	violations := CheckFile("docs/API_AUTHENTICATION.md", goodAPIAuthentication+"curl -H \"Authorization: Bearer TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n")
	if messagesContain(violations, "not immediately followed by the literal") {
		t.Fatalf("did not expect a forbidden-Authorization violation for a legitimate Bearer header, got %v", violations)
	}
}

func TestCheckFileGroupImportLimitsCompliant(t *testing.T) {
	content := "| Authentication | API key, as the canonical Authorization: Bearer header (the api_key query parameter was removed in 0.13.0) |\n"
	violations := CheckFile("docs/GROUP_IMPORT_LIMITS.md", content)
	if len(violations) != 0 {
		t.Fatalf("expected no violations for a compliant document, got %v", violations)
	}
}

func TestCheckFileGroupImportLimitsForbiddenExampleFails(t *testing.T) {
	content := "| Authentication | API key, as the canonical Authorization: Bearer header (the api_key query parameter was removed in 0.13.0) |\n" +
		"curl \"https://gophishfr.example/api/import/group?api_key=TOKEN\"\n"
	violations := CheckFile("docs/GROUP_IMPORT_LIMITS.md", content)
	if !messagesContain(violations, "api_key=") {
		t.Fatalf("expected a forbidden api_key= violation, got %v", violations)
	}
}

func TestCheckFileReleaseNoteCompliant(t *testing.T) {
	content := "" +
		"Authorization: Bearer <token> is, and remains, the only recommended and canonical transport.\n" +
		"- the api_key query parameter was removed in 0.13.0.\n" +
		"- the api_key form parameter was removed in 0.13.0.\n" +
		"- a raw Authorization header is deprecated, with no removal version announced.\n" +
		"Rotate any key ever sent as a query parameter.\n" +
		"Review and remove historical copies of that key from access logs under your retention policy.\n"
	violations := CheckFile("docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md", content)
	if len(violations) != 0 {
		t.Fatalf("expected no violations for a compliant document, got %v", violations)
	}
}

func TestCheckFileReleaseNoteMissingRotationOrLogGuidanceFails(t *testing.T) {
	content := "" +
		"Authorization: Bearer <token> is, and remains, the only recommended and canonical transport.\n" +
		"- the api_key query parameter was removed in 0.13.0.\n" +
		"- the api_key form parameter was removed in 0.13.0.\n" +
		"- a raw Authorization header is deprecated, with no removal version announced.\n"
	violations := CheckFile("docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md", content)
	if !messagesContain(violations, "rotation") {
		t.Fatalf("expected a missing-rotation-guidance violation, got %v", violations)
	}
	if !messagesContain(violations, "log cleanup") {
		t.Fatalf("expected a missing-log-cleanup-guidance violation, got %v", violations)
	}
}

// migrationGuideBase is a compliant fixture for
// docs/API_KEY_TRANSPORT_DEPRECATION.md.
const migrationGuideBase = "" +
	"Authorization: Bearer <token> is the only recommended and canonical transport.\n" +
	"| api_key query parameter | Removed | 0.13.0 |\n" +
	"| api_key form parameter | Removed | 0.13.0 |\n" +
	"A raw Authorization header is deprecated legacy, with no removal version announced.\n" +
	"Rotate any key ever sent as a query parameter.\n" +
	"Review and remove historical copies of that key from access logs under your retention policy.\n" +
	"Before (removed in 0.13.0):\n" +
	"```bash\n" +
	"curl \"https://gophishfr.example/api/campaigns/42/complete?api_key=REPLACE_WITH_YOUR_TOKEN\"\n" +
	"```\n" +
	"After (canonical):\n" +
	"```bash\n" +
	"curl -H \"Authorization: Bearer REPLACE_WITH_YOUR_TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
	"```\n"

func TestCheckFileMigrationGuideCompliant(t *testing.T) {
	violations := CheckFile("docs/API_KEY_TRANSPORT_DEPRECATION.md", migrationGuideBase)
	if len(violations) != 0 {
		t.Fatalf("expected no violations for a compliant migration guide, got %v", violations)
	}
}

// The migration guide's deliberate "Before (...)" examples -- now
// describing a fully removed transport rather than a currently-still-
// accepted deprecated one -- must never be flagged: it is explicitly
// exempt from the forbidden-literal checks (see checkCanonicalExamples
// instead).
func TestCheckFileMigrationGuideBeforeExamplesNotFlagged(t *testing.T) {
	violations := CheckFile("docs/API_KEY_TRANSPORT_DEPRECATION.md", migrationGuideBase)
	if messagesContain(violations, "api_key=") {
		t.Fatalf("did not expect the migration guide's deliberate \"Before\" example to be flagged, got %v", violations)
	}
}

func TestCheckFileMigrationGuideCanonicalExampleMissingBearerFails(t *testing.T) {
	content := strings.Replace(
		migrationGuideBase,
		"curl -H \"Authorization: Bearer REPLACE_WITH_YOUR_TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n",
		"curl \"https://gophishfr.example/api/campaigns/42/complete?api_key=REPLACE_WITH_YOUR_TOKEN\"\n",
		1,
	)
	violations := CheckFile("docs/API_KEY_TRANSPORT_DEPRECATION.md", content)
	if !messagesContain(violations, "does not contain the literal") {
		t.Fatalf("expected a canonical-example violation, got %v", violations)
	}
}

func TestCheckFileMigrationGuideCanonicalHeadingWithoutFenceFails(t *testing.T) {
	content := migrationGuideBase + "After (another one): no fenced code block follows this heading at all.\n"
	violations := CheckFile("docs/API_KEY_TRANSPORT_DEPRECATION.md", content)
	if !messagesContain(violations, "not followed by any fenced code block") {
		t.Fatalf("expected a missing-fence violation, got %v", violations)
	}
}

func TestCheckFileUnregisteredPathReturnsNil(t *testing.T) {
	violations := CheckFile("docs/SOME_OTHER_FILE.md", "api_key=TOKEN\nAuthorization: TOKEN\n")
	if violations != nil {
		t.Fatalf("expected nil for an unregistered path, got %v", violations)
	}
}

func TestCheckAllReadsEveryRegisteredCanonicalDocument(t *testing.T) {
	docs := map[string]string{
		"docs/API_AUTHENTICATION.md":                         goodAPIAuthentication,
		"docs/GROUP_IMPORT_LIMITS.md":                        "the canonical Authorization: Bearer header (the api_key query parameter, removed in 0.13.0) |\n",
		"docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md": goodAPIAuthentication + "Rotate any key ever sent as a query parameter.\nReview and remove historical copies of that key from access logs under your retention policy.\n",
		"docs/API_KEY_TRANSPORT_DEPRECATION.md":              migrationGuideBase,
	}
	root := t.TempDir()
	for path, content := range docs {
		writeTestFile(t, root, path, content)
	}
	violations, scanned, err := CheckAll(root)
	if err != nil {
		t.Fatalf("CheckAll returned an unexpected error: %v", err)
	}
	if scanned != len(canonicalDocs) {
		t.Fatalf("expected %d scanned files, got %d", len(canonicalDocs), scanned)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for a compliant document set, got %v", violations)
	}
}

func TestCheckAllFailsClosedOnMissingCanonicalDocument(t *testing.T) {
	root := t.TempDir()
	_, _, err := CheckAll(root)
	if err == nil {
		t.Fatalf("expected an error when every canonical document is missing")
	}
}

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
