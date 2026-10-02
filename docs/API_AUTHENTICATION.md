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

External clients also continue to authenticate, unchanged, with three
deprecated legacy transports that remain accepted during this release:

- a raw `Authorization` header carrying only the token, with no `Bearer`
  prefix — deprecated, with **no removal version announced**;
- the `api_key` query parameter — deprecated, **removal targeted for
  `0.13.0`**; and
- the `api_key` form parameter — deprecated, **removal targeted for
  `0.13.0`**.

These three were already documented here as legacy compatibility transports;
this release does not change what they accept, only how they are documented.
See [API-key transport deprecation](API_KEY_TRANSPORT_DEPRECATION.md) for
migration examples, the exact `0.13.0` removal behaviour, leaked-key rotation
and log-review guidance, and why no runtime deprecation signal (header or
otherwise) was added. Query credentials can be exposed by URL, browser, and
reverse-proxy access logging; migrating to the Bearer header and rotating any
key ever sent in a query string removes that exposure going forward, but does
not erase a copy already written to a log before migration.

Authentication mechanism selection happens **before** credential validation.
The presence of any Authorization header, query `api_key`, or form `api_key` —
including an empty value — selects API-key-only authentication. An invalid or
empty explicit credential never falls back to a valid web session. Multiple
explicit values are accepted when every value is identical; distinct values
are rejected as ambiguous rather than using the former form/query/header
precedence.

Explicit API credentials remain exempt from browser CSRF checks because they
are non-ambient authority. Authentication and RBAC still validate them, so an
empty, invalid, or conflicting attempt cannot gain access. Existing CORS,
OPTIONS, status, response, and unsafe-method compatibility remains unchanged.

Every explicit-credential authentication attempt (regardless of transport) is
also subject to a per-client-IP failure-budget rate limiter: repeated
client-attributable authentication failures from one IP eventually receive a
generic `429` response, while successful authentications, authorization
(RBAC) failures, and server-side failures never count against it. See
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
