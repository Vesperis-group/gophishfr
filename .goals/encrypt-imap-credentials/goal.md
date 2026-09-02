# Goal: Encrypt IMAP credentials at rest

## User Request

On branch `security/encrypt-imap-credentials`, integrate the first-party
`internal/credentials` AES-256-GCM foundation from PR #53 so IMAP passwords are
encrypted at rest. Deliver runtime keyring integration, explicit SQLite/MySQL
schema changes, an operator-triggered offline plaintext migration and rollback,
fail-closed runtime reads, correct create/update API behavior, Docker/native
deployment documentation, comprehensive tests, signed commits, a green PR, and
a safe merge. Do not encrypt any other credential type.

The complete user specification is the authoritative source that led to this
goal. This file distills its required behavior and acceptance criteria so the
Builder and Inspector can execute it without relying on conversation history.

## Refined Goal

Replace persisted plaintext IMAP passwords with strict versioned ciphertext
produced exclusively by `internal/credentials`, while temporarily retaining the
legacy column solely for explicit offline migration and rollback. Bind each
ciphertext to the stable authenticated user identity through AAD, require an
external operator-supplied keyring whenever an IMAP secret is read or written,
and never allow normal runtime plaintext fallback. Support and test the complete
lifecycle on the repository's real SQLite and MySQL schemas; explicitly reject
PostgreSQL credential migration because no PostgreSQL schema exists.

## Current Architecture Facts

- The table is `imap` in both
  `db/db_sqlite3/migrations/20200116000000_0.9.0_imap.sql` and
  `db/db_mysql/migrations/20200116000000_0.9.0_imap.sql`.
- `models.IMAP.Password` maps to plaintext `password varchar(255)`.
- The table currently has no primary key, foreign key, or `UNIQUE(user_id)`
  constraint. Application code assumes one configuration per user.
- `models.GetIMAP(uid)` reads all rows by `user_id`.
- `models.PostIMAP(im, uid)` currently deletes all rows for the user and saves
  one replacement without a transaction.
- `controllers/api/imap.go` already uses a response DTO that omits password.
- Existing POST semantics are: create with empty/absent password rejects;
  update with empty/absent password preserves; non-empty replaces; null rejects.
- Runtime ultimately passes the in-memory password to
  `imapClient.Login(mbox.User, mbox.Pwd)`.
- Goose v3 migrations run from `models.Setup`; the CLI uses Kingpin flags and
  has no subcommands. Small isolated migrate/rollback flags are feasible without
  adding a command framework or dependency.
- Real migration trees exist for SQLite and MySQL only. PostgreSQL connection
  code exists, but no `db_postgres` schema or migrations exist.
- The reusable primitive is `internal/credentials`; it already provides strict
  AES-256-GCM envelopes, key IDs, keyring loading, canonical base64, stable
  framed AAD, permission checks, rotation-ready decryption, and fail-closed
  errors.

## Acceptance Criteria

### Foundation and AAD

- [ ] Reuse `internal/credentials` directly; do not duplicate or weaken its
      cryptography, parser, keyring, permission checks, or error handling.
- [ ] Do not add any dependency and do not change `go.mod`, `go.sum`,
      `package.json`, or `yarn.lock`.
- [ ] IMAP AAD uses the fixed semantic values `Kind=imap-password`,
      `Table=imap`, `Column=password`, and `OwnerID=<authenticated user_id>`.
      `RecordID` may remain empty because `OwnerID` is the stable unique record
      identity. It must not include application version, hostname, timestamp,
      IMAP username/host, or mutable settings.
- [ ] Moving user A's ciphertext to user B's row fails authentication.

### Schema

- [ ] Add explicit versioned Goose migrations for SQLite and MySQL only.
- [ ] Keep the legacy `password` column temporarily and add a sufficiently large
      text column named consistently as `password_ciphertext`.
- [ ] Before enforcing uniqueness, detect duplicate `imap.user_id` rows and
      block migration without choosing or deleting a row.
- [ ] Enforce one IMAP row per user with an appropriate SQLite/MySQL unique
      constraint or unique index after the duplicate preflight.
- [ ] Define and test the state matrix:
      legacy = non-empty/ciphertext = empty before data migration;
      legacy = empty/ciphertext = valid after migration;
      both empty and both populated are invalid for a configured credential.
- [ ] Migration Up works on existing and fresh SQLite/MySQL databases.
- [ ] Migration Down reverses only this migration and is non-destructive. It may
      proceed only after explicit data rollback has restored plaintext; otherwise
      it fails with an operator instruction rather than losing ciphertext.
- [ ] Never invoke multiple Goose downs automatically and never rely on the old
      IMAP migration's destructive `DROP TABLE imap` Down.

### External keyring and bootstrap

- [ ] Support `GOPHISHFR_CREDENTIAL_KEYRING_FILE` for native and Docker
      deployments.
- [ ] Load a configured keyring once through the PR #53 loader and inject the
      immutable cipher cleanly; do not reread it on each request and do not use a
      mutable global when the architecture permits explicit injection.
- [ ] Never generate a key automatically and never store or bake a keyring in
      the database, image, repository, logs, or test artifacts intended for
      production.
- [ ] Missing/invalid/unsafe keyring errors are clear but contain no key or
      credential data.
- [ ] An installation with no encrypted IMAP rows may start without a keyring so
      unrelated functionality remains compatible. Any operation that must read
      or write an IMAP secret requires a valid keyring and fails closed if it is
      absent. Existing ciphertext is never interpreted without the keyring.
- [ ] Docker supports a read-only mounted keyring path such as `/run/secrets/...`;
      the final image contains no keyring.

### Explicit offline migration

- [ ] Add a small explicit operator CLI flag/action consistent with current
      Kingpin flag conventions; do not introduce a subcommand framework.
- [ ] The action is offline-only: documentation requires application shutdown,
      stopped writers, a restorable DB backup, and a separate keyring backup.
- [ ] Reject PostgreSQL as an unsupported database backend before any credential
      mutation; do not add a fake PostgreSQL schema.
- [ ] In one viable transaction for the small IMAP table: load and validate the
      keyring; validate schema; detect duplicate `user_id`; select only legacy
      rows; treat the legacy column as plaintext even when its value begins with
      the reserved envelope prefix; encrypt with the user's AAD; immediately
      decrypt and compare in memory; write ciphertext; clear legacy plaintext;
      and commit only after all rows verify.
- [ ] A failure on any row rolls back every row so no partial migration is
      visible.
- [ ] Re-running after success migrates zero rows, changes no ciphertext, and
      exits successfully with a clear no-op result.
- [ ] Mixed state containing valid migrated rows and legacy rows can safely
      complete the remaining legacy rows, but normal runtime never accepts the
      legacy rows.
- [ ] Migration output may identify rows/users and counts as non-secret metadata
      but never logs plaintext, key bytes, or full envelopes.

### Explicit data rollback

- [ ] Add a separate explicit operator rollback action using the same keyring and
      backend checks.
- [ ] In a transaction, decrypt every ciphertext with the correct user AAD,
      verify the result, restore the legacy plaintext column, and put the
      ciphertext column into the documented rollback state.
- [ ] A failure rolls back the entire data rollback.
- [ ] Data rollback is idempotent or returns a safe clear no-op on repetition.
- [ ] The schema Down is separate and is never run automatically by data
      rollback.
- [ ] The tested sequence encrypted data -> explicit data rollback -> this
      migration's Down only yields a database an old binary can read without
      credential loss.

### Runtime read and write behavior

- [ ] Normal runtime never performs `if ciphertext empty { use Password }` or any
      equivalent plaintext fallback.
- [ ] Decrypt only immediately before the IMAP password is needed; avoid
      attaching plaintext to a durable DB model or persisting it elsewhere when
      practical.
- [ ] Unknown key ID, wrong keyring, malformed/tampered envelope, wrong AAD, and
      invalid authentication tag make that IMAP credential unusable with a
      controlled non-sensitive error, no automatic rewrite, and no network
      connection attempt before successful decryption.
- [ ] Create with a non-empty password encrypts with the active key, stores only
      ciphertext, and leaves the legacy column empty.
- [ ] Create with empty or absent password remains rejected and does not encrypt
      an empty string.
- [ ] Update with a non-empty password writes a new active-key ciphertext,
      clears/keeps empty the legacy column, and makes the old secret unusable for
      that record.
- [ ] Update with empty or absent password preserves the existing ciphertext
      byte-for-byte; it does not decrypt/re-encrypt it.
- [ ] Update with `password: null` remains rejected.
- [ ] Preserve/update lookup is scoped exclusively by the authenticated user ID;
      user A can never preserve, retrieve, overwrite, or use user B's secret.
- [ ] Make the existing delete-and-insert replacement atomic or replace it with
      an equally safe transaction/upsert, avoiding the current data-loss window.
- [ ] Old-key ciphertext remains decryptable while that key is retained in the
      keyring, while newly supplied passwords use the active key. A bulk rotation
      command is not required.

### API and browser behavior

- [ ] `GET /api/imap/` returns required non-secret fields but never password,
      ciphertext, key ID, magic prefix, or a secret mask; the raw body is free of
      plaintext and ciphertext.
- [ ] POST tests cover create non-empty, create empty/absent, update empty,
      update absent, update non-empty, and null with direct database and
      decryption assertions.
- [ ] Browser behavior remains: password field empty on load, empty submit
      preserves ciphertext, non-empty submit rotates it, and create without a
      password rejects.
- [ ] Browser/network/DOM/storage/console observations contain no plaintext,
      ciphertext, key IDs, or envelope magic.
- [ ] No test can contact an external IMAP server; use an existing local fake or
      test the decryption boundary before networking.
- [ ] If frontend source is unchanged, generated frontend assets remain
      byte-identical. Unexplained asset changes are a blocker.

### Tests and security verification

- [ ] Tests prove synthetic plaintext is absent from the legacy column and absent
      as a readable substring of ciphertext after create/update/migration.
- [ ] Tests decrypt stored ciphertext with the exact expected AAD and recover the
      synthetic secret.
- [ ] Cover fresh install, legacy migration, mixed state, duplicate preflight,
      transaction interruption, idempotence, rollback, schema Up/Down,
      old-binary-compatible rollback state, preserve, rotation, old key, missing
      key ID, wrong key, tamper, wrong AAD, cross-user isolation, and missing
      keyring.
- [ ] Run targeted tests against real SQLite and the existing MySQL harness.
- [ ] Existing PR #53 unit, race, and bounded fuzz tests remain green.
- [ ] `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`,
      and `./scripts/verify.sh` pass.
- [ ] Run `govulncheck`, `gosec`, Gitleaks, actionlint, and zizmor. Preserve the
      documented narrow PR #53 G304 finding if still applicable; add no blanket
      suppression or hidden failure.
- [ ] Build the Docker image and test a read-only mounted keyring, encrypted IMAP
      operation behavior, and the documented no-keyring behavior.
- [ ] CI is fully green, including SQLite/MySQL integration, browser smoke,
      security, and container jobs.

### Documentation, Git, and delivery

- [ ] Add operational documentation containing prerequisites, keyring format and
      permissions, DB/keyring backups, stopped writers, schema upgrade, offline
      data migration, verification, startup, explicit data rollback, schema Down,
      restore, key-loss consequences, and future rotation.
- [ ] State prominently that rollback to an old binary requires explicit data
      decryption first and that a DB backup alone is insufficient after
      encryption.
- [ ] Document PostgreSQL accurately as unsupported for this credential migration
      because no real schema exists.
- [ ] Search new logging/error paths and ensure no plaintext, key material, or
      full envelope can be emitted.
- [ ] Document out-of-scope security backlogs without implementing them.
- [ ] Self-review the full diff against every criterion and document it in the PR.
- [ ] Every commit is a signed Conventional Commit with a body explaining why;
      signature verification must report `GOOD`, otherwise stop.
- [ ] Push only after the full local gate passes. Create a PR with the required
      threat, schema, migration, rollback, backend, keyring, API, security,
      testing, scanner, Docker, dependency, and breaking-change details.
- [ ] Merge only after independent crypto/security review passes, every CI check
      is green, no review thread remains, commits are verified, and no merge
      conflict exists.
- [ ] After merge, switch to `main`, fast-forward from `origin/main`, verify a
      clean tree, and stop without starting SMTP/webhook work.

## Scope Boundaries

**In scope:**
- Runtime integration of the existing first-party credential cipher/keyring.
- IMAP-only encrypted persistence and runtime decryption.
- SQLite and MySQL schema changes, uniqueness, migration, and rollback.
- Explicit offline CLI actions using existing Kingpin conventions.
- IMAP model/service/API/browser/Docker tests and operational documentation.
- Small directly coupled atomicity fixes required to prevent credential loss.

**Out of scope:**
- SMTP credentials or headers.
- Webhook secrets or URLs.
- `events.details`, API keys, DSNs, TLS private keys, or any other credential.
- KMS/Vault integration or automatic multi-secret rotation orchestration.
- PostgreSQL schema creation or claims of PostgreSQL IMAP support.
- Frontend modernization, dependency upgrades, global auth rewrites.
- Changes to PR #53 cryptographic primitives unless a generic defect is proven.
- Fixing `docker/run.sh` config logging or temporary admin password logging,
  except ensuring this PR introduces no additional secret logging.

## Applicable Project Conventions

**Quality gate command:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- Targeted SQLite/MySQL, browser, migration, rollback, Docker, fuzz, and scanner
  commands required by the acceptance criteria.

**Commit convention:**
- Signed Conventional Commits (`git commit -S`), with the reason in the body.
- Builder/Inspector role markers required by the Goal workflow.
- Assisted-by trailer required: `Assisted-by: Claude:Sonnet-4.6` for Builder
  commits and `Assisted-by: Claude:Haiku-4.5` for Inspector commits.
- Verify every commit with `git log --show-signature`; never fall back to an
  unsigned commit.

**Guidelines:**
- `CLAUDE.md`
- `docs/CREDENTIAL_ENCRYPTION_FOUNDATION.md`
- No `AGENTS.md`, `CONSTITUTION.md`, `.agents/guidelines`, or
  `.github/guidelines` were found.

**Rules:**
- Never work or commit directly on `main`.
- One security change, one branch, one PR.
- Preserve existing architecture, API semantics, compatibility, and attribution.
- No secrets in repository content, tests, logs, examples, images, or workflows.
- Validate untrusted input, fail closed, and never weaken a security control.
- Explicit versioned reversible migrations; stop on ambiguity or data-loss risk.
- No new dependency without justification; this goal requires no dependency.
- Third-party Actions remain pinned to full SHAs with minimal permissions,
  timeouts, concurrency, and `persist-credentials: false`.
- Full local gate before push; full green CI and formal self-review before merge.
