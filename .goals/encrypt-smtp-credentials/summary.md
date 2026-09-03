# SMTP Credential Encryption Goal Summary

## Outcome

The goal passed independent inspection in one Builder/Inspector iteration.
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
- Empty or absent updates preserve an existing ciphertext byte-for-byte;
  non-empty updates rotate it; unauthenticated profiles remain valid with both
  secret columns empty.
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

## Inspector Findings

No code-level defect remained. The Inspector confirmed the intentional API
breaking change and security-safe replacement of the legacy full-overwrite
password behavior. Operational requirements remain deliberate: operators must
retain the external keyring and backup, stop writers during offline transforms,
and complete data rollback before schema Down.

## Recommendations

- Preserve signed iteration commits rather than squashing them.
- Require independent cryptographic/security review and fully green CI before
  merge.
- Keep bulk SMTP key rotation, PostgreSQL schema support, clear-password UX, and
  all non-SMTP secret types in separate future changes.
