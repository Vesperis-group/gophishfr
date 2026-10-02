package docsguard

import (
	"strings"
	"testing"
)

// mustScan runs ScanText and fails the test immediately if scanning itself
// errored, so every ordinary fixture test can assert purely on violations.
func mustScan(t *testing.T, text string) []Violation {
	t.Helper()
	violations, err := ScanText(text)
	if err != nil {
		t.Fatalf("ScanText(%q) returned an unexpected error: %v", text, err)
	}
	return violations
}

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
		// Percent-encoded parameter names: Go's net/url and net/http both
		// percent-decode a query/form parameter name before comparing it,
		// so "api%5Fkey" (where %5F is "_") authenticates exactly like
		// "api_key" at runtime and must be caught the same way.
		`curl "https://gophishfr.example/api/groups/summary?api%5Fkey=TOKEN"`,
		`curl --data "api%5Fkey=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl "https://gophishfr.example/api/groups/?other=1&api%5Fkey=TOKEN"`,
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
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
		// Percent-decodes to something other than "api_key"; must stay
		// unflagged, proving decoding doesn't create false positives.
		`curl --data "api%5Fkeys=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
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
		// Wrong-case "bearer": the real extractor's strings.TrimPrefix only
		// strips the exact "Bearer " prefix, so any other casing is not
		// recognized as Bearer at runtime and must be flagged.
		`curl -H "Authorization: bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -H "authorization: bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -H "Authorization: BEARER TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -H "Authorization: BeaRer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
			if !containsKind(violations, KindRawAuthorization) {
				t.Fatalf("expected a raw-authorization violation for %q, got %v", line, violations)
			}
		})
	}

	negative := []string{
		`curl -H "Authorization: Bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		// The header *name* stays case-insensitive even for the exact
		// canonical scheme casing.
		`curl -H "authorization: Bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		`curl -H "AUTHORIZATION: Bearer TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		// Real, unrelated schemes stay allowed case-insensitively: this
		// repository's runtime has no special-case handling of them the way
		// it does for Bearer, so their registered case-insensitivity (RFC
		// 7235) applies normally.
		`curl -H "Authorization: Basic dXNlcjpwYXNz"`, // gitleaks:allow -- base64 of the literal synthetic fixture "user:pass", not a real credential
		`curl -H "Authorization: basic dXNlcjpwYXNz"`, // gitleaks:allow -- base64 of the literal synthetic fixture "user:pass", not a real credential
		`curl -H "Authorization: Digest username=\"foo\""`,
		`curl -H "Authorization: Negotiate abc"`,
		`curl -H "Authorization: NTLM abc"`,
		"a raw `Authorization` header carrying only the token, with no `Bearer` prefix",
		"Authorization selection happens before credential validation.",
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
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
	violations := mustScan(t, text)
	if len(violations) != 2 {
		t.Fatalf("expected exactly 2 violations, got %d: %v", len(violations), violations)
	}
	if violations[0].StartLine != 2 || violations[0].EndLine != 2 || violations[0].Kind != KindParameterCredential {
		t.Fatalf("unexpected first violation: %+v", violations[0])
	}
	if violations[1].StartLine != 4 || violations[1].EndLine != 4 || violations[1].Kind != KindRawAuthorization {
		t.Fatalf("unexpected second violation: %+v", violations[1])
	}
}

// TestScanTextOversizedLineFailsClosed proves ScanText does not silently
// drop the rest of a document when a single line exceeds the scanner's
// internal buffer. bufio.Scanner.Scan returns false and sets
// bufio.ErrTooLong in that case; a caller that ignored it would report "no
// violations" even though a real violation sits on the very next line,
// which is exactly the fail-open bug this test guards against.
func TestScanTextOversizedLineFailsClosed(t *testing.T) {
	oversizedLine := strings.Repeat("a", 2*1024*1024) // exceeds the 1 MiB max token size.
	text := oversizedLine + "\n" + `curl -d "api_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete` + "\n"

	violations, err := ScanText(text)
	if err == nil {
		t.Fatalf("expected ScanText to return an error for an oversized line, got violations=%v", violations)
	}
}

func TestScanTextUndeprecatedParameterMention(t *testing.T) {
	positive := []string{
		"| Authentication | API key, as an `Authorization: Bearer` header or an `api_key` parameter |",
		"Authentication can use a Bearer header or an api_key parameter.",
		"| Auth | api_key |",
		"Clients may authenticate with Bearer or an API_KEY parameter.",
		// Ordinary recommendation prose: a verb ("use"/"via"/"with"/
		// "accept"/"authenticate") governing api_key as a live
		// authentication option, with a companion anchor word, and no
		// deprecation context. Query and form variants, several casings.
		"Use the api_key query parameter for authentication.",
		"Use the API_KEY query parameter for authentication.",
		"You can authenticate via the api_key parameter.",
		"Clients authenticate using the api_key query parameter.",
		"Requests accept the api_key form parameter.",
		"Authenticate with the Api_Key header.",
		"Send requests using the api_key form field to authenticate.",
		"This endpoint accepts the api_key credential in the query string.",
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
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
		"Fixtures include a query `api_key` parameter and a form `api_key` value for the credential-syntax check.",
		// A table row that does not mention api_key at all.
		"| Authentication | session cookie |",
		// Internal/DB-schema prose using the identifier, not describing a
		// client-facing transport option.
		"SQLite stores a nullable legacy `api_key`, a raw BLOB verifier.",
		"The migration script reads the api_key column from the legacy table.",
		// Historical statements: describe past/no-longer-current behaviour,
		// already carrying deprecation-style context, not a recommendation.
		"Earlier releases let clients authenticate via the api_key parameter before this deprecation.",
		"This release deprecates using the api_key query parameter in favor of Bearer.",
		// Migration/deprecation description naming the recommendation verbs
		// only to contrast them with the canonical replacement.
		"Replace any client that authenticates with the api_key parameter; migrate it to Bearer instead.",
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected undeprecated-parameter-mention violation for %q: %v", line, violations)
			}
		})
	}
}

// TestScanTextWrappedRecommendation proves recommendation prose is detected
// even when ordinary Markdown line-wrapping splits it across several
// physical lines -- the paragraph-level analysis this test exercises is
// what line-local scanning alone could never catch.
func TestScanTextWrappedRecommendation(t *testing.T) {
	positive := []string{
		// The verb, the anchor word, and "api_key" are each on a different
		// physical line of the same paragraph.
		"Use the\napi_key query\nparameter for authentication.\n",
		"Clients should\nauthenticate\nvia the api_key\nparameter.\n",
		"This endpoint\naccepts the api_key\ncredential in the\nquery string.\n",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a wrapped-recommendation violation for %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// Wrapped, but already carrying affirmative deprecation context
		// somewhere in the same paragraph -- a historical statement, not a
		// live recommendation.
		"Earlier releases let clients\nauthenticate via the api_key parameter\nbefore this deprecation.\n",
		// Wrapped internal/schema prose with no recommendation verb+anchor
		// pairing at all.
		"SQLite stores a\nnullable legacy `api_key`,\na raw BLOB verifier,\nand a key ID.\n",
		// A blank line separates the recommendation-shaped second paragraph
		// from unrelated content; each paragraph is still judged on its own
		// merits, and this one is pure deprecation description.
		"Unrelated heading text.\n\nThe api_key query parameter\nis deprecated and targeted\nfor removal.\n",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected wrapped-recommendation violation for %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextNegatedContextDoesNotSuppress proves a negated
// deprecation-context word ("not deprecated", "no longer legacy") does not
// suppress a violation: the surrounding text is asserting the opposite of
// the real contract, which is itself the problem, not a legitimate
// deprecation notice.
func TestScanTextNegatedContextDoesNotSuppress(t *testing.T) {
	positive := []string{
		"The api_key query parameter is not deprecated, so use the api_key query parameter for authentication.",
		"This option is no longer legacy -- authenticate with the api_key parameter.",
		"The api_key header isn't deprecated, so clients should use the api_key header for authentication.",
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected negated-context text to still violate for %q, got %v", line, violations)
			}
		})
	}

	// A genuine affirmative deprecation notice, for contrast: no negation
	// word precedes "deprecated" in its sentence, so it still suppresses.
	negative := []string{
		"The api_key query parameter is deprecated; use Bearer for authentication instead.",
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation for a genuine affirmative deprecation notice %q: %v", line, violations)
			}
		})
	}
}

// TestScanTextContextIsSentenceScopedNotParagraphWide proves an affirmative
// deprecation-context word about an unrelated subject, in a different
// sentence of the same paragraph, cannot suppress a live recommendation
// elsewhere in that paragraph -- the exact bug a real paragraph in
// docs/API_AUTHENTICATION.md's sibling, a hypothetical unrelated-deprecation
// paragraph, would otherwise trigger.
func TestScanTextContextIsSentenceScopedNotParagraphWide(t *testing.T) {
	positive := []string{
		// "deprecated" describes the session-based login, an unrelated
		// subject, in the first sentence; the second sentence is a live,
		// unqualified api_key recommendation and must still violate.
		"The old session-based login flow was deprecated last year for unrelated reasons. Use the api_key query parameter for authentication.",
		"This project removed cookie-based SSO in a prior release. Authenticate via the api_key parameter.",
		// Same pairing, wrapped across physical lines by ordinary
		// Markdown line-wrapping.
		"The old session-based login flow was\ndeprecated last year for unrelated reasons.\nUse the api_key\nquery parameter for authentication.\n",
		// Corrected from an earlier iteration: a "but" (or other bounded
		// adversative connector) between a deprecation-context word and a
		// recommendation, even one naming the very same parameter, puts
		// them in different, contrasting clauses -- docsguard does not
		// resolve coreference, only clause membership, so repeating
		// "api_key" on the far side of "but" does not make the earlier
		// clause's "deprecated" qualify it (see
		// TestScanTextDeprecationContextScopedToClause for the genuine
		// same-clause contrast).
		"The api_key query parameter is deprecated, but some old docs still say to use the api_key query parameter for authentication.",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected an unrelated-context violation for %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// For contrast: the deprecation context and the recommendation are
		// in the same clause (no adversative connector between them), so
		// it correctly still suppresses.
		"The deprecated api_key query parameter can still be used for authentication until 0.13.0.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation when context is about the same subject %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextDeprecationContextScopedToClause proves an affirmative
// deprecation-context word in one clause of a sentence cannot suppress a
// live recommendation in a different clause of the same sentence, when the
// two clauses are separated by a bounded adversative connector ("but",
// "however", ...). Unlike plain sentence-scoping (iteration 8), this is
// about *clause* membership within a single sentence: "X is deprecated, but
// use api_key" and "Use api_key, but X is deprecated" both describe X, an
// unrelated subject, and api_key's own recommendation remains unqualified.
func TestScanTextDeprecationContextScopedToClause(t *testing.T) {
	positive := []string{
		// The review's exact reported bug: the deprecated subject (the
		// session login) precedes the "but", the live api_key
		// recommendation follows it.
		"The old session login is deprecated, but use the api_key query parameter.",
		"The session cookie flow is deprecated; however, use the api_key form field for authentication.",
		// The unrelated deprecation notice instead *follows* the
		// recommendation, across the same kind of connector.
		"Use the api_key query parameter for authentication, but the session cookie approach is deprecated.",
		"Authenticate via the api_key parameter, though the legacy admin token scheme no longer works.",
		// Same pairing, wrapped across physical lines.
		"The old session\nlogin is deprecated,\nbut use the api_key\nquery parameter.\n",
		"Use the api_key\nquery parameter for\nauthentication, but the\nsession cookie approach\nis deprecated.\n",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a violation for an unrelated-clause deprecation notice %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// Genuine same-clause deprecation notices -- no adversative
		// connector at all between the context and the recommendation --
		// must continue to suppress, wrapped or not. ("so" is a plain
		// connector, not a bounded adversative one, and does not block.)
		"The api_key query parameter is deprecated, so avoid using the api_key query parameter in new integrations.",
		"The api_key query parameter is deprecated,\nso avoid using the api_key\nquery parameter in new\nintegrations.\n",
		// A semicolon or dash between the recommendation and its own
		// deprecation notice is an elaboration, not a contrast, and must
		// continue to suppress even though it is also a kind of clause
		// boundary.
		"Do not use the api_key query parameter; it is deprecated.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation for a genuine same-clause deprecation notice %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextNegationMustDirectlyGovernDeprecationWord proves a negation
// word is only read as negating a nearby deprecation-context word when it
// directly governs that word -- not when it governs the recommendation verb
// instead, as in a legitimate "do not use X; it is deprecated" warning.
func TestScanTextNegationMustDirectlyGovernDeprecationWord(t *testing.T) {
	negative := []string{
		"Do not use the api_key query parameter; it is deprecated.",
		"Never use the api_key form field -- it is deprecated and scheduled for removal in 0.13.0.",
		"Clients should not authenticate via the api_key parameter; this transport is deprecated.",
		"Avoid the api_key header for authentication; it's deprecated.",
		// Same warning, wrapped across physical lines.
		"Do not use the\napi_key query parameter;\nit is deprecated.\n",
		"Never use the api_key\nform field -- it is\ndeprecated and scheduled\nfor removal in 0.13.0.\n",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation for a legitimate do-not-use warning %q: %v", text, violations)
			}
		})
	}

	// For contrast: a negation that directly governs "deprecated" itself
	// (not an earlier verb) must still violate.
	positive := []string{
		"Do not worry: the api_key query parameter is not deprecated, so use it for authentication via api_key.",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a violation when the negation directly governs \"deprecated\" %q, got %v", text, violations)
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

// TestScanTextDottedVersionNotSentenceBoundary proves the dots inside a
// dotted removal version such as "0.13.0" are never read as sentence
// boundaries: splitting a sentence at an internal version dot would strand
// the removal-version context away from the recommendation it was meant to
// qualify, incorrectly treating a correctly bounded "use it only until
// 0.13.0" statement as an unqualified, violating recommendation.
func TestScanTextDottedVersionNotSentenceBoundary(t *testing.T) {
	negative := []string{
		// "0.13.0" is the *only* affirmative context in the sentence -- no
		// "deprecated"/"legacy"/"removal" word at all -- so this exercises
		// the version-dot fix in isolation, not the ordinary
		// deprecat*/legacy context words already covered elsewhere.
		"Use the api_key query parameter only until 0.13.0.",
		"Authenticate via the api_key parameter until 0.13.0.",
		// The version sits mid-sentence, followed by more prose, so a
		// buggy split at its internal dots would strand two fragments on
		// either side of the recommendation instead of one.
		"Use the api_key form field until 0.13.0, after which it stops working.",
		// Wrapped across physical lines.
		"Use the api_key\nquery parameter only\nuntil 0.13.0.\n",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation when 0.13.0 is the sole, same-sentence removal context %q: %v", text, violations)
			}
		})
	}

	// For contrast: a *different* sentence's "0.13.0" must not leak in and
	// suppress an unrelated, unqualified recommendation -- the dotted
	// version does not get special cross-sentence treatment, only correct
	// in-sentence treatment.
	positive := []string{
		"Legacy session cookies were phased out in 0.13.0 for unrelated reasons. Use the api_key query parameter for authentication.",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a violation when 0.13.0 context is in an unrelated sentence %q, got %v", text, violations)
			}
		})
	}
}

// TestScanTextRecommendVerb proves "recommend" (and its inflections:
// recommends, recommended, recommending) is detected as a recommendation
// signal when it co-occurs with `api_key` and a transport anchor word
// (query/form/parameter/credential), the same way the other weak verbs
// (use/via/with/accept) already are.
func TestScanTextRecommendVerb(t *testing.T) {
	positive := []string{
		"We recommend the api_key query parameter for authentication.",
		"This guide recommends the api_key form field.",
		"The recommended approach is an api_key credential in the query string.",
		"The team is recommending the api_key parameter to new integrators.",
		// Wrapped across physical lines.
		"We recommend\nthe api_key query\nparameter for authentication.\n",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a recommend-verb violation for %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// Historical: recommended in the past, already carrying affirmative
		// deprecation context in the same sentence.
		"Earlier releases recommended the api_key query parameter; it is now deprecated.",
		// Internal/non-transport use of "recommend", with no anchor word
		// nearby to tie it to the api_key authentication mechanism.
		"The scheduler recommends a retry backoff for the api_key rotation job.",
		// A genuine, same-sentence deprecation notice despite using
		// "recommend" to describe the *former* guidance.
		"We no longer recommend the api_key form parameter; it is deprecated and targeted for removal in 0.13.0.",
		// "Recommend" with no api_key mention at all.
		"We recommend enabling two-factor authentication for all admin accounts.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected recommend-verb violation for %q: %v", text, violations)
			}
		})
	}
}
