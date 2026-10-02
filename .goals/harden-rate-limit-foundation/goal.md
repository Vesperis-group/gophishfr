# Goal: harden-rate-limit-foundation

## Status
IMMUTABLE after creation. This file is the sole acceptance contract for the
Builder and Inspector. Neither may alter it.

## Repository
/home/killian/gophishfr
Branch: security/harden-rate-limit-foundation
Base: main @ (see status.json initial_sha)

## Context (confirmed facts from prior read-only audit, not to be re-litigated)

- No API-key authentication rate limiter exists today (`/api/*` is unprotected
  by any limiter; `ratelimit.PostLimiter` only protects `POST /login`).
- `middleware/ratelimit.PostLimiter` is a token-bucket limiter
  (`golang.org/x/time/rate`), indexed by IP (`net.SplitHostPort(r.RemoteAddr)`
  with raw fallback), backed by a `map[string]*bucket` + `sync.RWMutex`.
- CONFIRMED BUG: `pollCleanup()` computes
  `time.NewTicker(time.Duration(limiter.cleanupInterval) * time.Second)`.
  `cleanupInterval` is already a `time.Duration`; multiplying it again by
  `time.Second` inflates the real ticker interval from the configured 1
  minute to approximately 147.7 years (empirically verified: 4659767778871345152
  ns). The periodic cleanup effectively never runs in production; only the
  existing test calls `Cleanup()` directly, bypassing the ticker entirely.
- `gorilla/handlers.ProxyHeaders` (vendored at `github.com/gorilla/handlers
  v1.4.2`, file `proxy_headers.go`) is applied unconditionally to the admin
  handler chain (`controllers/route.go`, `handlers.ProxyHeaders(adminHandler)`)
  and to the phishing handler chain (`controllers/phish.go`). It rewrites
  `r.RemoteAddr` from `X-Forwarded-For` (taking the FIRST, leftmost,
  comma-separated segment via `fwd[:strings.Index(fwd, ", ")]`) or
  `X-Real-IP` as fallback, with **no trusted-proxy allowlist of any kind**.
  This is exploitable even behind the project's own correctly-configured
  first-party nginx (`ansible-playbook/roles/gophish/templates/nginx.conf.j2`,
  which correctly uses `proxy_set_header X-Forwarded-For
  $proxy_add_x_forwarded_for;` — appending the real client IP to the right of
  any existing value) because the Go-side code reads the WRONG (leftmost)
  end of the resulting list. A client can forge
  `X-Forwarded-For: <anything>` and change their logical IP on every request,
  today, against the existing `/login` limiter.
- No trusted-proxy configuration, allowlist, or middleware exists anywhere in
  this repository today (confirmed by exhaustive grep).
- `golang.org/x/time` is already a direct dependency (used by `ratelimit`).
  No new Go or JS dependency may be introduced by this PR.
- This PR is a pure security-hardening/foundation PR. It must NOT add any new
  rate limiter for `/api/*` authentication. That is explicitly deferred to a
  future PR `security/rate-limit-api-auth`, which depends on this PR's output
  but is out of scope here.

## Objective

Fix the IP-resolution and rate-limiting foundation BEFORE any API-key rate
limiter is added, so that:

1. An explicit trusted-proxy model is introduced.
2. No `X-Forwarded-For`/`X-Real-IP` spoofing by a non-trusted peer is possible.
3. The client IP is correctly resolved behind a chain of trusted proxies.
4. The existing `PostLimiter` periodic cleanup bug is fixed.
5. `/login` functional behavior is preserved (status codes, body shape,
   thresholds, capacity, refill, expiry all unchanged except the one ticker
   bug fix).
6. A safe, reusable primitive is produced for the future API rate-limiter PR.

## Explicitly out of scope (must NOT be touched)

- Adding any `/api/*` authentication rate limiter.
- Modifying the API-key HMAC verifier/keyring.
- Modifying Bearer/query/form/raw-Authorization transport behavior or removal
  timeline (0.13.0).
- Modifying RBAC, session authentication, or CSRF behavior.
- npm/yarn dependency remediation (the 25 pre-existing alerts).
- `events.details` (unrelated, pre-existing out-of-scope item from prior PRs).
- Introducing Redis or any distributed store.
- Introducing any new Go or JS dependency. `go.mod`/`go.sum` and
  `package.json`/`yarn.lock` must be byte-identical to the base commit.
- Adding per-request telemetry/logging beyond what already exists.
- Trusting `X-Forwarded-For`/`X-Real-IP` globally/unconditionally under any
  circumstance.

## Required design (binding, not a menu of options)

### Trusted proxy configuration

- Add an explicit configuration list of trusted proxy networks (name
  consistent with existing repo conventions in `config/config.go`), accepting
  both single IPs (normalized to /32 or /128) and CIDR blocks.
- Absent/empty configuration = trust NO proxy. Default must never be
  `0.0.0.0/0` or `::/0` or an implicit "all private/loopback/Docker ranges"
  default.
- Invalid CIDR/IP in configuration must produce a clear configuration error
  at load time, not be silently ignored.

### Client IP resolution algorithm (binding)

- Let `peer` = the immediate TCP socket peer (`r.RemoteAddr`), parsed BEFORE
  any header-based rewriting. The trust decision for forwarding headers must
  ALWAYS use this original socket peer — never a value already derived from
  `X-Forwarded-For`.
- If `peer` is not in the trusted-proxies configuration: ignore
  `X-Forwarded-For` and `X-Real-IP` completely. Resolved client IP = `peer`.
  This must hold even if the headers contain a syntactically valid IP.
- If `peer` IS trusted:
  - Parse `X-Forwarded-For` as a comma-separated hop list.
  - Evaluate hops from RIGHTMOST to LEFTMOST. For each hop (starting from the
    rightmost/last), if that hop is itself a trusted proxy, continue scanning
    leftward; the first hop encountered that is NOT a trusted proxy is the
    resolved client IP.
  - If no such hop exists (e.g., all hops are trusted, or the header is
    absent/empty/unusable), and `X-Forwarded-For` was unusable: `X-Real-IP`
    may be used ONLY if `X-Forwarded-For` is absent entirely, the peer is
    trusted, and `X-Real-IP` contains exactly one syntactically valid IP.
  - Do not arbitrarily combine `X-Forwarded-For` and `X-Real-IP` in the same
    resolution pass.
  - If no usable/trusted-chain-consistent hop can be determined at all,
    fall back safely to `peer`. Never fail open to an attacker-controlled
    value.
- Malformed header handling: invalid IP literal, empty element, a hostname,
  an unexpected port suffix, an excessively long header value, or mixed
  IPv4/IPv6 garbage must never be trusted. Bound the number of hops actually
  parsed/evaluated to prevent unbounded CPU/memory work from a single
  oversized header. Error paths/log messages must never include the full
  attacker-controlled header value verbatim if that could leak into logs
  (summarize/redact instead).
- Use `net/netip` or `net` from the standard library only. No new dependency.
- IPv4, IPv6, and IPv4-mapped IPv6 addresses must all be supported and
  compared in normalized form.
- `r.RemoteAddr` parsing must extract the host correctly from `host:port`
  without a permissive fallback that would treat a malformed value as a
  usable spoofable identifier.

### Integration

- `gorilla/handlers.ProxyHeaders` must no longer be able to rewrite
  `r.RemoteAddr` before the trust decision is made anywhere it currently
  runs (admin chain in `controllers/route.go`, and the phishing chain in
  `controllers/phish.go` if applicable — audit both). Prefer removing/
  replacing it with the new trusted primitive. It may be kept in parallel
  only if there is a proven reason it cannot influence any security decision
  — this must be justified explicitly in the self-review, not assumed.
- Provide a clear primitive (e.g., a `ClientIP(r *http.Request) string`
  function or equivalent, plus any necessary config wiring) that can be
  reused, unmodified in its core algorithm, by future code (notably the
  future `/api` rate limiter) and by access logging if that is done safely.
- `middleware/ratelimit.PostLimiter` (used by `/login`) must be updated to use
  the new safe resolution instead of raw `r.RemoteAddr`.

### First-party nginx / Ansible

- Audit the actual `proxy_pass` target in
  `ansible-playbook/roles/gophish/templates/nginx.conf.j2` to determine the
  real peer address GophishFR would see from this first-party deployment
  (loopback vs. a Docker/other network). Do not assume `127.0.0.1` without
  checking.
- Update the Ansible-produced GophishFR configuration to declare the trusted
  proxy CIDR/IP that matches the actual deployment topology — not an
  arbitrarily broad CIDR chosen for convenience.

### Cleanup bug fix

- Fix `pollCleanup()`'s ticker computation to use the configured
  `cleanupInterval` directly (no double multiplication by `time.Second`).
- Do not change the default values of `expiry`, `cleanupInterval`, request
  capacity, or refill rate unless a test genuinely requires it (and if so,
  justify explicitly).
- Add a real test of the periodic mechanism itself (not just `Cleanup()`
  called directly), using an injectable clock/ticker or an equivalent
  testable primitive. Tests must not sleep for a real minute or longer. A
  minimal, narrowly-scoped refactor to make this testable is acceptable;
  do not build large new infrastructure. If a `Stop()`/lifecycle method is
  needed to make tests deterministic and avoid goroutine leaks across the
  test suite, add only the minimum necessary — no flaky goroutine-leak tests.

### Login compatibility (binding)

- `POST /login` must preserve, byte-for-byte unchanged except for the IP
  resolution and the ticker fix:
  - status codes below and at/above the threshold,
  - response body shape (still non-JSON/plain `http.Error`, not converted to
    JSON — that divergence is explicitly reserved for the future API limiter,
    not this PR),
  - the existing `Retry-After` absence (do not add it here),
  - capacity/refill/expiry defaults.
- Existing `middleware/ratelimit` tests must continue to pass, and new tests
  must prove the IP resolution plumbing is actually used (not just
  available).

## Required tests (minimum; not exhaustive — Builder/Inspector may add more)

- Direct peer + forged `X-Forwarded-For` → resolved IP is the real socket
  peer, header completely ignored.
- Untrusted proxy peer + `X-Forwarded-For` present → resolved IP is the
  untrusted proxy's own peer address, header ignored.
- Trusted nginx peer + forged leftmost XFF entry + proxy-appended real
  client on the right → resolved IP is the real (rightmost, untrusted) hop.
- Trusted multi-hop proxy chain (client → trusted A → trusted B → app) →
  first untrusted hop scanning from the right is selected.
- Malformed XFF (invalid IP, empty element, hostname, oversized value, mixed
  IPv4/IPv6 garbage) → safe fallback to peer, never a crash, never a trusted
  decision based on garbage.
- `X-Real-IP` from an untrusted peer → ignored.
- `X-Real-IP` from a trusted peer with `X-Forwarded-For` absent and exactly
  one valid IP in `X-Real-IP` → accepted.
- **Central spoof-bypass proof**: two requests from the same real client
  through the same simulated trusted nginx, each with a different forged
  leftmost XFF entry, must resolve to the SAME client IP and consume the SAME
  rate-limit bucket.
- A direct, non-proxied attacker sending `X-Forwarded-For: <random>` must
  never be able to change which rate-limit bucket their requests consume.
- `POST /login` under threshold → historical status; at/over threshold → 429,
  unchanged body/headers.
- Periodic cleanup actually purges an expired bucket, proven under
  `-race`, without sleeping for the real configured duration.
- Config loading rejects an invalid CIDR/IP in trusted-proxies configuration
  with a clear error, and accepts an empty/absent value as trust-none.
- No full attacker-controlled header value appears verbatim in any new log
  message/error, and no existing secret-free logging behavior regresses.

## Non-functional constraints

- No new Go or JS dependency. `go.mod`, `go.sum`, `package.json`, `yarn.lock`
  must be byte-identical to the base commit (verify via diff and/or
  checksum).
- Must keep Windows builds working (no new Unix-only syscalls).
- Must keep the whole repository's existing test suite, linters, and
  security scanners green (gofmt, golangci-lint, go vet, go build, go test,
  go test -race, govulncheck, gosec, gitleaks, actionlint, zizmor, yarn audit,
  retire, browser tests, Docker compatibility tests, Ansible bootstrap
  structure test).
- No behavior change to RBAC, session auth, CSRF, or any API-key transport.
- No telemetry/new structured logging introduced.

## Git / commit requirements (binding, same as prior PRs in this repo)

- Every commit's Author and Committer must be exactly
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- Every commit must be GPG-signed GOOD with key
  `EB3E931B95880FB8931299E59DFC16E2E234C67B`.
- Zero occurrences of "copilot" (case-insensitive) anywhere in any commit
  message, trailer, author, or committer field.
- Only trailer allowed: `Assisted-by: Claude:<model>`.
- Conventional commit messages, `type(scope): [B]`/`[I]` marker per the
  established convention in this repository's `.goals/` history.

## Documentation requirements

Document (in an appropriately named doc under `docs/`, consistent with this
repo's existing conventions):
- Trusted-proxy semantics and configuration syntax.
- Direct-deployment mode (no configuration required, headers ignored).
- Reverse-proxy deployment mode (must configure trusted_proxies).
- The right-to-left `X-Forwarded-For` trust algorithm, in plain language.
- Why client-provided `X-Forwarded-For` is never trusted by default.
- That this is a breaking security-hardening behavior change relative to the
  previous (vulnerable) unconditional trust of `ProxyHeaders`.
- Multi-instance deployments are unaffected (still per-instance in-memory
  state; this PR does not change that).
- `/login` rate-limiter behavior is unchanged except the cleanup bug fix and
  the underlying IP source.

## Acceptance criteria (the Inspector verifies ALL of these)

1. `TRUSTED_PROXY_MODEL`: an explicit, documented trusted-proxy configuration
   exists, defaulting to trust-none when absent/empty, rejecting invalid
   entries at load time.
2. `DIRECT_XFF_SPOOF`: a direct (non-trusted-peer) request with a forged
   `X-Forwarded-For`/`X-Real-IP` NEVER changes the resolved client IP from the
   real socket peer. Proven by test.
3. `TRUSTED_PROXY_XFF`: behind a trusted peer, the rightmost untrusted hop in
   `X-Forwarded-For` is correctly selected as the client IP, even when the
   leftmost (client-controlled) entry is forged. Proven by test.
4. `MULTI_PROXY_RESOLUTION`: a multi-hop trusted proxy chain resolves
   correctly by scanning from the right. Proven by test.
5. `LOGIN_LIMITER_IP`: `/login`'s rate limiter now uses the safe resolved
   client IP, not raw/spoofable `r.RemoteAddr`. Proven by test (same-client,
   different-forged-XFF, same-bucket proof).
6. `CLEANUP_TICKER`: the `pollCleanup` double-multiplication bug is fixed;
   the real ticker interval matches the configured `cleanupInterval`. Proven
   by test (not merely read from the diff).
7. `MEMORY_PURGE`: an expired bucket is actually purged by the periodic
   mechanism (not just by direct `Cleanup()` invocation) under `-race`.
8. `API_RATE_LIMIT_SCOPE_LEAK`: no `/api/*` authentication rate limiter is
   added by this PR. Verified by diff inspection.
9. `DEPENDENCY_DIFF`: zero diff in `go.mod`, `go.sum`, `package.json`,
   `yarn.lock` relative to the base commit.
10. `LOGIN_COMPATIBILITY`: existing `/login` behavior (status codes, body
    shape, capacity/refill/expiry defaults, absence of `Retry-After`/JSON)
    is unchanged apart from the IP-source and ticker fixes.
11. `FULL_VERIFY`: `./scripts/verify.sh` and the full scanner/test suite
    listed under Non-functional constraints all pass.
12. Git identity/signature requirements are met for every commit in the
    range.
13. Documentation is added per the Documentation requirements section.
14. First-party nginx/Ansible configuration is updated to declare the actual
    verified trusted peer (not an assumed or excessively broad value), with
    the reasoning for the chosen CIDR/IP documented.
