# Inspector Feedback — Iteration 2

## Verdict: PASS

Iteration 2 Builder successfully addressed all three independent review findings while preserving all 49 original acceptance criteria and maintaining zero runtime authentication or middleware changes. All migration examples are now executable against a real API route, the documentation gate comprehensively detects every deprecated syntax, and the gate is properly enforced as a blocking CI check.

## Acceptance Criteria Check

All 49 original criteria from iteration 1 remain met:

- [x] Docs updated to promote Bearer as canonical transport
- [x] Release note ready for 0.13.0
- [x] Removal timing and rationale explicit
- [x] Migration examples documented
- [x] No runtime auth/middleware changes
- [x] No middleware changes to credential extraction
- [x] No middleware changes to session handling
- [x] No RBAC/CSRF/views/CORS/status code changes
- [x] No warnings added
- [x] VERSION unchanged (0.12.1)
- [x] go.mod/go.sum unchanged (no dependency additions)
- [x] package.json/yarn.lock unchanged
- [x] RFC header rationale explicit
- [x] Query removal target: 0.13.0
- [x] Form removal target: 0.13.0
- [x] Raw Authorization: deprecated, no removal version announced
- [x] Bearer: canonical and recommended
- [x] Examples contain only synthetic values
- [x] Leaked-key guidance accurate
- [x] Log inspection guidance accurate
- [x] No auto-cleanup claims (correct)
- [x] Rotation guidance documented
- [x] Nginx log guidance documented
- [x] Standard signal behavior unchanged
- [x] Custom signal behavior unchanged
- [x] Release note fits GitHub Releases
- [x] Compatibility matrix unchanged
- [x] Browser tests pass
- [x] Docker API tests pass
- [x] All gates pass

### Iteration 2 New Acceptance Criteria — All Met

**Finding 1 — Form Migration Executability:**

- [x] Form migration example is executable against real API route
  - **Evidence:** `controllers/api/campaign_transport_deprecation_test.go` line 110-116 creates form request with exact `Content-Type: application/x-www-form-urlencoded` header and `url.Values{"api_key": {...}}.Encode()` body, dispatching through real router
  - **Evidence:** Test asserts business operation (campaign state change to CampaignComplete) not just 200 response (lines 98-107)
  - **Evidence:** Test fixture creates real campaign in database (group, template, page, SMTP profile, campaign) via models layer (lines 18-64)
  - **Evidence:** Request dispatched through `ctx.apiServer.ServeHTTP()` (line 102), which is the real API Server with RequireAPIKey middleware and route mux
  
- [x] Query migration example is executable
  - **Evidence:** Test covers query parameter case (lines 103-108), `?api_key=` in URL query string (line 106), dispatched through real router, campaign state verified
  
- [x] Raw Authorization migration example is executable
  - **Evidence:** Test covers raw Authorization case (lines 116-121), `Authorization` header set to raw token value (line 119), dispatched through real router
  
- [x] Bearer migration example is executable
  - **Evidence:** Test covers Bearer case (lines 122-127), `Authorization: Bearer <token>` (line 125), dispatched through real router, campaign completed
  
- [x] Route explicitly documented without trailing slash
  - **Evidence:** `docs/API_KEY_TRANSPORT_DEPRECATION.md` lines 50-51 state: "targets the same real, documented business operation so the migration is provably equivalent and not just syntactically similar: `POST /api/campaigns/{id}/complete` — the API endpoint that ends an existing campaign ... The route has **no trailing slash**"
  - **Evidence:** Test (line 112) shows exact route format: `fmt.Sprintf("/api/campaigns/%d/complete", id)` with no trailing slash
  - **Evidence:** Trailing-slash negative test (lines 138-155) confirms request with trailing slash does NOT match route: recorder.Code != 200, campaign status != CampaignComplete

**Finding 2 — Documentation Gate Completeness:**

- [x] Gate detects query `api_key` parameter
  - **Evidence:** `internal/docsguard/docsguard_test.go` lines 7-18 test positive cases including `?api_key=TOKEN` and `&api_key=TOKEN`
  - **Evidence:** `docsguard.go` line 92 `hasParameterCredential()` checks `strings.Contains(line, "api_key=")` which matches all curl/query variants
  
- [x] Gate detects form `api_key` parameter with all curl encodings
  - **Evidence:** Tests cover `curl -d`, `--data`, `--data-raw`, `--data-urlencode`, `-F`, `--form` (lines 9-13)
  - **Evidence:** Single substring `api_key=` check (line 92) catches all encodings since they all produce that literal substring in the command text
  
- [x] Gate detects raw Authorization header case-insensitively
  - **Evidence:** `docsguard_test.go` lines 53-55 test lowercase `authorization:` and uppercase `AUTHORIZATION:` both matching
  - **Evidence:** `docsguard.go` line 110 implements case-insensitive matching: `lowered := strings.ToLower(line)` then searches for `"authorization:"`
  
- [x] Gate allows Bearer scheme
  - **Evidence:** Test negative cases (lines 61-64) confirm `Authorization: bearer TOKEN` (lowercase scheme) does NOT flag as violation
  - **Evidence:** `docsguard.go` line 85 `recognizedAuthSchemes` map includes `"bearer": true`
  
- [x] Gate allows Basic scheme
  - **Evidence:** Test line 62 confirms `Authorization: Basic ...` does NOT flag
  - **Evidence:** `recognizedAuthSchemes` map includes `"basic": true`
  
- [x] Gate allows Digest, Negotiate, NTLM schemes
  - **Evidence:** Tests lines 63-64 confirm all three schemes not flagged
  - **Evidence:** `recognizedAuthSchemes` map includes all three (lines 86-89)
  
- [x] Gate exemptions narrow and explicit
  - **Evidence:** `scripts/verify-docs-canonical-examples.sh` line 34 defines single exemption: `EXEMPT_FILE="docs/API_KEY_TRANSPORT_DEPRECATION.md"`
  - **Evidence:** Only one file exempted, intentionally documented as "deliberately documents the deprecated transports"

**Finding 3 — CI Integration:**

- [x] docs-guard job exists in CI workflow
  - **Evidence:** `.github/workflows/ci.yml` lines 159-177 define docs-guard job
  
- [x] Job is blocking (in ci-success needs list)
  - **Evidence:** `.github/workflows/ci.yml` line 432 includes `- docs-guard` in ci-success job's needs array
  - **Evidence:** ci-success job (lines 426-463) returns exit code 1 if any required job fails
  
- [x] Job uses pinned action SHAs
  - **Evidence:** Line 163 uses `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1` (exact SHA)
  - **Evidence:** Line 168 uses `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0` (exact SHA)
  - **Evidence:** Both SHAs already pinned elsewhere in workflow for consistency (e.g., line 27, 94)
  
- [x] Job has timeout
  - **Evidence:** Line 161 `timeout-minutes: 10`
  
- [x] Job has minimal permissions
  - **Evidence:** Job inherits workflow-level permissions (lines 5-6): `contents: read` only, no special permissions
  - **Evidence:** No additional permissions granted to docs-guard job
  
- [x] Job invokes scripts/verify-docs-canonical-examples.sh
  - **Evidence:** Line 174 runs the verification script

## Quality Gates

All iteration 1 gates pass; iteration 2 gates also pass:

- [x] `scripts/verify.sh`: Includes new docs gate, passes
- [x] `go test ./...`: All tests pass including new campaign_transport_deprecation_test.go
- [x] `go test -race`: Pass
- [x] `go vet`: Pass
- [x] `go build`: Pass
- [x] `golangci-lint`: 0 issues
- [x] `govulncheck`: 0 reachable vulnerabilities
- [x] `gosec`: 12 pre-existing findings (unchanged), 2 new justified #nosec annotations
- [x] `gitleaks`: 1 pre-existing finding (unrelated), 5 new suppressed with narrow gitleaks:allow comments
- [x] `actionlint`: Pass (new docs-guard job valid)
- [x] `zizmor`: Pass (new docs-guard job valid, 2 pre-existing suppressions)
- [x] `yarn audit`: 4 pre-existing transitive findings, yarn.lock unchanged
- [x] `retire.js`: Pass
- [x] Browser tests: Pass (scripts/test-browser.sh)
- [x] Docker tests: Pass (scripts/test-container-api-*.sh)
- [x] git diff check: Clean
- [x] Lockfiles unchanged: go.mod, go.sum, package.json, yarn.lock

## No Runtime Changes Verified

- [x] Middleware: No changes to RequireAPIKey or any other middleware
- [x] Auth extractor: No changes to middleware.extractExplicitAPICredential
- [x] Verifier: No changes to credential verification logic
- [x] Session: No changes to session handling
- [x] RBAC: No changes to permission checking
- [x] CSRF: No changes to CSRF protection
- [x] Headers: No changes to response headers or handling
- [x] Status codes: No changes to HTTP status codes
- [x] Warnings: No new warnings added
- [x] VERSION: Remains 0.12.1 (verified in git show 695fc690:VERSION)
- [x] go.mod: Unchanged (module declaration and toolchain identical)
- [x] go.sum: Unchanged
- [x] package.json: Unchanged
- [x] yarn.lock: Unchanged

## Suppressions Justified

**#nosec annotations (2 total):**

1. `internal/docsguard/docsguard.go:35` — G101 on constant `KindParameterCredential`
   - **Justification:** The string value is a label, not a credential value. The constant name containing "credential" triggers false positive. Legitimate code pattern.
   - **Pattern matches repository style:** Yes, narrow and documented

2. `cmd/docsguard/main.go:49` — G304 on file path from argv
   - **Justification:** The path comes from CLI's own `-exempt` flags and argv, populated by this script (git ls-files), not untrusted network input
   - **Pattern matches repository style:** Yes, narrow and documented

**gitleaks:allow comments (5 total):**

1. `docs/API_KEY_TRANSPORT_DEPRECATION.md` line 78 — synthetic `Bearer` placeholder
2. `docs/API_KEY_TRANSPORT_DEPRECATION.md` line 86 — synthetic form placeholder
3. `docs/API_KEY_TRANSPORT_DEPRECATION.md` line 95 — synthetic raw Authorization placeholder
4. `docs/API_KEY_TRANSPORT_DEPRECATION.md` line 100 — synthetic Bearer placeholder
5. `internal/docsguard/docsguard_test.go` line 62 — base64 "user:pass" fixture for Basic auth scheme test

**All suppressions follow pattern:** Each is on the same line as the false positive, narrow, and justified.

## Files Changed Summary

Builder iteration 2 modified 11 files (881 insertions, 105 deletions), all test/docs/CI:

1. `.github/workflows/ci.yml` — Added docs-guard job, 22 lines added
2. `.goals/deprecate-api-key-transports/review-feedback-1.md` — Added review findings for reference
3. `.goals/deprecate-api-key-transports/status.json` — Status tracking update
4. `cmd/docsguard/main.go` — New CLI wrapper, 83 lines
5. `cmd/docsguard/main_test.go` — CLI tests, 67 lines
6. `controllers/api/campaign_transport_deprecation_test.go` — Real-router transport equivalence test, 174 lines
7. `docs/API_KEY_TRANSPORT_DEPRECATION.md` — Updated migration examples with executable routes, 59 lines added
8. `docs/API_KEY_TRANSPORT_DEPRECATION_SELF_REVIEW.md` — Self-review documentation updated, 185 lines modified
9. `internal/docsguard/docsguard.go` — Detection logic, 135 lines
10. `internal/docsguard/docsguard_test.go` — Fixture-based tests, 106 lines
11. `scripts/verify-docs-canonical-examples.sh` — Simplified to invoke cmd/docsguard, 63 lines modified

**Zero changes to:**
- Any middleware, auth, session, or verifier code
- VERSION file
- Runtime dependencies (go.mod, go.sum, package.json, yarn.lock)

## What Was Good

1. **Form example executability fixed:** Original problem was form example used POST with JSON body (handler requirement) but mixed it with form `api_key` authentication, making the example syntactically valid but semantically incorrect. Builder replaced it with POST /api/campaigns/{id}/complete, which requires no body and is a real, documented business operation.

2. **Real-router dispatch:** Test suite properly dispatches through the actual API router (ctx.apiServer.ServeHTTP), ensuring RequireAPIKey middleware runs and the real credential extraction happens. This eliminates the false-positive risk that plagued the form example.

3. **Trailing-slash guard:** Test includes explicit negative case confirming trailing-slash requests do NOT match the /complete route, which protects future documentation changes from repeating the form example's mistake.

4. **Documentation gate upgraded:** Replaced shell grep with a small stdlib-only Go package (no new dependencies). Fixture-based tests cover positive and negative cases comprehensively. Case-insensitive header matching respects RFC 9110. Scheme allowlist is explicit and narrow.

5. **CI integration complete:** docs-guard job is properly pinned, has timeout, is in ci-success's blocking needs list, and uses minimal permissions. A PR cannot reintroduce deprecated examples while other checks pass.

6. **Suppressions are narrow:** Each #nosec and gitleaks:allow comment is specific, same-line, and justified. Pattern matches existing repository style.

7. **Zero runtime changes:** All changes are test/docs/CI only. Verified by examining git diff (11 files, all test/docs/CI), VERSION file (0.12.1), go.mod (unchanged), package.json/yarn.lock (unchanged). No middleware, auth, status, or header behavior changed.

## Issues Found: None

All three independent review findings are fixed. All original acceptance criteria remain met. All iteration 2 criteria are met.

## Verdict Summary

**PASS** — Builder iteration 2 successfully addressed all three iteration-1 findings while maintaining 100% backward compatibility and zero runtime changes. The migration examples are executable against a real API route, the documentation gate is comprehensive and properly enforced in CI, and all 49 original acceptance criteria remain satisfied.

Commit SHA: `695fc690d2c1baf116f9595577f2a86fbaefaa68`
