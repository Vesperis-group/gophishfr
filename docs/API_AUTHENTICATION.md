# API authentication

GophishFR supports two deliberately separate authentication mechanisms for
`/api/*`.

## Browser SPA

The first-party administration SPA uses the existing same-origin `gophish`
web session. Its `fetch` requests send `credentials: "same-origin"` and do not
read or transmit the user's API key.

Requests without an explicit API credential use only the user already loaded
from the web session. A missing, expired, invalid, or logged-out session receives
the API's JSON `401` response; API routes do not redirect to the HTML login page.
Sessions whose user must complete a forced password change receive a JSON `403`
before any API handler or RBAC mutation runs. Completing the existing password
reset flow enables session API access. Explicit API keys retain their historical
behavior and are not coupled to that browser-session lifecycle guard.
The same user and role context then flows through the existing view-only and
route-specific RBAC middleware.

Unsafe session-authorized methods are ambient-cookie requests, so they pass
through the existing `filippo.io/csrf` `CrossOriginProtection` checks. Browser
requests marked cross-site or same-site by Fetch Metadata, or carrying a foreign
`Origin`, are rejected. Same-origin browser requests and safe methods retain
their existing behavior. No CSRF token protocol was added; deprecated
`csrf_token` form fields remain only for compatibility with existing non-API
forms.

Campaign completion is an unsafe operation. The SPA uses
`POST /api/campaigns/{id}/complete`, which receives the same origin and
view-only checks as other session mutations. Session-authenticated `GET` on
that legacy endpoint is rejected without changing campaign state. Explicit
API-key clients may continue using the legacy completion `GET` for public API
compatibility.

## External API clients

`Authorization: Bearer <token>` is the **only recommended and canonical**
transport. Use it for every new integration and every first-party example.

External clients also continue to authenticate, unchanged, with one
deprecated legacy transport that remains accepted during this release:

- a raw `Authorization` header carrying only the token, with no `Bearer`
  prefix — deprecated, with **no removal version announced**.

This was already documented here as a legacy compatibility transport; this
release does not change what it accepts, only how it is documented.

## Removed in `0.13.0`: query and form `api_key`

The `api_key` query parameter and the `api_key` form parameter were **removed
in `0.13.0`**, as announced in a prior release (see
[API-key transport deprecation](API_KEY_TRANSPORT_DEPRECATION.md) for the
full migration history, leaked-key rotation and log-review guidance, and why
no runtime deprecation signal was ever added for either transport). A request
carrying either — including an **empty** value, and including one that also
carries a valid session cookie or a valid `Authorization` (Bearer or raw)
credential — now receives the existing API JSON `401` response, with **no
fallback** to another credential or to the session. Their presence is still
explicitly detected so such a request can never be silently treated as
carrying no credential at all; only their *values* are no longer accepted for
authentication. Query credentials could previously be exposed by URL,
browser, and reverse-proxy access logging; this removal prevents any *future*
request from using that exposed transport, but it cannot retroactively erase
a copy already written to a log before this change — that remains an
operator-side log/rotation decision, exactly as the prior deprecation
guidance already said.

Authentication mechanism selection happens **before** credential validation.
The presence of any Authorization header — including an empty value —
selects API-key-only authentication; an invalid or empty explicit credential
never falls back to a valid web session. Multiple Authorization header
values are accepted when every value is identical; distinct values are
rejected as ambiguous rather than using the former header precedence. The
presence of a removed (query or form) `api_key` — by key, regardless of
value, and regardless of any other transport present in the same request —
is, on its own, sufficient to select API-key-only authentication and to fail
that authentication: it is never merged into the Bearer/raw credential value
used for lookup, and a simultaneously valid Authorization header never
"rescues" it, even when the removed transport's value happens to be
identical to that header's token.

Explicit API credentials remain exempt from browser CSRF checks because they
are non-ambient authority. Authentication and RBAC still validate them, so an
empty, invalid, conflicting, or removed-transport attempt cannot gain access.
Existing CORS, OPTIONS, status, response, and unsafe-method compatibility
remains unchanged.

Every explicit-credential authentication attempt (regardless of transport,
including a removed-transport attempt) is also subject to a per-client-IP
failure-budget rate limiter: repeated client-attributable authentication
failures from one IP eventually receive a generic `429` response, while
successful authentications, authorization (RBAC) failures, and server-side
failures never count against it. A removed-transport attempt always counts as
exactly one failure per HTTP request, even when both removed transports are
present simultaneously. See
[API-key authentication rate limiting](API_AUTH_RATE_LIMITING.md) for the
exact semantics, parameters, and caveats.

## Verifier storage and reveal-once management

Runtime authentication uses only indexed HMAC verifiers from the dedicated API
verifier keyring. It never reads or falls back to the nullable legacy
plaintext column. User GET/PUT responses and settings loads contain no token or
verifier metadata. Creation and reset reveal the generated token only in their
immediate successful response; a reset invalidates the old value at commit.

See [API-key HMAC verifiers](API_KEY_VERIFIER.md) for keyring setup, the
explicit irreversible offline migration, backup-only rollback, pepper
rotation, missing-key behavior, and limitations.
