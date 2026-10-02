# API-key transport deprecation self-review

This Builder review records the evidence for
`security/deprecate-legacy-api-key-transports`. The immutable acceptance
checklist in `.goals/deprecate-api-key-transports/goal.md` remains the
source of truth; this document groups the evidence by control area and has
been updated in place for each iteration rather than duplicated, so it always
describes the PR's current state.

## Scope of the change

This PR is documentation/test/CI-only. It touches:

- `docs/API_AUTHENTICATION.md` — marks query/form/raw as deprecated inline
  and points at the new deprecation guide instead of restating the full
  rationale in two places.
- `docs/GROUP_IMPORT_LIMITS.md` — the group-import authentication table row
  recommends `Authorization: Bearer` as canonical, marks the deprecated
  `api_key` query parameter (removal targeted for `0.13.0`), and states
  precisely — as of iteration 4 — that a multipart `api_key` form field is
  **not** read as a credential (`ParseForm` never parses multipart bodies),
  so this endpoint is effectively header- or query-authenticated only;
  links to the migration guide.
- `docs/API_KEY_TRANSPORT_DEPRECATION.md` — the canonical/deprecated
  contract table; old/new migration examples for query, form, and raw, all
  against the same real, executable business operation
  (`POST /api/campaigns/{id}/complete`); the exact `0.13.0` removal
  behaviour; leaked-key rotation and log-review guidance; and the explicit
  no-header/no-signal decision with rationale.
- `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md` — a concise,
  repository-conventional release-note artifact for the next GitHub Release.
- `middleware/api_auth_form_fields_test.go` (new) — the executable proof
  behind the migration guide's generic form template (added in iteration
  4): wraps the real `RequireAPIKey` middleware around a local handler and
  asserts unrelated `PostForm` business fields survive parsing and
  authentication byte-for-byte identical under both the deprecated form
  `api_key` transport and the canonical Bearer header.
- `controllers/api/campaign_transport_deprecation_test.go` (new) — the
  executable regression for the migration guide: it dispatches each
  deprecated transport and the canonical Bearer transport through the real
  API router (`Server.ServeHTTP`, so `RequireAPIKey` and the real mux route
  table both run) against `POST /api/campaigns/{id}/complete`, and asserts
  each one both authenticates and actually completes its campaign (reloaded
  from the database, not just a 200 status). A dedicated subtest proves a
  trailing-slash request to the same path does **not** match the route,
  which is the exact class of mistake that made the original form example
  non-executable.
- `internal/docsguard/` and `cmd/docsguard/` — the detection logic and CLI
  behind the documentation gate, plus their fixture-based table tests
  (`internal/docsguard/docsguard_test.go`, `cmd/docsguard/main_test.go`)
  covering every supported deprecated syntax (including, as of iteration 5,
  percent-encoded parameter names), every legitimate Authorization scheme
  this guard must leave alone (with Bearer itself now requiring the
  runtime's exact case-sensitive casing), every canonical-doc table row or
  alternative-option sentence that presents `api_key` as an ordinary option
  with no deprecation context, and scanner-error fail-closed behaviour.
  Both are `go test`-covered stdlib-only Go code; no new dependency.
- `scripts/verify-docs-canonical-examples.sh` — a thin wrapper that runs
  `go run ./cmd/docsguard` over tracked Markdown, with two explicit,
  auditable exemptions: `docs/API_KEY_TRANSPORT_DEPRECATION.md` (the
  migration guide, which shows deliberate old/new examples) and, as of
  iteration 6, this self-review document itself, which necessarily quotes
  and describes those same examples and the guard's own detection rules
  while recording review history. `scripts/verify.sh` wiring unchanged.
- `.github/workflows/ci.yml` — adds a `docs-guard` job that runs the same
  script as a required, blocking CI check (added to `ci-success`'s
  `needs:`), using the same pinned `actions/checkout`/`actions/setup-go` SHAs
  already used by every other job in this file; no permission, timeout, or
  pin change to any existing job.

No Go source outside `internal/docsguard`, `cmd/docsguard`, and the new
`controllers/api` test file changed; no middleware, controller handler,
model, schema, migration, frontend source/generated asset, `go.mod`,
`go.sum`, `package.json`, or `yarn.lock` changed. `VERSION` is unchanged
(`0.12.1`).

## Iteration 2: independent review findings and fixes

[`review-feedback-1.md`](../.goals/deprecate-api-key-transports/review-feedback-1.md)
returned a FAIL on iteration 1 with three findings. Each is addressed below.

### Finding 1 — form migration example was not executable

The iteration-1 form example mixed a credential demonstration (`api_key`
in a form body) with unrelated JSON business semantics (campaign creation),
which the real handler rejects outright regardless of authentication. Fixed
by replacing every migration example — query, form, and raw — with the same
real, existing, already-documented business operation:
`POST /api/campaigns/{id}/complete` (see "Campaign completion" in
`API_AUTHENTICATION.md`). That handler reads only the path's campaign ID and
the caller's identity and needs no request body, so it is usable, unmodified,
by every transport. The exact route, method, and absence of a trailing slash
are now stated explicitly in the guide, and
`controllers/api/campaign_transport_deprecation_test.go` executes all four
transports (query, form, raw, Bearer) and the trailing-slash negative case
through the real router, asserting each deprecated transport and its
canonical replacement both authenticate and both actually complete their
campaign.

### Finding 2 — documentation gate was incomplete

The iteration-1 grep gate missed several curl encodings, treated the
`Authorization` header name case-sensitively, and could have flagged a
legitimate non-API-key scheme. Fixed by replacing the gate's detection logic
with `internal/docsguard`, a small stdlib-only Go package (no new
dependency), with fixture-based table tests
(`internal/docsguard/docsguard_test.go`) covering, as positive cases: a query
`api_key` parameter, `curl -d`/`--data`/`--data-raw`/`--data-urlencode`/`-F`/
`--form` with an `api_key` value, and a standalone `api_key` assignment; and
as negative cases:
`Authorization: Bearer`, `Authorization: Basic`, `Authorization: Digest`,
`Authorization: Negotiate`, `Authorization: NTLM` (any letter-casing, since
HTTP header names are case-insensitive and the header name itself is matched
case-insensitively), and ordinary prose that merely mentions `api_key` or
`Authorization` without a literal credential example. `cmd/docsguard` is the
CLI wrapper, with its own test (`cmd/docsguard/main_test.go`) covering the
`-exempt` flag and every exit code. The exemption list is exactly one file
path, declared in one place
(`scripts/verify-docs-canonical-examples.sh`), and is otherwise unchanged
from iteration 1: narrow and auditable.

### Finding 3 — the gate was not enforced by CI

`scripts/verify.sh` called the gate, but `.github/workflows/ci.yml` never
did, so a PR could reintroduce a deprecated example while every CI-required
check stayed green. Fixed by adding a `docs-guard` job to `ci.yml` that runs
`./scripts/verify-docs-canonical-examples.sh`, using the same pinned
`actions/checkout`/`actions/setup-go` SHAs as every other job, the
repository's default minimal `contents: read` permissions (no elevation
needed), and a `timeout-minutes: 10`, and added to the `ci-success`
aggregating job's `needs:` list so it is a required, blocking check exactly
like `go-vet` or `go-build`. `actionlint` and `zizmor` both pass against the
updated workflow (see below). Local `scripts/verify.sh` integration is
unchanged.

## Iteration 3: independent review finding and fix

[`review-feedback-2.md`](../.goals/deprecate-api-key-transports/review-feedback-2.md)
returned a FAIL on iteration 2: `docs/GROUP_IMPORT_LIMITS.md` still listed
an `api_key` parameter as a normal, undeprecated-looking option, which
`internal/docsguard` did not catch because its
rules only recognized credential-syntax patterns (a literal `api_key`
assignment, raw `Authorization`), not a documentation table or sentence
that merely *names* `api_key` as a supported option.

### Finding — a canonical doc still advertised `api_key` as an ordinary option

Fixed in two parts:

1. **The doc itself.** `docs/GROUP_IMPORT_LIMITS.md`'s authentication table
   row now reads: `API key, as the canonical Authorization: Bearer header
   (an api_key query/form parameter is deprecated and targeted for removal
   in 0.13.0; see the migration guide)`, with a Markdown link to
   `API_KEY_TRANSPORT_DEPRECATION.md`.
2. **The gate.** `internal/docsguard` gained a new
   `KindUndeprecatedParameterMention` rule: a line is flagged if it mentions
   `api_key` (case-insensitive) and is either a Markdown table row (starts
   with `|`) or an "offered as an alternative" sentence (`... or an
   api_key ...`), *and* the same line carries no deprecation-context
   keyword (`deprecat`, `0.13.0`, `remov`, `sunset`, `migrat`, `legacy`).
   This is a regexp-based check, not a Markdown parser, and adds no
   dependency. Positive fixtures reproduce the exact original bug wording
   and three generalizations of it (prose, a bare table cell, and a
   different-casing variant); negative fixtures cover the fixed
   `GROUP_IMPORT_LIMITS.md` row, the deprecation guide's own contract table,
   plain prose that already says a transport is deprecated, and — the
   trickiest case — this guard's own meta-documentation describing its
   fixture categories, which mentions `api_key` and "parameter" together
   without "or a(n)" and without being a table row, and so correctly stays
   unflagged.

A repository-wide `grep -rn` search across every tracked `*.md` file (see
the Validation evidence below) confirmed `GROUP_IMPORT_LIMITS.md` was the
only canonical doc with this wording; `API_KEY_VERIFIER.md`'s mentions of
`api_key` describe an internal database column, not a client-facing
transport, and do not match the new rule.

## Iteration 4: independent review finding and fix

[`review-feedback-3.md`](../.goals/deprecate-api-key-transports/review-feedback-3.md)
returned a FAIL on iteration 3: the campaign-completion example is
executable precisely *because* its business semantics live in method/path
and its form body is credential-only — which proves transport
authentication equivalence, but not the separate migration rule that an
external client must remove only `api_key` from its own form body and
preserve every other field byte-for-byte. The review also asked for
precise language about Go's `ParseForm` scope and `/api/import/group`'s
actual (multipart, not form) authentication surface.

### Finding — the executable example couldn't demonstrate field preservation, and two technical claims needed precision

Fixed in four parts, all documentation/test only:

1. **Labelled the existing example's limits explicitly.** A new callout in
   `docs/API_KEY_TRANSPORT_DEPRECATION.md` states, before the query/form/raw
   examples, that the campaign-completion endpoint's business semantics live
   in its method and path, that its body is intentionally credential-only
   by design, and that this proves transport equivalence but not field
   preservation — pointing at the new generic template and middleware test
   for that separate claim. The "Form parameter" subsection repeats this
   inline.
2. **Added a separate, clearly-labelled generic migration template.** A new
   "Generic form migration template (illustrative only)" section states up
   front that no GophishFR endpoint accepts form-encoded business data and
   that the template shows the pattern an *external client* applies to its
   *own* form-based integration: before = `api_key` plus two placeholder
   business fields; after = the identical two fields, byte-for-byte, with
   `api_key` replaced by a canonical `Authorization: Bearer` header.
3. **Added the middleware-level regression.**
   `middleware/api_auth_form_fields_test.go`'s
   `TestFormTransportPreservesBusinessFields` wraps the real `RequireAPIKey`
   middleware (not a mock) around a local handler and asserts two
   unrelated `PostForm` fields are present and byte-for-byte identical
   after a request authenticated via the deprecated form `api_key`
   transport, and after the equivalent request authenticated via
   `Authorization: Bearer` with `api_key` removed from the body.
4. **Corrected two technical claims.** Replaced "a `GET` request cannot
   carry a form-urlencoded body" (false — HTTP permits a GET body; Go's
   `ParseForm` simply never reads one into `PostForm`, regardless of
   encoding) with that precise statement, and added "A technical note on
   the form transport's exact scope" stating `ParseForm` only reads
   `POST`/`PUT`/`PATCH` bodies, and specifically that a
   `multipart/form-data` request is **never** parsed into `PostForm` at
   all — confirmed empirically with a standalone Go program using
   `mime/multipart` + `httptest.NewRequest` + `req.ParseForm()`, which
   showed `PostForm` stayed empty for a multipart body containing an
   `api_key` field. `docs/GROUP_IMPORT_LIMITS.md`'s auth-contract row is
   corrected to match: that endpoint's `multipart/form-data` content type
   means its `api_key` support is header/query only, never a form field.

## Iteration 5: independent review findings and fixes

[`review-feedback-4.md`](../.goals/deprecate-api-key-transports/review-feedback-4.md)
returned a FAIL on iteration 4. Security review passed; code review found
four correctness gaps, all inside `internal/docsguard` itself (the gate's
own detection logic had drifted from the exact runtime contract it exists
to check), plus one documentation wording issue.

### Finding 1 — the gate accepted lowercase `bearer`

The real credential extractor only strips a literal, case-sensitive
`"Bearer "` prefix (`strings.TrimPrefix` in
`middleware.extractExplicitAPICredential`); any other casing of the word
is not recognized as Bearer at runtime and is treated as a raw/legacy
credential instead. The gate previously matched the scheme name
case-insensitively for every scheme, including Bearer, so a canonical
example showing the Authorization header with the word `bearer` in
lowercase would have passed the gate while silently not working the way
the example claims. Fixed: the
header *name* ("Authorization") still matches case-insensitively, per RFC
9110 and `http.Header.Values`, but the Bearer scheme token now requires
the exact case-sensitive spelling `Bearer`; any other casing (`bearer`,
`BEARER`, `BeaRer`, ...) is flagged as `raw_authorization`. Unrelated real
schemes (`Basic`, `Digest`, `Negotiate`, `NTLM`) remain allowed
case-insensitively, since this repository's runtime has no special-cased
handling of them the way it does for Bearer, and HTTP auth schemes are
registered case-insensitively (RFC 7235). Fixture tests cover all four
wrong-case Bearer variants as positive, and the header name in three
different cases paired with the exact Bearer casing as negative.

### Finding 2 — percent-encoded parameter names bypassed literal matching

Go's `net/url` and `net/http` percent-decode a query or form parameter
name before comparing it, so a key percent-encoded as `api%5Fkey`
(`%5F` is `_`) decodes to and authenticates exactly like the literal
`api_key` parameter at runtime, but the gate's previous literal substring
check never saw the encoded form. Fixed:
`hasParameterCredential` now finds every `key=` token in a line with a
regexp, percent-decodes each key with `net/url.QueryUnescape`, and
compares the *decoded* value to exactly `"api_key"`. This is still a plain
regexp/string check, not a URL or Markdown parser, and needed no new
dependency (`net/url` is already in the standard library this project
already depends on). Fixture tests cover a percent-encoded key in a query
string, a form body, and alongside another parameter, plus a negative case
where the percent-encoded key decodes to a different, non-matching
parameter name and must stay unflagged.

### Finding 3 — scanner errors were silently ignored

`bufio.Scanner.Scan` returns `false` on any error, including
`bufio.ErrTooLong` when a single line exceeds its internal buffer; the
previous `ScanText` never checked `scanner.Err()`, so a pathologically
long line would silently truncate the scan and everything after it —
reporting "no violations" even if a real violation sat on the very next
line. Fixed: `ScanText`'s signature changed from `[]Violation` to
`([]Violation, error)`, returning the scanner's error (if any) alongside
whatever partial violations were found before it. `cmd/docsguard` now
fails closed: a scan error is treated exactly like a file-read error
(`exit 2`), not as "zero violations found". A new test,
`TestScanTextOversizedLineFailsClosed`, builds a line larger than the 1
MiB buffer followed by a line with a real deprecated-parameter violation,
and asserts `ScanText` returns a non-nil error; a matching CLI-level test in
`cmd/docsguard/main_test.go` asserts `run()` exits `2` and reports a
scanning error for the same input.

### Finding 4 — Content-Type wording claimed an exact match

The migration guide stated the form transport requires the `Content-Type`
to be "exactly" `application/x-www-form-urlencoded`, but Go's `ParseForm`
calls `mime.ParseMediaType`, which parses out and discards media-type
parameters (such as `; charset=UTF-8`) before comparing only the base
media type — so a real request with those parameters still authenticates
via the form transport. Fixed: the doc now describes the **parsed media
type** via `mime.ParseMediaType`, explicitly stating parameters are
allowed and ignored. Verified empirically with a standalone Go program
that called `req.ParseForm()` on a body with
`Content-Type: application/x-www-form-urlencoded; charset=UTF-8` and
confirmed both `PostForm` fields parsed successfully.

## Iteration 6: independent review finding and fix

[`review-feedback-5.md`](../.goals/deprecate-api-key-transports/review-feedback-5.md)
returned a FAIL on iteration 5. Security review passed; code review found
the guard still missed ordinary prose that recommends a deprecated
transport without assignment or table syntax, for example "Use the
api_key query parameter for authentication."

### Finding — recommendation prose with no assignment or table syntax went undetected

`undeprecatedParameterMention` previously only checked for a Markdown
table row or an "or an api_key" alternative-option sentence. A plain
imperative or descriptive recommendation — "use", "using", "via", "with",
"accept(s)", or "authenticate (with/via)" governing `api_key` as a live
option — matched neither pattern and slipped through. Fixed by adding two
more checks to the same function (no new `Kind`; this is the same
documentation-accuracy category as the table/alternative checks):

- the word "authenticate" (any inflection: authenticates, authenticated,
  authenticating, authentication) co-occurring with `api_key` and no
  deprecation context is flagged on its own. This word is specific enough
  to an authentication recommendation that it needs no further anchor.
- the more generic verbs `use`/`using`/`used`/`via`/`with`/`accept(s/ed/ing)`
  are flagged only when a companion anchor word — `parameter`, `param`,
  `query`, `field`, `header`, or `credential` — also appears on the same
  line. This is exactly what distinguishes "use the api_key query
  parameter" from an incidental mention that happens to share a line with
  one of those very common words, such as this project's own migration
  guide saying `` `--form` with an `api_key` value ``, which must stay
  allowed.

Fixture tests added 8 positive cases (query and form phrasing, several
casings of `api_key`, and each of the required verbs) and 6 negative cases
(internal/database-schema prose, a migration-script description, two
historical statements, and a migration/deprecation description that names
the trigger verbs only to contrast them with the canonical replacement),
alongside the pre-existing table/alternative fixtures, all of which still
pass unchanged.

Extending the gate's own coverage surfaced three sentences in this very
document that the new rule correctly flagged as false positives:
historical/descriptive prose from earlier iterations that happened to
combine "authentication"/`api_key`/"parameter" on one line while
describing a *past* bug, not recommending a *current* option. Each was
reworded (e.g. "credential demonstration" instead of "authentication
demonstration", "auth-contract row" instead of "authentication row") with
no change in meaning, rather than weakening the new rule to tolerate them.

Writing *this* "Iteration 6" section, however, required extensively
quoting and describing the new rule and its exact trigger phrases, which
necessarily collided with the rule itself far more than a handful of
rewordable sentences could reasonably absorb. Rather than euphemize every
example into something that risks misdescribing the actual bug, this
document is now exempted from the gate the same way the migration guide
already is (see "Scope of the change" above); both exemptions are declared
in the one place `scripts/verify-docs-canonical-examples.sh` already
lists them.

A full run of the extended gate against every tracked Markdown file found
zero violations outside this now-exempted document; no other shipped doc
needed a wording change.

## Iteration 7: independent review findings and fixes

[`review-feedback-6.md`](../.goals/deprecate-api-key-transports/review-feedback-6.md)
returned a FAIL on iteration 6. Security review passed; code review found
two release-blocking issues in `internal/docsguard` itself.

### Finding 1 — recommendation prose split across physical lines, and negated context, both bypassed detection

The iteration-6 recommendation check (`recommendationMention`, then still
named `undeprecatedParameterMention`) matched one physical line at a time,
so a sentence wrapped across two or more Markdown lines — the normal
result of a line-length-limited editor — was invisible to it. Separately,
`hasDeprecationContext` matched the word "deprecated" (and siblings)
anywhere nearby with no regard for negation, so a sentence asserting the
opposite of the real contract, e.g. "the `api_key` parameter is **not**
deprecated," incorrectly suppressed a violation instead of raising one.

Fixed by:

- grouping non-blank lines into paragraphs (`paragraph`,
  `paragraphsFromLines`) and normalizing each into one whitespace-collapsed
  string, so a recommendation wrapped across lines reads as continuous
  text; the per-line table-row check (`tableRowMention`) and the two
  per-line credential-syntax checks (`KindParameterCredential`,
  `KindRawAuthorization`) are unaffected, since Markdown table rows and
  single credential tokens are inherently single-line;
- adding `negationWordPattern` (not/never/no longer/isn't/aren't/wasn't/
  weren't/doesn't/didn't/won't/wont) and `hasAffirmativeDeprecationContext`,
  which only treats a deprecation-context match as suppressing when no
  negation word immediately precedes it in the same sentence.

Widening the recommendation check from one line to a whole paragraph
initially introduced a new false positive: a purely descriptive paragraph
in `docs/API_AUTHENTICATION.md` explains that "the presence of any
Authorization header, query `api_key`, or form `api_key` ... selects
API-key-only authentication" — two unrelated sentences inside the same
paragraph each independently mention `api_key` and "authentication" with
no actual recommendation relationship between them, and the former
anywhere-in-text pattern matched regardless. Fixed by bounding both the
strong ("authenticate") and weak (use/via/with/accept, paired with an
anchor word) recommendation patterns to require the trigger word within a
small number of intervening words of the `api_key` mention
(`recommendationProximity`, currently four words either side), rather than
merely co-occurring anywhere in a paragraph. Every existing positive
fixture still matches well within that window; the `API_AUTHENTICATION.md`
paragraph — whose "authentication" and `api_key` mentions are separated by
an unrelated clause — no longer does, with no rewording needed.

New fixtures: `TestScanTextWrappedRecommendation` (positive: recommendation
text split across 2–4 physical lines; negative: wrapped historical/
deprecation-context, wrapped internal-schema text, and a two-paragraph case
proving paragraph-by-paragraph independence) and
`TestScanTextNegatedContextDoesNotSuppress` (positive: "not deprecated"/
"no longer legacy"/"isn't deprecated" phrasing, each paired with a genuine
nearby recommendation, must still violate; negative: genuine affirmative
"is deprecated" phrasing still suppresses). All prior fixtures, including
`TestScanTextUndeprecatedParameterMention`'s 8 positive/6 negative cases
from iteration 6, pass unchanged.

### Finding 2 — the CLI echoed the offending document text, risking a real secret in CI logs

`cmd/docsguard`'s diagnostic output previously printed the full matched
line, including the actual `api_key` value or `Authorization` header
content it had just flagged. A real, accidentally-committed credential
would therefore be echoed verbatim into CI logs by the very tool meant to
catch the mistake.

Fixed structurally rather than by runtime redaction: the `Violation`
struct no longer carries any document text at all — it was changed from
`{Line int; Text string; Kind Kind}` to `{StartLine, EndLine int; Kind
Kind}` (`EndLine` lets the paragraph-level check report the whole matching
span). A new `Kind.Explanation()` method returns a fixed, generic,
non-sensitive description per `Kind`, computed independently of any scanned
text. `cmd/docsguard/main.go`'s print loop was changed to use only
`path:StartLine[-EndLine]: FORBIDDEN (Kind): <Explanation>` — it has no
document text available to print even if it tried. `rawAuthorization`'s
signature was simplified from `(string, bool)` to just `bool` for the same
reason: it no longer has a reason string containing the actual token to
return.

New fixture: `cmd/docsguard/main_test.go` adds a subtest embedding a
distinctive synthetic secret
(`sk-SYNTH7f3c9a1b-not-a-real-credential-9e2d4f`) in both an `api_key=`
value and a raw `Authorization:` value, and asserts the secret string never
appears anywhere in stdout or stderr while `FORBIDDEN (parameter_credential)`,
`FORBIDDEN (raw_authorization)`, and the correct `path:line` locations do.

Both fixes preserve every prior detection rule, the fail-closed oversized-
line behaviour, and all existing CI wiring; no runtime authentication,
middleware, header, status code, logging, version, or dependency behaviour
changed.

## Iteration 8: independent review findings and fixes

[`review-feedback-7.md`](../.goals/deprecate-api-key-transports/review-feedback-7.md)
returned a FAIL on iteration 7. Security review passed; code review found
two remaining semantic-context defects in `internal/docsguard`'s
paragraph-wide analysis from the previous iteration.

### Finding 1 — an unrelated deprecation mention anywhere in the paragraph suppressed a later, unrelated recommendation

`hasAffirmativeDeprecationContext` was still being evaluated against the
*whole paragraph* for a recommendation match. Two sentences in the same
paragraph that each independently mention a deprecation-sounding word and
`api_key` — about two entirely different subjects — would incorrectly
suppress a genuine, unqualified `api_key` recommendation.

Fixed by adding `enclosingSentence`, which bounds a block of text to just
the sentence containing a given match (split on `.`/`!`/`?`), and changing
`recommendationMention` to look up each candidate recommendation match's
*own* enclosing sentence before checking for affirmative context, instead
of checking the whole paragraph once. An unrelated deprecation-context word
in an earlier or later sentence of the same paragraph no longer suppresses
a recommendation that appears in a different sentence.

### Finding 2 — generic nearby negation was assumed to negate "deprecated" even when it governed a different word

The previous negation check scanned back from a deprecation-context match
to the start of its sentence (cut only at `.`/`!`/`?`) for *any* negation
word in that entire span. A legitimate warning such as "Do not use the
`api_key` query parameter; it is deprecated." has "not" governing "use",
not "deprecated" — but the old, unbounded backward scan read it as
negating "deprecated" too, and incorrectly flagged this accurate warning
as a violation.

Fixed by tightening negation association to require the negation
word/phrase to be within `negationProximityWords` (3) words of the
deprecation-context word it must directly govern (e.g. "is not actually
deprecated"), and by adding `negationBoundaryPattern` (a semicolon, an
em/en dash, or a double hyphen standing in for one) as a hard stop a
negation word cannot be read across. "Do not use the api_key parameter;
it is deprecated" no longer reads "not" as negating "deprecated", because
the semicolon separates them; "The parameter is not deprecated" still
does, because "not" directly and closely precedes "deprecated" with no
boundary between them.

Both fixes are purely about *where* affirmative context is looked for and
*which* negation governs it — the underlying recommendation-detection
patterns (strong "authenticate" proximity, weak verb+anchor proximity, the
table-row and alternative-wording checks) are unchanged from iteration 7.

New fixtures: `TestScanTextContextIsSentenceScopedNotParagraphWide`
(positive: an unrelated subject's deprecation mention in one sentence does
not suppress a live, unqualified recommendation in a different sentence of
the same paragraph, including a wrapped variant; negative: a genuine
same-sentence context still suppresses) and
`TestScanTextNegationMustDirectlyGovernDeprecationWord` (negative: several
phrasings of "do not use X; it is deprecated", including one using `--`
as the dash and wrapped variants, must not violate; positive: a negation
that *does* directly govern "deprecated" itself, e.g. "is not actually
deprecated", must still violate). All prior fixtures, including every
iteration 5–7 fixture, pass unchanged.

A full run of `scripts/verify-docs-canonical-examples.sh` against every
tracked Markdown file found 0 violations; no doc wording changed.

## Iteration 9: independent review findings and fixes

[`review-feedback-8.md`](../.goals/deprecate-api-key-transports/review-feedback-8.md)
returned a FAIL on iteration 8. Security review passed; code review found
two bounded parser gaps in `internal/docsguard`'s sentence-scoping and
recommendation-detection logic.

### Finding 1 — a dotted version number's internal periods were read as sentence boundaries

`enclosingSentence` treated every `.` as a sentence terminator, including
the two periods inside a removal version like `0.13.0`. A sentence such as
"Use the `api_key` query parameter only until `0.13.0`." would be split at
the first internal dot, stranding the removal-version context in a
fragment the recommendation's own enclosing-sentence lookup never saw —
incorrectly treating a correctly version-bounded statement as an
unqualified, violating recommendation.

Fixed by adding `isDottedVersionPeriod` (a `.` flanked by an ASCII digit on
both sides is never a sentence boundary) and `nextSentenceTerminator`/
`prevSentenceTerminator`, which scan for a real terminator while skipping
any such period. Only the genuine sentence-ending period after `0.13.0.`
is treated as a boundary; the version's own internal dots are not.

New fixture: `TestScanTextDottedVersionNotSentenceBoundary` — several
phrasings where `0.13.0` is the *sole* affirmative context (no
"deprecated"/"legacy"/"removal" word at all) qualifying a recommendation
in the same sentence, including mid-sentence and wrapped placements, must
not violate; a contrasting case proves `0.13.0` context in an *unrelated*
sentence still does not suppress a different sentence's unqualified
recommendation (the sentence-scoping from iteration 8 is unaffected).

### Finding 2 — "recommend" was not part of the recommendation-verb candidate set

`weakRecommendationVerbPattern` covered `use`/`via`/`with`/`accept` but not
`recommend` (and its inflections: recommends, recommended, recommending),
so prose like "We recommend the `api_key` query parameter" went
undetected.

Fixed by adding `recommend\w*` to the same verb alternation, so it is
governed by the same proximity-to-`api_key` bound and the same
`recommendationAnchorPattern` (parameter/param/query/field/header/
credential) requirement as the other weak verbs — no new, looser code
path was introduced.

New fixture: `TestScanTextRecommendVerb` — positive cases cover each
inflection paired with an anchor word, including a wrapped form; negative
cases cover a historical "recommended" statement already carrying
same-sentence deprecation context, an internal/non-transport "recommends"
with no anchor word nearby, a "no longer recommend ... it is deprecated"
statement, and "recommend" with no `api_key` mention at all.

Both fixes are narrow, bounded corrections to the existing line-scanning/
paragraph-grouping/sentence-scoping design from iterations 7–8 — no
broader Markdown or natural-language parser was introduced. All prior
fixtures, rules, the redacted diagnostics, and the fail-closed scanner are
unchanged. A full run of `scripts/verify-docs-canonical-examples.sh`
against every tracked Markdown file found 0 violations; no doc wording
changed.

## Iteration 10: independent review finding and fix

[`review-feedback-9.md`](../.goals/deprecate-api-key-transports/review-feedback-9.md)
returned a FAIL on iteration 9. Security review passed; code review found a
remaining clause-scoping gap:

```text
The old session login is deprecated, but use the api_key query parameter.
```

### Finding — a deprecation keyword in an unrelated *clause* of the same sentence still suppressed a live recommendation

Iteration 8's sentence-scoping stopped an unrelated deprecation mention in
a *different sentence* from suppressing a recommendation, but it did
nothing for an unrelated mention in a different *clause* of the *same*
sentence. "The old session login is deprecated, but use the `api_key`
query parameter." is one sentence (a single trailing period), so the
`deprecated` match was still read as qualifying the `api_key`
recommendation that follows "but" — even though "but" signals these are
two independent, contrasting claims about two different subjects.

Fixed by adding `adversativeBoundaryPattern` — a bounded adversative
connector ("but", "however", "yet", "though", "although", "nevertheless",
"nonetheless", "whereas") — and changing `hasAffirmativeDeprecationContext`
to take the recommendation match's own position and check, for each
candidate context match, whether one of these connectors sits between the
two, in *either* direction. A connector in between means the context
describes a different, contrasting clause and does not qualify the
recommendation. A semicolon or em/en dash was deliberately **not** added to
this pattern: every existing fixture using one ("Do not use the `api_key`
query parameter; it is deprecated.") is an *elaboration* of the very thing
just recommended, not a contrast, and must continue to suppress — adding
semicolons/dashes to the adversative set would have broken that
already-required behavior. `hasAffirmativeDeprecationContext`'s existing
per-line (table row) and per-sentence (recommendation prose) callers were
updated to pass the recommendation's position (or `len(line)` for table
rows, preserving their prior whole-row behavior unchanged).

One existing fixture from iteration 8 needed correcting as part of this
fix: "The `api_key` query parameter is deprecated, but some old docs still
say to use the `api_key` query parameter for authentication." was
previously asserted as *not* a violation, reasoning that the context was
"about the same subject". Under the corrected, clause-scoped semantics
this reasoning does not hold — docsguard does not resolve coreference, it
only reads clause membership, and a "but" still separates the deprecation
notice from the recommendation regardless of the parameter name being
repeated on both sides. This fixture now correctly asserts a violation,
with a comment explaining why, and a new fixture
(`TestScanTextContextIsSentenceScopedNotParagraphWide`'s replacement
negative case) demonstrates the genuine same-clause contrast instead.

New fixture: `TestScanTextDeprecationContextScopedToClause` — the review's
exact reported sentence, a semicolon/"however" variant, two "following
clause" variants (the unrelated deprecation notice comes *after* the
recommendation instead of before), and wrapped forms, all violate; a
genuine same-clause notice connected by a non-adversative word ("so") and
the pre-existing semicolon-elaboration warning continue to suppress. All
prior fixtures — dotted-version, direct-negation, sentence-scoping,
proximity, encoded-name, recommend-verb, scanner fail-closed, and
diagnostic-redaction — pass unchanged.

A full run of `scripts/verify-docs-canonical-examples.sh` against every
tracked Markdown file found 0 violations; no doc wording changed.

## Iteration 11: independent security-review finding and fix

An independent security review of the iteration-10 state returned a
MEDIUM-severity finding (9/10 confidence): `internal/docsguard` scans raw
Markdown source and did not normalize Markdown backslash escapes or HTML
character references before matching. A canonical-looking documentation
example could render as a fully functional deprecated-transport example in
GitHub's Markdown while evading this literal scanner --
`api\_key=TOKEN`, `api&#95;key=TOKEN`, and `Authorization&#58; TOKEN` all
render identically to their plain forms but previously matched none of
this package's patterns.

### Finding — rendered Markdown/HTML escapes could hide a functional example from the literal scanner

Fixed by adding a single, bounded normalization step
(`normalizeRenderedEscapes`), applied to every line before any other
pattern in this package runs:

- **Backslash escapes** — `backslashEscapePattern` undoes a backslash
  followed by any of the ASCII punctuation characters CommonMark/GFM treats
  as escapable (`!"#$%&'()*+,-./:;<=>?@[\]^_`{|}~`), covering every
  punctuation character that appears in a protected anchor (`api_key`'s
  underscore, `Authorization`'s colon, and query/form syntax's `=`/`&`),
  not just the underscore named in the finding.
- **Numeric character references** — `numericCharRefPattern` decodes every
  decimal (`&#95;`) and hexadecimal (`&#x5f;`, `&#X5F;`, and any other case
  mix of the `x`/`X` prefix and hex digits) reference. This is safe and
  general by construction (each one names exactly one code point); it is
  not a loophole-prone guess, unlike named references.
- **Named character references** — a narrow, explicit
  `namedCharRefReplacements` table recognizes only the handful of HTML
  named references that render as a punctuation character this package's
  protected anchors depend on (`&amp;`, `&lowbar;`/`&UnderBar;`, `&colon;`,
  `&equals;`, `&num;`, `&quest;`, `&semi;`, `&sol;`, `&bsol;`). An
  unrecognized name (`&copy;`, `&mdash;`, `&euro;`, ...) is left exactly as
  written -- this is deliberately not a general ~2,000-entry HTML5
  named-entity table, which would turn this package into the general
  HTML/Markdown parser it is explicitly designed not to be.

Normalization happens once, per physical line, before the line is used for
*any* check -- the credential-syntax checks (`KindParameterCredential`,
`KindRawAuthorization`), the per-line table-row check, and the
paragraph-grouping that feeds the recommendation-prose check -- so an
escaped `api_key` cannot evade the documentation-accuracy checks either,
not only the credential-syntax ones. No downstream function needed to
change: each already receives already-normalized plain text.

New fixtures (`TestScanTextRenderedEscapeNormalization`): backslash-escaped
underscore and equals in a parameter credential; the underscore via both
recognized named references; the underscore via decimal and all four hex
case combinations; an HTML-encoded query-string ampersand not blocking
detection of the `api_key` it joins; a combined backslash+entity example in
one token; `Authorization`'s colon via backslash, named, and decimal/hex
numeric references; and an escaped `api_key` in recommendation prose and in
a table row. Negative fixtures prove an unrecognized named entity, a
backslash before a non-punctuation character, an unrelated backslash
escape, and an unrelated numeric reference do not create false positives,
and that a genuine deprecation notice containing an unrelated entity still
suppresses correctly.

All prior rules -- paragraph/clause-scoped context, direct-negation,
dotted-version-aware sentence splitting, proximity bounds, percent-decoded
parameter names, recommend-verb detection, the fail-closed scanner, and
redacted diagnostics -- are unchanged. A full run of
`scripts/verify-docs-canonical-examples.sh` against every tracked Markdown
file found 0 violations; no doc wording changed. No runtime
authentication/middleware/header/status/version/dependency behavior
changed.

## Iteration 12: final-review findings and fixes

Final independent reviews of the iteration-11 state returned three bounded
parser findings in `internal/docsguard`, addressed together and
surgically, without a general Markdown/NLP parser and with no runtime
behavior change.

### Finding 1 (security, MEDIUM, 9/10) — rendered inline markup split protected anchors and bypassed detection

A canonical-looking example could use a safe inline HTML tag or a Markdown
emphasis delimiter to visually split one of this package's protected
anchors while still rendering as plain, readable text:
`api<em>_</em>key=TOKEN`, `Authorization<em>:</em> TOKEN`, and "Use the
api<em>_</em>key query parameter..." all render exactly like their unsplit
forms but previously matched none of this package's patterns, across the
parameter-credential, raw-authorization, table-row, and
recommendation-prose checks alike.

Fixed with two narrow additions to `decodeRenderedMarkup`:

- `safeInlineTagPattern` strips an opening or closing tag for a short,
  explicit allow-list of safe inline formatting elements (`em`, `i`, `b`,
  `strong`, `u`, `s`, `del`, `ins`, `mark`, `small`, `sub`, `sup`, `span`,
  `abbr`, `code`), case-insensitively and tolerating any attributes,
  leaving everything else (a `<script>`, an `<img>`, an unrecognized or
  custom element) untouched.
- `renderedMarkupSplitPattern` strips a Markdown emphasis/strong/
  strikethrough delimiter run (`*`/`_`, one to three; `~`, one to two, each
  side independent) wrapping **exactly one** of this package's protected
  punctuation characters (`_`, `:`, `=`, `&`). This is deliberately
  narrower than general emphasis recognition: a delimiter run can equally
  be literal *content* rather than a wrapping delimiter (`**_**`
  strong-emphasizes a literal underscore), and there is no way to
  distinguish those without a real parser. Recognizing only "exactly one
  wrapped protected character" sidesteps that ambiguity entirely, since
  none of `_:=&` is itself a delimiter character this same pattern also
  tries to match as wrapped content.

New fixtures (`TestScanTextRenderedMarkupSplit`): the finding's exact
examples across every anchor (parameter, raw header, table, recommendation
prose); other allow-listed tags and a case variant; an attribute on a tag;
each emphasis/strong/strikethrough delimiter type wrapping the protected
character; mismatched delimiter-run lengths; a tag wrapping
emphasis-wrapped content and a tag wrapping an HTML character reference
(both nested combinations); and negative cases for an unrelated
non-allow-listed tag (`<kbd>`), a malformed/unmatched single asterisk
(which CommonMark renders as a literal character, never as emphasis), and
ordinary whole-word emphasis unrelated to any anchor.

### Finding 2 (code, MEDIUM-high) — normalization applied inside code spans and blocks, where CommonMark never decodes escapes/entities

The normalization added in iteration 11 ran unconditionally on every line,
but CommonMark does not process backslash escapes, HTML character
references, or (per Finding 1) emphasis/raw-HTML-tag rendering inside an
inline code span or a fenced/indented code block — that content renders
completely literally. Normalizing it anyway turned a harmless, literal,
non-functional example (`` `api\_key=TOKEN` ``, which renders as the
visible literal text "api\_key=TOKEN", not a working credential) into a
false positive.

Fixed by making normalization code-aware at two levels:

- `inlineCodeSpanRanges` is a small manual scanner (Go's `regexp` package
  cannot express "a closing backtick run of the same length as the opening
  one" — there are no backreferences) that finds backtick-delimited code
  span ranges within one line; `normalizeRenderedEscapes` now splits a line
  into alternating excluded (code span, left untouched) and included
  (normalized) segments instead of normalizing the whole line uniformly.
- `ScanText` now tracks fenced-code-block state across lines (recognizing
  both backtick and tilde fences, of any length ≥3, with or without an
  info string, closed only by a fence of the same character and at least
  the same length) and recognizes an indented (≥4 space or tab) line as
  code content. A line inside either is scanned **raw** — never passed to
  `normalizeRenderedEscapes` at all — so a literal forbidden example is
  still caught, while an escaped or entity-encoded one correctly is not.

New fixtures (`TestScanTextCodeAwareNormalization`): literal examples
inside a single-backtick span, a triple-backtick fence, a tilde fence with
an info string, a backtick fence with an info string, and a 4-space
indented block all still violate; the boundary case of exactly 3 spaces
(not indented code) still normalizes and violates when escaped. Negative
cases cover backslash-escaped and HTML-entity forms inside a code span (in
both credential-syntax and recommendation-prose contexts) and inside every
fence/indentation variant above, none of which violate.

### Finding 3 (code, MEDIUM-high) — unrelated words in a sentence suppressed forbidden recommendations

A deprecation-context word ("migration", "removal", "legacy", ...) counted
as affirmative context anywhere it appeared in the same sentence or
elaborating clause, with no requirement that it actually describe the
`api_key` mention being evaluated. A sentence recommending `api_key` with
no qualification of its own, merely co-occurring with an unrelated
subject's deprecation notice ("our legacy billing system also needs
migration, with removal scheduled separately"), was incorrectly suppressed.

Fixed by adding a proximity requirement to `hasAffirmativeDeprecationContext`:
a context word must now be within `deprecationContextProximityWords` (6)
words of an actual `api_key` mention in the same clause to count, in
addition to the existing negation and adversative-boundary checks. This is
generous enough for the context word to sit just past a short verb phrase
("the api_key parameter is deprecated", "recommended the api_key query
parameter; it is now deprecated") while excluding one that is merely
nearby but describes something else.

New fixtures (`TestScanTextContextBoundToTransportSyntax`): a long sentence
and a table-row cell, each pairing a live `api_key` recommendation with
deprecation-sounding words about an unrelated subject far enough away not
to qualify it (plain and wrapped), correctly violate; a genuinely close
"migration"-only context, and a fully qualified same-sentence notice, both
continue to suppress correctly.

All three fixes compose correctly together and preserve every prior rule
unchanged: dotted-version-aware sentence splitting, direct-negation,
clause/adversative scoping, proximity-bounded recommendation verbs,
percent-decoded parameter names, the iteration-11 escape normalization
itself, the fail-closed scanner, and redacted diagnostics. The
nonconforming historical commit flagged separately by the reviewers is
intentionally **not** touched in this iteration; the orchestrator will
seek explicit user authorization for that separately. No runtime
authentication/middleware/header/status/version/dependency behavior
changed. A full run of `scripts/verify-docs-canonical-examples.sh` against
every tracked Markdown file found 0 violations; no doc wording changed.

## Acceptance criteria evidence

| Area | Result | Evidence |
| --- | --- | --- |
| Authentication behaviour unchanged | PASS | No file under `middleware/`, `controllers/api/*.go` (other than the new test file), `models/`, `auth/`, or `internal/apikey` changed. `go test ./...` and `go test -race ./...` pass unmodified, including the full existing API-key extractor/middleware/verifier suites. Container compatibility re-proves Bearer, raw, query (including empty), form (including empty), and session behaviour end to end (see below), and the new campaign-completion regression re-proves the same four transports against a real business operation. |
| Canonical and deprecated contracts | PASS | `docs/API_AUTHENTICATION.md` states Bearer is the only recommended/canonical transport and lists query/form (deprecated, `0.13.0` removal) and raw (deprecated, no removal version) as still-accepted legacy contracts, not as never-public surfaces. |
| Client migration guidance | PASS | `docs/API_KEY_TRANSPORT_DEPRECATION.md` has synthetic (`REPLACE_WITH_YOUR_TOKEN`) old/new `curl` examples for query→Bearer, form→Bearer, and raw→Bearer, all against the same real campaign-completion endpoint with its exact route/method/trailing-slash stated; the exact `0.13.0` removal behaviour (empty/combined-with-session/combined-with-Bearer/raw → JSON `401`, no fallback); and an explicit statement that raw stays accepted/deprecated after that removal. Every example is now proven executable by `controllers/api/campaign_transport_deprecation_test.go`. |
| Leak remediation guidance | PASS | The same document names GophishFR access logs, nginx/reverse-proxy defaults, browser/shell history, and copied diagnostics/support traces as places a query key may already exist; instructs migrate+rotate+review/remove; explicitly disclaims automatic identification of affected clients or cleanup of third-party/proxy logs; and states this release does not stop query secrets from reaching access logs. Form is described as lower default logging risk but still deprecated to converge on one transport. The separate rate-limiting backlog item is named, not implemented. |
| Runtime signalling decision | PASS | A dedicated section states and justifies, per mechanism, why no RFC 9745 `Deprecation` header, no RFC 8594 `Sunset` header, no `Link: rel="deprecation"` header, and no custom header/server warning log were added (resource-identity mismatch, no calendar date, no stable published URI, and documentation as the deliberate channel, respectively). No token, URL/query, form body, `Authorization` value, verifier, or secret-derived value is emitted anywhere — unchanged, and reconfirmed by the unmodified middleware/controller code and passing test suites. |
| Release/version documentation | PASS | `VERSION` is untouched (`0.12.1`). `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md` states deprecated items, the `0.13.0` removal target, Bearer migration, key rotation/log cleanup, and that the breaking removal itself updates `VERSION` to `0.13.0` through the normal release process. No calendar sunset timestamp is invented anywhere. |
| Examples/search/gate | PASS | `internal/docsguard`'s fixture tests and a live run of `scripts/verify-docs-canonical-examples.sh` both confirm no canonical doc outside the one exempted page shows a deprecated-transport example. The gate is now enforced both locally (`scripts/verify.sh`) and by a blocking CI job (`docs-guard`, wired into `ci-success`). No OpenAPI/Swagger specification exists and none was created. |
| Tests and validation | PASS | See "Validation evidence" below. |
| Scope and roadmap | PASS | Nothing removed/rejected for query/form/raw. No verifier/keyring/token-format/session/CSRF/RBAC/rate-limit/log/`events.details`/dependency change. The deprecation page names the next breaking PR (`security/remove-legacy-api-key-transports`, `0.13.0`, retaining explicit presence detection so unsupported transports cannot fall back to session) and the independent next hardening item (`security/rate-limit-api-auth`) without implementing either. |

## Validation evidence

- `./scripts/verify.sh`: all gates pass — gofmt, golangci-lint (0 issues),
  `go mod verify`, `go vet`, `go build`, `go test` (including the new
  `internal/docsguard`, `cmd/docsguard`, and
  `controllers/api/campaign_transport_deprecation_test.go` suites), the
  rewritten "docs canonical API examples" gate, `go test -race`, the
  frontend double-build check (byte-identical; no frontend source changed),
  and `govulncheck` under the pinned `go1.25.13` toolchain (0 reachable
  vulnerabilities; 3 unreachable findings in required modules, unchanged
  from baseline). `action pins` was skipped locally (no authenticated
  `gh`); CI enforces it unconditionally, and the new `docs-guard` job reuses
  SHAs already pinned and verified elsewhere in `ci.yml`.
- `go test ./...` and `go test -race ./...`: pass across every package,
  including the three new test files and the unchanged
  `middleware/api_auth_test.go` and `middleware/middleware_test.go`.
- `TestCampaignCompleteTransportDeprecationEquivalence`
  (`controllers/api`): query, form, raw, and Bearer each authenticate and
  complete a real campaign through the real router; a trailing-slash request
  does not match the route and does not complete its campaign.
- `TestFormTransportPreservesBusinessFields` (`middleware`, new): two
  unrelated `PostForm` fields survive, byte-for-byte, through the real
  `RequireAPIKey` middleware under both the deprecated form `api_key`
  transport and the canonical Bearer transport with `api_key` removed from
  the body.
- Standalone verification of `ParseForm`'s multipart scope: a throwaway Go
  program built a real `multipart.Writer` body containing an `api_key`
  field, called `httptest.NewRequest` + `req.ParseForm()` against it, and
  printed `req.PostForm["api_key"]` — confirmed empty (`[]`), proving
  `docs/GROUP_IMPORT_LIMITS.md`'s corrected claim that a multipart
  `api_key` field is never read as a credential.
- `internal/docsguard`'s extended fixture suite (iteration 5):
  `TestScanTextRawAuthorization` now covers four wrong-case `bearer`
  variants as positive and the header name in three cases paired with
  exact-case `Bearer` as negative; `TestScanTextParameterCredential` covers
  three percent-encoded `api_key` placements as positive and a
  percent-encoded non-matching key as negative;
  `TestScanTextOversizedLineFailsClosed` asserts `ScanText` returns a
  non-nil error for a line exceeding the 1 MiB buffer followed by a real
  violation; `cmd/docsguard/main_test.go`'s matching CLI-level case asserts
  `run()` exits `2` with a scanning-error message for the same input.
- Standalone verification of the parsed-media-type Content-Type claim: a
  throwaway Go program called `req.ParseForm()` on a body with
  `Content-Type: application/x-www-form-urlencoded; charset=UTF-8` and
  confirmed both posted fields parsed successfully.
- `TestScanTextUndeprecatedParameterMention` (iteration 6): 8 new positive
  fixtures cover query/form recommendation phrasing, several casings of
  `api_key`, and each required verb (use/using/via/with/accept/
  authenticate-with); 6 new negative fixtures cover internal/database-schema
  prose, a migration-script description, two historical statements, and a
  migration/deprecation description naming the trigger verbs only to
  contrast them with Bearer — all alongside the pre-existing table and
  alternative-wording fixtures, none of which regressed.
- `./scripts/verify-docs-canonical-examples.sh` run against every tracked
  Markdown file (28, after adding this document itself to the narrow
  exemption list) reports 0 violations.
- `./scripts/test-browser.sh`: the real, non-mocked `TestBrowser*` suite in
  `controllers` passes against a fresh `corepack yarn build`.
- `scripts/test-container-api-session-auth.sh`: the PR #62/#63 real-container
  auth matrix passes — Bearer, raw, query (valid, and invalid/empty with a
  valid session present, no fallback), form (valid, invalid/empty, no
  fallback), a duplicate-value accept, a conflicting-value `401`, and that no
  API key appears in a standard page or in container logs.
- `scripts/test-container-api-key-verifier.sh`: the real-container API-key
  HMAC verifier lifecycle (issuance, reveal-once, storage, migration)
  passes, unmodified.
- `gosec ./...`: 12 pre-existing findings (open-redirect taint-analysis
  reports on unchanged redirect call sites, plus the previously documented
  TLS/path/log-mode findings), the same baseline as iterations 1–2. No new
  finding from this iteration's changes: `-nosec=true` confirms the new
  `KindUndeprecatedParameterMention` constant does not itself trigger a
  finding, so it carries no `#nosec` comment (one was tried and removed
  when it proved unnecessary, rather than left in place unjustified).
- `gitleaks detect --source . --no-banner --redact`: scanning full commit
  history still reports the 2 pre-existing `.goals/api-key-verifier`
  findings from 2026-09-03 (unrelated, unchanged by this PR) and 2 findings
  inside the iteration-1 commit's original (pre-fix) blob of
  `docs/API_KEY_TRANSPORT_DEPRECATION.md` — expected, since history scanning
  checks every past commit's snapshot and the iteration-2 `gitleaks:allow`
  fix could not retroactively annotate an earlier commit. The current
  working tree (`gitleaks detect --no-git`) is clean. No new finding was
  introduced by this iteration's changes.
- `scripts/verify-docs-canonical-examples.sh` sanity check: with
  `docs/GROUP_IMPORT_LIMITS.md`'s fix temporarily reverted via
  `git stash`, the gate correctly reported
  `FORBIDDEN (undeprecated_parameter_mention)` on the original wording; with
  the fix restored, the gate reports 0 violations across all 29 scanned
  files.
- `grep -rni "api_key" docs/*.md` (repository-wide Markdown search): every
  remaining canonical mention is either properly deprecation-flagged
  (`API_AUTHENTICATION.md`, `API_KEY_TRANSPORT_DEPRECATION.md`,
  `RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md`, this self-review) or an
  internal database-column/verifier-state reference
  (`API_KEY_VERIFIER.md`), never a client-facing "normal option" mention.
- `actionlint -color`: pass, including the new `docs-guard` job.
- `zizmor --min-severity=low .github/workflows/`: no findings (2 previously
  documented suppressions retained), including the new `docs-guard` job.
- `corepack yarn audit`: 4 moderate findings, all in a transitive `webpack`
  devDependency (`fast-uri`, via `schema-utils`/`ajv`); pre-existing in the
  unchanged lockfile, not introduced or touched by this PR, and out of scope
  (no dependency change in this PR).
- `npx --yes retire --path static --outputformat text`: pass, no findings.
- `git diff --check`: clean. `go.mod`, `go.sum`, `package.json`, and
  `yarn.lock` are byte-identical to `HEAD`.
- `TestScanTextWrappedRecommendation` and
  `TestScanTextNegatedContextDoesNotSuppress` (iteration 7, new): wrapped-
  line recommendation detection and negation-aware context suppression,
  both described above; all prior `internal/docsguard` fixtures, including
  iteration 6's 8 positive/6 negative recommendation-prose cases, pass
  unchanged under the new paragraph-wide, proximity-bounded logic.
- The CLI-level "a secret-bearing forbidden example is never echoed"
  subtest in `cmd/docsguard/main_test.go` (iteration 7, new): a distinctive
  synthetic secret embedded in both a flagged `api_key=` value and a
  flagged raw `Authorization:` value never appears in stdout or stderr,
  while the correct `FORBIDDEN (Kind)` and `path:line` diagnostics do.
- `./scripts/verify-docs-canonical-examples.sh` re-run against every
  tracked Markdown file after the iteration-7 fixes: 0 violations, and the
  `docs/API_AUTHENTICATION.md` false positive surfaced mid-iteration by the
  new paragraph-wide scope (see "Finding 1" above) no longer appears, with
  no doc wording changed to achieve that.
- `TestScanTextContextIsSentenceScopedNotParagraphWide` (iteration 8, new):
  an unrelated subject's deprecation mention in one sentence of a paragraph
  does not suppress a live, unqualified `api_key` recommendation in a
  different sentence of that paragraph (plain and wrapped forms); a
  genuine same-sentence context still suppresses.
- `TestScanTextNegationMustDirectlyGovernDeprecationWord` (iteration 8,
  new): several phrasings of a legitimate "do not use `api_key`; it is
  deprecated" warning, including a `--` dash variant and wrapped forms,
  are not violations, since the negation there governs "use", not
  "deprecated"; a negation that does directly govern "deprecated" itself
  still violates. All prior `internal/docsguard` fixtures, including every
  iteration 5–7 fixture, pass unchanged under the sentence-scoped,
  directly-governed negation logic.
- `./scripts/verify-docs-canonical-examples.sh` re-run against every
  tracked Markdown file after the iteration-8 fixes: 0 violations across
  28 shipped Markdown files, no doc wording changed.
- `TestScanTextDottedVersionNotSentenceBoundary` (iteration 9, new):
  `0.13.0` as the sole, same-sentence affirmative context for a
  recommendation — mid-sentence, trailing, and wrapped placements — is not
  a violation; `0.13.0` context in an unrelated sentence still does not
  suppress a different, unqualified recommendation.
- `TestScanTextRecommendVerb` (iteration 9, new): `recommend`/`recommends`/
  `recommended`/`recommending` paired with an anchor word and no
  affirmative context violates (plain and wrapped forms); a historical
  "recommended ... it is deprecated" statement, an anchor-less internal
  "recommends" mention, a "no longer recommend ... deprecated" statement,
  and a `recommend` with no `api_key` mention at all do not. All prior
  `internal/docsguard` fixtures, including every iteration 5–8 fixture,
  pass unchanged under the dotted-version-aware sentence scoping and the
  extended verb set.
- `./scripts/verify-docs-canonical-examples.sh` re-run against every
  tracked Markdown file after the iteration-9 fixes: 0 violations across
  28 shipped Markdown files, no doc wording changed.
- `TestScanTextDeprecationContextScopedToClause` (iteration 10, new): the
  review's exact reported sentence, a semicolon/"however" variant, and two
  "following clause" variants (the unrelated notice follows the
  recommendation instead of preceding it), plain and wrapped, all violate;
  a genuine same-clause notice joined by a non-adversative word ("so") and
  the pre-existing semicolon-elaboration warning continue to suppress. The
  corrected iteration-8 fixture (a "but" separating a deprecation notice
  from a recommendation naming the same parameter) now asserts a violation,
  with a new, genuinely same-clause fixture demonstrating the contrast. All
  prior `internal/docsguard` fixtures, including every iteration 5–9
  fixture, pass unchanged under the clause-scoped context association.
- `./scripts/verify-docs-canonical-examples.sh` re-run against every
  tracked Markdown file after the iteration-10 fix: 0 violations across 28
  shipped Markdown files, no doc wording changed.
- `TestScanTextRenderedEscapeNormalization` (iteration 11, new): a
  backslash-escaped underscore/equals in a parameter credential; the
  underscore via both recognized named HTML references; the underscore via
  decimal and all four hex-prefix/digit case combinations; an HTML-encoded
  query-string ampersand not blocking detection of the `api_key` it joins;
  a combined backslash+entity example in one token; `Authorization`'s
  colon via backslash, named, and decimal/hex numeric references; and an
  escaped `api_key` in both recommendation prose and a table row -- all
  violate. An unrecognized named entity, a backslash before a
  non-punctuation character, an unrelated backslash escape, an unrelated
  numeric reference, and a genuine deprecation notice containing an
  unrelated entity do not. All prior `internal/docsguard` fixtures,
  including every iteration 5–10 fixture, and the full repository test
  suite (`go test ./...`) pass unchanged under the new normalization step.
- `./scripts/verify-docs-canonical-examples.sh` re-run against every
  tracked Markdown file after the iteration-11 fix: 0 violations across 28
  shipped Markdown files, no doc wording changed.
- `TestScanTextRenderedMarkupSplit` (iteration 12, new): the finding's
  exact HTML-tag and emphasis-delimiter examples across every protected
  anchor (parameter, raw header, table row, recommendation prose); other
  allow-listed tags, a case variant, and an attribute; each delimiter type
  (`*`/`_`/`~`) and mismatched run lengths; two nested combinations (a tag
  wrapping emphasis, a tag wrapping an entity) -- all violate. An unrelated
  non-allow-listed tag, a malformed/unmatched asterisk (rendered literally
  by CommonMark), and ordinary whole-word emphasis do not.
- `TestScanTextCodeAwareNormalization` (iteration 12, new): literal
  examples inside a single-backtick span, a triple-backtick fence, a tilde
  fence with an info string, a backtick fence with an info string, and a
  4-space indented block all violate, as does the 3-space (not indented
  code) boundary case when escaped. Backslash-escaped and HTML-entity forms
  inside every one of those same code contexts -- credential syntax and
  recommendation prose alike -- do not.
- `TestScanTextContextBoundToTransportSyntax` (iteration 12, new): a long
  sentence and a table-row cell pairing a live, unqualified `api_key`
  recommendation with deprecation-sounding words describing an unrelated
  subject far enough away not to qualify it (plain and wrapped) violate; a
  genuinely close "migration"-only context and a fully qualified
  same-sentence notice continue to suppress. All prior `internal/docsguard`
  fixtures, including every iteration 5–11 fixture, and the full repository
  test suite (`go test ./...`) pass unchanged under all three fixes
  composed together.
- `./scripts/verify-docs-canonical-examples.sh` re-run against every
  tracked Markdown file after the iteration-12 fixes: 0 violations across
  28 shipped Markdown files, no doc wording changed.

## Why a bounded deprecation window instead of immediate removal

Query, form, and raw have been public, documented compatibility contracts
since before this PR, and shipped binaries carry no transport telemetry, so
the external client population still depending on any of them is unknown.
Removing them without notice risks breaking real external integrations with
no warning. A clearly announced, version-targeted window (`0.13.0` for
query/form; no date invented for raw) gives those clients a concrete target
to migrate against while this release changes no behaviour at all — every
currently-working client keeps working unchanged until it chooses to
migrate, or until the separately reviewed `0.13.0` PR actually ships.
