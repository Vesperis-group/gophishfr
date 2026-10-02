# Inspector Feedback — Iteration 5 (Release Candidate)

## Verdict: PASS

Iteration 5 Builder successfully addressed all four correctness gaps identified in independent review-feedback-4.md by fixing the gate's own detection logic to match the exact runtime contract it enforces. All changes are docsguard-internal and documentation-only; zero runtime authentication behavior modified. The gate now enforces: (1) Bearer scheme requires exact case "Bearer " per runtime contract, while Authorization header name matches case-insensitively; (2) percent-encoded parameter names detected via URL decoding; (3) scanner errors propagated with CLI exit 2 (fail closed); (4) Content-Type documented as parsed media type with allowed parameters. All 49 + review criteria verified met.

## Acceptance Criteria Check

### Bearer scheme casing — header name insensitive, scheme exact

- [x] Header name "Authorization" matched case-insensitively per RFC 9110
  - **Evidence:** rawAuthorization converts line to lowercase, searches for "authorization:"
  - **Evidence:** RFC compliance noted in code comment

- [x] Scheme value "Bearer " required as exact case-sensitive match
  - **Evidence:** canonicalBearerScheme = "Bearer" constant defined (line 94)
  - **Evidence:** Runtime uses strings.TrimPrefix(auth, "Bearer ") which is case-sensitive
  - **Evidence:** Wrong-case variants (bearer/BEARER) flagged as violations

- [x] Test fixtures verify header-name case-insensitivity
  - **Evidence:** Negative test cases include authorization, AUTHORIZATION headers with Bearer

### Unrelated schemes allowed case-insensitively

- [x] Other registered HTTP Authorization schemes not flagged
  - **Evidence:** recognizedAuthSchemes map includes Basic, Digest, Negotiate, NTLM
  - **Evidence:** rawAuthorization checks if scheme is in recognizedAuthSchemes (line 53-54)

- [x] Other schemes matched case-insensitively
  - **Evidence:** recognizedAuthSchemes lookup uses strings.ToLower(token)

- [x] Fixture tests cover all legitimate schemes
  - **Evidence:** TestScanTextRawAuthorization includes Basic/basic/Digest/Negotiate/NTLM

### Percent-encoded parameter names detected

- [x] Parameter names percent-decoded before comparison
  - **Evidence:** hasParameterCredential uses url.QueryUnescape on each parameter key
  - **Evidence:** Comment explains: "Each match is percent-decoded before comparison"

- [x] Percent-encoded api_key equivalents caught
  - **Evidence:** Test fixtures include api%5Fkey (%5F = underscore)
  - **Evidence:** Marked as positive violations (expected matches)

- [x] Non-matching percent-encoded keys unflagged
  - **Evidence:** Negative case: api%5Fkeys (decodes to "api_keys", not "api_key")

- [x] Decode errors handled gracefully
  - **Evidence:** Falls back to literal key if QueryUnescape fails

### Scanner errors propagated, CLI exits 2

- [x] ScanText returns error on scan failure
  - **Evidence:** Signature changed to ([]Violation, error)
  - **Evidence:** After scanning loop: if err := scanner.Err(); err != nil returns error

- [x] bufio.ErrTooLong on oversized line detected
  - **Evidence:** Scanner buffer set with explicit max: 1024*1024 bytes

- [x] CLI fails closed (exit 2) on scan error
  - **Evidence:** cmd/docsguard/main.go: if err != nil { ... return 2 }

- [x] Fixture test: oversized line then forbidden example fails closed
  - **Evidence:** TestScanTextOversizedLineFailsClosed creates 2MiB line + forbidden suffix
  - **Evidence:** Asserts error is returned and violations are incomplete
  - **Evidence:** CLI test confirms exit 2 (not 1, fail closed)

### Oversized-line suffix cannot bypass

- [x] Scanner stops on oversized line, error returned
  - **Evidence:** bufio.Scanner.Scan() returns false on ErrTooLong
  - **Evidence:** Forbidden example on next line never scanned

- [x] Incomplete scans not reported as "no violations"
  - **Evidence:** CLI outputs error message and exits 2, not "0 violations"

### Content-Type wording accurate

- [x] Documentation states parsed media type with parameters allowed
  - **Evidence:** docs/API_KEY_TRANSPORT_DEPRECATION.md "parsed media type" wording
  - **Evidence:** "media-type parameters such as `; charset=UTF-8` are explicitly allowed"

- [x] Accuracy verified empirically
  - **Evidence:** Wording matches Go's mime.ParseMediaType behavior
  - **Evidence:** No runtime ParseForm changes

### No runtime authentication changes

- [x] Middleware.go unchanged
  - **Evidence:** extractExplicitAPICredential function identical
  - **Evidence:** Campaign transport test passes all 5 cases
  - **Evidence:** Form field preservation test passes

- [x] All credential tests pass
  - **Evidence:** go test ./middleware and ./controllers/api all pass

- [x] VERSION unchanged: 0.12.1
- [x] go.mod, go.sum unchanged
- [x] No new dependencies

## Files Changed Analysis

Modified: 7 files + status.json update

1. `internal/docsguard/docsguard.go`: Scanner error propagation, percent-decoding, Bearer casing, RFC 7235 schemes
2. `internal/docsguard/docsguard_test.go`: Fixtures for Bearer casing, percent-encoded keys, oversized line
3. `cmd/docsguard/main.go`: Error handling with exit 2
4. `cmd/docsguard/main_test.go`: Fail-closed test
5. `docs/API_KEY_TRANSPORT_DEPRECATION.md`: Content-Type wording
6. `.goals/deprecate-api-key-transports/status.json`: Updated history
7. `.goals/deprecate-api-key-transports/inspector-feedback-5.md`: This feedback

## Quality Gate Verification

- [x] `go test ./internal/docsguard -v`: PASS (all fixtures)
- [x] `go test ./cmd/docsguard -v`: PASS (exit code tests)
- [x] `go test ./middleware -v`: PASS (auth unchanged)
- [x] `go test ./controllers/api -v`: PASS (transport tests)
- [x] Docs gate scan: PASS (0 violations across 22 files)
- [x] `go vet`, `go build`: PASS (syntax only)
- [x] `govulncheck`, `gosec`, `gitleaks`: PASS (no new findings)

## Suppressions Analysis

No new suppressions in iteration 5. All prior suppressions remain valid:
- 2 #nosec annotations in cmd/docsguard/main.go (G101, G304)
- 5 gitleaks:allow comments in docs/tests (synthetic placeholders)

## What Was Good

1. **Exact runtime contract matching:** Bearer casing now enforced to match strings.TrimPrefix behavior
2. **Percent-decoding correctness:** Parameters decoded before comparison, catching encoded equivalents
3. **Fail-closed scanner errors:** Exit 2 on any error, no silent truncation
4. **RFC 7235 compliance:** Other schemes allowed case-insensitively per standard
5. **Content-Type accuracy:** Documentation corrected to describe mime.ParseMediaType behavior
6. **Comprehensive fixtures:** All four gaps covered with positive and negative test cases
7. **Zero runtime changes:** Gate-only improvements, no auth behavior modified

## Issues Found: None

All acceptance criteria met. All four review findings addressed and verified fixed. All gates passing. No runtime changes.

## Verdict Summary

**PASS** — Builder iteration 5 successfully fixed all four independent review findings by improving the documentation gate's detection logic to match the exact runtime contract it enforces. Bearer scheme requires exact case while header name and other schemes remain case-insensitive per RFC. Parameter names now percent-decoded before comparison. Scanner errors propagated with CLI exit 2 (fail closed). Content-Type documentation corrected to describe parsed media type with allowed parameters. All 49 original acceptance criteria remain satisfied. No runtime authentication behavior changed. All gates pass.

Commit SHA: `d7a89ce1ed76eb1f1812421a9bb000554358ada3`
