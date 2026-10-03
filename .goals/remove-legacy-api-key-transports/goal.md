# Goal: remove legacy query/form API-key transports for 0.13.0 (security/remove-legacy-api-key-transports)

This file is the immutable binding acceptance contract for this PR. The
Builder implements against it; the Inspector judges strictly against it and
nothing else. If any acceptance criterion conflicts with a convenience
shortcut, the criterion wins. If a criterion is found to be factually wrong
after re-validation against the current codebase, STOP and report the
conflict instead of silently reinterpreting it.

## 0. Context (confirmed facts, re-validate before writing code)

- `middleware/middleware.go`'s `extractExplicitAPICredential` is the SINGLE
  function that currently merges FOUR credential sources into one `values`
  slice: query `api_key`, form `api_key`, and every `Authorization` header
  (Bearer-prefixed or raw). Presence is tracked independently of value
  (`explicitAPICredential.present`); an empty/malformed/ambiguous credential
  is already a rejection, never a silent session fallback.
- `RequireAPIKey` (same file) is the sole consumer: if `!credential.present`,
  it falls back to the ambient session (`ctx.Get(r, "user")`); otherwise it
  checks the failure-budget rate limiter (`currentAPIAuthRateLimiter()`)
  BEFORE validating the credential value, then rejects malformed/ambiguous/
  empty credentials with exactly one `limiter.RecordFailure(clientIP)` call
  and a `401 Invalid API Key` JSON response, and otherwise performs
  `apiKeyLookup` normally.
- `CSRFExceptions` (same file) exempts a request from the CSRF same-origin
  check whenever `extractExplicitAPICredential(r).present` is true — this is
  presence-based, not value-based, and this PR must not change that: a
  request carrying a now-unsupported query/form credential is still an
  explicit API-auth *attempt*, not a browser page navigation, and must stay
  exempt from the browser-page CSRF contract exactly as today.
- Docs: `docs/API_KEY_TRANSPORT_DEPRECATION.md` already documents query/form
  removal target as `0.13.0`, and raw `Authorization: <token>` (no `Bearer `
  prefix) as deprecated with **no** announced removal version. This PR must
  not change raw Authorization's runtime acceptance or its "no removal
  version" documented status.
- `internal/docsguard/docsguard.go` currently has two facts named
  `factQueryDeprecated0130` and `factFormDeprecated0130` asserting "query/
  form are marked deprecated with removal targeted for 0.13.0" against
  specific canonical docs. This PR changes the state those docs describe
  (query/form are now REMOVED, not merely deprecated), so these two
  assertions must be replaced with new, equally narrow, literal, bounded
  assertions proving the docs now say "removed in 0.13.0" — docsguard must
  NOT grow into a Markdown/HTML parser; reuse its existing narrow-literal-
  match style exactly (see `factBearerCanonical`, `factRawDeprecated
  NoRemovalVersion`, `factRotationGuidance`, `factLogCleanupGuidance`,
  `checkForbiddenParameterCredential`, `checkForbiddenAuthorization` for the
  established pattern/style to follow).
- Known test files that currently assert query/form transport SUCCESS and
  must be inverted to assert REJECTION (re-verify each still exists at
  these paths/names before editing; if any has moved/been renamed, use the
  actual current location):
  - `middleware/api_auth_test.go` — `TestExplicitAPICredentialExtraction`,
    subtests `"query parameter"`, `"form parameter"`, `"identical repeated
    query parameters"`, `"identical values across every source"`.
  - `controllers/api/campaign_transport_deprecation_test.go` —
    `TestCampaignCompleteTransportDeprecationEquivalence`, subtests `"query
    parameter (deprecated) completes the campaign"` and `"form parameter
    (deprecated) completes the equivalent campaign"`.
  - `middleware/api_auth_form_fields_test.go` —
    `TestFormTransportPreservesBusinessFields`, subtest `"deprecated form
    api_key preserves business fields"`.
  - `middleware/api_auth_rate_limit_test.go` — already asserts `401` for bad
    query/form credentials today (treating them as ordinary auth failures);
    re-verify its assertions still make sense under the new contract (an
    unsupported-transport request should behave identically to today's
    "bad credential" path for rate-limiter purposes — single failure count,
    401 below threshold, 429 at/above threshold, per this goal's binding
    rate-limiter semantics below) and extend it with the specific new cases
    listed in section 8 below if not already covered.
- `docs/API_AUTHENTICATION.md`, `docs/API_KEY_TRANSPORT_DEPRECATION.md`,
  `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md`,
  `docs/GROUP_IMPORT_LIMITS.md` all currently contain query/form
  transport references that must be updated to describe the REMOVED (not
  merely deprecated) state, while leaving the raw-Authorization and Bearer
  content accurate and unchanged.
- No first-party (Go or frontend) runtime code constructs a query or form
  `api_key` request anywhere in this repository (confirmed by exhaustive
  prior audit). SPA session flow is unaffected and uses no API key at all.
- `VERSION`, `package.json`'s `"version"` field, `go.mod`'s `go`/`toolchain`
  directives, and the release workflow are explicitly OUT OF SCOPE for this
  PR — do not touch any of them, even if a `govulncheck` toolchain finding
  surfaces during validation (report it separately, do not fix it here).
- `go.mod`/`go.sum`/`package.json`/`yarn.lock` must remain byte-identical to
  base; no new dependency anywhere.

If re-validation finds any of the above facts to now be wrong (file moved,
function renamed, test no longer exists, docsguard fact names differ),
STOP and report the discrepancy before writing code that depends on it.

## 1. Binding target contract (the actual behavior to implement)

For every request reaching `RequireAPIKey`:

| Input | Outcome |
|---|---|
| `Authorization: Bearer <valid token>` | API-key auth succeeds (unchanged) |
| `Authorization: <valid token>` (raw, no `Bearer `) | API-key auth succeeds, still deprecated (unchanged) |
| No explicit credential at all, valid session | Session auth succeeds (unchanged) |
| Query `api_key` present, ANY value including empty/absent-`=` | `401` JSON, explicit-attempt semantics (no session fallback) |
| Form `api_key` present, ANY value including empty | `401` JSON, explicit-attempt semantics (no session fallback) |
| Query `api_key` present **and** a valid session cookie | `401` — session is NEVER used as a fallback |
| Form `api_key` present **and** a valid session cookie | `401` — session is NEVER used as a fallback |
| Query `api_key` present **and** a valid `Authorization` header (Bearer or raw) | `401` — the unsupported transport's mere presence invalidates the whole attempt; the valid Authorization value is NOT treated as a "winner" |
| Form `api_key` present **and** a valid `Authorization` header | `401`, same rule |
| Query **and** form `api_key` both present | `401`, counted as exactly ONE authentication failure (never two) |
| Query `api_key` present with a value IDENTICAL to a simultaneously-present valid `Authorization` header's token | `401` — identical value does NOT rescue it; presence of the unsupported transport alone is disqualifying, regardless of any value-equality coincidence with a supported transport |
| Two distinct `Authorization` header values (Bearer and/or raw) | Existing ambiguous-credential behavior, UNCHANGED by this PR |
| IP already blocked by the failure-budget limiter | `429` with the existing `Retry-After` JSON shape, returned BEFORE any credential/transport inspection — unchanged pre-check ordering |

## 2. Implementation shape (binding on *behavior*, not literal code — but
   this is the verified-correct minimal design; deviate only with a
   documented, equally-correct reason)

In `extractExplicitAPICredential`:
- Detect query `api_key` presence via `r.URL.Query()["api_key"]`'s `ok`
  boolean (exactly as today), but do **not** fold its values into the
  `values` slice used for the Bearer/raw credential value and ambiguity
  computation anymore.
- Detect form `api_key` presence via `r.PostForm["api_key"]`'s `ok` boolean
  (exactly as today, same content-types as today — do not add multipart
  form parsing or any new body-parsing logic), likewise without folding its
  values into `values`.
- Keep `values` built from `r.Header.Values("Authorization")` only (Bearer-
  prefix-stripped as today) — this preserves the existing identical-value-
  tolerant / distinct-value-ambiguous behavior for Authorization headers
  alone, unchanged.
- Add a new field (e.g. `unsupportedTransport bool`) to
  `explicitAPICredential`, set true when EITHER query or form `api_key` was
  present (by key, regardless of value — an empty value or a value equal to
  a concurrently-present Authorization token must still set this true).
- `present` must become true whenever `unsupportedTransport` is true, even
  if `values` is empty (e.g. query `api_key` present with zero Authorization
  headers at all) — this is required so `RequireAPIKey` never falls back to
  session and so `CSRFExceptions`' presence-based exemption still applies
  unchanged (see goal.md item 0's CSRF note).

In `RequireAPIKey`:
- After the existing blocked-IP pre-check (unchanged, runs first,
  regardless of transport), extend the rejection condition that already
  covers `credential.malformed || credential.ambiguous || credential.value
  == ""` to ALSO cover `credential.unsupportedTransport` — all four
  conditions must share the exact same single `limiter.RecordFailure
  (clientIP)` call and the exact same `401 Invalid API Key` JSON response,
  so presence of an unsupported transport is indistinguishable, from the
  rate-limiter's and the response's point of view, from any other kind of
  invalid explicit credential. Do not add a second, parallel rejection path
  or a different status/body for this case unless this goal explicitly
  requires one (it does not — see item 3 below on response shape).
- Do not alter the blocked-IP pre-check ordering, the single-
  `RecordFailure`-per-request invariant, or the session-fallback-only-when-
  `!present` structure in any other way.

## 3. Response shape (binding)

- Reuse the EXISTING `401 Invalid API Key` JSON error response
  (`JSONError(w, http.StatusUnauthorized, "Invalid API Key")`) for the
  unsupported-transport rejection path — do not invent a new, more specific
  error message (e.g. do not add "Unsupported API key transport; use
  Authorization: Bearer <token>" as a distinct string). A generic message
  is deliberately preferred here: it avoids creating a new oracle that
  distinguishes "transport rejected" from "credential invalid" for an
  unauthenticated caller, and keeps exactly one JSON error shape across
  every explicit-credential rejection reason. If, after implementing,
  there is a concrete, demonstrated reason a distinct message is actually
  necessary, STOP and raise it rather than silently adding one.
- No HTML, no redirect, no plaintext `http.Error` — JSON only, consistent
  with every other API error path.

## 4. Rate limiter semantics (binding, matches the explicit product
   decision in this task's instructions)

- An unsupported query/form transport attempt is an EXPLICIT API
  AUTHENTICATION FAILURE, full stop. It must:
  - Be subject to the exact same pre-check (`limiter.Blocked(clientIP)`,
    `429` if already blocked) as every other explicit-credential attempt,
    BEFORE any transport/value inspection.
  - Count as exactly ONE `RecordFailure` call per HTTP request, even when
    both query and form `api_key` are present simultaneously in the same
    request, and even when an Authorization header is ALSO present in the
    same request.
  - Use the exact same `ResolveClientIP`/trusted-proxy resolution already
    established by PR #66/#67 — do not introduce a second IP-resolution
    path.
  - Never be treated as a "protocol rejection, not counted" category. Do
    not add such a category anywhere in this PR.
- A request with NO explicit credential at all (no Authorization header,
  no query/form `api_key`) and a valid session must consume ZERO limiter
  budget, exactly as today (session path never touches the limiter).

## 5. CSRF (binding)

- `CSRFExceptions`' behavior must be unchanged in STRUCTURE: it already
  exempts any request where `extractExplicitAPICredential(r).present` is
  true from the same-origin check. Because this PR makes `present` true
  for unsupported-transport-only requests too (per item 2), this exemption
  naturally continues to apply to them — this is correct and intentional,
  not a regression: an unsupported-transport API-auth attempt is still not
  a browser page navigation, and misclassifying it as one would be a
  CSRF-contract regression in the other direction. Do not add new logic to
  `CSRFExceptions` to special-case `unsupportedTransport`.
- Re-run existing CSRF tests unmodified and confirm they still pass,
  proving no accidental contract change.

## 6. RBAC (binding)

- Query/form-only requests must never reach `apiKeyLookup` or any RBAC
  permission check — they are rejected before that point, exactly like
  today's malformed/empty/ambiguous rejection path. No RBAC code should
  need to change at all. If you find yourself touching any RBAC/permission
  file, STOP — that would indicate the design has drifted from this
  contract.

## 7. Docs (binding — update exactly these, no scope creep)

- `docs/API_AUTHENTICATION.md`: supported-transport section must read
  Bearer (canonical) + raw Authorization (deprecated, no removal version)
  + session (as currently documented). Query/form must move to a removed/
  historical section stating "removed in 0.13.0", not "deprecated,
  removal target 0.13.0".
- `docs/API_KEY_TRANSPORT_DEPRECATION.md`: update the transport table and
  surrounding prose so query/form read "Removed in 0.13.0" (past/completed
  state, not a future target), while raw Authorization's row is UNCHANGED
  ("deprecated legacy", "none announced"). Keep the Bearer migration
  examples. Any "Before" query/form example used purely as illustrative
  historical migration material may remain, clearly labeled as such (this
  is not a "recommended example" per goal.md's definition from the prior
  audit).
- `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md`: update to reflect
  the actual breaking change landing in 0.13.0: query/form no longer
  accepted at all, Bearer canonical, rotate keys historically sent via
  query, historical access-log cleanup is an operator decision (this PR
  does not retroactively scrub any log), raw Authorization still
  temporarily accepted with no announced removal version.
- `docs/GROUP_IMPORT_LIMITS.md`: fix any remaining query/form auth
  example/text to reflect removal; do not touch any non-auth import-limit
  content in this file.
- Do NOT claim this PR "fixes" or "removes" any historical access-log
  exposure risk from already-received query-string requests — explicitly
  state the opposite: removing the transport prevents FUTURE acceptance,
  it cannot retroactively un-receive or redact anything already logged
  before this change; that remains an operator-side log/rotation decision,
  exactly as the prior deprecation docs already said.

## 8. Tests (binding minimum — all must exist and pass)

- Invert the identified "query/form success" subtests listed in goal.md
  item 0 to assert REJECTION (401, explicit-attempt semantics, i.e.
  `IsSessionAuthentication` must report false and no session-derived user
  must be used) instead of success. Keep Bearer/raw subtests passing
  unchanged.
- Add/confirm explicit test coverage for EVERY row of section 1's
  contract table, at minimum:
  - Query present, non-empty valid-looking value → 401.
  - Query present, empty value (`?api_key=`) → 401.
  - Query key present with no `=` at all (`?api_key`) → 401.
  - Form present, non-empty value → 401.
  - Form present, empty value → 401.
  - Query present + valid session cookie → 401, never session fallback
    (assert via `IsSessionAuthentication`/response body, not just status).
  - Form present + valid session cookie → 401, same assertion.
  - Query present + a simultaneously valid Authorization header (Bearer or
    raw) → 401 (the valid Authorization value must NOT "win").
  - Form present + a simultaneously valid Authorization header → 401.
  - Query value IDENTICAL to a simultaneously-present valid Authorization
    header's token → 401 (identical-value coincidence does not rescue it;
    this specifically must NOT reuse the old "identical duplicate is
    accepted" logic for query/form).
  - Query AND form both present in the same request → 401, and the test
    must prove exactly ONE `RecordFailure` call (e.g. via the existing
    rate-limiter test harness/counter used by PR #67's tests), not two.
  - Session-only request (no explicit credential at all) → success,
    ZERO limiter consumption (reuse/extend the existing PR #67 assertion
    pattern for this).
  - IP already blocked (via the existing limiter test harness) + a
    query/form request → `429` directly, without needing a valid-looking
    token (proving the blocked pre-check still runs first).
  - Two distinct Authorization header values (Bearer and/or raw) → existing
    ambiguous behavior, confirm UNCHANGED (regression guard, not new
    behavior).
- `middleware/api_auth_rate_limit_test.go`: extend with the specific new
  rate-limiter cases above (single-failure-per-request for combined query+
  form, and for Authorization+query/form combinations), following its
  existing harness/style — do not duplicate PR #67's entire suite, only
  add what's new.
- `controllers/api/campaign_transport_deprecation_test.go`: rename/update
  so Bearer/raw subtests assert success (unchanged) and query/form
  subtests assert rejection (the campaign must NOT complete), including
  re-verifying any GET/POST-specific legacy nuance the existing test
  already encodes.
- `middleware/api_auth_form_fields_test.go`: confirm/extend so a form
  request WITHOUT `api_key` still has all business fields preserved
  (unchanged), and a form request WITH `api_key` is rejected before any
  business-field-dependent handler logic runs.
- `go test -race` clean for every new/modified test in this PR.
- Full existing CSRF test suite re-run unmodified, confirmed still green
  (regression guard per item 5).
- Docker/integration auth smoke (if the repo's existing Docker-based
  integration test harness covers API auth — check first; extend it with
  the specific new cases from section 1's table if it does, or state
  explicitly if no such harness exists and this step is not applicable).
- Browser (Playwright) full suite: expected ZERO change, since the SPA
  never sends an API key; this is a regression guard, not new coverage.
  If any frontend asset changes as a result of this PR, STOP and
  investigate why before proceeding — none is expected.

## 9. Docsguard (binding — narrow, literal, no parser growth)

- Remove `factQueryDeprecated0130` and `factFormDeprecated0130` (and their
  registrations against the canonical docs they currently check).
- Add new, equally narrow, literal-string-bounded facts (same style:
  simple substring/line-anchored checks, NOT a Markdown/HTML parser)
  proving the canonical docs now state query and form are REMOVED in
  0.13.0, registered against the same canonical docs the old facts
  covered (re-verify exact file list from the current
  `canonicalDocs`/registration table before writing the new entries).
- Keep unchanged: `factBearerCanonical`, `factRawDeprecatedNoRemoval
  Version`, `factRotationGuidance`, `factLogCleanupGuidance`,
  `checkForbiddenParameterCredential` (still forbids a working `api_key=`
  example in non-migration docs — if anything, this check becomes MORE
  clearly correct now that query/form are removed, not just deprecated),
  `checkForbiddenAuthorization` (raw Authorization promotion still
  forbidden outside the migration doc).
- `checkCanonicalExamples`'s legacy-"Before"-example exemption logic may
  need a small, narrow adjustment to reflect that the "Before" examples in
  the migration doc are now describing a fully-removed transport rather
  than a currently-still-accepted deprecated one — keep this adjustment
  as narrow as the rest of docsguard's existing style; do not generalize
  it into a broader markdown-semantics engine.

## 10. Explicit out-of-scope guardrails (binding — verify at the end, not
    just at the start)

- `VERSION` file: byte-identical to base.
- `package.json`'s `"version"` field: byte-identical to base.
- `go.mod`'s `go`/`toolchain` directives: byte-identical to base.
- `.github/workflows/release.yml` and any other release-workflow file:
  byte-identical to base.
- Raw (non-Bearer) `Authorization` acceptance and its "deprecated, no
  removal version" documented status: UNCHANGED.
- Rate-limiter thresholds/config, trusted-proxy resolution logic, CSRF
  architecture, API-key verifier/keyring mechanics: UNCHANGED.
- No frontend asset diff expected (`static/js/dist/**`,
  `static/css/dist/**`) — if the build pipeline produces any diff there,
  STOP and investigate before proceeding; do not just accept it.
- `go.mod`/`go.sum`/`package.json`/`yarn.lock`: byte-identical to base.

## 11. Git/commit discipline (binding, same as every prior PR this
    session)

- Every commit signed (`git commit -S`), author AND committer
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`, verified after
  EVERY commit with `git log -1 --format=fuller --show-signature` showing
  `Good signature`.
- If GPG signing fails, appears unavailable, or requires an unlock that is
  not available: STOP immediately and report to the user. NEVER commit
  unsigned "temporarily." NEVER claim GPG is unavailable without
  independently proving it (this exact failure mode happened once already
  in this session, on a prior PR — an Inspector falsely claimed "GPG
  unavailable" and committed unsigned; that must never happen again).
- Zero Copilot authorship/committer/co-authored-by/metadata anywhere in
  any commit.

## 12. Definition of done / merge gates

- Every row of section 1's contract table verified by a passing test.
- Section 4's rate-limiter semantics verified by a passing test
  (single-failure-per-request for combined/simultaneous transports).
- Section 5's CSRF regression guard green (existing suite unmodified,
  still passing).
- Section 6's RBAC claim verified (no RBAC file touched in the diff).
- Section 7's docs fully updated, section 9's docsguard updated and
  green.
- Section 10's out-of-scope guardrails verified via `git diff --stat`
  showing none of those files touched.
- `./scripts/verify.sh`, `go build ./...`, `go vet ./...`, `go test ./...`,
  `go test -race ./...` green.
- `govulncheck`, `gosec`, `gitleaks`, `actionlint`, `zizmor` green (no NEW
  findings vs. the established baseline; a pre-existing toolchain-related
  govulncheck finding, if reproduced, must be reported separately, not
  fixed here).
- `yarn audit`/Retire.js baseline unchanged (no frontend change expected
  at all in this PR).
- Full CI green on the PR.
- Independent security review: no findings (or all findings resolved).
- Independent code review: no findings (or all findings resolved).
- Goal Inspector PASS against this exact file.
- `git status`/`git diff --exit-code` clean before every commit and
  before merge.

Do not start, in this PR: `release/0.13.0-preparation`, Go toolchain
bump, `package.json` version alignment, automatic retention, credential
minimization, raw-Authorization removal. These remain explicitly
deferred items for separate future work.
