# Inspector Feedback — Iteration 1

## Verdict: PASS

This hardened rate-limit foundation is ready. All 14+ acceptance criteria are met with strong evidence: the trusted-proxy model is explicit, safe, and comprehensively tested; X-Forwarded-For/X-Real-IP spoofing by untrusted peers is provably impossible; the right-to-left algorithm is correctly implemented and exercised; the /login limiter is wired to the new resolver and verified under `-race`; the periodic cleanup ticker bug is fixed; no `/api` rate limiter was added; and dependencies, Git authorship, and documentation are all in order.

## Acceptance Criteria Check

1. **TRUSTED_PROXY_MODEL** ✓  
   - Explicit configuration exists: `AdminServer` and `PhishServer` structs have `TrustedProxies clientip.TrustedProxies` fields (`config/config.go`).
   - `ParseTrustedProxies()` rejects invalid entries (e.g., hostnames, malformed CIDRs) at load time with descriptive errors, tested comprehensively in `config/config_test.go` (`TestLoadConfigTrustedProxiesInvalid`).
   - Empty/absent defaults to trust-none (`TrustedProxies` defaults to `nil`), verified in `TestLoadConfigTrustedProxiesDefault`.
   - Single IPs normalized to `/32` (IPv4) or `/128` (IPv6), confirmed by `TestParseTrustedProxies/single_IP_normalized_to_/32` and `/single_IPv6_normalized_to_/128`.

2. **DIRECT_XFF_SPOOF** ✓  
   - `TestClientIP_DirectPeer_ForgedXFFIsIgnored` proves a direct peer at `203.0.113.9` sending `X-Forwarded-For: 1.1.1.1` resolves to the socket peer, not the header.
   - `TestClientIP_DirectPeer_ForgedXRealIPIsIgnored` proves `X-Real-IP` is also ignored from an untrusted peer.
   - `TestClientIP_DirectAttacker_CannotChangeBucketAcrossRequests` proves multiple forged headers from the same direct peer all resolve to the same identity, preventing bucket-switching attacks.
   - Implementation: `peerAddr()` reads `r.RemoteAddr` before any header is consulted; `ClientIP()` returns `peerKey` immediately if peer is not in the trusted list.

3. **TRUSTED_PROXY_XFF** ✓  
   - `TestClientIP_TrustedPeer_RightmostUntrustedHopSelected` simulates nginx's `proxy_add_x_forwarded_for` (appending real client to the right), sending `X-Forwarded-For: 6.6.6.6, 203.0.113.42` from a trusted peer (`127.0.0.1`), and correctly resolves to `203.0.113.42` (the rightmost, untrusted hop).
   - `resolveForwardedFor()` evaluates hops from right to left, stopping at the first non-trusted entry.

4. **MULTI_PROXY_RESOLUTION** ✓  
   - `TestClientIP_MultiHopTrustedChain` chains client → trusted A (10.0.0.1) → trusted B (10.0.0.2) → app, sending `X-Forwarded-For: 198.51.100.77, 10.0.0.1, 10.0.0.2` from peer `10.0.0.2`, and correctly resolves to `198.51.100.77` (the first untrusted hop scanning rightward).
   - The algorithm is proven correct: rightmost hop (`10.0.0.2`) is trusted, continue left; next (`10.0.0.1`) is trusted, continue left; next (`198.51.100.77`) is not trusted—result.

5. **LOGIN_LIMITER_IP** ✓  
   - `middleware/ratelimit/ratelimit.go`: `PostLimiter.Limit()` calls `limiter.clientIP(r)` (line `clientIP := limiter.clientIP(r)`) instead of raw `r.RemoteAddr`.
   - `NewPostLimiter()` defaults `clientIP` to `clientip.NewResolver(nil).ClientIP` (trusts no proxy) unless overridden by `WithClientIP()`.
   - **Central spoof-bypass proof** (`TestLoginLimiterSpoofBypassProof`): two requests from the same real client (`203.0.113.42`) through the trusted nginx peer (`127.0.0.1`), with different forged leftmost XFF entries (`1.1.1.1` and `2.2.2.2`), both resolve to the same identity and consume the same bucket (first succeeds, second is rate-limited). No bucket-switching is possible.
   - `TestDirectAttackerCannotChangeBucketViaXFF`: a direct attacker trying three different forged X-Forwarded-For values gets rate-limited on the second request—all resolve to the same bucket.

6. **CLEANUP_TICKER** ✓  
   - Bug fix confirmed: `pollCleanup()` now calls `limiter.newTicker(limiter.cleanupInterval)` directly (line in `ratelimit.go`), not `limiter.newTicker(time.Duration(limiter.cleanupInterval) * time.Second)` (the double-multiplication bug).
   - **Real periodic mechanism test** (`TestCleanupTickerUsesConfiguredIntervalDirectly`): configures a 15ms cleanup interval, marks a bucket as expired, and polls for up to 2 seconds. The test confirms the bucket is purged by the *background ticker itself* (not direct `Cleanup()` invocation), without sleeping for a full minute. The test passes, proving the ticker fires at the configured rate.
   - `Stop()` lifecycle method added for clean test teardown.

7. **MEMORY_PURGE** ✓  
   - `TestCleanupTickerUsesConfiguredIntervalDirectly` was run as part of the full test suite with `-race` (output: `go test -race ./...` passed all packages, including `middleware/ratelimit`).
   - The test's use of goroutine communication (`limiter.stop` channel) and lock-based access to `limiter.visitors` under `-race` proves the purge is race-free.

8. **API_RATE_LIMIT_SCOPE_LEAK** ✓  
   - `git diff 5db1339..7904263` shows no new rate-limiter middleware or handler added to `/api/*` routes.
   - `controllers/api/server.go` is unchanged: no `limiter` field was wired to any API route (verified by unchanged file in diff).
   - The pre-existing `limiter`/`WithLimiter` plumbing in `api.Server` remains unused; it is not wired to any route in this PR.

9. **DEPENDENCY_DIFF** ✓  
   - `git diff 5db1339..7904263 go.mod go.sum package.json yarn.lock` returns zero output.
   - No new Go or JS dependencies introduced.

10. **LOGIN_COMPATIBILITY** ✓  
    - `controllers/route.go`: `/login` route is `mid.Use(as.Login, as.limiter.Limit)` — unchanged in routing, still rate-limited by `PostLimiter`.
    - Response behavior: `PostLimiter.Limit()` still returns `http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)` (status 429, plain text body), not JSON, no `Retry-After`.
    - Threshold/capacity/refill: `DefaultRequestsPerMinute` is still 5, `DefaultExpiry` still 10 minutes, `DefaultCleanupInterval` still 1 minute (all unchanged except the ticker computation bug fix).
    - `TestRateLimitEnforcement`: limits to 3 requests/minute (configurable), enforces 429 on the 4th—behavior unchanged from before.

11. **FULL_VERIFY** ✓  
    - `go build ./...` passes.
    - `go vet ./...` passes.
    - `gofmt -l .` returns no files needing formatting.
    - `go test ./...` passes all packages, including new clientip/ratelimit tests.
    - `go test -race ./...` passes, proving no data races in the new code or cleanup mechanism.
    - `golangci-lint run` returns 0 issues.
    - `gosec ./...` returns only the pre-existing 12 issues (same as before this PR).
    - `gitleaks detect --no-git` reports no leaks.
    - `GOOS=windows GOARCH=amd64 go build ./...` succeeds (Windows cross-compile works).

12. **GIT AUTHORSHIP & SIGNATURE** ✓  
    - Commit author: `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>` ✓
    - Commit committer: same (implied) ✓
    - GPG signature: GOOD with key `EB3E931B95880FB8931299E59DFC16E2E234C67B` ✓
    - Zero "copilot" mentions (case-insensitive) in commit message ✓
    - Single trailer: `Assisted-by: Claude:Sonnet-5` ✓
    - Conventional commit: `security(ratelimit): [B] ...` ✓

13. **DOCUMENTATION** ✓  
    - `docs/TRUSTED_PROXY_RESOLUTION.md` created, 204 lines, covers:
      - Why the change was made (vulnerable `ProxyHeaders` behavior)
      - Configuration syntax (single IPs, CIDR blocks, empty/absent defaults)
      - Direct deployment mode (no config needed, headers ignored)
      - Reverse-proxy deployment mode (must configure `trusted_proxies`)
      - Right-to-left algorithm in plain language
      - Why client-provided XFF is never trusted by default
      - **Breaking-change note**: "This is a breaking security-hardening behavior change"
      - Multi-instance deployments unaffected (per-instance in-memory state)
      - `/login` limiter behavior unchanged except IP source and ticker fix
      - Periodic cleanup bug fix explained
    - All required topics present and accurate.

14. **NGINX/ANSIBLE TOPOLOGY** ✓  
    - `ansible-playbook/roles/gophish/templates/nginx.conf.j2`: `proxy_pass https://127.0.0.1:8080` (phishing server)
    - `ansible-playbook/roles/gophish/files/config.json`:
      - `admin_server`: no `trusted_proxies` set (trust-none default, correct—nginx does NOT front admin at 3333)
      - `phish_server`: `"trusted_proxies": ["127.0.0.1/32"]` (correct—nginx proxies to 127.0.0.1:8080, so peer is loopback)
    - Topology verified: nginx fronts phishing server only, admin is direct.
    - Configuration correctly matches.

## Quality Gate

All gates passed:
- `go build ./...` ✓
- `go vet ./...` ✓
- `gofmt -l .` ✓ (no files need formatting)
- `go test ./...` ✓
- `go test -race ./...` ✓
- `golangci-lint run` ✓ (0 issues)
- `gosec ./...` ✓ (pre-existing 12 issues, none new)
- `gitleaks detect --no-git` ✓ (no leaks)
- `GOOS=windows GOARCH=amd64 go build ./...` ✓
- Commit authorship and GPG signature ✓

## Malformed-hop safety argument (deep validation)

The goal's algorithm states: "A malformed hop anywhere in the chain aborts to safe peer fallback." The implementation in `parseHopAddr()` is intentionally strict:
- Requires a bare IP literal (bare `netip.ParseAddr`, no port handling)
- Rejects empty strings, hostnames, values with port suffixes (e.g., `"203.0.113.5:1234"`), and anything `netip.ParseAddr` fails on
- Returns `(netip.Addr{}, false)` on any parse error
- The `resolveForwardedFor()` loop exits (returns `"", false`) if any hop is malformed, forcing a safe fallback to `peer`

**Can an attacker ever exploit a malformed hop to get a trust decision based on attacker-controlled data?**

- If hop is malformed: `parseHopAddr()` returns `(invalid, false)`, loop exits, fallback to `peer` ✓
- If hop is a valid IP but trailing port (e.g., `"203.0.113.5:9999"`): `netip.ParseAddr("203.0.113.5:9999")` fails (colons are not valid in bare IP literals), rejected ✓
- If hop is duplicated comma (e.g., `"203.0.113.5,,1.1.1.1"`): splits into `["203.0.113.5", "", "1.1.1.1"]`, empty element fails parse, loop exits ✓
- If hop has leading/trailing whitespace (e.g., `" 203.0.113.5 "`): `resolveForwardedFor()` calls `strings.TrimSpace()` before `parseHopAddr()`, so whitespace is stripped before parsing ✓
- If header is oversized (>4096 bytes): `ClientIP()` checks `len(xff) <= maxForwardedForHeaderBytes` before parsing; if oversized, falls back to `peer` ✓
- If hop count exceeds 64: `resolveForwardedFor()` keeps only the rightmost 64 hops, so unbounded CPU work is prevented ✓
- If attacker sends mixed IPv4/IPv6 garbage (e.g., `"::ffff:1.2.3.4.5"`): `netip.ParseAddr()` rejects (extra dot in IPv4 section), exits loop, fallback ✓

No bypass found. The implementation is defensible and aligns with the goal's literal requirement.

## Issues Found

None. The implementation is complete, correct, and secure.

## What Must Be Fixed (FAIL only)

N/A — verdict is PASS.
