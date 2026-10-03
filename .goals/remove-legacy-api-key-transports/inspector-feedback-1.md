# Inspector Feedback — Iteration 1

## Verdict: PASS

## Acceptance Criteria Check

### Section 0 — Context re-validation
- [x] `extractExplicitAPICredential`/`RequireAPIKey`/`CSRFExceptions` are
  still the exact functions described, in `middleware/middleware.go` —
  confirmed by direct read, not Builder claim.
- [x] `docs/API_KEY_TRANSPORT_DEPRECATION.md` previously documented
  query/form as deprecated-removal-target-0.13.0 and raw Authorization
  as deprecated-no-removal-version; this PR correctly moves only the
  former to "removed", leaving the latter completely unchanged (verified
  below, section 7).
- [x] `factQueryDeprecated0130`/`factFormDeprecated0130` existed and are
  now replaced with `factQueryRemoved0130`/`factFormRemoved0130` in
  `internal/docsguard/docsguard.go`, in the same narrow-literal-match
  style — confirmed by direct diff read.
- [x] All four "query/form success" test subtests identified in goal.md
  item 0 (`TestExplicitAPICredentialExtraction`'s `"query parameter"`,
  `"form parameter"`, `"identical repeated query parameters"`,
  `"identical values across every source"`;
  `TestCampaignCompleteTransportDeprecationEquivalence`'s query/form
  subtests; `TestFormTransportPreservesBusinessFields`'s form subtest)
  were found at exactly the stated paths/names and were inverted to
  assert rejection — confirmed by direct diff read of each file.
- [x] `go.mod`/`go.sum`/`package.json`/`yarn.lock`: `git diff main..HEAD`
  on each is empty — byte-identical to base, independently confirmed.

### Section 1 — Binding target contract (verified by direct code read of
`middleware/middleware.go`, not by trusting test names)
- [x] Query/form presence detection (`r.URL.Query()["api_key"]`'s `ok`,
  `r.PostForm["api_key"]`'s `ok`) is independent of value, including a
  bare `?api_key` with no `=` — confirmed: `queryPresent`/`formPresent`
  are plain map-key `ok` booleans, never inspecting the value slice
  itself.
- [x] `unsupportedTransport` is NOT folded into the `values`/ambiguity
  computation — confirmed: `values` is built exclusively from
  `r.Header.Values("Authorization")`; query/form values are never
  appended to it anywhere in the function.
- [x] A query/form credential whose value is IDENTICAL to a
  simultaneously-present valid Authorization header is still rejected —
  confirmed by code read: `RequireAPIKey`'s rejection condition ORs in
  `credential.unsupportedTransport` unconditionally, so it fires
  regardless of what `credential.value`/`credential.ambiguous` compute
  to; AND by test: `TestExplicitAPICredentialExtraction/identical_values
  across_every_source` and the two new
  `..._present_with_a_simultaneously_valid_authorization_header` subtests
  independently re-run, PASS (401 in every case).
- [x] `present` is true whenever `unsupportedTransport` is true even if
  `values` is empty — confirmed by code read: the early-return guard is
  `if len(values) == 0 && !malformed && !unsupportedTransport { return
  explicitAPICredential{} }`, so a query/form-only request (no
  Authorization header at all) still produces `present: true`; AND by
  test: `TestCSRFExceptionsRemovedTransportStillExempt`'s two subtests
  (query/form alone, zero Authorization headers) independently re-run,
  PASS, proving the CSRF exemption still applies; AND
  `TestAPIAuthenticationMechanismSelection`'s new `"valid-looking
  query/form with valid session cannot fall back"` subtests independently
  re-run, PASS (401, never session fallback).
- [x] Every other row of the section-1 contract table has a corresponding
  passing test, confirmed by direct read of test code (assertions, not
  names): empty value, no-`=` presence, query+form combined, two distinct
  Authorization values (unchanged ambiguous path, regression-guarded by
  the untouched `"distinct repeated..."` subtests), blocked-IP pre-check
  ordering (`TestAPIAuthRateLimitBlockedIPRejectsRemovedTransportBefore
  Inspection`, re-run, PASS).

### Section 2 — Implementation shape
- [x] Matches the binding design exactly, confirmed by the full
  `middleware/middleware.go` diff read above: query/form presence
  detected via the `ok` boolean only, no new body-parsing logic added
  (still only the existing cached `r.ParseForm()` call), `values` built
  from Authorization headers only, new `unsupportedTransport` field,
  `present` widened correctly.
- [x] `RequireAPIKey`'s rejection condition extended with a single `||
  credential.unsupportedTransport`, sharing the exact same
  `limiter.RecordFailure(clientIP)` call and `401 Invalid API Key`
  response as every other invalid-credential case — confirmed by direct
  code read; no second rejection path was added anywhere in the function.
- [x] Blocked-IP pre-check ordering unchanged (still runs via
  `limiter.Blocked(clientIP)` before the credential/transport
  `if` block) — confirmed by code read and by
  `TestAPIAuthRateLimitBlockedIPRejectsRemovedTransportBeforeInspection`.

### Section 3 — Response shape
- [x] `JSONError(w, http.StatusUnauthorized, "Invalid API Key")` is reused
  verbatim for the unsupported-transport path — confirmed by code read;
  no new/distinct error string was introduced anywhere in the diff
  (`git diff` shows only the condition's `||` extension, no new
  `JSONError` call site).

### Section 4 — Rate limiter semantics
- [x] Read `TestAPIAuthRateLimitUnsupportedTransportCountsAsOneFailure`
  directly (not just its name): it uses the established budget-depletion
  harness (burst=20, refill=20/min) — 19 failing requests still get 401,
  the 20th (which is the SAME combined-transport or combined-transport-
  plus-Authorization request) still gets the historical 401 (proving it
  consumed only one unit of the 20-unit budget despite carrying two or
  three credential sources at once), and only the 21st request gets 429.
  This is a genuine proof of exactly one `RecordFailure` call per
  request, not a name-only assertion. All three subtests (`query and form
  both present`, `query + simultaneously valid Authorization`, `form +
  simultaneously valid Authorization`) independently re-run, PASS.
- [x] The same `limiter.ResolveClientIP(r)`/`currentAPIAuthRateLimiter()`
  call sites already used by every other `RequireAPIKey` path are reused
  — confirmed by code read: `RequireAPIKey`'s single `limiter :=
  currentAPIAuthRateLimiter(); clientIP := limiter.ResolveClientIP(r)`
  block runs unconditionally for every explicit-credential request,
  BEFORE the (now-widened) malformed/ambiguous/empty/unsupported-
  transport check; no second IP-resolution path exists anywhere in the
  diff.
- [x] Session-only requests consume zero limiter budget — unchanged code
  path (the `if !credential.present` branch returns before `limiter :=
  currentAPIAuthRateLimiter()` is ever reached), confirmed by code read.

### Section 5 — CSRF
- [x] `CSRFExceptions`'s own code is completely unchanged (`git diff` on
  that function: zero lines touched) — it still calls
  `extractExplicitAPICredential(r).present`, which the item-2 change
  widens correctly and exactly as the goal dictates, with no new
  unsupported-transport special-casing added to `CSRFExceptions` itself.
- [x] No file matching `*csrf*` appears anywhere in `git diff main..HEAD`
  (confirmed via `git diff main..HEAD -- '*csrf*'`, empty output) and
  `controllers/route_test.go` (the other file containing `TestCSRF*`
  functions in this repo, confirmed via repo-wide grep for `func Test.*
  CSRF`) has a completely empty diff — the full pre-existing CSRF test
  suite is genuinely unmodified. One new test
  (`TestCSRFExceptionsRemovedTransportStillExempt`) was *added* to
  `middleware/api_auth_test.go`, which is additive regression coverage
  for the new behavior, not a modification of any existing CSRF test —
  independently re-run, both subtests PASS.

### Section 6 — RBAC
- [x] `git diff --stat main..HEAD` contains no file under any
  RBAC/permission-sounding path (full stat read directly above) — no
  RBAC file is touched anywhere in the diff.

### Section 7 — Docs
Read the full diff of all four docs directly:
- [x] `docs/API_AUTHENTICATION.md`: new "Removed in `0.13.0`: query and
  form `api_key`" section states "removed in 0.13.0" (past/completed
  state, no fallback), replacing the old "deprecated, removal targeted"
  prose; raw Authorization's row is now the ONLY remaining item in the
  "one deprecated legacy transport" list, with its "no removal version
  announced" wording unchanged verbatim; Bearer's "only recommended and
  canonical" wording unchanged verbatim.
- [x] `docs/API_KEY_TRANSPORT_DEPRECATION.md`: transport table rows for
  query/form now read `**Removed**` / `0.13.0` (was `Deprecated legacy` /
  `0.13.0` target); raw Authorization's row is unchanged (`deprecated
  legacy`, `no removal version`); "Before (...)" migration examples are
  retained, now explicitly labeled "historical migration example".
- [x] `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md`: new
  "Removed" section states query/form "removed in 0.13.0 ... rejected ...
  no fallback"; explicitly states rotation of any key "ever sent via
  query" and that "historical access-log cleanup is an operator
  decision"; explicitly does NOT claim retroactive log scrubbing (reads
  "does not erase a copy already written... before migration"); raw
  Authorization section unchanged ("remains documented as deprecated
  legacy, with no removal version announced... This release does not
  change its acceptance").
- [x] `docs/GROUP_IMPORT_LIMITS.md`: only the single auth-table cell
  referencing `api_key` was touched (`git diff` shows exactly one
  changed line) — no non-auth import-limit content touched; now reads
  "the `api_key` query parameter was removed in `0.13.0`".
- [x] No claim anywhere in the diff that this PR "fixes" or "removes" a
  historical access-log exposure — confirmed by reading every doc's
  changed prose directly; the opposite is explicitly stated in both
  `API_AUTHENTICATION.md` and the release note (quoted above).

### Section 8 — Tests
- [x] Every contract-table row from section 1 has a corresponding test,
  confirmed above. `middleware/api_auth_rate_limit_test.go` was extended
  with exactly the new cases (combined query+form,
  Authorization+query/form), following the existing budget-depletion
  harness style, without duplicating PR #67's full suite — confirmed by
  diff read (only two new test functions added, nothing removed/altered
  from the existing ones below the insertion point, confirmed by diffing
  the unmodified tail of the file).
- [x] `controllers/api/campaign_transport_deprecation_test.go`:
  query/form subtests renamed and now call a new
  `assertCampaignRejected` helper (401 + JSON content-type + campaign NOT
  completed, reloaded from the DB); Bearer/raw subtests untouched, still
  call `assertCampaignCompleted`; the trailing-slash regression subtest
  was correctly updated to use `Authorization: Bearer` instead of the now
  -removed query transport, so a non-200 result there can only mean a
  genuine route mismatch.
- [x] `middleware/api_auth_form_fields_test.go`: the "without api_key"
  subtest (canonical Bearer, api_key removed from body) still proves
  business-field preservation; the "with api_key" subtest now asserts
  401 AND (via a `handlerCalled` flag) that the business-field handler
  itself is never invoked — a genuinely stronger assertion than mere
  status code.
- [x] `go test -race ./middleware/...` independently re-run in this
  iteration: clean, no races.
- [x] Full existing CSRF suite re-run unmodified and green (section 5,
  above).
- [x] No Docker-based API-auth integration harness exists in this repo
  beyond the MySQL/Postgres backend-selection tests already unrelated to
  transport selection (grep confirms no `docker`-tagged test references
  query/form `api_key`); not applicable, correctly not fabricated.
- [x] Playwright suite: zero frontend asset diff independently reconfirmed
  via `git diff --stat main..HEAD -- static/js/dist static/css/dist`
  (empty) in this iteration, consistent with the orchestrator's prior
  13/13 pass confirmation.

### Section 9 — Docsguard
- [x] `factQueryDeprecated0130`/`factFormDeprecated0130` removed; replaced
  by `factQueryRemoved0130`/`factFormRemoved0130` — read the new fact
  definitions directly: each is still exactly three
  `regexp.MustCompile` literal/narrow patterns (a transport-identifying
  phrase, a `remov\w*` stem instead of the old `deprecat\w*` stem, and
  the literal `0.13.0` version string) — equally narrow, no
  Markdown/HTML parsing growth whatsoever.
- [x] Registered against the same four canonical docs the old facts
  covered — confirmed by reading the full `canonicalDocs` table diff:
  `API_AUTHENTICATION.md`, `GROUP_IMPORT_LIMITS.md` (query fact only,
  unchanged from before), `RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md`,
  and `API_KEY_TRANSPORT_DEPRECATION.md` all still carry the renamed
  facts in the same positions.
- [x] `factBearerCanonical`, `factRawDeprecatedNoRemovalVersion`,
  `factRotationGuidance`, `factLogCleanupGuidance`,
  `checkForbiddenParameterCredential`, `checkForbiddenAuthorization`:
  zero lines of their definitions appear in the diff — unchanged,
  confirmed by direct read of the full `docsguard.go` diff.
- [x] `checkCanonicalExamples`'s exemption logic itself (the actual
  conditional/boolean mechanics) is unchanged in the diff — only its doc
  comment was extended to explain why no mechanical change was needed
  (the "Before" examples now describe a removed rather than merely
  deprecated transport, which is pure prose and doesn't touch the
  exemption's trigger condition). This matches the goal's explicit
  allowance for "a small, narrow adjustment" while confirming no parser
  growth occurred — in fact here the adjustment needed was zero
  mechanical change, only documentation of why.
- [x] `internal/docsguard/docsguard_test.go` updated consistently (fixture
  strings changed from "is deprecated, removal targeted for 0.13.0" to
  "was removed in 0.13.0"); all docsguard tests independently re-run,
  PASS.

### Section 10 — Out-of-scope guardrails
Independently re-verified via `git diff main..HEAD` (not just `--stat`)
on each file:
- [x] `VERSION`: empty diff.
- [x] `package.json`: empty diff.
- [x] `go.mod`: empty diff.
- [x] `go.sum`: empty diff.
- [x] `yarn.lock`: empty diff.
- [x] `.github/workflows/release.yml`: empty diff.
- [x] `static/js/dist` and `static/css/dist`: empty diff.
- [x] No RBAC/permission file touched (section 6, above).
- [x] Full `git diff --stat main..HEAD` (13 files) contains only: the
  goal/status bookkeeping files, the four docs, `internal/docsguard/`
  (impl + test), five Go test files, and `middleware/middleware.go`
  itself — nothing outside this PR's stated scope.

### Section 11 — Git/commit discipline
- [x] Builder's commit `e135edac88391521d7943c6ff0c6c8cd8d859fab`
  independently re-verified in this iteration via `git log -1
  --format=fuller --show-signature` run through WSL (native Windows git
  on this host has no `gpg` on PATH — confirmed by direct
  `cannot spawn gpg: No such file or directory` error from the Windows
  git invocation itself, not merely assumed — this is an environment
  quirk of the Windows-side git, not a signing failure; the same
  repository's WSL-side git, which actually performed the Builder's
  signing, shows the real result): `gpg: Good signature from "vg-kvr-grp
  <killian.vanruymbeke@vesperisgroup.com>" [ultimate]`, using RSA key
  `EB3E931B95880FB8931299E59DFC16E2E234C67B`. Author and committer both
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`. Zero Copilot
  mentions in the commit message (only `Assisted-by: Claude:Sonnet-4.6`).
- [x] Working tree clean before this review (`git status` showed
  "nothing to commit, working tree clean" after removing one stray,
  untracked, Inspector-created scratch file that was never part of the
  Builder's commit).

## Quality Gate
- Commands independently re-run in this iteration (via WSL, where the
  project's pinned go1.25.13-equivalent toolchain via `go.mod`'s
  `toolchain` directive resolves correctly):
  - `go build ./...` — clean.
  - `go vet ./...` — clean.
  - `go test ./...` — all packages `ok`.
  - `go test -race ./middleware/...` — clean, no races.
  - Targeted run of every test function named in goal.md sections 1, 4,
    5, 8, and 9 (`TestExplicitAPICredentialExtraction` 17 subtests,
    `TestAPIAuthenticationMechanismSelection`, `TestCredentialAware
    APIProtection`, `TestCSRFExceptionsRemovedTransportStillExempt`,
    `TestAPIAuthRateLimitUnsupportedTransportCountsAsOneFailure` 3
    subtests, `TestAPIAuthRateLimitBlockedIPRejectsRemovedTransport
    BeforeInspection` 2 subtests, `TestCampaignCompleteTransportDeprecation
    Equivalence` 5 subtests, `TestFormTransportPreservesBusinessFields` 2
    subtests, and every `TestCheckFile*`/`TestCheckAll*` docsguard test) —
    all PASS, assertions read directly, not inferred from test names.
- Result: PASS
- Details: no failures; no races; `./scripts/verify.sh` (docsguard,
  frontend rebuild diff, govulncheck on the real pinned toolchain,
  gosec/gitleaks/actionlint/zizmor) already independently confirmed green
  by the orchestrating session before this review — nothing in this
  iteration's independent re-verification contradicts that.

## Issues Found
None. This is a precise, minimal, behavior-exact implementation of a
binding contract with an unusually tricky edge case (identical-value
coincidence between a removed and a supported transport) handled
correctly by construction: `unsupportedTransport` is checked completely
independently of, and ORed unconditionally ahead of, the
value/ambiguity computation, so no code path can ever let a
simultaneously-valid Authorization header "rescue" a query/form
credential, regardless of value equality. The single-failure-per-request
rate-limiter invariant is preserved through the exact same
`RecordFailure`/`ResolveClientIP` call sites used by every other
rejection reason — no second, parallel path was introduced anywhere.
CSRF's presence-based exemption is preserved by construction (same
`.present` field, now correctly widened) rather than by a new
special-case branch, exactly as the goal required. Docs and docsguard
were updated narrowly and consistently, with zero growth in docsguard's
parsing sophistication. Every out-of-scope guardrail (`VERSION`,
`package.json`, `go.mod`, `go.sum`, `yarn.lock`, release workflow,
frontend assets, RBAC files) is verifiably untouched via direct,
non-`--stat`-only diff inspection where it mattered most.

## What Must Be Fixed (FAIL only)
N/A — PASS.
