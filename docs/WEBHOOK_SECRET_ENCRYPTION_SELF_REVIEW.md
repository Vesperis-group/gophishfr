# Webhook secret encryption self-review

This Builder review records the security and behavior evidence for
`security/encrypt-webhook-secrets`. The immutable acceptance checklist in
`.goals/encrypt-webhook-secrets/goal.md` remains the source of truth; this
document groups every criterion by its corresponding control and test
evidence.

## Security and behavior controls

| Area | Result | Evidence |
| --- | --- | --- |
| Foundation and keyring | PASS | Webhooks reuse the unchanged `internal/credentials` cipher, envelope parser, keyring loader, and `GOPHISHFR_CREDENTIAL_KEYRING_FILE` bootstrap -- the identical cipher instance IMAP and SMTP already use. No second keyring, parser, or environment variable was added. `go.mod`, `go.sum`, `package.json`, and `yarn.lock` are byte-identical to `HEAD`. |
| AAD and identity | PASS | Context is exactly `webhook-secret / webhooks / secret / (empty owner) / webhooks.id`. Name, URL, active state, and every other mutable field are excluded. Tests prove: copying ciphertext to a different webhook ID fails authentication; renaming/changing URL/active-state alongside a preserve leaves the ciphertext decryptable and unchanged; an old key remains usable while retained and a fresh rotation uses the newly active key. |
| Schema and state model | PASS | Paired SQLite/MySQL migrations retain `secret`, add a 2048-character `secret_ciphertext`, and add no owner/uniqueness/event-relation constraint. A conservative 1020-byte worst-case envelope bound (255 runes at the maximum 4 UTF-8 bytes) is derived and compile-time-checked against the column width. Runtime rejects legacy-only (not-yet-migrated), dual-populated, malformed, and oversized states while preserving no-secret and encrypted-valid states. PostgreSQL is rejected before mutation. SQLite Down is guarded on remaining ciphertext and preserves the autoincrement high-water mark after a deleted highest-ID row. |
| API isolation | PASS | Write DTOs use `json.RawMessage` to distinguish an absent/null "secret" (preserve) from an explicitly present empty string (clear) from a non-empty string (replace) -- a deliberate, documented, minimal deviation from literal legacy semantics, required because the legacy contract (full-row `Save`, clear-on-empty) cannot coexist with a write-only API without silently revoking every ordinary edit. Response DTOs never carry `Secret`/`SecretCiphertext`, and raw-body tests assert the absence of plaintext, ciphertext, envelope markers, and the key ID across list/single/create/update/validate responses. `PermissionModifySystem` is enforced on every webhook route; an unprivileged user is rejected before any handler logic runs and cannot read, preserve, replace, clear, or delete. |
| Frontend | PASS | The secret field is never prefilled and the browser never fetches plaintext or ciphertext. A dedicated, initially unchecked "remove the existing secret" control (hidden on create, where there is nothing yet to remove) is the only way to send an explicit `secret:""`; typing a replacement and checking removal are mutually exclusive in both directions. A real, non-mocked browser test creates with a secret, edits (confirms blank/never-refetched), performs an ordinary preserve (request omits "secret"), replaces, explicitly clears, creates without a secret, and asserts both secrets are absent from every response body, `localStorage`, `sessionStorage`, and console output. Two clean `corepack yarn build` runs are byte-identical; only `webhooks.min.js` changed. |
| Transactional writes | PASS | Create inserts the row with empty secret columns, waits for the generated ID, encrypts, guards the ciphertext write on the just-inserted empty state, and reads both columns back before committing -- a SQLite trigger that aborts on any plaintext `INSERT` proves the plaintext is never persisted, not even transiently. Update reads the existing row inside the same transaction, applies preserve/clear/replace, and guards the write on the exact secret state just read; a silent-mutation trigger proves a concurrently changed ciphertext is detected by the read-back verification and rolled back completely. A dedicated test guards against a MySQL-specific false positive: an ordinary no-op resubmission (nothing actually changed) reports 0 *changed* rows under MySQL's default semantics even though it matched, so the concurrency guard only fails closed on `>1`, relying on the read-back verification (not row-count) to catch a genuine concurrent divergence. Delete is transactional and idempotently rejects a repeat delete as not-found. |
| Send boundary | PASS | `models.ActiveWebhookEndpoints` is the only place a webhook secret exists as plaintext outside a single encrypt/decrypt call; it is never attached to a persisted, loggable Webhook value. A no-secret webhook never decrypts and reproduces the exact historical empty-key HMAC-SHA256 signature and header. A migrated/encrypted secret produces a signature independently cross-checked against a fresh `crypto/hmac` computation over the exact received bytes, proving it is identical to what legacy plaintext would have produced. A webhook whose ciphertext cannot be authenticated (unknown key or tampered ciphertext) is excluded before `webhook.Send` is ever reached: deterministic tests using local `httptest` servers assert zero outbound requests to the bad endpoints while the good endpoint is still delivered with the correct signature, both through `ActiveWebhookEndpoints` directly and through the real `AddEvent` campaign-delivery path with `SetWebhookCredentialCipher` wired exactly as `main.go` wires it. `POST /webhooks/{id}/validate` supports the same tri-state secret (stored/no-secret/inline-only-in-memory) and fails before any HTTP request on a credential failure, proven at the API layer with a keyring that never held the encrypting key. |
| Offline migration | PASS | The explicit `--migrate-webhook-secrets` action validates the backend, schema, unique positive IDs, and every existing row state before writing; a row with both legacy and ciphertext populated blocks the whole migration. Each legacy row is encrypted, immediately decrypted and compared in memory, written while retaining plaintext, read back byte-for-byte, then cleared and re-verified. It is idempotent (a second run updates zero rows) and an interruption trigger proves a mid-batch failure rolls back every row, leaving no partial ciphertext. |
| Data rollback | PASS | `--rollback-webhook-secrets` decrypts with the exact ID AAD, stages plaintext while retaining ciphertext, verifies both byte-for-byte, then clears and re-verifies ciphertext; it never invokes Goose. A wrong-key rollback fails closed. Schema Down is tested separately: it refuses while any ciphertext remains, and after a real data rollback it succeeds, preserves every row/ID/sequence, and leaves an old-binary-compatible schema with the restored plaintext readable. |
| SQLite / MySQL / PostgreSQL | PASS | Real SQLite lifecycle (legacy DB, schema Up, offline migration, runtime, idempotence, interrupted-migration rollback, data rollback, guarded Down, sequence preservation after a deleted max-ID row, fresh DB) and real MySQL 8.4 lifecycle (the same data/schema behavior plus a non-strict-SQL-mode boundary test using a 255-rune, 4-byte-per-rune secret) both pass. PostgreSQL is rejected by `ValidateWebhookCredentialBackend` before mutation; no PostgreSQL application schema was added or claimed. |
| IMAP / SMTP regression | PASS, with two required fixes | Adding a migration newer than SMTP's exposed that `TestSQLiteIMAPSchemaMigrationGuardsDuplicatesAndCiphertext`, `TestMySQLIMAPCredentialLifecycle`, and `TestMySQLSMTPCredentialLifecycle` each assumed a single blind `goose.Down` call would land on a specific earlier migration. That assumption breaks the moment any later migration exists; all three now use `goose.DownTo` against an explicit target version, which is correct regardless of how many later migrations exist (including a future one). No other IMAP/SMTP model, controller, or browser behavior changed; the fix is confined to test setup and is covered by the same tests it repairs. |
| Scope | PASS | No other secret family (`events.details`, user API keys, DSN/config, temporary admin passwords, TLS material), `internal/credentials`, or PostgreSQL schema was touched. Webhook headers do not exist in this schema and were not introduced. Webhook URL userinfo/query-token exposure is tracked as a separate backlog item (issue #56), not addressed here. |

## Validation evidence

- `./scripts/verify.sh`: all gates pass -- gofmt, golangci-lint (0 issues),
  `go mod verify`, `go vet`, `go build`, `go test`, `go test -race`, the
  frontend double-clean-build byte-identical check, action-pin verification,
  and `govulncheck` under the pinned `go1.25.13` toolchain.
- `go test ./...` and `go test -race ./...`: pass across every package,
  including the full new `models/webhook_credentials_test.go`,
  `models/webhook_test.go`, `models/webhook_delivery_test.go`, and
  `controllers/api/webhook_test.go` suites.
- Real MySQL 8.4.11 (local container, matching CI's `mysql:8.4.x` service):
  `TestMySQLDriverIntegration`, `TestMySQLIMAPCredentialLifecycle`,
  `TestMySQLSMTPCredentialLifecycle`,
  `TestMySQLSMTPCredentialStorageBoundsNonStrict`,
  `TestMySQLWebhookCredentialLifecycle`, and
  `TestMySQLWebhookCredentialStorageBoundsNonStrict` all pass together
  against a freshly reset database, run twice for reproducibility.
- Browser suite (`./scripts/test-browser.sh`, real SQLite + real credential
  cipher, Chromium): 13/13 pass, including the new
  `webhook-secret-lifecycle.spec.ts` and the pre-existing
  `frontend-smoke.spec.ts` webhook section, unmodified.
- Docker: `docker build` of the production image succeeds with the updated Go
  and frontend sources. `scripts/test-container-imap-keyring.sh` passes against
  that image using the one externally mounted, read-only shared keyring and
  verifies that no keyring is baked in. Its webhook-specific lifecycle covers
  legacy migration and idempotence, fresh secret/no-secret API writes,
  byte-identical preserve, replacement, explicit clear, deterministic
  HMAC-SHA256 delivery (including the historical empty-key signature),
  copied-AAD/tamper/wrong-key failures with exactly zero HTTP requests,
  wrong-key rollback refusal, successful rollback, and missing-keyring
  refusal. The existing IMAP/SMTP container lifecycle remains covered by the
  same run.
- Frontend dependency audit: `corepack yarn audit` reports 0 vulnerabilities
  across 113 audited packages. Retire.js reports 0 findings against the
  shipped `static/` assets; an unrelated pre-existing finding
  (`jquery 2.1.1`) appears only inside a transitive dev dependency's own test
  fixture (`node_modules/hammerjs/tests/unit/assets/jquery.min.js`), which is
  never shipped and was not introduced by this change.
- Dependency change check: `go.mod`, `go.sum`, `package.json`, and
  `yarn.lock` have no changes, so there is no dependency delta to review.

## Scanner review

| Scanner | Result | Classification |
| --- | --- | --- |
| `govulncheck` with the pinned Go 1.25.13 toolchain | PASS, 0 reachable vulnerabilities | No regression. |
| `gosec ./...` | 14 findings | All pre-existing: import-site and SMTP TLS certificate verification, a legacy RSA TLS cipher suite, existing XSS/open-redirect taint findings, and existing operator-selected file paths/permissions. None originate in `models/webhook.go`, `models/webhook_credentials.go`, or `controllers/api/webhook.go`. |
| Gitleaks | PASS, 881 commits and working tree, 0 leaks | No regression. |
| actionlint | PASS | No regression; no workflow file was touched. |
| zizmor | PASS, 0 unsuppressed findings (2 pre-existing repository suppressions remain) | No regression; no workflow file was touched. |
| Yarn audit | PASS, 0 vulnerabilities | No regression. |
| Retire.js (shipped `static/` assets) | PASS, 0 findings | No regression. |

`gosec`, Gitleaks, Yarn audit, and Retire.js are not wired as discrete CI
jobs today (CI's own security coverage is `actionlint`, `zizmor`,
`golangci-lint`, and `govulncheck`); this mirrors the SMTP goal's own honest
accounting of that gap rather than introducing a new one. All of the above
were still run locally against the full working tree before this report.

## Backlog recorded

- Issue #56, "Audit and redact userinfo/query-string tokens embedded in
  webhook URLs" -- `webhooks.url` may itself carry sensitive material and is
  explicitly out of scope for this goal.