# API-key transport deprecation self-review

This Builder review records the evidence for
`security/deprecate-legacy-api-key-transports`. The immutable acceptance
checklist in `.goals/deprecate-api-key-transports/goal.md` remains the
source of truth; this document groups the evidence by control area.

## Scope of the change

This PR is documentation- and release-note-only. It touches:

- `docs/API_AUTHENTICATION.md` — marks query/form/raw as deprecated inline
  and points at the new deprecation guide instead of restating the full
  rationale in two places.
- `docs/API_KEY_TRANSPORT_DEPRECATION.md` (new) — the canonical/deprecated
  contract table, old/new migration examples for query and form, raw→Bearer
  guidance, the exact `0.13.0` removal behaviour, leaked-key rotation and
  log-review guidance, and the explicit no-header/no-signal decision with
  rationale.
- `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md` (new) — a concise,
  repository-conventional release-note artifact for the next GitHub Release.
- `scripts/verify-docs-canonical-examples.sh` (new) and `scripts/verify.sh`
  (one added `run_gate` line) — a small grep-based gate that fails if a
  tracked Markdown file other than `docs/API_KEY_TRANSPORT_DEPRECATION.md`
  reintroduces a deprecated-transport example: a query-string credential
  example, a form-body credential example, or a raw, non-Bearer-prefixed
  credential header example. It adds no dependency and no Markdown parser; it
  is deliberately
  scoped to tracked `*.md` files, so `scripts/*.sh` and `tests/**` compatibility
  fixtures that legitimately exercise query/form/raw are untouched and
  unaffected.

No Go source, middleware, controller, model, schema, migration, frontend
source/generated asset, workflow, `go.mod`, `go.sum`, `package.json`, or
`yarn.lock` changed. `VERSION` is unchanged (`0.12.1`).

## Acceptance criteria evidence

| Area | Result | Evidence |
| --- | --- | --- |
| Authentication behaviour unchanged | PASS | No file under `middleware/`, `controllers/`, `models/`, `auth/`, or `internal/apikey` changed. `go test ./...` and `go test -race ./...` pass unmodified, including the full existing API-key extractor/middleware/verifier suites. Container compatibility re-proves Bearer, raw, query (including empty), form (including empty), and session behaviour end to end (see below). |
| Canonical and deprecated contracts | PASS | `docs/API_AUTHENTICATION.md` now states Bearer is the only recommended/canonical transport and lists query/form (deprecated, `0.13.0` removal) and raw (deprecated, no removal version) as still-accepted legacy contracts, not as never-public surfaces. |
| Client migration guidance | PASS | `docs/API_KEY_TRANSPORT_DEPRECATION.md` has synthetic (`REPLACE_WITH_YOUR_TOKEN`) old/new `curl` examples for query→Bearer and form→Bearer (preserving the business form field), prose raw→Bearer guidance, the exact `0.13.0` removal behaviour (empty/combined-with-session/combined-with-Bearer all → JSON `401`, no fallback), and an explicit statement that raw stays accepted/deprecated after that removal. |
| Leak remediation guidance | PASS | The same document names GophishFR access logs, nginx/reverse-proxy defaults, browser/shell history, and copied diagnostics/support traces as places a query key may already exist; instructs migrate+rotate+review/remove; explicitly disclaims automatic identification of affected clients or cleanup of third-party/proxy logs; and states this release does not stop query secrets from reaching access logs. Form is described as lower default logging risk but still deprecated to converge on one transport. The separate rate-limiting backlog item is named, not implemented. |
| Runtime signalling decision | PASS | A dedicated section states and justifies, per mechanism, why no RFC 9745 `Deprecation` header, no RFC 8594 `Sunset` header, no `Link: rel="deprecation"` header, and no custom header/server warning log were added (resource-identity mismatch, no calendar date, no stable published URI, and documentation as the deliberate channel, respectively). No token, URL/query, form body, `Authorization` value, verifier, or secret-derived value is emitted anywhere — unchanged, and reconfirmed by the unmodified middleware/controller code and passing test suites. |
| Release/version documentation | PASS | `VERSION` is untouched (`0.12.1`). `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md` is a self-contained artifact that states deprecated items, the `0.13.0` removal target, Bearer migration, key rotation/log cleanup, and that the breaking removal itself updates `VERSION` to `0.13.0` through the normal release process. No calendar sunset timestamp is invented anywhere. |
| Examples/search/gate | PASS | `grep`-based repository search found no deprecated-transport example in canonical docs outside the new deprecation page; `README.md` has no API examples at all. The new `scripts/verify-docs-canonical-examples.sh` gate encodes that search as a repeatable, dependency-free CI/local check and is wired into `scripts/verify.sh`. No OpenAPI/Swagger specification exists and none was created. |
| Tests and validation | PASS | See "Validation evidence" below. |
| Scope and roadmap | PASS | Nothing removed/rejected for query/form/raw. No verifier/keyring/token-format/session/CSRF/RBAC/rate-limit/log/`events.details`/dependency change. The deprecation page names the next breaking PR (`security/remove-legacy-api-key-transports`, `0.13.0`, retaining explicit presence detection so unsupported transports cannot fall back to session) and the independent next hardening item (`security/rate-limit-api-auth`) without implementing either. |

## Validation evidence

- `./scripts/verify.sh`: all gates pass — gofmt, golangci-lint (0 issues),
  `go mod verify`, `go vet`, `go build`, `go test`, the new "docs canonical
  API examples" gate, `go test -race`, the frontend double-build check
  (byte-identical; no frontend source changed), and `govulncheck` under the
  pinned `go1.25.13` toolchain (0 reachable vulnerabilities; 3 unreachable
  findings in required modules, unchanged from baseline). `action pins` was
  skipped locally (no authenticated `gh`); CI enforces it unconditionally and
  no workflow file changed in this PR.
- `go test ./...` and `go test -race ./...`: pass across every package,
  unchanged test set, including `middleware/api_auth_test.go` and
  `middleware/middleware_test.go`.
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
  TLS/path/log-mode findings); none originates in a file this PR touches —
  every file this PR touches is Markdown or a shell script, and `gosec`
  scans Go only.
- `gitleaks detect --source . --no-banner --redact`: 2 pre-existing findings,
  both in `.goals/api-key-verifier/inspector-feedback-1.md` from commits
  dated 2026-09-03, unrelated to and unchanged by this PR; no new finding.
- `actionlint -color`: pass (no workflow changed).
- `zizmor --min-severity=low .github/workflows/`: no findings (2 previously
  documented suppressions retained; no workflow changed).
- `corepack yarn audit`: 4 moderate findings, all in a transitive `webpack`
  devDependency (`fast-uri`, via `schema-utils`/`ajv`); pre-existing in the
  unchanged lockfile, not introduced or touched by this PR, and out of scope
  (no dependency change in this PR).
- `npx --yes retire --path static --outputformat text`: pass, no findings.
- `shellcheck` was not separately run here; `scripts/verify-docs-canonical-examples.sh`
  follows the same style as the existing `scripts/verify-action-pins.sh` and
  uses only POSIX-portable `grep` with basic negation (no Perl-only lookahead)
  so it runs the same way under GNU and BSD grep.
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
