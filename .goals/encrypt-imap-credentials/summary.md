# Encrypted IMAP credentials: completed

## Outcome

The goal passed independent inspection in one iteration. IMAP passwords now
use the first-party `internal/credentials` AES-256-GCM foundation for storage,
with stable user-bound AAD and no runtime plaintext fallback.

## Acceptance criteria delivered

- SQLite and MySQL have versioned schema migrations that retain the legacy
  password column for controlled rollback, add `password_ciphertext`, reject
  duplicate users before adding uniqueness, and use a non-destructive guarded
  Down.
- `GOPHISHFR_CREDENTIAL_KEYRING_FILE` loads an external immutable keyring once.
  Secret operations fail closed without it, while unrelated installations with
  no encrypted IMAP data can still start.
- Explicit `--migrate-imap-credentials` and
  `--rollback-imap-credentials` operator actions perform transactional,
  verified, idempotent data transformations. PostgreSQL is rejected before any
  mutation because the repository has no PostgreSQL schema.
- Fresh creates store ciphertext only. Empty or absent update passwords preserve
  the ciphertext byte-for-byte, non-empty updates rotate it with the active key,
  and null remains invalid.
- Runtime decryption occurs immediately before IMAP use. Wrong keys, missing key
  IDs, tampering, malformed envelopes, and cross-user AAD mismatches fail before
  networking and never fall back to the legacy plaintext column.
- GET responses and browser behavior expose neither plaintext nor ciphertext.
  Tests cover API semantics, migration, rollback, SQLite, real MySQL, browser,
  Docker, race detection, fuzz targets, wrong-key/tamper behavior, and
  cross-user isolation.
- Operational documentation covers backups, stopped writers, keyring mounts,
  upgrade, verification, rollback, old-binary compatibility, key loss, and the
  PostgreSQL limitation.
- Dependency manifests and generated frontend assets remain unchanged.

## Iteration history

Iteration 1 passed. The Builder implemented and validated the complete
lifecycle in one signed commit. The Inspector independently reviewed all
acceptance-criterion groups and returned PASS in a second signed commit.

The Inspector's first attempt to commit its verdict was blocked because its
Windows-side process could not locate GPG. No unsigned commit was created. The
same Inspector verdict was then committed through the configured WSL Git/GPG
environment and verified with a GOOD signature.

## Inspector findings

No product deficiency remained. The Inspector confirmed that the existing
cryptographic primitive was reused unchanged, schema and data rollback paths are
guarded, runtime behavior is fail-closed, backend claims are accurate, and the
test/deployment documentation covers the required lifecycle.

## Recommendations

- Implement bulk key rotation as a separate future operator command.
- Address SMTP passwords, webhook secrets, captured event details, API key
  hashing, DSN logging, and temporary admin password logging in separate scoped
  security changes.
- Add real PostgreSQL schema support only as an explicit independent project,
  not as part of IMAP credential encryption.
