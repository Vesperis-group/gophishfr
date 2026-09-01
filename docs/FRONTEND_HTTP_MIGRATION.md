# Frontend HTTP migration

Audit date: 2026-09-01

The frontend historically exposes API wrappers from
`static/js/src/app/gophish.js`. All 47 wrappers returned a jQuery jqXHR through
`query()`, including 32 wrappers that explicitly set `async: false`. This
migration introduces a native Promise/fetch transport without changing the
legacy helper or any synchronous caller.

## Current scope

Four wrappers whose complete consumer set already handles both success and
failure now use `requestJSON()`:

- `api.users.get`
- `api.userId.get`
- `api.webhookId.ping`
- `api.send_test_email`

Their callers use the two-handler form of `Promise.then()`. This keeps every
rejection handled without adding a jqXHR compatibility shim. The remaining 43
wrappers still use `query()`: all 32 synchronous wrappers and 11 asynchronous
wrappers.

No endpoint, method, payload, authentication rule, or backend handler changed.
The direct form-encoded `$.post()` in `settings.js` is separate from `query()`
and is deliberately outside this migration.

## Wrapper inventory

`Sync classification` describes why a synchronous wrapper remains synchronous
for now:

- **required**: current ordering depends on the request completing before later
  code runs;
- **likely accidental**: the consumer is callback-driven and no immediate
  dependency was found, but changing timing requires its own regression tests;
- **unused/unknown**: no first-party caller was found, so runtime consumers
  cannot be ruled out safely.

| Wrapper | Request | Legacy mode | Current state | Reason |
| --- | --- | --- | --- | --- |
| `campaigns.get` | `GET /campaigns/` | sync | retained | unused/unknown |
| `campaigns.post` | `POST /campaigns/` | sync | retained | likely accidental |
| `campaigns.summary` | `GET /campaigns/summary` | sync | retained | likely accidental |
| `campaignId.get` | `GET /campaigns/:id` | async | deferred | shared caller without rejection handling |
| `campaignId.delete` | `DELETE /campaigns/:id` | sync | retained | likely accidental |
| `campaignId.results` | `GET /campaigns/:id/results` | async | deferred | shared caller without rejection handling |
| `campaignId.complete` | `GET /campaigns/:id/complete` | async | deferred | caller has no rejection handler |
| `campaignId.summary` | `GET /campaigns/:id/summary` | async | deferred | caller has no rejection handler |
| `groups.get` | `GET /groups/` | sync | retained | unused/unknown |
| `groups.post` | `POST /groups/` | sync | retained | likely accidental |
| `groups.summary` | `GET /groups/summary` | async | deferred | shared callers lack rejection handling |
| `groupId.get` | `GET /groups/:id` | sync | retained | likely accidental |
| `groupId.put` | `PUT /groups/:id` | sync | retained | likely accidental |
| `groupId.delete` | `DELETE /groups/:id` | sync | retained | likely accidental |
| `templates.get` | `GET /templates/` | sync | retained | required by campaign option ordering |
| `templates.post` | `POST /templates/` | sync | retained | likely accidental |
| `templateId.get` | `GET /templates/:id` | sync | retained | unused/unknown |
| `templateId.put` | `PUT /templates/:id` | sync | retained | likely accidental |
| `templateId.delete` | `DELETE /templates/:id` | sync | retained | likely accidental |
| `pages.get` | `GET /pages/` | sync | retained | required by campaign option ordering |
| `pages.post` | `POST /pages/` | sync | retained | likely accidental |
| `pageId.get` | `GET /pages/:id` | sync | retained | unused/unknown |
| `pageId.put` | `PUT /pages/:id` | sync | retained | likely accidental |
| `pageId.delete` | `DELETE /pages/:id` | sync | retained | likely accidental |
| `SMTP.get` | `GET /smtp/` | sync | retained | required by campaign option ordering |
| `SMTP.post` | `POST /smtp/` | sync | retained | likely accidental |
| `SMTPId.get` | `GET /smtp/:id` | sync | retained | unused/unknown |
| `SMTPId.put` | `PUT /smtp/:id` | sync | retained | likely accidental |
| `SMTPId.delete` | `DELETE /smtp/:id` | sync | retained | likely accidental |
| `IMAP.get` | `GET /imap/` | sync | retained | likely accidental |
| `IMAP.post` | `POST /imap/` | sync | retained | likely accidental |
| `IMAP.validate` | `POST /imap/validate` | async | deferred | validation and `always()` lifecycle |
| `users.get` | `GET /users/` | async | migrated | all callers handle rejection |
| `users.post` | `POST /users/` | async | deferred | submit family |
| `userId.get` | `GET /users/:id` | async | migrated | all callers handle rejection |
| `userId.put` | `PUT /users/:id` | async | deferred | submit family |
| `userId.delete` | `DELETE /users/:id` | async | deferred | destructive-action family |
| `webhooks.get` | `GET /webhooks/` | sync | retained | likely accidental |
| `webhooks.post` | `POST /webhooks/` | sync | retained | likely accidental |
| `webhookId.get` | `GET /webhooks/:id` | sync | retained | likely accidental |
| `webhookId.put` | `PUT /webhooks/:id` | async | deferred | submit family |
| `webhookId.delete` | `DELETE /webhooks/:id` | sync | retained | likely accidental |
| `webhookId.ping` | `POST /webhooks/:id/validate` | async | migrated | all callers handle rejection |
| `import_email` | `POST /import/email` | sync | retained | likely accidental |
| `clone_site` | `POST /import/site` | sync | retained | likely accidental |
| `send_test_email` | `POST /util/send_test_email` | async | migrated | all callers handle rejection |
| `reset` | `POST /reset` | async | deferred | settings/authentication control |

The synchronous classification totals 3 required, 24 likely accidental, and 5
unused/unknown wrappers. Those labels are migration inputs, not permission to
change them in bulk.

## Measured legacy contract

`tests/browser/http-transport.spec.ts` records the behavior of `query()` before
comparing the native transport:

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

The frontend smoke suite covers the four migrated consumer families, including
failed user loads, failed webhook pings, button restoration, and both test-email
error surfaces. Tests never send email or contact a non-loopback host.

## Follow-up families

Future changes should remain incremental:

1. Migrate the 11 deferred asynchronous wrappers by consumer family, first
   adding complete rejection and lifecycle handling.
2. Remove accidental synchronous XHR one workflow at a time, with ordering and
   UI-state regression coverage.
3. Delete or justify unused wrappers after checking external extension
   compatibility.
4. Remove `query()` and jQuery Ajax only after no caller depends on jqXHR or
   synchronous completion.
