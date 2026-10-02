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

func containsKind(violations []Violation, kind Kind) bool {
	for _, v := range violations {
		if v.Kind == kind {
			return true
		}
	}
	return false
}
