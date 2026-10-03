# API-key transport deprecation and removal

This page is the migration, deprecation, and removal companion to
[API authentication](API_AUTHENTICATION.md). It documents which legacy
explicit API-key transports were deprecated, which two were **removed in
`0.13.0`** (security/remove-legacy-api-key-transports), what remains accepted
today, and what operators must do about keys that may already be exposed.

All tokens shown below are synthetic placeholders. Never copy a real API key
into documentation, a test, or a support channel.

## Current contracts (as of `0.13.0`)

Two explicit transports are detected and continue to authenticate; the two
query/form transports were removed and are rejected outright, not merely
deprecated:

| Transport | Status | Removal |
|---|---|---|
| `Authorization: Bearer <token>` | **Canonical / recommended** | n/a |
| `Authorization: <token>` (no `Bearer` prefix) | Deprecated legacy | none announced |
| `api_key` query parameter | **Removed** | `0.13.0` |
| `api_key` form parameter | **Removed** | `0.13.0` |

A request carrying an `api_key` query parameter or `api_key` form
parameter — including an **empty** value, and including a request that also
carries a valid session cookie or a valid `Authorization` (Bearer or raw)
credential — is rejected with the existing API JSON `401` response and
**no fallback** to another credential or to the session. Their presence is
still explicitly detected, exactly as before this release, so a request
using either can never be silently treated as carrying no credential at all
and therefore can never fall back to session authentication; only their
*values* stopped being accepted for authentication. This holds even when the
removed transport's value is identical to a simultaneously-present, valid
`Authorization` header's token: presence of the removed transport alone is
disqualifying, and a valid `Authorization` header never "rescues" it.

Nothing about raw `Authorization` acceptance, RBAC, CSRF, view-only, CORS,
status codes, error bodies, or verifier storage changed in this release. See
API_AUTHENTICATION.md for that unchanged behaviour and
[API-key HMAC verifiers](API_KEY_VERIFIER.md) for verifier/keyring details.
An unsupported-transport attempt is also subject to the same per-client-IP
failure-budget rate limiter as every other explicit-credential attempt, and
counts as exactly one failure per HTTP request even when both removed
transports are present simultaneously, or alongside a valid `Authorization`
header; see
[API-key authentication rate limiting](API_AUTH_RATE_LIMITING.md).

## Migrate to the canonical transport

Replace every query or form `api_key` usage with `Authorization: Bearer
<token>`. Raw `Authorization` (no `Bearer` prefix) remains accepted and
deprecated, with no removal version decided yet; it does not need to migrate
today, though `Authorization: Bearer` is still recommended for it too.

Every example below targets the same real, documented business operation so
the migration is provably equivalent and not just syntactically similar:
`POST /api/campaigns/{id}/complete` — the API endpoint that ends an existing
campaign (see "Campaign completion" in
[API authentication](API_AUTHENTICATION.md)). The route has **no trailing
slash**; its handler reads only the numeric campaign ID from the path and the
caller's identity, so it needs no request body at all, which is what makes it
usable with every transport below without inventing a fictitious business
contract. Replace `42` with a real campaign ID and
`REPLACE_WITH_YOUR_TOKEN` with a real token; never a real token in
documentation. `controllers/api/campaign_transport_deprecation_test.go` is an
executable regression that dispatches every request below through the real
API router (including `RequireAPIKey` and the real route table, not a direct
handler call) and asserts the canonical and raw transports authenticate and
complete the same campaign, while the removed query and form transports are
rejected and never complete it.

**What these examples prove, and what they deliberately don't.** This
endpoint's business semantics live entirely in its method and path; every
historical form-transport example below sent an intentionally credential-only
body, by design, so transport was the only thing that changed between
"before" and "after." That proved transport-level authentication equivalence
while the form transport was still accepted. It does **not**, by itself,
demonstrate the separate rule that matters for a client whose own form body
also carries business data: migrating removes only `api_key` and preserves
every other field byte-for-byte. For that rule, see
["Generic form migration template"](#generic-form-migration-template-illustrative-only)
below, and the middleware-level
`TestFormTransportPreservesBusinessFields` (in
`middleware/api_auth_form_fields_test.go`), which wraps the real
`RequireAPIKey` middleware around a local handler and asserts non-`api_key`
`PostForm` fields survive parsing and authentication unchanged under the
canonical `Authorization: Bearer` transport, and that a form request still
carrying `api_key` is rejected before that handler ever runs.

### Query parameter → Bearer (removed in `0.13.0`): historical migration example

This transport no longer authenticates. The request below is shown only as
the "before" half of the migration it used to require; sending it today
receives a `401`, not the completed campaign it once returned.

```bash
curl -X POST \
  "https://gophishfr.example/api/campaigns/42/complete?api_key=REPLACE_WITH_YOUR_TOKEN"
```

After (canonical):

```bash
curl -X POST "https://gophishfr.example/api/campaigns/42/complete" \
  -H "Authorization: Bearer REPLACE_WITH_YOUR_TOKEN" # gitleaks:allow -- synthetic placeholder, not a real token
```

### Form parameter → Bearer (removed in `0.13.0`): historical migration example

This endpoint needs no business fields, so — intentionally — the body here
was nothing but the credential; this was never a template for a form body
that also carries business data (see the generic template below for that).
This transport no longer authenticates; sending it today receives a `401`.

```bash
curl -X POST "https://gophishfr.example/api/campaigns/42/complete" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data "api_key=REPLACE_WITH_YOUR_TOKEN"
```

After (canonical): the credential moves from the form body to the
`Authorization` header, so the request becomes a plain, bodyless `POST`:

```bash
curl -X POST "https://gophishfr.example/api/campaigns/42/complete" \
  -H "Authorization: Bearer REPLACE_WITH_YOUR_TOKEN" # gitleaks:allow -- synthetic placeholder, not a real token
```

### Raw `Authorization` → Bearer (deprecated legacy, no removal version announced): the header carries
only the token, with no scheme prefix. This transport is **not** part of the
`0.13.0` removal and still authenticates today.

```bash
curl -X POST "https://gophishfr.example/api/campaigns/42/complete" \
  -H "Authorization: REPLACE_WITH_YOUR_TOKEN" # gitleaks:allow -- synthetic placeholder, not a real token
```

After (canonical): prefix the same token with the standard `Bearer` scheme.
No other part of the request changes.

```bash
curl -X POST "https://gophishfr.example/api/campaigns/42/complete" \
  -H "Authorization: Bearer REPLACE_WITH_YOUR_TOKEN" # gitleaks:allow -- synthetic placeholder, not a real token
```

The documented legacy completion `GET` (see API_AUTHENTICATION.md) works the
same way for raw and Bearer `Authorization`, as it always did; it is no
longer reachable with a query `api_key` at all, since that transport is now
rejected before any route-specific behaviour runs.

### A technical note on the former form transport's exact scope

While it was still accepted, the form `api_key` transport worked only where
all of the following held: the request method was `POST`, `PUT`, or `PATCH`;
the `Content-Type` header's **parsed media type** (via Go's
`mime.ParseMediaType`, which `ParseForm` calls internally) was
`application/x-www-form-urlencoded` — media-type parameters such as
`; charset=UTF-8` were explicitly allowed and ignored, only the base media
type had to match; and the credential was read from Go's `r.PostForm` (via
`r.ParseForm()`, which `middleware.GetContext` and the credential extractor
both call). In particular:

- a **`multipart/form-data`** request — the content type every real
  GophishFR file-upload endpoint uses, including
  `POST /api/import/group` — was never parsed into `PostForm` by
  `ParseForm` at all. A multipart field literally named `api_key` was never
  read as a credential, and this remains true after removal. `/api/import/group`
  is, in practice, header-authenticated only (the removed query transport's
  presence is still detected there too, and is rejected like anywhere else);
  see [Group import limits](GROUP_IMPORT_LIMITS.md).
- a `GET` request's body, if it had one, was never parsed into `PostForm`
  either — again because of how `ParseForm` scopes itself to
  `POST`/`PUT`/`PATCH`, not because `GET` cannot carry a body. This detail is
  purely historical now that the form transport itself is removed.

## Generic form migration template (illustrative only)

**This is not a real GophishFR endpoint.** No GophishFR API route accepts
`application/x-www-form-urlencoded` business data — every real write
endpoint (campaigns, groups, templates, pages, SMTP profiles, webhooks,
users) expects a JSON body, which is exactly why the executable example
above has to use a credential-only endpoint. This template instead shows,
for historical reference, the migration pattern an external client should
have applied to **its own** form-based integration before the form
transport's removal: remove only the `api_key` field and keep every other
field byte-for-byte identical. A client that still has an unmigrated form
integration today must migrate it now: the "before" shape below no longer
authenticates at all.

Before (illustrative, historical; `your_field`/`another_field` stand in for
that client's real business data):

```bash
curl -X POST \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data "api_key=REPLACE_WITH_YOUR_TOKEN&your_field=unchanged-value&another_field=42" \
  "https://your-service.example/some-endpoint"
```

After (illustrative; `your_field=unchanged-value&another_field=42` is
copied byte-for-byte — nothing about the business payload changes, only
the credential moves to a header):

```bash
curl -X POST \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data "your_field=unchanged-value&another_field=42" \
  "https://your-service.example/some-endpoint" \
  -H "Authorization: Bearer REPLACE_WITH_YOUR_TOKEN" # gitleaks:allow -- synthetic placeholder, not a real token
```

`TestFormTransportPreservesBusinessFields`
(`middleware/api_auth_form_fields_test.go`) is the executable proof behind
this rule: it wraps the real `RequireAPIKey` middleware (the same one every
`/api/*` route uses) around a local handler, sends unrelated `PostForm`
fields alongside the canonical `Authorization: Bearer` header with `api_key`
removed from the body, and asserts the captured business fields are
identical, byte-for-byte, to the same fields sent before migration. It also
asserts a form request that still carries `api_key` is rejected before that
handler ever runs.

## What changed in `0.13.0`

This release (`security/remove-legacy-api-key-transports`) implements the
removal announced in the prior deprecation release:

- any request carrying an `api_key` query parameter or `api_key` form
  parameter — including an **empty** value, and including a request that
  also carries a valid session cookie or a valid `Authorization` (Bearer or
  raw) credential — is now rejected with the existing API JSON `401`
  response and **no fallback** to another credential or to the session;
- explicit presence detection for the removed transports was retained, so a
  request using them still cannot silently fall back to session
  authentication instead of failing closed;
- raw `Authorization: <token>` remains **accepted and deprecated**, with no
  sunset version decided yet. Its removal, if it happens, will be announced
  and implemented separately.

This document does not implement any further removal. Raw `Authorization`'s
future, if any, will be announced separately.

## You may already have leaked keys in logs

Moving a client to `Authorization: Bearer` does **not** retroactively remove
any token already written to a log before migration, and removing the query
transport in `0.13.0` does **not** retroactively remove or redact anything
already logged before this change either — it only prevents a *future*
request from using that exposed transport. Query-string credentials in
particular were commonly captured by infrastructure that was never designed
to treat a URL as a secret:

- GophishFR's own combined access-style logs;
- nginx and other reverse-proxy default request logs (which log the full
  request line, including the query string, by default);
- browser history and autocomplete, and shell history on any host where the
  URL was typed or scripted;
- support tickets, screen recordings, and diagnostics that copy a URL
  verbatim.

Form bodies and `Authorization` headers (raw or Bearer) were not written to
those same default log lines, so form-based credentials were lower default
logging risk than query credentials. Both are removed now for one converged,
canonical transport, rather than because one was ranked worse than the
other.

**Operators must, for any key that was ever sent as a query parameter
(before this release, when that transport still worked):**

1. confirm every client using that key has migrated to
   `Authorization: Bearer` — a client that has not migrated can no longer
   authenticate at all after this release, so this is now also an
   availability concern, not only a security one;
2. rotate (reset) the key through existing API-key reset so the previously
   logged value stops working; see
   [API-key HMAC verifiers](API_KEY_VERIFIER.md#issuance-and-browser-behavior);
   and
3. review and remove historical copies of that key from access logs,
   reverse-proxy logs, and any copied diagnostics, under your own log
   retention and incident-handling policy.

GophishFR cannot automatically identify which external clients were
affected, and it cannot reach into or clean third-party or reverse-proxy log
storage it does not control. This document is guidance, not a tool: step 3
above is a manual operator action against infrastructure this project does
not manage. This release stops query-string secrets from reaching this
project's own access logs *going forward*; it does not and cannot touch any
copy already written before this release, or any copy a reverse proxy or
other infrastructure this project does not control wrote on its own.

## Why no runtime deprecation signal was ever added

Neither the earlier deprecation release nor this removal release added a
machine-readable deprecation signal to API responses:

- **No RFC 9745 `Deprecation` header.** That header deprecates the resource
  identified by the request URI. `/api/*` endpoints themselves were never
  deprecated — only specific *credential transports* for those same
  endpoints were. Sending the header would have misrepresented the resource
  as a whole.
- **No RFC 8594 `Sunset` header.** `Sunset` asserts a specific calendar date
  after which the resource stops being available. No official sunset
  **date** exists for raw `Authorization`, and the query/form removal was
  tied to a version (`0.13.0`), not a calendar date, so there was no value
  to put in this header without inventing one.
- **No `Link: rel="deprecation"` header.** That relation points readers at a
  stable, published documentation URI. This repository has no stable,
  repository-hosted absolute URL for this page; publishing a `Link` header
  that points nowhere durable would have been worse than omitting it.
- **No custom header and no server-side warning log entry.** Per-request
  signalling of this kind is easy to miss, easy to silently depend on, and
  would have been the first transport-usage telemetry this project has ever
  added. Documentation and release notes were the deliberate migration
  channel for this deprecation and removal instead.

No change in this release emits a token, a full request URL or query string,
a form body, an `Authorization` header value, a verifier, or any other
secret-derived value in a log, response, or header. That is unchanged from
before this release.

## Out of scope / follow-up work

This release does **not**:

- remove, reject, or change raw `Authorization` acceptance or its "no
  removal version announced" documented status;
- change the credential extractor's Bearer/raw handling, the verifier,
  keyring, token format, session, CSRF, RBAC, or `events.details` behaviour;
- add API-key rate limiting beyond what already existed before this release
  (see [API-key authentication rate limiting](API_AUTH_RATE_LIMITING.md),
  shipped separately);
- bump `VERSION`, `package.json`'s version field, or `go.mod`'s `go`/
  `toolchain` directives. Those remain explicitly out of scope for this PR
  and are handled through the normal release process.

Raw `Authorization` removal, a Go toolchain bump, and release preparation
remain explicitly deferred items for separate future work.
