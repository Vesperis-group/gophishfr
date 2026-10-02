package docsguard

import "testing"

// Fixture-based positive/negative coverage for every deprecated transport
// syntax and every legitimate Authorization scheme this guard must leave
// alone. Each case is a single line, matching how the guard is actually used
// (scanning Markdown line by line).
func TestScanTextParameterCredential(t *testing.T) {
	positive := []string{
		`curl "https://gophishfr.example/api/groups/summary?api_key=TOKEN"`,
		`curl "https://gophishfr.example/api/groups/?other=1&api_key=TOKEN"`,
		`curl -d "api_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl --data "api_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl --data-raw "api_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl --data-urlencode "api_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -F "api_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl --form "api_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`api_key=TOKEN`,
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := ScanText(line)
			if !containsKind(violations, KindParameterCredential) {
				t.Fatalf("expected a parameter-credential violation for %q, got %v", line, violations)
			}
		})
	}

	negative := []string{
		`curl -H "Authorization: Bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		"The `api_key` query parameter is deprecated.",
		"Set `GOPHISH_INITIAL_ADMIN_API_TOKEN` before first boot.",
		"| Authentication | API key, as an `Authorization: Bearer` header |",
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := ScanText(line)
			if containsKind(violations, KindParameterCredential) {
				t.Fatalf("unexpected parameter-credential violation for %q: %v", line, violations)
			}
		})
	}
}

func TestScanTextRawAuthorization(t *testing.T) {
	positive := []string{
		`curl -H "Authorization: TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		"- a raw `Authorization: TOKEN` value;",
		`curl -H "authorization: TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -H "AUTHORIZATION: TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := ScanText(line)
			if !containsKind(violations, KindRawAuthorization) {
				t.Fatalf("expected a raw-authorization violation for %q, got %v", line, violations)
			}
		})
	}

	negative := []string{
		`curl -H "Authorization: Bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -H "authorization: bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -H "Authorization: Basic dXNlcjpwYXNz"`, // gitleaks:allow -- base64 of the literal synthetic fixture "user:pass", not a real credential
		`curl -H "Authorization: Digest username=\"foo\""`,
		`curl -H "Authorization: Negotiate abc"`,
		`curl -H "Authorization: NTLM abc"`,
		"a raw `Authorization` header carrying only the token, with no `Bearer` prefix",
		"Authorization selection happens before credential validation.",
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := ScanText(line)
			if containsKind(violations, KindRawAuthorization) {
				t.Fatalf("unexpected raw-authorization violation for %q: %v", line, violations)
			}
		})
	}
}

func TestScanTextMultiLineAndLineNumbers(t *testing.T) {
	text := "line one is fine\n" +
		"curl \"https://x/api/groups/summary?api_key=TOKEN\"\n" +
		"line three is fine\n" +
		"curl -H \"Authorization: TOKEN\" https://x\n"
	violations := ScanText(text)
	if len(violations) != 2 {
		t.Fatalf("expected exactly 2 violations, got %d: %v", len(violations), violations)
	}
	if violations[0].Line != 2 || violations[0].Kind != KindParameterCredential {
		t.Fatalf("unexpected first violation: %+v", violations[0])
	}
	if violations[1].Line != 4 || violations[1].Kind != KindRawAuthorization {
		t.Fatalf("unexpected second violation: %+v", violations[1])
	}
}

func TestScanTextUndeprecatedParameterMention(t *testing.T) {
	positive := []string{
		"| Authentication | API key, as an `Authorization: Bearer` header or an `api_key` parameter |",
		"Authentication can use a Bearer header or an api_key parameter.",
		"| Auth | api_key |",
		"Clients may authenticate with Bearer or an API_KEY parameter.",
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := ScanText(line)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected an undeprecated-parameter-mention violation for %q, got %v", line, violations)
			}
		})
	}

	negative := []string{
		// The fixed docs/GROUP_IMPORT_LIMITS.md row: same deprecation
		// context (deprecated/removal/0.13.0) as the table cell itself.
		"| Authentication | API key, as the canonical `Authorization: Bearer` header (an `api_key` query/form parameter is deprecated and targeted for removal in `0.13.0`; see the migration guide) |",
		// The deprecation guide's own contract table.
		"| `api_key` query parameter | Deprecated legacy | `0.13.0` |",
		"| `api_key` form parameter | Deprecated legacy | `0.13.0` |",
		// Prose describing the deprecation itself, not offering api_key as
		// a live alternative.
		"The `api_key` query parameter is deprecated.",
		"Query and form `api_key` are legacy transports slated for removal.",
		// Meta-documentation describing this guard's own fixtures -- "a
		// query api_key parameter", not "or an api_key parameter" -- must
		// stay allowed even though it is not itself a deprecation sentence.
		"covering, as positive cases: a query `api_key` parameter, curl -d",
		// A table row that does not mention api_key at all.
		"| Authentication | session cookie |",
		// Internal/DB-schema prose using the identifier, not describing a
		// client-facing transport option.
		"SQLite stores a nullable legacy `api_key`, a raw BLOB verifier.",
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := ScanText(line)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected undeprecated-parameter-mention violation for %q: %v", line, violations)
			}
		})
	}
}

func containsKind(violations []Violation, kind Kind) bool {
	for _, v := range violations {
		if v.Kind == kind {
			return true
		}
	}
	return false
}
