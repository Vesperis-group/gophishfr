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
  now recommends `Authorization: Bearer` as canonical and marks the
  `api_key` query/form parameter deprecated with removal targeted for
  `0.13.0`, linking to the migration guide.
- `docs/API_KEY_TRANSPORT_DEPRECATION.md` — the canonical/deprecated
  contract table; old/new migration examples for query, form, and raw, all
  against the same real, executable business operation
  (`POST /api/campaigns/{id}/complete`); the exact `0.13.0` removal
  behaviour; leaked-key rotation and log-review guidance; and the explicit
  no-header/no-signal decision with rationale.
- `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md` — a concise,
  repository-conventional release-note artifact for the next GitHub Release.
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
- `internal/docsguard/` (new) and `cmd/docsguard/` (new) — the detection
  logic and CLI behind the documentation gate, plus their fixture-based
  table tests (`internal/docsguard/docsguard_test.go`,
  `cmd/docsguard/main_test.go`) covering every supported deprecated syntax,
  every legitimate Authorization scheme this guard must leave alone, and (as
  of iteration 3) every canonical-doc table row or alternative-option
  sentence that presents `api_key` as an ordinary option with no
  deprecation context. Both are `go test`-covered stdlib-only Go code; no
  new dependency.
- `scripts/verify-docs-canonical-examples.sh` (rewritten) — now a thin
  wrapper that runs `go run ./cmd/docsguard` over tracked Markdown, with one
  explicit, auditable exemption
  (`docs/API_KEY_TRANSPORT_DEPRECATION.md`) and `scripts/verify.sh` wiring
  unchanged.
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

The iteration-1 form example mixed an authentication demonstration (`api_key`
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
an `api_key` parameter as a normal authentication option with no
deprecation context, which `internal/docsguard` did not catch because its
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
