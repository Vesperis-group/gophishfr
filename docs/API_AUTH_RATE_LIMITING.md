# API-key authentication failure rate limiting

Packages: [`middleware/ratelimit`](../middleware/ratelimit),
[`middleware`](../middleware) (`RequireAPIKey`)

## What this protects, and what it does not

`RequireAPIKey`'s explicit-credential branch -- Bearer, raw `Authorization`,
query `api_key`, and form `api_key`, uniformly, for as long as each remains
accepted (see
[API-key transport deprecation](API_KEY_TRANSPORT_DEPRECATION.md)) -- is now
protected by a per-client-IP **failure budget**. Before this change, no rate
limiter protected `/api/*` authentication at all: an attacker could brute
force API tokens as fast as the network allowed.

This is deliberately **not** the same kind of limiter as `/login`'s
`middleware/ratelimit.PostLimiter` (still 5 requests/minute, unchanged,
counting every POST attempt, success or failure). The new limiter only ever
spends budget on an **authentication failure attributable to the client
credential**:

- A successful API-key authentication consumes nothing.
- A successful API-key authentication does not reset or restore budget
  already consumed by prior failures. An attacker holding one valid key
  cannot authenticate once to erase their brute-force history against other
  tokens from the same IP.
- Budget only recovers through natural time-based refill.
- An authorization (RBAC permission, or `EnforceViewOnly`) failure that
  happens *after* a successful authentication never touches this limiter.
- A server-side/infrastructure failure -- the API verifier keyring being
  unavailable, an internal ambiguous-candidate state, or any raw database
  error -- never touches this limiter either. A server-side outage must never
  be able to lock out every client IP.
- Session-only requests (no explicit API credential present at all) never
  touch this limiter. `IsSessionAuthentication`/the session branch of
  `RequireAPIKey` has zero lookups or mutations against it.

## Client IP

The limiter resolves client IP exclusively through the same
`middleware/clientip.Resolver` primitive used everywhere else in the request
chain (the admin/phishing handler chains, and the `/login` `PostLimiter`) --
see [Trusted-proxy client IP resolution](TRUSTED_PROXY_RESOLUTION.md). It
never re-parses `r.RemoteAddr`, `X-Forwarded-For`, or `X-Real-IP` itself.
Production startup (`controllers/route.go`) installs the limiter with the
deployment's configured `trusted_proxies`, exactly like the `/login` limiter.
The bucket key is the resolved client IP alone -- never IP+token, a token
hash, user ID, or transport.

## Parameters (v1)

- **Burst: 20 failures.** **Refill: 20 failures/minute.**
- These are intentionally more permissive than `/login`'s 5/minute, because
  only failures spend budget here (a well-behaved, high-throughput API
  client spends nothing) and because shared NAT/CI egress IPs are common for
  legitimate API traffic.
- Bucket expiry: ~10 minutes of inactivity (the same `DefaultExpiry` as
  `/login`'s limiter), purged by the same corrected periodic-cleanup pattern
  introduced for `/login` (no ticker-interval bug reintroduced).
- These values are not configurable in this release. They may be tuned
  later if operational experience warrants it.
- `/login`'s 5/minute and all its other parameters and response behavior are
  completely unchanged by this work.

## Boundary convention

The failure budget is a token-bucket-style counter, not a hard request
counter, so there is one precise, binding rule for the exact request that
pushes a bucket from "at threshold" to "over threshold":

> The request that consumes the last unit of budget (e.g. the 20th recorded
> failure against a fresh bucket) is **not** itself blocked. The
> block/no-block pre-check runs once, *before* that request's own
> authentication attempt, and at that point budget was still available, so
> it still receives its historical per-case response (e.g. `401`). Only the
> **next** request against that IP, whose pre-check now finds the budget at
> zero, is rejected with `429`.

## Pre-check ordering

Every explicit-credential request is checked against the limiter *before*
anything else is done with the credential -- before HMAC candidate
computation and before the database lookup. If the IP is already blocked:

- HMAC candidate computation is never performed.
- The database lookup is never performed.
- The response is `429 Too Many Requests`, written with the existing
  `middleware.JSONError` helper (the same `models.Response{Success: false,
  Message: ...}` JSON shape every other `RequireAPIKey` failure already
  uses) -- never `http.Error`, HTML, or a redirect.
- A `Retry-After` header is always present: a non-negative integer
  delta-seconds value (never an HTTP-date), with a minimum of 1 whenever any
  positive wait is needed.
- The response body is completely generic. It never reveals whether a token
  exists, whether a user exists, whether an account is locked, which
  verifier key matched, or which transport was used -- this is true
  regardless of what the request's own credential would otherwise have
  produced (malformed, ambiguous, empty, unknown, or even genuinely valid).

## Failure classification

`models.GetUserByAPIKey` already collapses "unknown token" and "token
belongs to a locked account" into a single `models.ErrInvalidAPICredential`
sentinel, specifically so a locked account's token stays indistinguishable
from an unknown one (see [API-key HMAC verifiers](API_KEY_VERIFIER.md)).
This work adds the minimum possible classification on top of that existing
sentinel set: `models.ErrInvalidAPICredential` is the one client-attributable
failure; every other error (`ErrAPIKeyVerifierUnavailable`,
`ErrInvalidAPIKeyState`, `ErrAmbiguousAPICredential`, or any raw database
error) is a server-side failure and is never recorded. No new error type,
and no new oracle, was introduced. This internal classification is used
*only* to decide whether to record a limiter failure -- it never changes any
externally-visible status code or response body for a request below the
rate-limit threshold.

A malformed, ambiguous, or empty explicit credential (the existing
`extractExplicitAPICredential` paths) counts as exactly one client
failure for that request, regardless of how many conflicting values or
transports were present in it.

## Logging

No new log line is added per authentication attempt, success or failure.
Nothing about this limiter logs the credential value, the `Authorization`
header, the query string, the form body, or the computed HMAC candidate.

**APPLICATION AUTH LOGGING: none added.**

**LEGACY ACCESS-URI RISK: remains until 0.13.0.** The query `api_key`
transport can still appear in the URI recorded by the admin server's
`CombinedLoggingHandler` access log. That pre-existing, separately-tracked
risk is unrelated to this change and is not resolved by it; see
[API-key transport deprecation](API_KEY_TRANSPORT_DEPRECATION.md) for its
removal timeline.

## Operational caveats

- **In-memory, per-instance store.** Like `/login`'s `PostLimiter`, state
  lives only in process memory and resets on restart. It is not shared
  across multiple GophishFR instances/replicas.
- **NAT/shared-IP caveat.** Many clients behind the same NAT gateway or CI
  egress IP share one failure budget. The 20-failure burst is sized with
  this in mind, but a shared IP with many genuinely failing clients can
  still exhaust it for everyone behind that IP.
- **Distributed-attack caveat.** An attacker spreading failed attempts across
  many source IPs is not slowed by this limiter at all -- it is explicitly
  an IP-only, single-instance mechanism, not a global or cross-IP limiter,
  and no distributed coordination store (e.g. Redis) was added. This is a
  known, accepted limitation of this release, not a claim that credential
  brute-forcing is fully solved.
