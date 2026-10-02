# Trusted-proxy client IP resolution

Packages: [`middleware/clientip`](../middleware/clientip),
[`middleware/ratelimit`](../middleware/ratelimit)

This document is the human-readable companion to `middleware/clientip`'s own
package doc comment, which is the authoritative reference for exact
behaviour. It also records why this change was made and what changed for
operators.

## Why this exists

Until this change, `github.com/gorilla/handlers.ProxyHeaders` was applied
unconditionally to both the admin handler chain (`controllers/route.go`) and
the phishing handler chain (`controllers/phish.go`). It rewrote
`r.RemoteAddr` from `X-Forwarded-For` (taking the **leftmost**,
client-controlled segment) or `X-Real-IP`, for every request, with **no
trusted-proxy allowlist of any kind**.

That meant a direct, non-proxied client could set
`X-Forwarded-For: <anything>` and change the IP GophishFR believed it was
talking to on every request — even behind this project's own correctly
configured nginx (`ansible-playbook/roles/gophish/templates/nginx.conf.j2`),
because nginx appends the real client to the **right** of the header
(`proxy_add_x_forwarded_for`) while the old code trusted the **left** end.
This was exploitable against anything keying state off of the client IP,
notably the `/login` rate limiter (`middleware/ratelimit.PostLimiter`), which
let an attacker reset their own rate-limit bucket on demand.

**This is a breaking security-hardening behavior change relative to the
previous (vulnerable) unconditional trust of `ProxyHeaders`.** By default
(no `trusted_proxies` configured), `X-Forwarded-For` and `X-Real-IP` are now
**always ignored**, even if a reverse proxy is actually in front of
GophishFR. A deployment that relies on either header for correct client IPs
must explicitly configure `trusted_proxies` (see below) after upgrading.

## Configuration

Both `admin_server` and `phish_server` accept an independent
`trusted_proxies` array in `config.json`:

```json
{
  "admin_server": { "...": "..." },
  "phish_server": {
    "listen_url": "127.0.0.1:8080",
    "trusted_proxies": ["127.0.0.1/32"]
  }
}
```

Each entry is either a single IP address (normalized internally to a `/32`
or `/128` prefix) or a CIDR block. IPv4, IPv6, and IPv4-mapped IPv6 addresses
are all supported and compared in normalized (unmapped) form.

- **Absent or empty `trusted_proxies` (including omitting the key entirely)
  means trust no proxy.** This is the default and the safe starting point
  for a direct, non-proxied deployment.
- **An invalid entry (not a valid IP or CIDR) fails config loading
  immediately** with a descriptive error; it is never silently ignored or
  treated as "trust everyone" or "trust no one" by surprise.
- The default is never `0.0.0.0/0`, `::/0`, or an implicit
  "all private/loopback/Docker ranges" shortcut. Every trusted network must
  be listed explicitly.

`admin_server` and `phish_server` are configured independently because they
are commonly exposed through different (or no) reverse proxies. In this
project's first-party Ansible deployment
(`ansible-playbook/roles/gophish/files/config.json`), `admin_server` listens
on `127.0.0.1:3333` and is **not** fronted by the provisioned nginx vhost at
all (nginx's only `proxy_pass` target is `127.0.0.1:8080`, the phishing
server), so its `trusted_proxies` is left unset: trust none, matching the
actual topology. `phish_server` *is* fronted by that nginx, which proxies to
`127.0.0.1:8080` — so GophishFR sees the loopback address as the peer for
every proxied request — and its `trusted_proxies` is set to `["127.0.0.1/32"]`
to match. A deployment that puts a proxy in front of `admin_server` too (for
example, a shared ingress) must add the equivalent entry to
`admin_server.trusted_proxies` itself; this change does not assume that.

## Deployment modes

**Direct deployment (default, no configuration required).** With
`trusted_proxies` absent or empty, `X-Forwarded-For`/`X-Real-IP` are always
ignored. The resolved client IP is always the direct TCP socket peer. This
is correct and safe for any deployment where GophishFR's listener is reached
directly by clients (no reverse proxy in front of it).

**Reverse-proxy deployment (must configure `trusted_proxies`).** If
GophishFR is behind a reverse proxy, that proxy's peer address (as
GophishFR's listener sees it — check the actual `proxy_pass`/upstream
target, don't assume `127.0.0.1`) must be added to `trusted_proxies` for the
corresponding server (`admin_server` and/or `phish_server`). Only then are
forwarding headers consulted, and only using the algorithm below.

## The right-to-left `X-Forwarded-For` trust algorithm

In plain language: **scan the forwarding chain from the proxy you trust
backwards towards the client, stopping at the first entry that isn't
another proxy you trust.**

1. `peer` is the immediate TCP socket peer (the connection GophishFR itself
   accepted), read **before** any header is consulted.
2. If `peer` is not in `trusted_proxies`, `X-Forwarded-For` and `X-Real-IP`
   are ignored completely. The resolved client IP is `peer`. This holds even
   if the headers contain a syntactically valid IP — an untrusted peer gets
   no say in the matter at all.
3. If `peer` **is** trusted, `X-Forwarded-For` (a comma-separated hop list,
   oldest/client-added entries on the left, each proxy appending to the
   right as the request passes through — which is exactly what this
   project's own nginx does via `proxy_add_x_forwarded_for`) is evaluated
   **from the rightmost hop towards the left**: as long as a hop is itself a
   trusted proxy, keep moving left; the **first hop that is not a trusted
   proxy is the resolved client IP**.
4. If every hop turns out to be a trusted proxy, or the header can't be
   parsed safely (see below), resolution falls back to `peer` — never to
   client-controlled data.
5. `X-Real-IP` is only ever consulted as a fallback, and only when all of
   the following hold: `peer` is trusted, `X-Forwarded-For` is **absent
   entirely** (not merely present-but-unusable), and `X-Real-IP` contains
   exactly one syntactically valid IP. The two headers are never combined in
   the same resolution pass.

### Why client-provided `X-Forwarded-For` is never trusted by default

Because the header is, by HTTP's design, exactly as trustworthy as whoever
sent the request — and a direct client can send any header it likes. The
only thing that can make a hop in that header meaningful is a chain of
intermediaries GophishFR has explicitly decided to trust: each trusted proxy
is expected to either strip and replace, or honestly append to, the header
before passing a request along. Without an explicit, operator-configured
trust boundary, there is no way to tell "a proxy added this" from "an
attacker typed this".

### Malformed-input handling

- An invalid IP literal, an empty element (e.g. `"1.2.3.4,,5.6.7.8"`), a
  hostname, an unexpected port suffix (e.g. `"1.2.3.4:9999"`), or mixed
  IPv4/IPv6 garbage encountered anywhere while scanning is never trusted,
  and never crashes resolution: it safely falls back to `peer`.
- The header value is bounded (4096 bytes) and the number of hops evaluated
  is bounded (64, keeping only the rightmost, since scanning is
  right-to-left) before any parsing happens, so a single oversized header
  cannot force unbounded CPU/memory work.
- No full attacker-controlled header value is ever logged verbatim by this
  package; it introduces no new logging at all.
- Parsing uses only `net`/`net/netip` from the standard library — no new
  dependency.

## Integration

- `(*clientip.Resolver).Middleware` is the drop-in replacement for the
  removed `gorilla/handlers.ProxyHeaders` in both
  `controllers/route.go` (admin chain) and `controllers/phish.go` (phishing
  chain). It rewrites `r.RemoteAddr` to the resolved client IP using the
  algorithm above — the same slot `ProxyHeaders` used to occupy — so access
  logging (`handlers.CombinedLoggingHandler`) and any other code reading
  `r.RemoteAddr` downstream (for example the phishing server's GeoIP/event
  recording in `controllers/phish.go`'s `setupContext`) keep working, but can
  now only ever reflect a forwarded address when it came through an
  explicitly trusted proxy.
- `middleware/ratelimit.PostLimiter` (the `/login` rate limiter) is
  independently wired to a `(*clientip.Resolver).ClientIP` via the
  `ratelimit.WithClientIP` option, rather than relying on request-chain
  ordering. This is both defense-in-depth and what makes the limiter's own
  spoof-resistance independently testable. A limiter constructed without
  `WithClientIP` defaults to a resolver that trusts no proxy (the same safe
  default), not to any raw, unconditional `r.RemoteAddr` read.
- `(*clientip.Resolver).ClientIP` is also the reusable primitive intended for
  a future `/api` authentication rate limiter (`security/rate-limit-api-auth`,
  out of scope here) and for any future access-logging work: the exact same
  resolution algorithm, with no re-implementation.

## `/login` rate-limiter behavior

Unchanged except for the IP source and the cleanup-ticker fix described
below: thresholds, capacity, refill rate, `expiry`, response status codes
(`429` at/over the threshold), and response body shape (plain
`http.Error`, not JSON; no `Retry-After` header) are all exactly as before.

## Periodic cleanup bug fix

`PostLimiter.pollCleanup()` computed
`time.NewTicker(time.Duration(limiter.cleanupInterval) * time.Second)`.
`cleanupInterval` is already a `time.Duration`; multiplying it again by
`time.Second` (1,000,000,000 nanoseconds) inflated the real ticker interval
from the configured 1 minute to roughly 147.7 years, so the periodic cleanup
essentially never ran in production — only tests that called `Cleanup()`
directly ever exercised it. The ticker now uses `limiter.cleanupInterval`
directly. `middleware/ratelimit/ratelimit_test.go` proves the fix by
configuring a short real interval and asserting that an expired bucket is
purged by the *background ticker itself* (not by a direct `Cleanup()` call),
under `-race`, without ever sleeping for a real minute or longer.

A minimal `(*PostLimiter).Stop()` lifecycle method was added so that tests
constructing many limiters don't accumulate live background tickers for the
remainder of the test binary; it is optional in production, where the
limiter's lifetime is the process's lifetime.

## Multi-instance deployments

Unaffected by this change. Rate-limit and trusted-proxy state remain
per-instance, in-memory only (`map[string]*bucket` behind a mutex); this PR
does not introduce, and does not change anything about the absence of, any
shared/distributed store.
