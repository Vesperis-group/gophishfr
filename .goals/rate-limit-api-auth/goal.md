# Goal: rate-limit-api-auth

## Status
IMMUTABLE after creation. Sole acceptance contract for Builder and Inspector.
Neither may alter it.

## Repository
/home/killian/gophishfr
Branch: security/rate-limit-api-auth
Base: main @ (see status.json initial_sha = 00305ef0dc60b705c32b5d4e4d6b21b0c7d43196)

## Context (confirmed facts from prior read-only audit and PR #66, not to be
## re-litigated)

- `middleware/clientip` (merged in PR #66) resolves the real client IP behind
  an explicit trusted-proxy model: default trust-none, right-to-left
  X-Forwarded-For evaluation, X-Real-IP only under strict conditions, safe
  fallback to the raw socket peer on anything malformed or untrusted. This is
  the ONLY source of truth for client IP anywhere in this PR.
- `middleware/ratelimit.PostLimiter` (fixed in PR #66) protects `POST /login`
  only, using the new safe `ClientIP` resolution, 5 requests/minute, with a
  corrected periodic-cleanup ticker. Its behavior, parameters, and request
  semantics (not failure-only — it limits ALL POST attempts, success or
  failure) must NOT change in this PR.
- `middleware.RequireAPIKey` (in `middleware/middleware.go`) selects between
  API-key and session authentication via `extractExplicitAPICredential`:
  presence of ANY of query `api_key`, form `api_key`, or an `Authorization`
  header (Bearer-prefix-stripped or raw) makes the request go through the
  API-key path; absence of all three uses session authentication. Explicit
  credential + bad/ambiguous/empty value NEVER falls back to session — this
  is a binding, already-tested contract (`middleware/api_auth_test.go`,
  `TestAPIAuthenticationMechanismSelection`).
- `models.GetUserByAPIKey` → `getUserByAPIKey` computes HMAC-SHA256
  "candidates" (one per keyring key, bounded to 16) via
  `internal/apikey.Service.Candidates`, then does one indexed DB lookup
  (`candidateWhere`, unique composite index on `(api_key_verifier_key_id,
  api_key_verifier)`). Locked accounts are excluded directly in the SQL WHERE
  clause (`account_locked IS NULL OR account_locked = false`), so a locked
  account's token is indistinguishable from an unknown token at the error
  level — this must remain true; do not introduce a new oracle.
- Verifier/keyring-unavailable (`ErrAPIKeyVerifierUnavailable`), ambiguous-
  candidate (`ErrAmbiguousAPICredential`), invalid-credential
  (`ErrInvalidAPICredential`), and raw database errors are currently all
  flattened into a single `401 Invalid API Key` response by
  `middleware.RequireAPIKey`. This PR must introduce the MINIMUM internal
  error classification needed to distinguish failures attributable to the
  client (count against the rate limit) from server/infrastructure failures
  (never count), WITHOUT changing the external response body/status for
  requests below the rate-limit threshold.
- `middleware.JSONError(w, code, message)` is the existing JSON error helper
  (`models.Response{Success: false, Message: m}`) already used by
  `RequireAPIKey`. Use it for the new 429 response; do not invent a different
  response shape.
- No rate limiter protects `/api/*` authentication today. `controllers/api/
  server.go` has an unused `limiter *ratelimit.PostLimiter` field and
  `WithLimiter` option that are never wired to any route.
- `golang.org/x/time/rate` is already a direct dependency, used by
  `ratelimit.PostLimiter` as a classic "successful call consumes a token"
  limiter via `limiter.Allow()`. That exact pattern must NOT be reused naively
  here, because it would consume budget on every request (including valid
  ones) rather than only on client-attributable authentication failures. A
  small internal primitive (failure-budget semantics: pre-check without
  consuming, explicit single-failure recording, no success-driven consumption
  or reset) must be built on top of/alongside the existing `golang.org/x/time/
  rate` token-bucket primitive or equivalent minimal logic — no new
  dependency.
- Prior read-only audit (session history) established: HMAC candidate
  computation + indexed DB lookup for a bad token is cheap (microseconds),
  not the kind of expensive operation the pre-check exists to protect against
  resource-wise — but a pre-check before HMAC/DB computation is still
  required by this goal's binding design (see below) for volumetric/strict
  semantics reasons (avoiding unnecessary work entirely once an IP is
  blocked), not because the cost is otherwise dangerous.

## Objective (single, exact)

Add failure-based rate limiting to explicit API-key authentication attempts
(Bearer/raw Authorization/query/form, for as long as each remains accepted),
using the client IP resolved by PR #66's foundation, counting ONLY
authentication failures attributable to the client credential, never
touching session-only requests, RBAC/authorization failures, or server-side
failures.

## Explicitly out of scope (must NOT be touched)

- Removing query/form API-key transports (still scheduled for 0.13.0,
  unrelated to this PR).
- Removing or deprecating raw Authorization further than its current state.
- Changing Bearer transport behavior.
- Changing the API-key token format, the HMAC verifier protocol, or the
  keyring.
- Changing `middleware/clientip`'s trusted-proxy semantics (PR #66) in any way
  — only consume its existing public API.
- Changing `/login`'s `PostLimiter` behavior, parameters (5/min), response
  body, or status semantics.
- Changing session authentication, RBAC, or CSRF behavior.
- Adding Redis or any distributed store.
- Adding any new Go or JS dependency. `go.mod`, `go.sum`, `package.json`,
  `yarn.lock` must be byte-identical to the base commit.
- Adding a global (cross-IP) rate limiter.
- Adding a token-fingerprint-based bucket key (IP-only, per the binding
  design below).
- Adding persistent telemetry/structured per-attempt logging.
- A multi-instance/distributed rate-limit coordination mechanism.
- npm/yarn dependency remediation, `events.details`, or any other item from
  prior out-of-scope lists.

## Required design (binding, not a menu of options)

### Limited subject

The subject being rate-limited is exactly: **an explicit API credential
authentication attempt**. This includes Bearer, raw Authorization, query
`api_key`, and form `api_key` uniformly — do not branch the limiter logic per
transport. It explicitly EXCLUDES any request where
`extractExplicitAPICredential` reports no explicit credential present (pure
session-only requests). The limiter must be invoked only in the branch of
`RequireAPIKey` where an explicit credential is present; the session branch
must have zero limiter lookups and zero bucket mutations.

### Client IP

Use exclusively the `middleware/clientip` primitive from PR #66 (the
`Resolver`/`ClientIP` already wired into the admin/phishing handler chains
and reused by `PostLimiter`). Do not re-parse `r.RemoteAddr`,
`X-Forwarded-For`, or `X-Real-IP` anywhere in the new limiter code. There must
be exactly one source of truth for client IP.

### Bucket key

The bucket key is the resolved client IP ONLY — never IP+token, token hash,
user ID, verifier, or transport. Reuse the same IPv4/IPv6 normalization
`clientip` already performs.

### Failure-budget semantics (binding)

This is NOT a classic "successful call consumes a token" limiter. It
represents a budget of AUTHENTICATION FAILURES attributable to the client:

- A pre-check must be able to answer "is this IP currently blocked by its own
  prior failures?" WITHOUT consuming/recording a new failure and WITHOUT
  requiring a successful or failed authentication attempt to have just
  occurred.
- A successful API-key authentication consumes NOTHING from the budget.
- A successful API-key authentication does NOT reset or decrease the
  failure budget (an attacker holding one valid key must not be able to erase
  their brute-force history against other tokens from the same IP by
  authenticating once). The budget only recovers via natural refill over
  time.
- A client-attributable authentication failure records EXACTLY ONE failure
  against the bucket, regardless of which transport or how many conflicting
  credentials were present in the single request (ambiguous credentials in
  one request = one failed attempt, not one per credential).
- Authorization (RBAC/permission) failures occurring AFTER successful
  authentication must never touch the limiter at all.
- Server-side/infrastructure failures (verifier keyring unavailable,
  ambiguous internal state, raw database errors) must never touch the
  limiter at all — a server-side outage must never be able to lock out all
  client IPs.
- You must build this on top of `golang.org/x/time/rate` (already a
  dependency) or an equally minimal internal mechanism — no new dependency,
  no large new generic rate-limiting framework. Implement something like
  `Blocked(ip, now) bool`, `RecordFailure(ip, now)`, `RetryAfter(ip, now)
  time.Duration` (naming flexible) with these exact semantics, thread-safe
  under concurrent access, with a single, clearly-documented, deterministic
  convention for the exact request that pushes a bucket from "at threshold"
  to "over threshold" (either that request itself may already return 429, or
  it may still return the historical failure status and only the NEXT
  request is blocked — pick one, document it, test it consistently).

### Pre-check ordering (binding)

If the resolved client IP is already blocked (budget exhausted) when an
explicit-credential request arrives:
- HMAC candidate computation must NOT be performed.
- The database lookup must NOT be performed.
- The response must be `429 Too Many Requests`, via `middleware.JSONError`
  (JSON body, matching the existing `models.Response` shape), never
  `http.Error`/HTML/redirect.
- A `Retry-After` header must be present, as an integer, non-negative,
  delta-seconds value (never an HTTP-date), computed from when the bucket
  will next allow an attempt, with a sane minimum (e.g. at least 1 when any
  positive wait is needed).
- The 429 response body must be generic and must never reveal whether a
  token exists, whether a user exists, whether an account is locked, which
  verifier key matched, or which transport was used.
- This must be provable by a test that proves HMAC/DB work is skipped when
  blocked (an injectable hook/instrumentation point for tests is acceptable;
  it must not be exposed/reachable in production response behavior).

### Failure taxonomy (binding)

MUST count as exactly one client-attributable authentication failure:
- Unknown API token (valid format, no matching user).
- Malformed explicit credential (the existing `credential.malformed` path).
- Empty explicit credential value (`api_key=`, `Authorization: ` with no
  token, etc. — these are explicit attempts with an empty value, not absence
  of credential).
- Ambiguous explicit credentials (distinct values across transports in one
  request) — counts as ONE failure for that request, not one per transport.
- A token whose format/verifier lookup succeeds structurally but which
  belongs to a locked account, IF AND ONLY IF this remains indistinguishable
  from "unknown token" at the error-classification level (no new oracle
  introduced). Do not add a distinct "locked" failure class that an attacker
  could use to enumerate valid-but-locked tokens faster/slower than unknown
  ones.

MUST NOT count:
- A session-only request (no explicit credential at all).
- A successful API-key authentication.
- An RBAC/permission-denied (403) response after successful authentication.
- A view-only/EnforceViewOnly mutation refusal after successful
  authentication.
- CSRF-related session failures (unrelated code path entirely).
- Verifier keyring unavailable / infrastructure misconfiguration.
- Any raw database error / internal server error.

### External response semantics below threshold (binding)

Below the rate-limit threshold, the existing public behavior must be
UNCHANGED: the same status codes and JSON error bodies that `RequireAPIKey`
currently returns for bad/empty/ambiguous credentials, server failures, RBAC
denials, etc. The new internal error classification (client vs. server
failure) must be used ONLY to decide whether to record a limiter failure —
it must not leak into or change any externally-visible response for
non-blocked requests. Do not change public error messages "just because" the
limiter needs to know the failure class internally.

### Parameters (v1, binding — do not tune without justification)

- Burst: 20 failures.
- Refill: 20 failures/minute.
- These are intentionally more permissive than `/login`'s 5/minute because
  only failures consume budget (valid high-throughput API clients spend
  nothing) and because shared NAT/CI egress IPs exist.
- Bucket expiry: ~10 minutes of inactivity, consistent with the existing
  `ratelimit` package's `DefaultExpiry`. Reuse PR #66's corrected periodic-
  cleanup mechanism/pattern rather than reintroducing the historical ticker
  bug. Do not duplicate that bug.
- No new user-facing configuration knob in this PR. Document that these
  values may be tuned later.
- Do NOT change `/login`'s 5/minute or any of its other parameters.

### Internal error classification (binding, minimal)

Introduce the minimum internal typing needed to distinguish
`CLIENT_AUTH_FAILURE` from `SERVER_AUTH_FAILURE` inside the API-key
authentication path (e.g. sentinel errors, `errors.Is`, or a small typed
error — follow existing repository conventions such as the sentinel errors
already in `models/user.go`: `ErrAPIKeyVerifierUnavailable`,
`ErrInvalidAPIKeyState`, `ErrInvalidAPICredential`,
`ErrAmbiguousAPICredential`). Never log the credential value anywhere in this
classification path.

### Logging (binding)

- No new log line per authentication attempt (success or failure). Any
  attack that tried to brute-force must not be able to cause log
  amplification/disk exhaustion via this PR's code.
- Never log: the token/credential value, the `Authorization` header value,
  the full query string, the form body, the computed verifier/HMAC
  candidate.
- If a historical global 429-related log line exists, audit (do not modify
  unless required) that it carries no sensitive query data.
- Explicitly report in the final write-up: `APPLICATION AUTH LOGGING: none
  added` and `LEGACY ACCESS-URI RISK: remains until 0.13.0` (query `api_key`
  can still appear in the URI seen by `CombinedLoggingHandler`'s access log —
  this is a pre-existing, separately-tracked risk, out of scope to fix here;
  do not claim it is resolved).

## Required tests (minimum; Builder/Inspector may add more)

- N failures below threshold (N < 20) → historical per-case auth status
  (401/etc.) for each, bucket not yet blocking.
- Threshold boundary (exactly at 20th failure, and the 21st) → prove the
  chosen, documented convention deterministically.
- Blocked IP → 429 JSON with `Retry-After`, and HMAC/DB work is proven
  skipped (via a test-only hook/instrumentation, not in production code
  paths).
- Refill: after waiting (via injected clock, never a real sleep), a
  previously-blocked IP becomes unblocked again per the 20/minute rate.
- A successful API-key authentication does not consume budget (prove: do N
  successes, still 20 failures of headroom afterward).
- A successful API-key authentication does not reset existing recorded
  failures (prove: accumulate some failures, succeed once, confirm the
  failure count/remaining budget is unchanged, not reset to 20/full).
- RBAC (403) after successful auth does not consume budget.
- View-only (`EnforceViewOnly`) refusal after successful auth does not
  consume budget.
- Server/keyring/DB failure simulation does not consume budget.
- Independent IPs: blocking IP A must leave IP B fully functional.
- IPv4 and IPv6 buckets are independent and both work.
- Concurrency: concurrent requests at/near the threshold must not allow
  unbounded overshoot of the burst due to a non-atomic check-then-act race;
  proven under `go test -race`.
- Periodic cleanup/expiry of an inactive bucket proven without sleeping for
  the real configured duration.
- Explicit bad credential + valid session present simultaneously → still
  goes through the API-key branch, still counts as a failure, NEVER falls
  back to session authentication (reuses/extends the existing
  `TestAPIAuthenticationMechanismSelection` contract — that test and
  `TestLockedAccountsRejectEveryAPIKeyTransport` /
  `TestSessionAndAPIKeyRBACParity` /
  `TestInvalidStoredSessionIdentityFailsWithoutPanic` must continue to pass
  unmodified in their assertions, only augmented if genuinely necessary).
- Empty explicit credential (`api_key=`, empty Bearer value, empty form
  value) counts as a failure, not as "no credential".
- Ambiguous distinct credentials in one request count as exactly one
  failure.
- A forged `X-Forwarded-For` from a direct (non-trusted-peer) attacker, or
  two different forged leftmost XFF values from the same real client behind
  a simulated trusted proxy, must resolve to the same bucket as proven by PR
  #66's `clientip` tests — add an integration-level test here proving the
  NEW limiter actually uses that same resolution (not just that `clientip`
  itself is correct in isolation).
- `/login`'s existing test suite (`middleware/ratelimit/ratelimit_test.go`)
  must continue to pass completely unmodified in its assertions.
- The full existing auth-matrix regression suite
  (`middleware/api_auth_test.go`, `middleware/api_auth_form_fields_test.go`,
  `controllers/api/campaign_transport_deprecation_test.go`) must continue to
  pass with identical externally-visible behavior below the rate-limit
  threshold.

## Non-functional constraints

- No new Go or JS dependency; `go.mod`, `go.sum`, `package.json`, `yarn.lock`
  byte-identical to base.
- No real-time sleeps in tests; use an injectable clock/time source.
- Must keep Windows builds working.
- Full existing test/lint/scanner suite must remain green: gofmt,
  golangci-lint, go vet, go build, go test, go test -race, govulncheck,
  gosec, gitleaks, actionlint, zizmor, yarn audit, retire, browser tests,
  Docker compatibility tests, Ansible bootstrap test.
- No change to RBAC, session auth, CSRF, or any API-key transport mechanics.
- No new persistent logging.

## Git / commit requirements (binding, same as prior PRs in this repo)

- Every commit's Author and Committer must be exactly
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- Every commit must be GPG-signed GOOD with key
  `EB3E931B95880FB8931299E59DFC16E2E234C67B`.
- Zero occurrences of "copilot" (case-insensitive) anywhere in any commit
  message, trailer, author, or committer field.
- Only trailer allowed: `Assisted-by: Claude:<model>`.
- Conventional commit messages with `[B]`/`[I]` markers per this
  repository's established `.goals/` convention.

## Documentation requirements

Create/update an appropriate security doc under `docs/` covering: failure-
only rate limiting, explicit-API-credential-only scope, 20 failure burst,
20/min refill, IP resolved via the PR #66 trusted-proxy model, 429 response
shape, `Retry-After`, session requests unaffected, authorization (RBAC)
failures not counted, server failures not counted, in-memory/per-instance
store, reset on restart, NAT/shared-IP caveat, distributed-attack caveat
(explicitly not solved by this PR), and that query/form remain accepted
(and protected) until the already-scheduled 0.13.0 removal.

## Acceptance criteria (the Inspector verifies ALL of these)

1. `API_AUTH_RATE_LIMIT`: a failure-based rate limiter protects every
   `/api/*` route that goes through `RequireAPIKey`'s explicit-credential
   branch, for Bearer, raw Authorization, query, and form uniformly.
2. `FAILURE_ONLY`: only client-attributable authentication failures consume
   budget; proven by test for every taxonomy item above.
3. `SUCCESS_CONSUMES_BUDGET` = NO, proven by test.
4. `SUCCESS_RESETS_BUDGET` = NO, proven by test.
5. `SESSION_AFFECTED` = NO: zero limiter lookups/mutations on the pure-
   session branch, proven by test/inspection.
6. `RBAC_COUNTS` = NO, proven by test.
7. `SERVER_FAILURE_COUNTS` = NO, proven by test.
8. `CLIENT_IP_TRUST`: the new limiter exclusively reuses PR #66's
   `middleware/clientip` resolution; no re-parsing of `RemoteAddr`/XFF/
   X-Real-IP anywhere in the new code.
9. `XFF_BYPASS` = BLOCKED: forged-XFF bucket-rotation is proven impossible
   through the new limiter specifically (not only in `clientip` isolation).
10. `429_JSON`: blocked requests return 429 via `middleware.JSONError` with
    a generic body (no token/user/locked/verifier/transport oracle).
11. `RETRY_AFTER`: present, integer delta-seconds, deterministic, tested.
12. `MEMORY_CLEANUP`: periodic purge of inactive buckets proven under
    `-race`, no historical ticker bug reintroduced.
13. `LOGIN_REGRESSION` = NONE: `/login`'s existing tests pass unmodified in
    their assertions; its parameters/behavior are untouched.
14. `QUERY_FORM_COMPATIBILITY`: query/form remain accepted and are protected
    identically to Bearer/raw by the new limiter; no removal.
15. `DEPENDENCY_DIFF` = NONE: `go.mod`/`go.sum`/`package.json`/`yarn.lock`
    byte-identical to base.
16. `FULL_VERIFY` = PASS: the full gate suite (see Non-functional
    constraints) passes.
17. Pre-check ordering is proven: HMAC/DB work is skipped when an IP is
    already blocked.
18. Git identity/signature requirements are met for every commit in the
    range.
19. Documentation is added per the Documentation requirements section.
20. The complete existing auth-matrix regression suite continues to pass
    with unchanged assertions below threshold.
