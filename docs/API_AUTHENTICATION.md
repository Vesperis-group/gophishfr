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

External clients continue to authenticate with any of the legacy explicit
transports:

- `Authorization: Bearer <key>`;
- a raw `Authorization: <key>` value;
- the `api_key` query parameter; or
- the `api_key` form parameter.

Query and form credentials are retained solely for compatibility. Query
credentials can be exposed by URL/access logging and should be migrated to the
Authorization header; removing those transports is deferred.

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

## Verifier storage and reveal-once management

Runtime authentication uses only indexed HMAC verifiers from the dedicated API
verifier keyring. It never reads or falls back to the nullable legacy
plaintext column. User GET/PUT responses and settings loads contain no token or
verifier metadata. Creation and reset reveal the generated token only in their
immediate successful response; a reset invalidates the old value at commit.

See [API-key HMAC verifiers](API_KEY_VERIFIER.md) for keyring setup, the
explicit irreversible offline migration, backup-only rollback, pepper
rotation, missing-key behavior, and limitations.
