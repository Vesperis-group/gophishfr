# Frontend HTTP migration

Audit date: 2026-09-02

The frontend historically exposes API wrappers from
`static/js/src/app/gophish.js`. All 47 wrappers returned a jQuery jqXHR through
`query()`, including 32 wrappers that explicitly set `async: false`. This
migration introduced a native Promise/fetch transport and incrementally moved
every retained wrapper to it. The legacy helper remains present for a separate
removal change, but has no runtime caller.

`window.api` and its `api.*` properties are internal frontend implementation
details, not a public API or supported extension point. This does not change the
separate HTTP REST API exposed under `/api/`.

## Current scope

All 41 retained wrappers now use `requestJSON()` with measured native-Promise
contracts:

- `api.users.get`
- `api.userId.get`
- `api.webhookId.ping`
- `api.send_test_email`
- `api.campaignId.get`
- `api.campaignId.results`
- `api.campaignId.complete`
- `api.groups.summary`
- `api.groups.post`
- `api.groupId.get`
- `api.groupId.put`
- `api.groupId.delete`
- `api.templates.get`
- `api.IMAP.validate`
- `api.users.post`
- `api.userId.put`
- `api.userId.delete`
- `api.webhookId.put`
- `api.reset`
- `api.webhooks.get`
- `api.webhookId.get`
- `api.webhooks.post`
- `api.webhookId.delete`
- `api.templates.post`
- `api.templateId.put`
- `api.templateId.delete`
- `api.import_email`
- `api.pages.get`
- `api.pages.post`
- `api.pageId.put`
- `api.pageId.delete`
- `api.clone_site`
- `api.SMTP.get`
- `api.SMTP.post`
- `api.SMTPId.put`
- `api.SMTPId.delete`
- `api.campaigns.post`
- `api.campaigns.summary`
- `api.campaignId.delete`
- `api.IMAP.get`
- `api.IMAP.post`

Their callers use native Promise settlement handlers without a jqXHR
compatibility shim. IMAP validation uses `finally()` for its unconditional
control cleanup. `query()` and its jQuery Ajax implementation remain unchanged,
but no retained wrapper calls them.

Six unused internal wrappers were removed rather than migrated:
`campaigns.get`, `campaignId.summary`, `groups.get`, `templateId.get`,
`pageId.get`, and `SMTPId.get`.

The first synchronous-XHR increment migrated the read-only `webhooks.get` and
`webhookId.get` wrappers. Their consumers remain void callback boundaries, while
tests delay each response to prove that table and modal state are not consumed
before the native Promise settles. Superseded responses cannot overwrite newer
state, and edit controls remain disabled until the current detail request
succeeds.

The webhook write increment migrated `webhooks.post` and `webhookId.delete`.
Creation keeps the modal open and retryable on failure, while deletion returns
the native Promise to SweetAlert and preserves confirmation, cancellation,
validation, and success-reload behavior. Duplicate submissions share or suppress
the in-flight write instead of creating duplicate records or deletions.

The group CRUD increment migrated `groups.post`, `groupId.get`, `groupId.put`,
and `groupId.delete`. Group create/update payloads continue to serialize every
DataTables target row in the table's current order. Modal request contexts reject
stale edit and save settlements, while SweetAlert consumes one native deletion
Promise per confirmation attempt.

The template mutation increment migrated `templates.post`, `templateId.put`,
`templateId.delete`, and `import_email`. The later option-loader increment
migrated `templates.get`.
Create, update, and import requests keep their modal data retryable on failure;
duplicate writes are suppressed; and settlements from a closed modal cannot
overwrite a newer modal. The exact CodeMirror HTML source remains the request
and import source of truth, and imported previews remain confined to the
existing sandboxed iframe. Edit and delete actions capture stable template IDs
rather than mutable table indexes. Pending attachment reads temporarily block
submission and cannot write into a later modal.

The landing page mutation increment migrated `pages.post`, `pageId.put`,
`pageId.delete`, and `clone_site`. The later option-loader increment migrated
`pages.get`. Create, update, and site
clone requests suppress duplicate actions and keep their current modal retryable.
Settlements from a closed modal cannot overwrite a newer modal, while edit and
delete actions capture stable page IDs instead of mutable table indexes. Exact
CodeMirror source remains the save and clone destination, and cloned untrusted
HTML reaches rendered DOM only through the existing sanitized, sandboxed preview.

The sending profile mutation increment migrated `SMTP.post`, `SMTPId.put`, and
`SMTPId.delete`. The later option-loader increment migrated `SMTP.get`. Create
and update payloads preserve
explicit empty credentials, false booleans, and complete header replacement.
Duplicate writes are suppressed, stale settlements cannot overwrite a newer
modal, and destructive actions capture stable profile IDs. An omitted
credential in the canonical response is rendered as an empty form field rather
than the literal string `"undefined"`.

The campaign flow increment migrated `campaigns.post`, `campaigns.summary`, and
`campaignId.delete`. Launch preserves the exact form payload and date
serialization while suppressing duplicate submissions and restoring the modal
after a handled failure. Both summary pages keep their distinct loading/error
behavior while remaining responsive during a delayed response. All three
deletion callers preserve their existing confirmation and success navigation,
capture stable campaign IDs, suppress duplicate deletions, and handle native
rejections without leaking them.

The IMAP settings increment migrated `IMAP.get` and `IMAP.post`. Repeated loads
cannot let an older response overwrite current settings, while duplicate saves
are suppressed until the current request settles. The exact credential-bearing
payload, success-feedback/reload order, retry behavior, and unconditional scroll
cleanup remain covered with synthetic fixtures.

That migration did not change an endpoint, method, payload, authentication rule,
or backend handler. A later security hardening made the stored IMAP password
write-only: `GET /api/imap/` omits the `password` property and every accepted
frontend load clears the password input. `POST /api/imap/` still accepts a
non-empty password to create or rotate the secret; an empty or omitted password
preserves the authenticated user's existing secret, but cannot create a new
configuration, and JSON `null` is rejected. The direct form-encoded `$.post()`
in `settings.js` was subsequently replaced by a local native `fetch` path. It
keeps `POST /settings`, same-origin session credentials,
`application/x-www-form-urlencoded`, DOM-order successful controls, explicit
empty values, and content-type-based response parsing. `FormData` supplies the
browser's successful-control set, file values are excluded to match
`serialize()`, and the final encoding preserves jQuery's `+` representation for
spaces. The unused `X-Requested-With` library-identification header is not
retained; no backend or middleware reads it.

The final hard option-loader increment migrated `templates.get`, `pages.get`,
and `SMTP.get`. `setupOptions()` now returns a Promise while preserving the
measured templates, pages, and SMTP request order. New and copied campaigns keep
launch controls disabled until the current option workflow settles; copy waits
for those options before loading campaign detail. Superseded workflows cannot
overwrite the latest action, closing the modal invalidates pending work,
hard-loader failures stop continuation and remain retryable, and group-summary
errors retain their independent historical behavior. The same wrappers also
drive their three list pages through native Promise handlers that ignore
superseded responses.

This removes the last runtime caller of `query()` and the last executable
first-party jQuery use outside its retained implementation. Removing `query()`
itself remains intentionally out of scope for this increment.

## Wrapper inventory

`Legacy mode` records the original jqXHR mode. Earlier audit classifications
used these categories:

- **required**: current ordering depends on the request completing before later
  code runs;
- **likely accidental**: the consumer is callback-driven and no immediate
  dependency was found, but changing timing requires its own regression tests;
- **unused/unknown**: no first-party caller was found, so runtime consumers
  cannot be ruled out safely.

| Wrapper | Request | Legacy mode | Current state | Reason |
| --- | --- | --- | --- | --- |
| `campaigns.get` | `GET /campaigns/` | sync | removed | unused internal wrapper; REST endpoint unchanged |
| `campaigns.post` | `POST /campaigns/` | sync | migrated | launch payload and modal state wait for one native Promise |
| `campaigns.summary` | `GET /campaigns/summary` | sync | migrated | both list consumers wait for the native Promise |
| `campaignId.get` | `GET /campaigns/:id` | async | migrated | copy and report flows handle rejection |
| `campaignId.delete` | `DELETE /campaigns/:id` | sync | migrated | all three callers capture stable IDs and suppress duplicate deletion |
| `campaignId.results` | `GET /campaigns/:id/results` | async | migrated | load and poll failures are handled; refresh UI is restored |
| `campaignId.complete` | `GET /campaigns/:id/complete` | async | migrated | SweetAlert consumes the native Promise directly |
| `campaignId.summary` | `GET /campaigns/:id/summary` | async | removed | unused internal wrapper; REST endpoint unchanged |
| `groups.get` | `GET /groups/` | sync | removed | unused internal wrapper; REST endpoint unchanged |
| `groups.post` | `POST /groups/` | sync | migrated | modal stays retryable and refreshes once after success |
| `groups.summary` | `GET /groups/summary` | async | migrated | group list and campaign setup both handle rejection |
| `groupId.get` | `GET /groups/:id` | sync | migrated | edit state waits for the current native Promise |
| `groupId.put` | `PUT /groups/:id` | sync | migrated | modal stays retryable and refreshes once after success |
| `groupId.delete` | `DELETE /groups/:id` | sync | migrated | SweetAlert consumes one native Promise per confirmation attempt |
| `templates.get` | `GET /templates/` | sync | migrated | explicit Promise chain preserves campaign option ordering |
| `templates.post` | `POST /templates/` | sync | migrated | modal stays retryable and suppresses duplicate writes |
| `templateId.get` | `GET /templates/:id` | sync | removed | unused internal wrapper; REST endpoint unchanged |
| `templateId.put` | `PUT /templates/:id` | sync | migrated | modal stays retryable and rejects stale settlements |
| `templateId.delete` | `DELETE /templates/:id` | sync | migrated | SweetAlert consumes one native Promise per confirmation attempt |
| `pages.get` | `GET /pages/` | sync | migrated | explicit Promise chain preserves campaign option ordering |
| `pages.post` | `POST /pages/` | sync | migrated | modal stays retryable and suppresses duplicate writes |
| `pageId.get` | `GET /pages/:id` | sync | removed | unused internal wrapper; REST endpoint unchanged |
| `pageId.put` | `PUT /pages/:id` | sync | migrated | stable page ID and modal context reject stale settlements |
| `pageId.delete` | `DELETE /pages/:id` | sync | migrated | SweetAlert consumes one native Promise per confirmation attempt |
| `SMTP.get` | `GET /smtp/` | sync | migrated | explicit Promise chain preserves campaign option ordering |
| `SMTP.post` | `POST /smtp/` | sync | migrated | explicit credential and header payloads remain stable across native settlement |
| `SMTPId.get` | `GET /smtp/:id` | sync | removed | unused internal wrapper; REST endpoint unchanged |
| `SMTPId.put` | `PUT /smtp/:id` | sync | migrated | stable profile ID and modal context reject stale settlements |
| `SMTPId.delete` | `DELETE /smtp/:id` | sync | migrated | SweetAlert consumes one native deletion Promise per confirmation attempt |
| `IMAP.get` | `GET /imap/` | sync | migrated | settings form ignores superseded native responses; stored password is omitted |
| `IMAP.post` | `POST /imap/` | sync | migrated | empty/omitted password preserves an existing authenticated-user secret |
| `IMAP.validate` | `POST /imap/validate` | async | migrated | `finally()` preserves unconditional control cleanup |
| `users.get` | `GET /users/` | async | migrated | all callers handle rejection |
| `users.post` | `POST /users/` | async | migrated | submit success and rejection are covered |
| `userId.get` | `GET /users/:id` | async | migrated | all callers handle rejection |
| `userId.put` | `PUT /users/:id` | async | migrated | submit success and rejection are covered |
| `userId.delete` | `DELETE /users/:id` | async | migrated | SweetAlert consumes the native Promise directly |
| `webhooks.get` | `GET /webhooks/` | sync | migrated | list state waits for the native Promise |
| `webhooks.post` | `POST /webhooks/` | sync | migrated | modal stays retryable and refreshes once after success |
| `webhookId.get` | `GET /webhooks/:id` | sync | migrated | edit state waits for the native Promise |
| `webhookId.put` | `PUT /webhooks/:id` | async | migrated | submit success and rejection are covered |
| `webhookId.delete` | `DELETE /webhooks/:id` | sync | migrated | SweetAlert consumes one native Promise per confirmation attempt |
| `webhookId.ping` | `POST /webhooks/:id/validate` | async | migrated | all callers handle rejection |
| `import_email` | `POST /import/email` | sync | migrated | import state waits for the current native Promise |
| `clone_site` | `POST /import/site` | sync | migrated | untrusted HTML waits for the current sandboxed-preview context |
| `send_test_email` | `POST /util/send_test_email` | async | migrated | all callers handle rejection |
| `reset` | `POST /reset` | async | migrated | API key update and server error paths are covered |

The synchronous classification now totals zero retained wrappers.

### Synchronous families

No retained wrapper uses synchronous XHR. The legacy `query()` implementation
remains available but has no runtime caller.

## Measured legacy contract

`tests/browser/http-transport.spec.ts` records the behavior of `query()` before
comparing the native transport. `tests/browser/async-api-wrappers.spec.ts`
records the request and settlement contracts of the native wrappers that
retained production consumers:

| Case | Legacy jqXHR behavior |
| --- | --- |
| GET with `{}` | appends the literal query string `?{}` |
| POST object | sends `JSON.stringify(data)` |
| POST `null` | sends the literal body `null` |
| Undefined data | omits the body |
| HTTP 204 | resolves with `undefined` and `nocontent` |
| Empty HTTP 200 body | rejects with `parsererror` |
| Invalid JSON | rejects with `parsererror` |
| HTTP 400/404/500 JSON | rejects and exposes `responseJSON` |
| Network failure | rejects with status `0` and `error` |

The native helper preserves the API URL, serialization, 204, parse-error,
HTTP-error, and network-error behavior relevant to migrated callers. It sends:

- `Authorization: Bearer ...`;
- `Accept: application/json, text/javascript, */*; q=0.01`;
- `Content-Type: application/json`;
- same-origin cookies through `credentials: "same-origin"`.

It intentionally omits jQuery's `X-Requested-With` header. No backend route
reads that header, and adding it would preserve library identification rather
than an application contract.

`APIRequestError` exposes only `status`, `statusText`, parsed `data`,
`responseText`, and `cause`. `requestErrorMessage()` extracts a validated
server message or returns a generic message. Neither helper logs response data,
credentials, or authorization values.

## Test boundary

The browser contract suite verifies the legacy and native transports against
local synthetic responses, including success, 204, empty and invalid JSON,
HTTP failures, and an aborted network request. It also temporarily removes
`window.$` and `window.jQuery` before calling the native helper, proving that
the migrated path has no jQuery runtime dependency.

The frontend smoke suite covers all migrated consumer families, including
campaign refresh cleanup, report lookup failure, group option failure,
completion, user update and deletion, webhook update, API-key reset, IMAP
success/failure cleanup, failed user loads, failed webhook pings, and both
test-email error surfaces. Webhook coverage includes legacy/native HTTP parity,
list, create, edit, and delete failures, cancellation, repeated actions,
delayed-response ordering, and fixed UI messages. The wrapper suite also removes
`window.$` and `window.jQuery` while invoking all four webhook CRUD wrappers.
Template mutation coverage records the exact create/update payloads, attachment
content, CodeMirror HTML, email-import source and result, sandboxed preview, and
DELETE contract. It also proves retry behavior, duplicate-write suppression,
closed-modal settlement isolation, and operation of all four migrated wrappers
with `window.$` and `window.jQuery` removed.
Landing page mutation coverage applies the same transport, retry, duplicate,
stale-modal, stable-ID, and DELETE assertions to create/update/delete and site
clone. It additionally proves exact CodeMirror request/response strings and that
cloned scripts, event handlers, forms, links, and external resources remain
confined by the sanitized sandboxed preview without contacting a remote host.
Sending profile mutation coverage records exact create/update/delete transport
and payloads, including filled, unchanged, and empty credentials, empty and
populated headers, and a false certificate-error flag. It proves failure retry,
duplicate suppression, stable IDs, stale-modal isolation, canonical same-profile
refresh, and operation without `window.$` or `window.jQuery`.
Campaign flow coverage records exact launch/summary/delete transport, HTTP and
network failures, the complete two-group launch payload, canonical template
attachments, and stored date instants. It also proves duplicate suppression,
stable deletion IDs, handled retryable failures, page-specific loading behavior,
responsiveness under delayed summaries, and operation without `jQuery.ajax`.
Hard option-loader coverage records exact GET contracts, the explicit
templates-pages-SMTP order, New and Copy campaign state, preselection,
per-loader failure and retry, delayed list rendering, DataTables stability, and
superseded list and Copy isolation, including modal closure during a pending
Copy. It also invokes all three wrappers and the complete Copy workflow with
both jQuery globals removed.
The account-settings form contract inventories every real control and compares
its legacy and native form body, order, encoding, session cookie, disabled and
empty-field behavior, HTTP 400/403/500 handling, retry, duplicate submissions,
redirect handling, and operation with both jQuery globals removed.
Tests never send email or contact a non-loopback host.

## Follow-up families

Future changes should remain incremental. Remove the now-unused `query()` and
jQuery Ajax implementation only in a dedicated follow-up that also audits the
intentionally retained legacy/dead-code boundaries.
