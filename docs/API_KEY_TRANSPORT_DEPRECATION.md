# API-key transport deprecation

This page is the migration and deprecation companion to
[API authentication](API_AUTHENTICATION.md). It documents which legacy
explicit API-key transports are deprecated, what remains accepted during this
release, what changes in `0.13.0`, and what operators must do about keys that
may already be exposed. It does not change any runtime behaviour: every
transport described here authenticates exactly as it did before this page was
added.

All tokens shown below are synthetic placeholders. Never copy a real API key
into documentation, a test, or a support channel.

## Current contracts (this release)

Four explicit transports are detected and continue to authenticate:

| Transport | Status | Removal target |
|---|---|---|
| `Authorization: Bearer <token>` | **Canonical / recommended** | n/a |
| `Authorization: <token>` (no `Bearer` prefix) | Deprecated legacy | none announced |
| `api_key` query parameter | Deprecated legacy | `0.13.0` |
| `api_key` form parameter | Deprecated legacy | `0.13.0` |

These were previously documented in `API_AUTHENTICATION.md` as "legacy
compatibility transports." That description stands: query, form, and raw
`Authorization` are public, documented contracts — not an accidental surface —
even though no in-repository first-party or business client uses them today.
Because shipped binaries carry no transport telemetry, the external client
population still using query, form, or raw transports is unknown. That is the
reason this is a deprecation window rather than an immediate removal: a
bounded, clearly announced window gives real external clients time to migrate
before the credential detection behaviour described in
[API authentication](API_AUTHENTICATION.md) changes for them.

Nothing about detection, validation, duplicate/conflict handling, RBAC,
CSRF, view-only, CORS, status codes, error bodies, or verifier storage changes
in this release. See API_AUTHENTICATION.md for that unchanged behaviour and
[API-key HMAC verifiers](API_KEY_VERIFIER.md) for verifier/keyring details.

## Migrate to the canonical transport

Replace every query or form `api_key` usage, and every raw `Authorization`
header, with `Authorization: Bearer <token>`.

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
executable regression that dispatches the old and new requests below through
the real API router (including `RequireAPIKey` and the real route table, not
a direct handler call) and asserts both authenticate and both actually
complete the same campaign.

**What these examples prove, and what they deliberately don't.** This
endpoint's business semantics live entirely in its method and path; every
form-transport example below sends an intentionally credential-only body, by
design, so transport is the only thing that changes between "before" and
"after." That proves transport-level authentication equivalence — exactly
what `TestCampaignCompleteTransportDeprecationEquivalence` asserts. It does
**not**, by itself, demonstrate the separate rule that matters for a client
whose own form body also carries business data: migrating removes only
`api_key` and preserves every other field byte-for-byte. For that rule, see
["Generic form migration template"](#generic-form-migration-template-illustrative-only)
below, and the middleware-level
`TestFormTransportPreservesBusinessFields` (in
`middleware/api_auth_form_fields_test.go`), which wraps the real
`RequireAPIKey` middleware around a local handler and asserts non-`api_key`
`PostForm` fields survive parsing and authentication unchanged under both
the deprecated form transport and the canonical Bearer header.

### Query parameter → Bearer

Before (deprecated, removal targeted for `0.13.0`):

```bash
curl -X POST \
  "https://gophishfr.example/api/campaigns/42/complete?api_key=REPLACE_WITH_YOUR_TOKEN"
```

After (canonical):

```bash
curl -X POST "https://gophishfr.example/api/campaigns/42/complete" \
  -H "Authorization: Bearer REPLACE_WITH_YOUR_TOKEN" # gitleaks:allow -- synthetic placeholder, not a real token
```

### Form parameter → Bearer

Before (deprecated, removal targeted for `0.13.0`). This endpoint needs no
business fields, so — intentionally — the body here is nothing but the
credential; this is not a template for a form body that also carries
business data (see the generic template below for that):

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

### Raw `Authorization` → Bearer

Before (deprecated legacy, no removal version announced): the header carries
only the token, with no scheme prefix.

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
same way for query, raw, and Bearer credentials. The form transport
specifically requires `POST` (or `PUT`/`PATCH`) — not because a `GET`
request cannot carry a body (HTTP permits one), but because Go's
`net/http` `ParseForm` only reads a request body into `PostForm` for
`POST`, `PUT`, and `PATCH`; it never reads a `GET` request's body as form
data, however that body is encoded.

### A technical note on the form transport's exact scope

The form `api_key` transport works only where all of the following hold:
the request method is `POST`, `PUT`, or `PATCH`; the `Content-Type` is
exactly `application/x-www-form-urlencoded`; and the credential is read
from Go's `r.PostForm` (via `r.ParseForm()`, which `middleware.GetContext`
and the credential extractor both call). In particular:

- a **`multipart/form-data`** request — the content type every real
  GophishFR file-upload endpoint uses, including
  `POST /api/import/group` — is **not** parsed into `PostForm` by
  `ParseForm` at all. A multipart field literally named `api_key` is never
  read as a credential. `/api/import/group` is, in practice,
  **header- or query-authenticated only**; see
  [Group import limits](GROUP_IMPORT_LIMITS.md).
- a `GET` request's body, if it has one, is never parsed into `PostForm`
  either — again because of how `ParseForm` scopes itself to
  `POST`/`PUT`/`PATCH`, not because `GET` cannot carry a body.

## Generic form migration template (illustrative only)

**This is not a real GophishFR endpoint.** No GophishFR API route accepts
`application/x-www-form-urlencoded` business data — every real write
endpoint (campaigns, groups, templates, pages, SMTP profiles, webhooks,
users) expects a JSON body, which is exactly why the executable example
above has to use a credential-only endpoint. This template instead shows
the migration pattern an external client should apply to **its own**
form-based integration: remove only the `api_key` field and keep every
other field byte-for-byte identical.

Before (illustrative; `your_field`/`another_field` stand in for that
client's real business data):

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
fields alongside the deprecated form `api_key` in one case and alongside a
canonical Bearer header with `api_key` removed from the body in the other,
and asserts the captured business fields are identical, byte-for-byte, in
both cases.

## What changes in `0.13.0`

The next breaking release, tracked as
`security/remove-legacy-api-key-transports`, is expected to:

- reject any request carrying an `api_key` query parameter or `api_key` form
  parameter — including an **empty** value, and including a request that also
  carries a valid session cookie or a valid `Authorization` (Bearer or raw)
  credential — with the existing API JSON `401` response and **no fallback**
  to another credential or to the session;
- retain explicit presence detection for the removed transports, so a request
  using them cannot silently fall back to session authentication instead of
  failing closed;
- leave raw `Authorization: <token>` **accepted and deprecated**, with no
  sunset version decided yet. Its removal, if it happens, will be announced
  separately and is not part of `0.13.0`.

This document does not implement that removal. It only announces it so
external clients have a fixed target to migrate against.

## You may already have leaked keys in logs

Moving a client to `Authorization: Bearer` does **not** retroactively remove
any token already written to a log before migration. Query-string credentials
in particular are commonly captured by infrastructure that was never designed
to treat a URL as a secret:

- GophishFR's own combined access-style logs;
- nginx and other reverse-proxy default request logs (which log the full
  request line, including the query string, by default);
- browser history and autocomplete, and shell history on any host where the
  URL was typed or scripted;
- support tickets, screen recordings, and diagnostics that copy a URL
  verbatim.

Form bodies and `Authorization` headers (raw or Bearer) are not written to
those same default log lines, so form-based credentials are lower default
logging risk than query credentials. Form `api_key` is still deprecated
alongside query `api_key`, because the goal is one converged, canonical
transport rather than ranking the legacy ones against each other.

**Operators must, for any key that was ever sent as a query parameter:**

1. migrate every client using that key to `Authorization: Bearer`;
2. rotate (reset) the key through existing API-key reset so the previously
   logged value stops working; see
   [API-key HMAC verifiers](API_KEY_VERIFIER.md#issuance-and-browser-behavior);
   and
3. review and remove historical copies of that key from access logs,
   reverse-proxy logs, and any copied diagnostics, under your own log
   retention and incident-handling policy.

GophishFR cannot automatically identify which external clients are affected,
and it cannot reach into or clean third-party or reverse-proxy log storage it
does not control. This document is guidance, not a tool: step 3 above is a
manual operator action against infrastructure this project does not manage.

This release does not stop query-string secrets from reaching access logs —
that only happens once a client migrates away from the query transport, and
removal of the query transport itself is deferred to `0.13.0`. Until a given
client migrates, its query-string key keeps appearing in logs exactly as
before.

## Why no runtime deprecation signal was added

This release deliberately adds **no** machine-readable deprecation signal to
API responses:

- **No RFC 9745 `Deprecation` header.** That header deprecates the resource
  identified by the request URI. `/api/*` endpoints themselves are not
  deprecated — only specific *credential transports* for those same endpoints
  are. Sending the header would misrepresent the resource as a whole.
- **No RFC 8594 `Sunset` header.** `Sunset` asserts a specific calendar date
  after which the resource stops being available. No official sunset
  **date** exists for raw `Authorization`, and query/form removal is tied to
  a version (`0.13.0`), not a calendar date, so there is no value to put in
  this header without inventing one.
- **No `Link: rel="deprecation"` header.** That relation points readers at a
  stable, published documentation URI. This repository has no stable,
  repository-hosted absolute URL for this page; publishing a `Link` header
  that points nowhere durable would be worse than omitting it.
- **No custom header and no server-side warning log entry.** Per-request
  signalling of this kind is easy to miss, easy to silently depend on, and
  would be the first transport-usage telemetry this project has ever added.
  Documentation and release notes are the deliberate migration channel for
  this deprecation instead.

No change in this release emits a token, a full request URL or query string, a
form body, an `Authorization` header value, a verifier, or any other
secret-derived value in a log, response, or header. That is unchanged from
before this release.

## Out of scope / follow-up work

This release does **not**:

- remove, reject, or change the status code for query, form, or raw
  `Authorization` requests;
- change the credential extractor, API auth middleware, verifier, keyring,
  token format, session, CSRF, RBAC, rate limiting, `events.details`, or any
  other runtime behaviour;
- add API-key rate limiting. Weak, legacy, low-entropy tokens accepted via any
  transport remain only as strong as the operator who issued them; bringing
  rate limiting to API-key authentication is tracked separately as the
  next, independent, HIGH-priority hardening item
  (`security/rate-limit-api-auth`) and is **not** implemented here;
- bump `VERSION`. This repository stays on its current version through this
  deprecation PR; `0.13.0` is a future breaking release that will update
  `VERSION` through the normal release process when the removal above ships.

The next breaking PR, `security/remove-legacy-api-key-transports`, performs
the `0.13.0` removal described above. This PR stops after merge and does not
start that PR or the rate-limiting PR.
