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

// TestScanTextBearerExactness proves rawAuthorization requires the
// runtime's exact literal prefix -- "Bearer" followed by one space,
// followed by a non-empty credential token -- not merely the scheme word
// "Bearer" appearing somewhere on the line. The real extractor's
// strings.TrimPrefix(authorization, "Bearer ") only strips that exact byte
// sequence: a tab or a second space instead of the one canonical space
// leaves the whole original string (including the word "Bearer" itself, or
// the extra whitespace) as the "credential" value, and no token at all
// after "Bearer " leaves an empty one -- neither is a working canonical
// example, even though both superficially look like one.
func TestScanTextBearerExactness(t *testing.T) {
	positive := []string{
		// A tab between "Bearer" and the token: strings.TrimPrefix would
		// not strip anything (it requires the exact "Bearer " substring),
		// so the real extracted value would be the literal string
		// "Bearer\tTOKEN", not a working credential.
		"curl -H \"Authorization: Bearer\tTOKEN\" https://gophishfr.example/api/campaigns/42/complete",
		// Two spaces instead of one: strings.TrimPrefix still strips
		// exactly "Bearer " (one space), leaving a leading space in the
		// extracted value, which would not match any real stored key.
		`curl -H "Authorization: Bearer  TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		// "Bearer " followed by nothing at all: the extracted credential
		// would be empty.
		`curl -H "Authorization: Bearer " https://gophishfr.example/api/campaigns/42/complete`,
		// The scheme word alone, with no separator or token whatsoever.
		`curl -H "Authorization: Bearer" https://gophishfr.example/api/campaigns/42/complete`,
	}
	for _, line := range positive {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
			if !containsKind(violations, KindRawAuthorization) {
				t.Fatalf("expected a raw-authorization violation for non-canonical Bearer spacing %q, got %v", line, violations)
			}
		})
	}

	negative := []string{
		// The runtime's exact canonical form: "Bearer", one space, a
		// non-empty token -- must not be flagged.
		`curl -H "Authorization: Bearer REPLACE_WITH_YOUR_TOKEN" https://gophishfr.example/api/campaigns/42/complete`, // gitleaks:allow -- synthetic placeholder, not a real token
		// Ordinary prose naming the header/scheme generically, inside an
		// inline code span with the scheme word immediately closed by
		// its backtick and nothing else inside -- the exact real-world
		// shape this repository's own canonical documentation uses (a
		// table cell describing the authentication mechanism, not a
		// worked example). Must not be mistaken for an attempted-but-
		// broken example with a missing token.
		"| Authentication | API key, as the canonical `Authorization: Bearer` header |",
	}
	for _, line := range negative {
		t.Run(line, func(t *testing.T) {
			violations := mustScan(t, line)
			if containsKind(violations, KindRawAuthorization) {
				t.Fatalf("unexpected raw-authorization violation for a canonical Bearer example %q: %v", line, violations)
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
		// "0.13.0"'s internal dots must not be read as sentence
		// boundaries, stranding the removal-version context away from the
		// recommendation it qualifies. A bare version number is not
		// sufficient context on its own (see
		// TestScanTextBareVersionRequiresConstruction), so each of these
		// also pairs it with an explicit removal/deprecation construction
		// word, keeping the focus here on the dot-splitting fix itself.
		"Use the api_key query parameter only until it is removed in 0.13.0.",
		"Authenticate via the api_key parameter until it is deprecated in 0.13.0.",
		// The version sits mid-sentence, followed by more prose, so a
		// buggy split at its internal dots would strand two fragments on
		// either side of the recommendation instead of one.
		"Use the api_key form field until it is removed in 0.13.0, after which it stops working.",
		// Wrapped across physical lines.
		"Use the api_key\nquery parameter only\nuntil it is removed\nin 0.13.0.\n",
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

// TestScanTextBareVersionRequiresConstruction proves a bare mention of the
// removal version number, "0.13.0", with no accompanying
// deprecation/removal construction word, is not by itself sufficient
// affirmative context: a sentence can mention the version number for an
// entirely unrelated reason (a release note, a changelog entry) without
// asserting that api_key itself is deprecated.
func TestScanTextBareVersionRequiresConstruction(t *testing.T) {
	positive := []string{
		// "0.13.0" is mentioned, but nothing nearby says it is being
		// deprecated or removed -- just that it exists as a release.
		"Use the api_key query parameter; see the 0.13.0 release notes for details.",
		"Use the api_key query parameter, part of the 0.13.0 release.",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a violation when 0.13.0 has no accompanying construction word %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// "0.13.0" accompanied by an explicit construction word ("removed",
		// "deprecated") continues to count as affirmative context.
		"Use the api_key query parameter only until it is removed in 0.13.0.",
		"The api_key query parameter is deprecated and targeted for removal in 0.13.0.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation when 0.13.0 has an accompanying construction word %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextTransportMismatchDoesNotSuppress proves a deprecation notice
// about one specific api_key transport (form, raw, or a response/body
// field) cannot suppress a live recommendation of a *different* transport
// (query): docsguard binds affirmative context to the specific transport
// the recommendation names, not merely "some api_key mention nearby".
func TestScanTextTransportMismatchDoesNotSuppress(t *testing.T) {
	positive := []string{
		// A deprecated form parameter must not qualify a live, separate
		// query-parameter recommendation in the same sentence.
		"The api_key form parameter is deprecated; use the api_key query parameter instead.",
		// A deprecated raw Authorization header must not qualify a live
		// query-parameter recommendation.
		"The raw api_key Authorization header is deprecated; use the api_key query parameter for authentication.",
		// A deprecated response/body field must not qualify a live
		// query-parameter recommendation -- a different kind of api_key
		// mention entirely, not a request transport at all.
		"The api_key response field is deprecated; use the api_key query parameter for authentication.",
		// Same mismatch, wrapped across physical lines.
		"The api_key form\nparameter is deprecated;\nuse the api_key query\nparameter instead.\n",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a violation when the context names a different transport %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// The deprecation notice and the recommendation name the *same*
		// transport, so it correctly still suppresses.
		"The api_key query parameter is deprecated; use the api_key query parameter only for legacy integrations.",
		"The api_key form parameter is deprecated; use the api_key form parameter only for legacy integrations.",
		// Neither side names a specific transport (ambiguous on both
		// sides), so the prior, transport-agnostic behavior applies.
		"The api_key parameter is deprecated; use the api_key parameter only for legacy integrations.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation when the context names the same transport %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextCompactMixedTransportDirectional proves transportKindNear's
// directional, occurrence-specific word-distance binding (as opposed to a
// flat, symmetric character window) correctly classifies a compact
// sentence naming two transports close together with no anchor word like
// "parameter" to space them out, and that a genuinely ambiguous
// classification -- two different transport words exactly tied for
// nearest to the same `api_key` mention -- fails safe and never suppresses,
// even though the recommendation's own transport is unambiguous.
func TestScanTextCompactMixedTransportDirectional(t *testing.T) {
	positive := []string{
		// The reviewer's exact compact example: no "parameter" anchor
		// word after either transport word.
		"api_key form deprecated; use api_key query",
		// Same compact mismatch inside a single table row.
		"| Note | api_key form is deprecated; use api_key query instead. |",
		// A genuine tie: "raw" and "form" are both immediately adjacent to
		// the *first* api_key occurrence (one directly before, one
		// directly after, at the same distance), so that occurrence's own
		// transport cannot be determined at all -- this must fail safe
		// and never suppress, even though the recommendation's own
		// occurrence ("query") is perfectly unambiguous.
		"The raw api_key form field is deprecated; use the api_key query parameter instead.",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a violation for the compact mismatch %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// Preserve a natural, accepted warning with no distinguishing
		// transport word at all on either side.
		"Do not use the api_key parameter; it is deprecated.",
		// Preserve the same-transport compact case: both sides are
		// unambiguously "query", so this correctly still suppresses.
		"api_key query deprecated; use api_key query only for legacy integrations.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation for %q: %v", text, violations)
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

// TestScanTextRenderedEscapeNormalization proves a Markdown-source escape
// that a renderer (GitHub's included) turns back into a literal character --
// a CommonMark backslash escape or an HTML numeric/named character
// reference -- cannot be used to hide a functional deprecated-transport
// example from this literal scanner: `api\_key=TOKEN`, `api&#95;key=TOKEN`,
// and `Authorization&#58; TOKEN` must all be caught exactly like their plain
// forms, across every anchor this package protects (api_key's underscore,
// Authorization's colon, and the `=`/`&` of query/form syntax), not just the
// three examples named in the finding that prompted this fixture set.
func TestScanTextRenderedEscapeNormalization(t *testing.T) {
	positive := []struct {
		text string
		kind Kind
	}{
		// Backslash-escaped underscore and equals in a parameter credential.
		{`curl -d "api\_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		{`curl -d "api_key\=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		// Named HTML character references for the underscore, in both
		// recognized spellings.
		{`curl "https://gophishfr.example/api/groups/summary?api&lowbar;key=TOKEN"`, KindParameterCredential},
		{`curl "https://gophishfr.example/api/groups/summary?api&UnderBar;key=TOKEN"`, KindParameterCredential},
		// Decimal and hexadecimal numeric character references for the
		// underscore, including both cases of the hex prefix and digits.
		{`curl "https://gophishfr.example/api/groups/summary?api&#95;key=TOKEN"`, KindParameterCredential},
		{`curl "https://gophishfr.example/api/groups/summary?api&#x5f;key=TOKEN"`, KindParameterCredential},
		{`curl "https://gophishfr.example/api/groups/summary?api&#X5F;key=TOKEN"`, KindParameterCredential},
		{`curl "https://gophishfr.example/api/groups/summary?api&#x5F;key=TOKEN"`, KindParameterCredential},
		{`curl "https://gophishfr.example/api/groups/summary?api&#X5f;key=TOKEN"`, KindParameterCredential},
		// An HTML-encoded query-string ampersand joining an otherwise
		// plain api_key parameter must not block detection of it.
		{`curl "https://gophishfr.example/api/groups/?other=1&amp;api_key=TOKEN"`, KindParameterCredential},
		// Backslash and character-reference escapes combined in the same
		// token, proving the normalization order handles both.
		{`curl -d "api&#95;key\=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		// Authorization's colon, escaped three ways.
		{`curl -H "Authorization\: TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{`curl -H "Authorization&colon; TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{`curl -H "Authorization&#58; TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{`curl -H "Authorization&#x3a; TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{`curl -H "Authorization&#X3A; TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		// Recommendation prose and a table row, each with an escaped
		// api_key, proving the normalization also protects the
		// documentation-accuracy checks, not just the credential-syntax
		// ones.
		{`Use the api\_key query parameter for authentication.`, KindUndeprecatedParameterMention},
		{`Use the api&#95;key query parameter for authentication.`, KindUndeprecatedParameterMention},
		{`| Auth | api\_key |`, KindUndeprecatedParameterMention},
	}
	for _, c := range positive {
		t.Run(c.text, func(t *testing.T) {
			violations := mustScan(t, c.text)
			if !containsKind(violations, c.kind) {
				t.Fatalf("expected a %s violation for %q, got %v", c.kind, c.text, violations)
			}
		})
	}

	negative := []string{
		// An unrecognized named entity (not in the narrow table) must be
		// left exactly as written, not decoded into something unexpected.
		"Price: &euro;100 or &pound;80, see api_key docs for details.",
		// A genuine affirmative deprecation notice that happens to also
		// contain an unrelated named entity must still suppress.
		"The `api_key` query parameter is deprecated &mdash; see the migration guide.",
		// A backslash preceding a non-punctuation character (not in
		// CommonMark's escapable set) must be left untouched.
		`Use \n for newlines, not the api_key parameter, which is deprecated.`,
		// A backslash-escaped character that decodes to something with no
		// relationship to api_key at all.
		"The regex uses `a\\.b` to match a literal dot, unrelated to api_key naming.",
		// A numeric character reference that decodes to a character with
		// no relationship to api_key.
		"Use &#65; as a placeholder value; it has nothing to do with api_key.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if len(violations) != 0 {
				t.Fatalf("unexpected violation(s) for %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextContextBoundToTransportSyntax proves a deprecation-context
// word ("migration", "removal", "legacy", ...) only counts as affirmative
// context when it is close enough to an actual `api_key` mention to be
// describing that transport specifically -- not merely sharing a sentence
// or clause with an unrelated subject's own deprecation notice.
func TestScanTextContextBoundToTransportSyntax(t *testing.T) {
	positive := []string{
		// "legacy"/"migration"/"removal" are all present, but describe an
		// unrelated billing system, far enough from the live api_key
		// recommendation to not qualify it.
		"Use the api_key query parameter for authentication purposes in production environments; our legacy billing system also needs migration, with removal of old servers scheduled separately.",
		// Table row variant: the row recommends api_key with no close
		// context; the deprecation-sounding words describe something else
		// entirely, much further along in the same cell.
		"| `api_key` query parameter | see the separate, fully unrelated legacy billing migration and removal notes for other systems elsewhere |",
		// Wrapped across physical lines.
		"Use the api_key query\nparameter for authentication purposes\nin production environments; our\nlegacy billing system also\nneeds migration, with removal\nof old servers scheduled separately.\n",
	}
	for _, text := range positive {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if !containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("expected a violation when context describes an unrelated subject %q, got %v", text, violations)
			}
		})
	}

	negative := []string{
		// "migration" sits close enough to a live-looking api_key mention
		// to genuinely describe it, so it correctly still suppresses.
		"Use the api_key query parameter; see the migration guide for the removal timeline.",
		// A genuine, immediately adjacent deprecation notice.
		"Use the api_key query parameter for authentication; it is deprecated and targeted for removal in 0.13.0, see the migration guide.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if containsKind(violations, KindUndeprecatedParameterMention) {
				t.Fatalf("unexpected violation for a genuinely close deprecation notice %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextCodeAwareNormalization proves rendering normalization (see
// normalizeRenderedEscapes) only applies where CommonMark would actually
// decode an escape or character reference: never inside an inline code
// span, a fenced code block (backtick or tilde, with or without an info
// string), or an indented code block. A literal forbidden example in any of
// those must still be caught; a backslash-escaped or character-referenced
// one -- which renders completely literally there, not as a working
// credential -- must not be.
func TestScanTextCodeAwareNormalization(t *testing.T) {
	positive := []struct {
		text string
		kind Kind
	}{
		// A literal example inside a single-backtick inline code span.
		{"`curl -d \"api_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete`", KindParameterCredential},
		// A literal example inside a triple-backtick fenced code block.
		{"```\n" +
			"curl -d \"api_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
			"```\n", KindParameterCredential},
		// A literal example inside a tilde-fenced code block with an info
		// string.
		{"~~~bash\n" +
			"curl -H \"Authorization: TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
			"~~~\n", KindRawAuthorization},
		// A literal example inside a backtick-fenced block with an info
		// string.
		{"```bash\n" +
			"curl -d \"api_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
			"```\n", KindParameterCredential},
		// A literal example inside a 4-space indented code block.
		{"    curl -d \"api_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n", KindParameterCredential},
		// Exactly 3 spaces of indentation is *not* an indented code block
		// per CommonMark, so this line is still normalized as ordinary
		// text; included here as a positive case because the escaped form
		// below (4 spaces) is the matching negative for the same text.
		{"   Use the api\\_key parameter for authentication.", KindUndeprecatedParameterMention},
	}
	for _, c := range positive {
		t.Run(c.text, func(t *testing.T) {
			violations := mustScan(t, c.text)
			if !containsKind(violations, c.kind) {
				t.Fatalf("expected a %s violation for %q, got %v", c.kind, c.text, violations)
			}
		})
	}

	negative := []string{
		// Backslash-escaped and HTML-entity forms inside a single-backtick
		// inline code span: CommonMark renders these completely literally,
		// so they never actually decode into a working credential.
		"`curl -d \"api\\_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete`",
		"`curl -d \"api&#95;key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete`",
		"`curl -H \"Authorization\\: TOKEN\" https://gophishfr.example/api/campaigns/42/complete`",
		// The same two forms inside recommendation prose, wrapped in a
		// code span: the paragraph never actually contains an unescaped
		// "api_key" substring, so this must not even reach the
		// recommendation check.
		"Use the `api\\_key` query parameter for authentication.",
		"Use the `api&#95;key` query parameter for authentication.",
		// Backslash-escaped and HTML-entity forms inside a fenced code
		// block (the content line, not the fence delimiters).
		"```\n" +
			"curl -d \"api\\_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
			"```\n",
		"```bash\n" +
			"curl -d \"api&#95;key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
			"```\n",
		"~~~\n" +
			"curl -H \"Authorization&#58; TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n" +
			"~~~\n",
		// Backslash-escaped form inside a 4-space indented code block.
		"    curl -d \"api\\_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if len(violations) != 0 {
				t.Fatalf("unexpected violation(s) for a non-rendering encoding inside code %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextRenderedMarkupSplit proves a Markdown emphasis delimiter or a
// safe inline HTML tag that splits one of this package's protected anchors
// -- `api<em>_</em>key=TOKEN`, `Authorization<em>:</em> TOKEN`, "Use the
// api<em>_</em>key query parameter..." -- is caught the same way the
// unsplit form is, across the parameter-credential, raw-authorization,
// table-row, and recommendation-prose checks, while unrelated or malformed
// markup never creates a false positive.
func TestScanTextRenderedMarkupSplit(t *testing.T) {
	positive := []struct {
		text string
		kind Kind
	}{
		// The exact examples from the finding, across every anchor this
		// package protects.
		{`curl -d "api<em>_</em>key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		{`curl -H "Authorization<em>:</em> TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{"Use the api<em>_</em>key query parameter for authentication.", KindUndeprecatedParameterMention},
		{"| Auth | api<em>_</em>key |", KindUndeprecatedParameterMention},
		// Other safe inline tags on the allow-list, and a case variant.
		{`curl -d "api<b>_</b>key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		{`curl -H "Authorization<span>:</span> TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{`curl -d "api<EM>_</EM>key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		// A tag with an attribute.
		{`curl -d "api<em class='x'>_</em>key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		// Markdown emphasis/strong/strikethrough delimiters, each wrapping
		// exactly the protected punctuation character.
		{`curl -d "api*_*key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		{`curl -H "Authorization**:** TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{`curl -H "Authorization_:_ TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindRawAuthorization},
		{`curl -d "api~_~key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		// Mismatched delimiter run lengths on each side.
		{`curl -d "api**_*key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		// Combinations: a safe tag wrapping emphasis-wrapped content, and a
		// safe tag wrapping an HTML character reference.
		{`curl -d "api<em>*_*</em>key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
		{`curl -d "api<em>&#95;</em>key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`, KindParameterCredential},
	}
	for _, c := range positive {
		t.Run(c.text, func(t *testing.T) {
			violations := mustScan(t, c.text)
			if !containsKind(violations, c.kind) {
				t.Fatalf("expected a %s violation for %q, got %v", c.kind, c.text, violations)
			}
		})
	}

	negative := []string{
		// An unrelated, non-whitelisted tag must not be touched, and is
		// harmless regardless since nothing here relates to api_key.
		"Press <kbd>Ctrl</kbd> to copy the value.",
		// A table row using only a non-whitelisted tag, no api_key at all.
		"| Shortcut | <kbd>Ctrl</kbd>+<kbd>C</kbd> |",
		// Malformed/unmatched emphasis: a single opening asterisk with no
		// matching close anywhere nearby renders as a *literal* asterisk
		// character in CommonMark, not as emphasis, so the text never
		// actually renders as "api_key".
		`curl -d "api*_key=TOKEN" https://gophishfr.example/api/campaigns/42/complete`,
		// Ordinary emphasis around a whole, unrelated word must not be
		// touched (it wraps more than the single protected-character shape
		// this package recognizes, and has nothing to do with api_key
		// regardless).
		"This is *emphasized* text, not a credential example.",
		// A tag outside the allow-list, unrelated to api_key.
		"Avoid embedding <script> tags in documentation examples.",
	}
	for _, text := range negative {
		t.Run(text, func(t *testing.T) {
			violations := mustScan(t, text)
			if len(violations) != 0 {
				t.Fatalf("unexpected violation(s) for %q: %v", text, violations)
			}
		})
	}
}

// TestScanTextHTMLBlockVisibleText proves a raw HTML block (a line
// starting with a tag, continuing per CommonMark's own closing rule for
// its kind) is scanned for only its *visible* rendered text (see
// html.go): a credential or recommendation inside ordinary element content
// (<div>, <p>, <pre>, nested/multi-line tags) violates exactly like it
// would in a paragraph, an HTML character reference inside one decodes
// (see the "HTML-block entity decoding" finding this responds to), while
// one that only ever appears in an HTML comment, an attribute value, or
// <script>/<style> content -- none of which ever renders as visible text
// -- does not, and malformed markup fails closed toward scanning rather
// than silently away from it.
func TestScanTextHTMLBlockVisibleText(t *testing.T) {
	positive := []struct {
		name string
		text string
		kind Kind
	}{
		{
			"block div with a credential",
			"<div>api_key=TOKEN</div>\n",
			KindParameterCredential,
		},
		{
			"block p with a recommendation",
			"<p>Use the api_key query parameter for authentication.</p>\n",
			KindUndeprecatedParameterMention,
		},
		{
			"pre renders its content as visible text",
			"<pre>api_key=TOKEN</pre>\n",
			KindParameterCredential,
		},
		{
			"multi-line nested tags",
			"<div>\n  <span>Use the api_key query parameter for authentication.</span>\n</div>\n",
			KindUndeprecatedParameterMention,
		},
		{
			// The underscore arrives only as a decoded HTML entity: a
			// browser parsing this raw HTML block resolves it to "_"
			// regardless of Markdown, so the visible text is
			// "api_key=TOKEN", a working credential.
			"HTML entity inside a block decodes",
			"<div>api&#95;key=TOKEN</div>\n",
			KindParameterCredential,
		},
		{
			// An unterminated attribute value is malformed: this package
			// cannot confidently skip the tag at all, so it fails closed
			// by keeping the remainder -- including the credential that
			// happens to appear where an attribute value would otherwise
			// have hidden it -- as ordinary visible text instead of
			// silently excluding it.
			"malformed/unterminated tag fails closed toward scanning",
			"<div data-example=\"unterminated api_key=TOKEN\n",
			KindParameterCredential,
		},
	}
	for _, c := range positive {
		t.Run(c.name, func(t *testing.T) {
			violations := mustScan(t, c.text)
			if !containsKind(violations, c.kind) {
				t.Fatalf("expected a %s violation for %q, got %v", c.kind, c.text, violations)
			}
		})
	}

	negative := []struct {
		name string
		text string
	}{
		{
			"HTML comment never renders as visible text",
			"<!-- api_key=TOKEN -->\n",
		},
		{
			"attribute value never renders as visible text",
			"<div data-example=\"api_key=TOKEN\">Just a label.</div>\n",
		},
		{
			"script content never renders as visible text",
			"<script>var api_key = \"TOKEN\";</script>\n",
		},
		{
			"style content never renders as visible text",
			"<style>/* api_key=TOKEN */</style>\n",
		},
		{
			"script tag case-insensitive and with an attribute",
			"<SCRIPT type=\"text/javascript\">api_key=TOKEN;</SCRIPT>\n",
		},
		{
			"a processing instruction never renders as visible text",
			"<?xml-stylesheet api_key=\"TOKEN\" ?>\n",
		},
		{
			"a declaration never renders as visible text",
			"<!DOCTYPE api_key=\"TOKEN\">\n",
		},
		{
			"a CDATA section never renders as visible text",
			"<![CDATA[api_key=TOKEN]]>\n",
		},
		{
			"ordinary visible text around the block, unrelated to api_key",
			"<div>Just an ordinary note.</div>\n",
		},
		{
			// An entity that decodes to something unrelated to any
			// protected anchor must not create a false positive.
			"unrecognized/unrelated entity inside a block",
			"<div>Price: &euro;100, see the docs.</div>\n",
		},
	}
	for _, c := range negative {
		t.Run(c.name, func(t *testing.T) {
			violations := mustScan(t, c.text)
			if len(violations) != 0 {
				t.Fatalf("unexpected violation(s) for %q: %v", c.text, violations)
			}
		})
	}
}

// TestScanTextSoftWrappedAuthorization proves a raw Authorization header
// example is still detected when an ordinary Markdown soft line break
// separates the header name from its value -- "Authorization:\nTOKEN"
// renders as "Authorization: TOKEN" -- both in ordinary prose and inside a
// raw HTML block's collapsible (non-<pre>/<code>) visible text, while an
// unwrapped header on one line is still reported exactly once, two
// unrelated paragraphs each containing only half of the header are not
// wrongly merged, and an ordinary multi-line form/query credential example
// is unaffected.
func TestScanTextSoftWrappedAuthorization(t *testing.T) {
	t.Run("soft line break between header name and value in prose", func(t *testing.T) {
		text := "Authorization:\nTOKEN\n"
		violations := mustScan(t, text)
		if !containsKind(violations, KindRawAuthorization) {
			t.Fatalf("expected a raw-authorization violation for %q, got %v", text, violations)
		}
	})

	t.Run("soft line break inside an HTML block's visible text", func(t *testing.T) {
		text := "<div>\nAuthorization:\nTOKEN\n</div>\n"
		violations := mustScan(t, text)
		if !containsKind(violations, KindRawAuthorization) {
			t.Fatalf("expected a raw-authorization violation for %q, got %v", text, violations)
		}
	})

	t.Run("unwrapped header on one line is reported exactly once", func(t *testing.T) {
		text := "Authorization: TOKEN\n"
		violations := mustScan(t, text)
		count := 0
		for _, v := range violations {
			if v.Kind == KindRawAuthorization {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly one raw-authorization violation for %q, got %d: %v", text, count, violations)
		}
	})

	t.Run("two unrelated paragraphs are not merged across the boundary", func(t *testing.T) {
		text := "This sentence happens to end with the word Authorization:\n\nTOKEN is a separate, unrelated paragraph about something else entirely.\n"
		violations := mustScan(t, text)
		if containsKind(violations, KindRawAuthorization) {
			t.Fatalf("unexpected raw-authorization violation merging across a blank-line block boundary for %q: %v", text, violations)
		}
	})

	t.Run("ordinary multi-line form/query example is unaffected", func(t *testing.T) {
		text := "```\ncurl -d \"api_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n```\n"
		violations := mustScan(t, text)
		if !containsKind(violations, KindParameterCredential) {
			t.Fatalf("expected a parameter-credential violation for %q, got %v", text, violations)
		}
	})
}

// TestScanTextHTMLPreCodeWhitespaceExplicit proves <pre>/<code> content
// inside a raw HTML block is visible text like any other element's (so a
// credential example inside one is still caught per line), but its
// whitespace is *never* collapsed across lines for detection purposes --
// matching how a real browser preserves <pre>/<code>'s own line breaks
// visually instead of collapsing them into a single space the way it does
// for ordinary flowed HTML text. An Authorization header deliberately
// split across <pre>'s own line breaks is therefore correctly *not*
// treated as if it were on one line, the same explicit choice this
// package already makes for an ordinary fenced/indented code block.
func TestScanTextHTMLPreCodeWhitespaceExplicit(t *testing.T) {
	t.Run("a credential wholly on one pre line is still caught", func(t *testing.T) {
		text := "<pre>\ncurl -d \"api_key=TOKEN\" https://gophishfr.example/api/campaigns/42/complete\n</pre>\n"
		violations := mustScan(t, text)
		if !containsKind(violations, KindParameterCredential) {
			t.Fatalf("expected a parameter-credential violation for %q, got %v", text, violations)
		}
	})

	t.Run("an Authorization header split across pre's own line breaks is not joined", func(t *testing.T) {
		// Unlike the ordinary-prose and ordinary-HTML-text soft-wrap
		// cases (see TestScanTextSoftWrappedAuthorization), <pre>
		// preserves this literal line break visually, so this is not
		// read as "Authorization: TOKEN" on one line.
		text := "<pre>\nAuthorization:\nTOKEN\n</pre>\n"
		violations := mustScan(t, text)
		if containsKind(violations, KindRawAuthorization) {
			t.Fatalf("unexpected raw-authorization violation joining across pre's own line breaks for %q: %v", text, violations)
		}
	})

	t.Run("code behaves the same as pre", func(t *testing.T) {
		text := "<code>\nAuthorization:\nTOKEN\n</code>\n"
		violations := mustScan(t, text)
		if containsKind(violations, KindRawAuthorization) {
			t.Fatalf("unexpected raw-authorization violation joining across code's own line breaks for %q: %v", text, violations)
		}
	})
}
