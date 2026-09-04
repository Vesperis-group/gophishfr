# Goal: Replace recoverable API keys with HMAC verifiers

## User Request

On branch `security/api-key-verifier`, eliminate permanent recovery and plaintext
database storage of user API keys. Introduce a first-party HMAC-SHA-256 verifier
with a dedicated external versioned pepper keyring, indexed verifier schema,
explicit irreversible offline migration preserving existing client token
values, verifier-only runtime authentication, reveal-once user creation and
rotation, removal from all GET/settings surfaces, old-pepper support/lazy
upgrade, guarded schema rollback, and complete SQLite/MySQL/API/browser/Docker
security coverage.

The complete specification in
`pasted-text-beaf27fa-d5a6-4614-9ad1-b00841f2d1f2.txt` is authoritative.
`internal/credentials` and its AES/keyring are explicitly not applicable.

## Refined Goal

Store only `HMAC-SHA-256(dedicated pepper, framed protocol domain + exact client
token bytes)` and a verifier key ID for each user. Keep generated and legacy
client token values/formats unchanged, but remove all normal recovery after
creation/rotation and clear legacy plaintext only through an explicit global
offline migration with a verified backup-only rollback boundary. Authenticate
through one bounded indexed lookup across accepted pepper keys, lazily migrate
old-key verifiers with concurrency-safe CAS, and retain the session/RBAC/CSRF
selection contract from PR #62 without any plaintext fallback.

## Measured Legacy Contracts

- `users.api_key` is `VARCHAR(255) NOT NULL UNIQUE` in SQLite/MySQL and a
  `models.User.ApiKey string` serialized as `json:"api_key"`.
- There is one API key per user and no disabled-key state.
- Normal generation is 32 CSPRNG bytes encoded as 64 lowercase hexadecimal
  characters (256 bits).
- `GOPHISH_INITIAL_ADMIN_API_TOKEN` may provide any non-empty legacy value
  verbatim, so not every stored token has provable high entropy.
- Initial admin bootstrap and user POST generate/store plaintext. `/api/reset`
  rotates plaintext and returns it.
- Runtime lookup is parameterized `WHERE api_key = ?` and loads the user's Role.
- GET user collection/single, POST, PUT, reset, and dedicated settings expose the
  complete key.
- PR #62 removed API keys from common page state and first-party transport;
  session/API-key selection, no-fallback, conflicts, CSRF, RBAC, view-only, and
  legacy Bearer/raw/query/form transports are established contracts.
- SQLite and MySQL ship application schema/migrations. PostgreSQL connectivity
  exists but no `db_postgres` application migration tree exists.
- Current offline credential transforms use explicit Kingpin actions, global DB
  transactions, preflight, CAS/readback, and stopped writers. API-key hashing is
  irreversible and cannot reuse their decrypt rollback.

## Acceptance Criteria

### Independent first-party verifier package

- [ ] Add a focused `internal/apikey` (or equivalently named) package independent
      of `internal/credentials`, with no imports or key/material fallback between
      them.
- [ ] Use only Go standard library cryptography and parsing; add zero dependency.
- [ ] Define one immutable verifier protocol version and domain, conceptually
      `gophishfr-api-key-verifier:v1`, framed unambiguously with exact token bytes.
      Protocol version is not application/release/git version.
- [ ] Compute HMAC-SHA-256 with exact 32-byte output. Store raw bytes, not hex.
- [ ] Expose typed, non-sensitive errors for malformed keyring, unavailable key,
      invalid verifier state, and verification failure.
- [ ] Use `hmac.Equal` for any in-Go MAC comparison. Do not overclaim removal of
      SQL timing effects.
- [ ] Unit/race/fuzz tests cover deterministic vectors, domain separation,
      changed token/key/domain, empty/arbitrary legacy token bytes as applicable,
      output length, concurrency, and malformed inputs.
- [ ] Document why HMAC is used: generated tokens are high entropy, but arbitrary
      legacy bootstrap tokens require a pepper to resist offline guessing after
      a DB-only leak.
- [ ] Document limits: no protection from host/process+pepper compromise,
      write-capable DB tampering, query/form log leaks, reveal-time browser
      compromise, or online guessing of weak tokens.

### Dedicated verifier keyring

- [ ] Add `GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE`; never read or fall back to
      `GOPHISHFR_CREDENTIAL_KEYRING_FILE`.
- [ ] Use a versioned JSON format with required active key ID and an array of
      `{id,key}` entries so duplicate IDs can be detected.
- [ ] Require a known format version, non-empty bounded safe key IDs, active ID
      present exactly once, canonical base64, and exactly 32 decoded bytes per
      key.
- [ ] Reject unknown fields/versions, duplicate IDs, invalid encoding/length,
      missing/unknown active key, empty key set, and oversized input with
      non-sensitive errors.
- [ ] Load once into immutable concurrent-read state; no request-time reads,
      mutable hot reload, default key, auto-generation, repository/image key, or
      DB copy.
- [ ] Follow normal mounted-secret symlinks and accept read-only `0400`/`0440`
      style deployment while rejecting group/world-writable keyring files. Do
      not chmod operator files.
- [ ] Keyring tests cover file bounds, permissions, symlink mounts, canonical
      encoding, duplicate keys, active/old keys, and errors/log redaction.
- [ ] Docker and docs prove credential-encryption and API-verifier keyrings are
      separate sources/material with no cross-fallback.

### Schema and runtime state

- [ ] Add explicit SQLite/MySQL Goose migrations only:
      nullable legacy `api_key`, `api_key_verifier` as SQLite BLOB/MySQL
      BINARY(32), `api_key_verifier_key_id VARCHAR(64)`, and a unique composite
      index on `(key_id, verifier)`.
- [ ] Preserve one key per user, all current user columns/constraints/indexes,
      username uniqueness, foreign-key behavior, IDs, and SQLite
      `sqlite_sequence` high-water mark during rebuilds.
- [ ] MySQL schema works in strict and relevant non-strict modes without silent
      verifier/key-ID truncation; exact readback detects mismatch.
- [ ] Do not add a public token ID, multi-key table, scopes, labels,
      `last_used_at`, disabled-key feature, or new user capability.
- [ ] Remove the plaintext API key from the production `User` model/runtime
      serialization path. Access the legacy column only through a migration-only
      row type using nullable semantics.
- [ ] Verifier and key ID fields are internal/DB-only and always `json:"-"`.
- [ ] Define states:
      LEGACY = plaintext non-empty, verifier/key ID absent;
      MIGRATED = plaintext NULL, verifier exactly 32 bytes, key ID valid;
      CONTROLLED BOTH = migrator-only matching state before clear;
      INVALID = mismatched BOTH, one verifier component missing, wrong length,
      empty plaintext, or all fields absent.
- [ ] Normal runtime rejects every non-MIGRATED state and never queries or falls
      back to plaintext.
- [ ] PostgreSQL verifier migration/schema is explicitly unsupported because no
      application migration tree exists; do not fabricate DDL.

### Guarded schema Down and irreversible boundary

- [ ] Schema Down refuses before mutation whenever dropping verifier columns or
      restoring NOT NULL plaintext would lose migrated verifier-only state.
- [ ] Down affects only this migration and never invents/reconstructs a token.
- [ ] Down succeeds only for a safely restored pre-migration/legacy database
      state and preserves user IDs, sequence, roles, hashes, permissions, and
      legacy tokens.
- [ ] Documentation explicitly states HMAC is one-way and there is no
      `--rollback-api-keys` command that can recover values.
- [ ] Real rollback is either restoration of a tested pre-migration DB backup or
      reissuance of keys. Backup restoration is exercised with an old runtime/
      legacy lookup test.

### Bootstrap and keyring availability

- [ ] Load/validate optional verifier keyring before `models.Setup` can create an
      initial user, without coupling it to the credential cipher.
- [ ] Fresh admin creation with a valid keyring and
      `GOPHISH_INITIAL_ADMIN_API_TOKEN` stores HMAC/key ID only and uses exactly
      the supplied client token.
- [ ] Fresh admin creation without an initial API token preserves strong
      generation, stores only verifier/key ID, emits/logs no token, and documents
      that an operator reveals a usable token later via authenticated settings
      reset.
- [ ] Fresh creation that requires an API key but lacks/has invalid verifier
      keyring fails before any partial user insert, while preserving PR #59
      password bootstrap atomicity.
- [ ] Existing installations may start and use session administration when the
      verifier keyring is missing; API-key authentication, user creation, and
      reset fail closed locally with controlled non-sensitive errors.
- [ ] Existing schema-upgraded legacy plaintext rows are not authenticated by
      runtime. Operators must run the explicit migration before external API
      clients resume.
- [ ] Initial admin password remains absent from logs and
      `GOPHISH_INITIAL_ADMIN_PASSWORD_FILE` behavior remains unchanged.

### Explicit offline migration

- [ ] Add only `--migrate-api-keys`, consistent with existing Kingpin offline
      actions. Do not add a deceptive rollback command.
- [ ] Action requires a valid dedicated verifier keyring, supported backend and
      expected schema, runs before serving, reports only counts, and exits.
- [ ] Documentation requires stopped writers, restorable DB backup, separate
      verifier-keyring backup, tested restoration, client verification, and
      retained rollback window.
- [ ] Preflight all users before mutation: positive unique IDs, coherent nullable
      states, non-empty bounded legacy tokens, exact verifier length, valid key
      IDs, no ambiguous/malformed data, and collision/duplicate analysis.
- [ ] LEGACY rows use exact token bytes to compute active-key HMAC.
- [ ] Write verifier+active key ID first while plaintext remains; read back and
      compare exact 32 bytes/key ID; then clear plaintext to NULL and read back
      final state.
- [ ] CONTROLLED BOTH rows are accepted only when stored key ID exists and
      `hmac.Equal(recomputed(token,key), storedVerifier)` succeeds; then clear
      plaintext. Otherwise whole migration is blocked.
- [ ] MIGRATED rows are structurally/key-ID validated and left byte-for-byte
      unchanged.
- [ ] Use one global transaction and guarded row counts/CAS. Any row-N,
      collision, truncation, wrong key, ambiguous state, or readback failure
      rolls back every row without losing plaintext.
- [ ] Re-running after success updates zero rows and changes no verifier/key ID.
- [ ] Existing generated and arbitrary/weak legacy token values continue to
      authenticate unchanged after migration.
- [ ] No migration log/error contains token, verifier, key bytes, keyring content,
      or a secret-derived fingerprint.

### Indexed runtime authentication

- [ ] Replace `WHERE api_key = ?` entirely with verifier authentication.
- [ ] For a presented token with no embedded key ID, compute candidates under all
      accepted peppers and perform one bounded parameterized indexed logical
      lookup on `(key_id, verifier)` pairs (or an equivalently race-safe indexed
      strategy).
- [ ] Query preloads Role and returns exactly:
      zero matches = invalid;
      one = authenticated user;
      more than one = fail closed without choosing an identity.
- [ ] No full-table scan, bcrypt, AES, plaintext DB lookup, plaintext cache, or
      request-time keyring read.
- [ ] Missing keyring, unknown referenced key ID, wrong pepper, malformed state,
      tampered verifier, and ambiguous matches fail closed without plaintext
      fallback or sensitive output.
- [ ] Session authentication from PR #62 remains available according to its
      policy and never attempts verifier lookup when no explicit API credential
      exists.
- [ ] Preserve explicit Bearer/raw/query/form extraction, conflict rules,
      invalid/empty no-session-fallback, status/JSON, CSRF selection, RBAC,
      view-only, forced-password-change, and campaign mutation boundaries.

### Old peppers and lazy rekey

- [ ] A verifier under any retained old key authenticates while that key remains
      accepted.
- [ ] On an old-key match, compute active-key verifier and update key ID/verifier
      with a conditional CAS on the exact matched user/old pair; never write
      plaintext.
- [ ] If CAS succeeds, subsequent auth works without the old key.
- [ ] If CAS reports zero rows, re-read current verifier state:
      accept only if the same presented token matches the current accepted state
      (for example another concurrent lazy upgrade);
      reject if reset/rotation/replacement made the token stale. This preserves
      immediate invalidation under concurrency.
- [ ] A non-conflict database error during best-effort lazy upgrade may leave the
      accepted old verifier and allow the already verified request, but must
      surface only a non-sensitive operational signal consistent with logging
      policy.
- [ ] Test concurrent lazy upgrades, lazy-upgrade vs reset, exact one-user
      identity, and no old-token success after reset commit.
- [ ] Document that old peppers can be retired only when no row references their
      ID or affected users reset/lazily upgrade. Offline bulk rekey without
      plaintext is impossible.
- [ ] Key loss makes referenced API keys unverifiable; session admin plus a
      restored/new keyring can reissue, never recover, old values.

### Token issuance and uniqueness

- [ ] Preserve generated token format exactly: 32 bytes from `crypto/rand`, 64
      lowercase hex, 256 bits.
- [ ] New tokens are never placed in the persistent User model, logs, errors,
      config, cookies, global browser state, or DB plaintext.
- [ ] Before accepting a generated token, ensure it cannot resolve to an existing
      user under any retained verifier key. On collision/ambiguous uniqueness,
      retry a small bounded number or return a controlled error; never log it.
- [ ] Store active key ID plus 32-byte verifier atomically and verify exact
      readback.
- [ ] Preserve one active token per user.

### API DTOs and reveal-once

- [ ] Use explicit request/response DTOs so ordinary User serialization exposes
      no `api_key`, verifier, key ID, mask, last-four, or `has_key`.
- [ ] GET user collection and single contain none of those values in raw bodies.
- [ ] POST user creation generates/persists verifier only and may return the
      plaintext token exactly once in a dedicated creation/reveal response.
- [ ] PUT user updates never reveal, regenerate, clear, or overwrite verifier
      state.
- [ ] `/api/reset` atomically replaces verifier/key ID with active-key HMAC,
      invalidates the old token immediately after commit, and returns the new
      plaintext once.
- [ ] Preserve reset authorization for legacy callers, including a caller using
      its own valid API key where currently permitted.
- [ ] If the create/reset response is lost, no grace/two-phase fallback exists;
      old token stays invalid and an authenticated session/admin must reissue.
- [ ] Wrong/missing verifier keyring during create/reset returns controlled
      failure and does not partially create a user or invalidate the old token.
- [ ] Raw API response tests use distinctive synthetic values and prove no token/
      verifier/key ID outside the immediate successful reveal response.

### Settings and browser reveal lifecycle

- [ ] Remove server-side existing API-key value from settings HTML.
- [ ] Initial settings load displays no recoverable key and explains that reset
      issues a non-recoverable replacement.
- [ ] Successful reset displays the new token in one dedicated accessible
      reveal/copy UI with a clear one-time warning.
- [ ] Closing the reveal removes plaintext from DOM and clears retained JS state
      where reasonable. Reload cannot recover it.
- [ ] User-creation UI reveals a generated token once if that workflow currently
      needs it, without adding it to lists or reusable state.
- [ ] Normal pages and settings load contain no token, verifier, key ID, mask,
      presence oracle, global state, data attribute, localStorage,
      sessionStorage, cookie, or subsequent request.
- [ ] Network inspection shows token only in the immediate successful POST/reset
      response and only when the operator intentionally performs that action.
- [ ] Do not claim absolute JavaScript memory zeroization.

### Backends and lifecycle tests

- [ ] SQLite lifecycle covers legacy schema/data, Up rebuild, sequence/constraints,
      offline migration, unchanged old token auth, migrated API responses,
      verifier-only create, reset/reveal, old invalidation, idempotence,
      interruption rollback, guarded Down, backup restore, and fresh DB.
- [ ] Real MySQL covers equivalent lifecycle, nullable legacy column,
      BINARY(32), key-ID bounds, composite uniqueness, strict/non-strict
      readback/truncation, no-op/CAS, failure rollback, and fresh DB.
- [ ] PostgreSQL claim is explicitly UNSUPPORTED with no migration or false test
      coverage.
- [ ] Backup rollback test restores the pre-migration DB and proves old
      runtime/plaintext lookup authenticates the same token.
- [ ] Pepper tests cover active+old keys, lazy upgrade, old-key retirement
      precondition, missing/unknown/wrong key, tamper, ambiguous match, and key
      loss recovery through session/reissue.
- [ ] Record-substitution tests/documentation state that a write-capable DB
      attacker can move the authentication index; verifier protects token
      confidentiality under read-only leak, not DB integrity.

### Docker and regression coverage

- [ ] Docker uses a separate read-only mounted verifier keyring via
      `GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE`; neither keyring is baked into
      image/layers/filesystem or printed.
- [ ] Real container covers fresh bootstrap with password file plus verifier
      keyring, provided and generated initial API-token cases, legacy migration,
      existing client Bearer/query/form compatibility, session SPA, GET secrecy,
      create/reset reveal-once, old-token invalidation, old pepper/lazy upgrade,
      wrong/missing keyring, and zero token/pepper in logs.
- [ ] Re-run the full PR #62 auth matrix: explicit bad/empty/conflict with session,
      identity, CSRF, forced reset, view-only/RBAC, campaign completion, logout.
- [ ] Re-run PR #59 bootstrap, PR #60 config-no-log, PR #61 log-file, and
      IMAP/SMTP/webhook credential/keyring anti-leak behavior.
- [ ] Tests use local services only and cannot send external email/webhooks or
      contact external databases.

### Documentation, scope, and validation

- [ ] Add `docs/API_KEY_VERIFIER.md` and update authentication/settings docs with
      threat, algorithm/domain, dedicated keyring, format, schema, migration,
      backup-only rollback, unchanged client tokens, reveal-once, fresh install,
      missing/key-loss behavior, pepper rotation/lazy upgrade/retirement,
      limitations, and query/form/rate-limit backlogs.
- [ ] Document breaking changes:
      GET/settings no longer return existing keys;
      fresh create/reset reveal once;
      verifier keyring required for API-key operations/new user creation;
      offline migration clears plaintext irreversibly.
- [ ] `go.mod`, `go.sum`, `package.json`, and `yarn.lock` remain unchanged; no
      external dependency or crypto/keyring reuse.
- [ ] Regenerate only canonical affected frontend assets; two clean builds are
      byte-identical.
- [ ] `./scripts/verify.sh`, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, and `go build ./...` pass.
- [ ] Run real SQLite/MySQL, full auth matrix, browser reveal lifecycle, Docker,
      verifier unit/race/fuzz, migration/backup, pepper rotation, and frontend
      reproducibility tests.
- [ ] Run `govulncheck`, `gosec`, Gitleaks, actionlint, zizmor, Yarn audit,
      Retire.js, and dependency-delta review. Report unavailable tools and
      pre-existing findings honestly; add no suppression.
- [ ] Final search classifies every `ApiKey`/`api_key`/`WHERE api_key` occurrence;
      production runtime plaintext reads equal zero outside migration/schema
      compatibility.
- [ ] Formal self-review maps all security/behavior/migration/reveal criteria to
      evidence.

### Git and delivery

- [ ] Every branch commit has Author and Committer exactly
      `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- [ ] Every commit is signed and locally reports GOOD.
- [ ] No author, committer, co-author, message, trailer, or metadata refers to
      Copilot. Explicit user instruction overrides default trailer.
- [ ] Signed Conventional Commits use why-focused bodies and Goal role markers;
      `Assisted-by: Claude:*` is allowed.
- [ ] Push only after all local gates.
- [ ] PR description includes every threat, verifier/keyring/schema, migration/
      irreversibility/rollback, auth, reveal, pepper, backend, Docker,
      compatibility, dependency, test, and scanner field required by the
      specification.
- [ ] Explicitly state existing client token values/formats do not change, DB
      stores no recoverable token after migration, and rollback requires backup
      or reissue.
- [ ] Merge only after Goal Inspector PASS, independent crypto/security and code
      review PASS, all CI green, every GitHub commit Verified under `vg-kvr-grp`,
      no Copilot metadata, no conflict, and no unresolved thread.
- [ ] After merge, switch to `main`, fast-forward `origin/main`, verify clean
      status, and stop without starting `events.details`.

## Scope Boundaries

**In scope:**
- Dedicated HMAC verifier/keyring/protocol.
- SQLite/MySQL schema, explicit irreversible offline migration, indexed runtime
  lookup, old-pepper lazy upgrade, bootstrap issuance, API DTO/reveal-once,
  settings/browser, Docker/CI/tests/docs/review.
- Directly coupled concurrency and atomicity required for immediate rotation and
  migration safety.

**Out of scope:**
- Removal of raw Authorization/query/form transports and API-key rate limiting.
- Token format/public ID/multi-key/scopes/labels/last-used.
- Session/RBAC/CSRF mechanism changes beyond required regression compatibility.
- `internal/credentials`, IMAP/SMTP/webhook, passwords/login, log/config
  hardening, PostgreSQL schema, secret managers/KMS, and `events.details`.
- External dependencies or fake reversible rollback.

## Applicable Project Conventions

**Quality gate commands:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- Real SQLite/MySQL, browser, Docker, fuzz, migration/backup, and scanners above.

**Commit convention:**
- Signed Conventional Commits with why-focused body and Goal role markers.
- `Assisted-by: Claude:Sonnet-4.6` / `Claude:Haiku-4.5` allowed.
- Author/Committer exactly
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- No Copilot author, committer, co-author, message, trailer, or metadata.

**Guidelines:**
- `CLAUDE.md`
- `SECURITY.md`
- `CONTRIBUTING.md`
- `.github/PULL_REQUEST_TEMPLATE.md`
- Existing credential, API-auth, migration, bootstrap, Docker, and logger docs.
- No `AGENTS.md`, `CONSTITUTION.md`, `.agents/guidelines`, or
  `.github/guidelines` exist.

**Rules:**
- Never commit/push directly to `main`.
- Never store/log real token or key material in code/tests/docs/images.
- No plaintext runtime fallback or fake rollback.
- Preserve client token values and #62 security boundaries.
- No dependency or hidden scanner bypass.
- Full local gates, independent reviews, green CI, formal self-review, verified
  identity/signatures, and no open thread before merge.
