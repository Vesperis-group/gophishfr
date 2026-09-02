# Frontend HTTP migration

Audit date: 2026-09-01

The frontend historically exposes API wrappers from
`static/js/src/app/gophish.js`. All 47 wrappers returned a jQuery jqXHR through
`query()`, including 32 wrappers that explicitly set `async: false`. This
migration introduces a native Promise/fetch transport without changing the
legacy helper or any synchronous caller.

## Current scope

Sixteen wrappers whose complete consumer set has a measured native-Promise
contract now use `requestJSON()`:

- `api.users.get`
- `api.userId.get`
- `api.webhookId.ping`
- `api.send_test_email`
- `api.campaignId.get`
- `api.campaignId.results`
- `api.campaignId.complete`
- `api.groups.summary`
- `api.IMAP.validate`
- `api.users.post`
- `api.userId.put`
- `api.userId.delete`
- `api.webhookId.put`
- `api.reset`
- `api.webhooks.get`
- `api.webhookId.get`

Their callers use the two-handler form of `Promise.then()`. This keeps every
rejection handled without adding a jqXHR compatibility shim. IMAP validation
uses `finally()` for its unconditional control cleanup. The remaining 31
wrappers still use `query()`: 30 synchronous wrappers and the unused
asynchronous `campaignId.summary` wrapper.

This increment migrated 10 of the 11 asynchronous wrappers that remained after
the first native-transport change. `campaignId.summary` has no first-party
consumer, so changing its externally observable jqXHR return is not justified
without usage evidence.

The first synchronous-XHR increment migrated the read-only `webhooks.get` and
`webhookId.get` wrappers. Their consumers remain void callback boundaries, while
tests delay each response to prove that table and modal state are not consumed
before the native Promise settles. Superseded responses cannot overwrite newer
state, and edit controls remain disabled until the current detail request
succeeds.

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
| `campaignId.get` | `GET /campaigns/:id` | async | migrated | copy and report flows handle rejection |
| `campaignId.delete` | `DELETE /campaigns/:id` | sync | retained | likely accidental |
| `campaignId.results` | `GET /campaigns/:id/results` | async | migrated | load and poll failures are handled; refresh UI is restored |
| `campaignId.complete` | `GET /campaigns/:id/complete` | async | migrated | SweetAlert consumes the native Promise directly |
| `campaignId.summary` | `GET /campaigns/:id/summary` | async | retained | unused/unknown; jqXHR return may be externally consumed |
| `groups.get` | `GET /groups/` | sync | retained | unused/unknown |
| `groups.post` | `POST /groups/` | sync | retained | likely accidental |
| `groups.summary` | `GET /groups/summary` | async | migrated | group list and campaign setup both handle rejection |
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
| `IMAP.validate` | `POST /imap/validate` | async | migrated | `finally()` preserves unconditional control cleanup |
| `users.get` | `GET /users/` | async | migrated | all callers handle rejection |
| `users.post` | `POST /users/` | async | migrated | submit success and rejection are covered |
| `userId.get` | `GET /users/:id` | async | migrated | all callers handle rejection |
| `userId.put` | `PUT /users/:id` | async | migrated | submit success and rejection are covered |
| `userId.delete` | `DELETE /users/:id` | async | migrated | SweetAlert consumes the native Promise directly |
| `webhooks.get` | `GET /webhooks/` | sync | migrated | list state waits for the native Promise |
| `webhooks.post` | `POST /webhooks/` | sync | retained | likely accidental |
| `webhookId.get` | `GET /webhooks/:id` | sync | migrated | edit state waits for the native Promise |
| `webhookId.put` | `PUT /webhooks/:id` | async | migrated | submit success and rejection are covered |
| `webhookId.delete` | `DELETE /webhooks/:id` | sync | retained | likely accidental |
| `webhookId.ping` | `POST /webhooks/:id/validate` | async | migrated | all callers handle rejection |
| `import_email` | `POST /import/email` | sync | retained | likely accidental |
| `clone_site` | `POST /import/site` | sync | retained | likely accidental |
| `send_test_email` | `POST /util/send_test_email` | async | migrated | all callers handle rejection |
| `reset` | `POST /reset` | async | migrated | API key update and server error paths are covered |

The synchronous classification totals 3 required, 22 likely accidental, and 5
unused/unknown wrappers. Those labels are migration inputs, not permission to
change them in bulk.

### Synchronous families

The 30 retained synchronous wrappers divide into bounded workflow families:

| Family | Wrappers | Count | Main migration risk |
| --- | --- | ---: | --- |
| Campaign flow | `campaigns.get`, `campaigns.post`, `campaigns.summary`, `campaignId.delete` | 4 | launch and destructive-action timing |
| Group CRUD | `groups.get`, `groups.post`, `groupId.get`, `groupId.put`, `groupId.delete` | 5 | modal and DataTable refresh ordering |
| Template/import | `templates.get`, `templates.post`, `templateId.get`, `templateId.put`, `templateId.delete`, `import_email` | 6 | untrusted HTML and import sequencing |
| Landing page/clone | `pages.get`, `pages.post`, `pageId.get`, `pageId.put`, `pageId.delete`, `clone_site` | 6 | untrusted HTML and remote clone flow |
| Sending profile | `SMTP.get`, `SMTP.post`, `SMTPId.get`, `SMTPId.put`, `SMTPId.delete` | 5 | credential-bearing forms and campaign option ordering |
| IMAP settings | `IMAP.get`, `IMAP.post` | 2 | credential-bearing form and chained lifecycle callbacks |
| Webhook writes | `webhooks.post`, `webhookId.delete` | 2 | list refresh and destructive-action timing |

The next recommended synchronous follow-up is the write pair `webhooks.post`
and `webhookId.delete`. Their modal and SweetAlert boundaries already isolate
the asynchronous work, but creation refresh and destructive-action timing need
their own measured regression tests.

## Measured legacy contract

`tests/browser/http-transport.spec.ts` records the behavior of `query()` before
comparing the native transport. `tests/browser/async-api-wrappers.spec.ts`
additionally records every one of the 11 formerly deferred wrappers before the
consumer migration, then applies the same method, path, body, success, and
failure assertions afterward:

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
test-email error surfaces. The webhook-read coverage includes legacy/native HTTP
parity, list and edit failures, repeated actions, delayed-response ordering, and
fixed UI messages. The wrapper suite also removes `window.$` and
`window.jQuery` while invoking migrated wrappers.
Tests never send email or contact a non-loopback host.

## Follow-up families

Future changes should remain incremental:

1. Establish whether `campaignId.summary` has an external runtime consumer
   before changing or removing its jqXHR contract.
2. Migrate the synchronous webhook writes as a separately tested workflow.
3. Remove other accidental synchronous XHR one workflow at a time, with
   ordering and UI-state regression coverage.
4. Delete or justify unused wrappers after checking external extension
   compatibility.
5. Remove `query()` and jQuery Ajax only after no caller depends on jqXHR or
   synchronous completion.
