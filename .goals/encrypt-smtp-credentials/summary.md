# SMTP Credential Encryption Goal Summary

## Outcome

The goal passed independent inspection and a security-specialist re-review after
two Builder iterations.
SMTP sending-profile passwords are now write-only at the HTTP/browser boundary,
encrypted at rest with the existing AES-256-GCM credential foundation, bound to
the authenticated owner and immutable profile ID, and decrypted only at the SMTP
dialer boundary.

## Acceptance Criteria

- Existing `internal/credentials` crypto and the shared external keyring are
  reused without dependency or envelope-format changes.
- SQLite and MySQL retain the legacy password column for explicit rollback and
  add a guarded ciphertext column; PostgreSQL migration is rejected.
- Create obtains the generated profile ID inside one transaction before
  encryption, while profile/header mutations are atomic and owner-scoped.
- Empty or absent updates preserve an existing ciphertext byte-for-byte only
  when interface, host/port, username, and TLS verification policy are
  unchanged. Routing changes require a non-empty replacement, while
  unauthenticated profiles remain freely editable with both secret columns
  empty.
- Stored-password test email ignores request-controlled SMTP context and uses
  the complete owner-authorized stored profile. An explicit non-empty password
  uses the request context only in memory.
- Plaintext is limited to 255 valid UTF-8 bytes and v1 ciphertext to its derived
  464-byte maximum within the 2048-character column. Create, rotate, migration,
  and rollback re-read and compare secret columns byte-for-byte in the same
  transaction.
- Migration stages verified ciphertext before clearing plaintext; rollback
  stages and verifies full plaintext before clearing the only ciphertext copy.
- SMTP, campaign, and test-email API responses expose neither plaintext nor
  ciphertext metadata, and the frontend never prefills stored passwords.
- Campaign and test-email sends decrypt only at the use boundary and fail before
  network activity on missing, malformed, tampered, unknown-key, or wrong-AAD
  ciphertext.
- Explicit offline migration and rollback are transactional, idempotent,
  fail-closed, and documented with backup and stopped-writer requirements.
- SQLite/MySQL lifecycle, API, browser, campaign/test-email, Docker/keyring,
  credential-foundation, IMAP regression, fuzz, and scanner coverage were added
  or executed without dependency changes.

## Iteration History

1. Builder implemented the complete schema, model, API, frontend, runtime,
   migration/rollback, Docker, test, CI, and documentation changes in commit
   `2710482ca8f7a98d645f070d46069f64c838a110`.
2. Inspector independently checked every immutable criterion and returned PASS
   with no product-code findings in commit
   `15a73468cbd213dbecd0ef192ca58b1c5cb3586a`.
3. A security specialist identified credential-context redirection and
   non-strict MySQL truncation risks.
4. Builder iteration 2 bound preserved credentials to their stored context,
   enforced byte-safe limits and staged verification, and added cross-layer
   regression coverage in commit
   `63c00cc31c649821eaae2b9021ba5162c2c76def`.
5. The Goal Inspector returned PASS again in commit
   `fddb6726cba274e47bcb853f3af79d07119f40f6`, and the independent security
   specialist re-review also returned PASS.

## Inspector Findings

Both security findings are corrected. PUT rejects preserved-password changes to
host, username, TLS policy, or equivalent routing fields; blank/absent
stored-profile test-email requests use the complete stored profile. Explicit
replacement passwords still permit context changes. UTF-8 byte bounds,
conservative envelope sizing, staged migration/rollback, and transactional
readback checks prevent silent storage truncation from committing.
The Goal Inspector and the independent security specialist both verified these
corrections with no remaining high-confidence security finding.

## Iteration 2 validation

- `./scripts/verify.sh`: PASS with Go 1.25.13 and Node 24.19.0.
- Full unit, race, vet, build, format, module verification, and lint gates: PASS.
- Browser suite: PASS.
- Real MySQL 8.4 driver, IMAP, SMTP lifecycle, and SMTP non-strict storage-bound
  tests: PASS.
- Docker build and credential lifecycle, including redirect rejection and
  stored-context test-email delivery: PASS.
- Both credential fuzz targets completed bounded 10-second runs.
- `govulncheck`, Gitleaks, actionlint, zizmor, Yarn audit, and Retire.js: PASS.
- Gosec reports the same 14 documented pre-existing findings and no finding in
  the new credential-bound/storage-verification code.
- Dependency manifests, lockfiles, and generated frontend assets are unchanged.

## Recommendations

- Preserve signed iteration commits rather than squashing them.
- Require independent cryptographic/security review and fully green CI before
  merge.
- Keep bulk SMTP key rotation, PostgreSQL schema support, clear-password UX, and
  all non-SMTP secret types in separate future changes.
