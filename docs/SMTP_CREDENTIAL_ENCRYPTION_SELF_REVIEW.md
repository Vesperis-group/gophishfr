# SMTP credential encryption self-review

This Builder review records the security and behavior evidence for
`security/encrypt-smtp-credentials`. The immutable acceptance checklist remains
the source of truth; this document groups every criterion by its corresponding
control and test evidence.

## Security and behavior controls

| Area | Result | Evidence |
| --- | --- | --- |
| Foundation and keyring | PASS | SMTP uses the unchanged `internal/credentials` cipher, envelope parser, keyring loader, file-permission checks, and `GOPHISHFR_CREDENTIAL_KEYRING_FILE` bootstrap. One immutable cipher is injected into API, worker, mailer, IMAP, and SMTP paths. Dependency manifests and locks are unchanged. |
| AAD and rotation | PASS | SMTP context is exactly `smtp-password / smtp / password / user_id / id`. Mutable profile fields are excluded. Tests reject owner and same-owner record copying, verify retained old keys, and verify active-key writes. |
| Schema and state model | PASS | Paired SQLite/MySQL migrations retain `password`, add a 2048-byte ciphertext column, add no uniqueness constraint, and guard Down while ciphertext remains. Runtime rejects legacy, dual-populated, malformed, wrong-key, and tampered secret states while preserving empty and legacy-compatible empty-password profiles. PostgreSQL is rejected before mutation. |
| API isolation | PASS | Write DTOs accept string passwords and explicitly reject JSON `null`; response DTOs and model tags omit plaintext and ciphertext. Raw list, detail, create, update, campaign list/detail, and test-email responses are checked for plaintext, ciphertext, key IDs, envelope markers, and secret field names. Owner-scoped get, update, delete, and test-email paths are covered. |
| Frontend | PASS | Edit and copy leave password blank. Empty edits preserve ciphertext and non-empty edits rotate it. Browser coverage checks create with and without auth, edit/copy/delete, retry behavior, and absence from response objects, DOM, storage, and console. Only `sending_profiles.min.js` changed, and repeated clean builds were byte-identical. |
| Transactional writes | PASS | Create inserts empty secret columns, obtains the immutable ID, encrypts and verifies in memory, conditionally writes ciphertext by `(user_id,id)`, writes headers, and commits once. Update and delete scope all row/header mutations by owner and run atomically. Trigger-based tests prove profile, credential, and header rollback. |
| Send boundaries | PASS | Campaign and test-email paths carry ciphertext until `GetDialer`, where plaintext is local only. Existing-profile tests use an owner-authorized stored envelope; inline passwords are unexported, non-persistent values. No-auth skips AUTH. Wrong key, unknown key, tamper, and AAD mismatch fail before dialing. Tests use mocks or a loopback-only fake SMTP server. |
| Offline migration | PASS | The explicit CLI action validates backend/schema/identities/all rows before writes, verifies existing envelopes without changing them, migrates legacy plaintext transactionally, preserves no-secret rows, and is idempotent. Ambiguous state and interruption tests prove all-or-nothing behavior. Nullable legacy passwords are treated as empty. |
| Data rollback | PASS | The explicit rollback decrypts with exact AAD, restores legacy data in one transaction, clears ciphertext, preserves no-secret rows, rejects ambiguity/wrong keys/tamper, and is idempotent. It never invokes Goose; guarded single Down is tested separately and preserves profiles and headers for old binaries. |
| Docker | PASS | The built image uses the same external read-only keyring and contains no mounted keyring. The container test covers legacy IMAP/SMTP migration, idempotence, fresh encrypted and no-auth SMTP API writes, empty update preservation, rotation, response secrecy, local authenticated test-email delivery, tamper-before-network failure, wrong-key/tamper rollback failure, explicit rollback, and missing-keyring failure. |
| Operations | PASS | The operations guide documents the threat model, optional auth, breaking API contract, keyring lifecycle, backups, stopped writers, schema/data migration, verification, startup, rollback ordering, old-binary compatibility, PostgreSQL exclusion, key loss, Docker mounts, and future rotation. |
| Scope | PASS | No other secret type, crypto format, cryptographic primitive, dependency, or PostgreSQL schema was changed. IMAP changes are limited to migration-order regression tests and shared documentation/wiring needed for the newer SMTP migration. |

## Validation evidence

- `./scripts/verify.sh`: all functional gates pass after correcting the new
  test's checked-close lint finding.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and
  `go build ./...`: pass.
- Real MySQL 8.4: `TestMySQLDriverIntegration`,
  `TestMySQLIMAPCredentialLifecycle`, and
  `TestMySQLSMTPCredentialLifecycle` pass together against a clean database.
- Browser suite: 12/12 tests pass. An initial unrelated settings timing failure
  passed on the full rerun; the SMTP mutation test passed on both runs.
- Docker image build and `scripts/test-container-imap-keyring.sh`: pass.
- Credential parser/decrypt fuzz targets: each passed a bounded 10-second run.
- Frontend dependency audit: Yarn reports zero vulnerabilities; Retire.js
  reports zero findings.
- Dependency change check: `go.mod`, `go.sum`, `package.json`, and `yarn.lock`
  have no changes, so there are no new dependencies for a Dependabot delta
  review.

## Scanner review

| Scanner | Result | Classification |
| --- | --- | --- |
| `govulncheck` with the pinned Go 1.25.13 toolchain | PASS, zero reachable vulnerabilities | No regression. |
| `gosec ./...` | 14 findings | All pre-existing: configurable SMTP certificate verification, import-site certificate verification, legacy RSA TLS suites, existing XSS/open-redirect reports, and existing operator-selected file paths/permissions. The SMTP TLS finding is on unchanged behavior moved by this diff. No finding originates in new credential code. |
| Gitleaks | PASS, 875 commits and working tree, zero leaks | No regression. |
| actionlint | PASS | No regression. |
| zizmor | PASS, zero unsuppressed findings (two repository suppressions remain) | No regression. |
| Yarn audit | PASS, zero vulnerabilities | No regression. |
| Retire.js | PASS, zero findings | No regression. |

An exploratory scan using the machine's newer, unpinned Go 1.26.5 installation
reported seven standard-library advisories fixed in Go 1.26.6. The repository
build and CI are pinned to Go 1.25.13; the required pinned-toolchain
`govulncheck` run reports zero reachable vulnerabilities. No dependency or
toolchain change was made as part of this goal.
