# Webhook Secret Encryption Goal Summary

## Outcome

The goal passed independent inspection, security review, and final code review
after two Builder iterations. Webhook authentication secrets are write-only at
the API/browser boundary, encrypted at rest with the existing shared
AES-256-GCM credential foundation, bound to immutable webhook IDs, and decrypted
only immediately before the unchanged HMAC-SHA256 operation.

## Acceptance Criteria Achieved

- `internal/credentials` and `GOPHISHFR_CREDENTIAL_KEYRING_FILE` are reused
  unchanged across IMAP, SMTP, and webhooks.
- AAD is bound to `webhook-secret / webhooks / secret / <empty owner> / id`;
  copying ciphertext between webhook records fails authentication.
- SQLite and MySQL retain nullable legacy `secret` for explicit rollback and add
  guarded ciphertext storage without changing global-resource ownership.
- Create obtains the generated ID transactionally without ever storing
  plaintext. All secret writes use exact in-transaction readback before clearing
  the prior recoverable copy.
- Create absent/null/empty yields a no-secret webhook. Update absent/null
  preserves ciphertext byte-for-byte, explicit empty clears, and non-empty
  replaces with the active key.
- The frontend never prefills a secret, omits it for ordinary edits, and requires
  explicit intent to replace or revoke.
- GET, POST, PUT, validate, and error responses expose no plaintext, ciphertext,
  key ID, envelope marker, mask, or secret-presence oracle.
- Historical HMAC-SHA256 bytes/header remain unchanged, including the empty-key
  signature for no-secret webhooks.
- Credential failures happen before outbound HTTP. Wrong/missing keys, tamper,
  malformed data, and AAD mismatch produce zero external request.
- Offline migration and data rollback are transactional, idempotent,
  fail-closed, storage-verified, and supported only for SQLite/MySQL.
- Schema Down refuses ciphertext loss, preserves SQLite ID high-water marks, and
  supports tested old-binary rollback.
- The legacy 255-character webhook secret capacity is retained, with multibyte
  and non-strict MySQL truncation coverage.
- Docker uses the same read-only external keyring and contains no baked key.
- Dependencies and cryptographic format remain unchanged.

## Iteration History

1. Builder iteration 1 implemented the complete schema, model, API, frontend,
   delivery, migration/rollback, Docker, test, CI, and documentation change in
   commit `66b5a1e47c29f305abcdbbf77654d51ae3a6c35c`.
2. Goal Inspector iteration 1 returned PASS in commit
   `91cb411b569f8f3370a6c8153528e4ed01ef0e85`.
3. The security specialist found that nullable legacy secrets could cause a
   metadata update to report success without changing the row and could block
   valid rollback.
4. Builder iteration 2 made all relevant predicates NULL-aware, added exact
   full-row transactional verification, expanded SQLite/real-MySQL regressions,
   and fixed MySQL CI selection in commit
   `3f59d3c73487025e3896ba3ae8bf43387c072231`.
5. Goal Inspector iteration 2 returned PASS in commit
   `9246bdc3c329bd4e16194595a1a0084c3b979750`.
6. Independent security and complete-diff code re-reviews both returned PASS.

## Key Review Resolution

NULL and empty legacy secret storage are now consistently recognized as the
valid no-legacy-secret state. Runtime updates verify name, URL, active state,
legacy secret, and ciphertext in one transaction, preventing false success from
MySQL changed-row semantics or NULL-sensitive predicates. Deactivation is
verified against active delivery, and rollback preserves ciphertext until the
complete plaintext is proven stored.

## Recommendations

- Preserve the signed Builder/Inspector commits rather than squashing them.
- Require fully green CI and GitHub-verified signatures before merge.
- Handle webhook URL userinfo/query tokens in a separate audit/redaction-policy
  PR; do not mix it with secret-at-rest encryption.
- Keep `events.details`, API-key hashing, and bulk key rotation as separate,
  explicitly scoped changes.
