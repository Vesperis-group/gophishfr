# Goal: Encrypt SMTP credentials at rest

## User Request

On branch `security/encrypt-smtp-credentials`, completely secure SMTP sending
profile passwords: stop returning them to APIs and the frontend, encrypt stored
passwords exclusively with `internal/credentials`, preserve legitimate
unauthenticated profiles, preserve the safe empty-password update workflow,
provide explicit transactional offline migration and rollback for SQLite/MySQL,
decrypt only at the SMTP use boundary, and verify API/browser/send/Docker
behavior without touching any other secret type.

The complete user specification is authoritative. This immutable goal captures
its required outcomes plus the legacy behavior measured before implementation.

## Refined Goal

Remove all normal HTTP/browser read exposure of SMTP passwords and migrate
present SMTP secrets from plaintext to strict AES-256-GCM envelopes using the
existing application keyring. Bind every ciphertext to the immutable sending
profile ID and authenticated owner ID, obtain generated IDs transactionally
without ever persisting plaintext, and decrypt only immediately before the
single SMTP dialer boundary. Preserve no-auth profiles and introduce a deliberate
security-safe update contract where empty or absent passwords keep the existing
ciphertext, because the legacy frontend could only avoid clearing it by
round-tripping the plaintext that this change must stop exposing.

## Measured Legacy Contracts

- Table: `smtp`; PK: autoincrement `id`; owner: `user_id`.
- Multiple sending profiles per user are legitimate. No `UNIQUE(user_id)` may
  be introduced.
- `password varchar(255)` is nullable and currently stored/serialized as
  plaintext.
- `SMTP.Validate()` checks neither username nor password.
- `username=""` causes the pinned gomail implementation to skip SMTP AUTH.
  Therefore `username=""`, `password=""` is a valid unauthenticated profile.
- `username!=""`, `password=""` attempts AUTH with an empty password.
- Create accepts password non-empty, empty, or absent. JSON null is rejected.
- Legacy PUT full-overwrites every field. Empty/absent password therefore clears
  the stored secret. The UI masks this behavior by reading and resending the
  plaintext password from GET.
- Removing GET exposure makes literal preservation of full-overwrite semantics
  unsafe: ordinary non-secret edits would silently erase an existing secret.
  This goal intentionally changes empty/absent update to preserve an existing
  ciphertext byte-for-byte, as required by the user specification. No new clear
  mechanism is added.
- PUT null handling is inconsistent and only incidentally rejected; this goal
  requires explicit rejection.
- GET collection, GET single, POST response, PUT response, and potentially
  campaign responses serialize the password because the persistence model is
  reused as an API representation.
- The sending-profiles frontend prefills the password input from the GET list
  payload.
- `GetDialer()` is the sole production password consumption boundary for both
  campaign sends and test email.
- A blank password in the sending-profiles page's full test-email payload is
  currently used as empty; a bare-name reference falls back to the stored
  profile. The secured endpoint must use the authorized stored ciphertext for
  an existing profile without returning it to the browser.
- `PutSMTP` updates by ID only and header deletion relies on a controller
  pre-fetch for ownership. The encryption path must enforce `(user_id,id)` at
  the model/write boundary as a directly coupled defense-in-depth correction.
- Real schema trees exist only for SQLite/MySQL. PostgreSQL has driver tests but
  no application schema and must be rejected before credential mutation.

## Acceptance Criteria

### Foundation, keyring, and AAD

- [ ] Reuse `internal/credentials` and the generic PR #54 keyring bootstrap
      unchanged; do not duplicate crypto, keyring parsing, envelope parsing,
      permissions, or error mapping.
- [ ] Use the existing `GOPHISHFR_CREDENTIAL_KEYRING_FILE` and one immutable
      cipher instance for IMAP, SMTP, and future credential kinds.
- [ ] Do not add dependencies or modify `go.mod`, `go.sum`, `package.json`, or
      `yarn.lock`.
- [ ] SMTP AAD is exactly and stably based on `Kind=smtp-password`,
      `Table=smtp`, `Column=password`, `OwnerID=<authenticated user_id>`, and
      `RecordID=<immutable smtp.id>`.
- [ ] AAD excludes application/release/build versions, profile name, host,
      port, username, from address, headers, timestamps, and other mutable data.
- [ ] Copying ciphertext across users or between two profile IDs owned by the
      same user fails authentication.
- [ ] Old-key ciphertext remains decryptable while retained in the keyring;
      newly supplied passwords use the active key. Bulk rotation is future work.

### Schema and state model

- [ ] Add paired explicit Goose migrations for SQLite and MySQL only, retaining
      legacy `password` temporarily and adding a sufficiently large
      `password_ciphertext` text column.
- [ ] Do not add `UNIQUE(user_id)` or otherwise prevent multiple SMTP profiles
      per user. Existing immutable autoincrement `id` remains the record
      identity.
- [ ] Define and enforce these encrypted-runtime states:
      no-auth profile = username empty, legacy empty, ciphertext empty;
      authenticated/secret profile = legacy empty, valid ciphertext present;
      legacy non-empty, both columns populated, or malformed ciphertext are
      invalid normal-runtime states.
- [ ] Do not encrypt an empty string merely to populate ciphertext.
- [ ] Migration Up succeeds on fresh and existing SQLite/MySQL databases.
- [ ] Migration Down reverses only this migration and refuses to discard
      ciphertext. It proceeds only after explicit data rollback leaves the
      schema safe, otherwise fails with a non-sensitive operator instruction.
- [ ] Never run chained Goose downs automatically.
- [ ] PostgreSQL SMTP credential migration returns unsupported before mutation;
      do not create a PostgreSQL schema.

### API request/response separation

- [ ] Use explicit request/response DTOs or an equally safe separation so write
      decoding remains compatible while no persistence secret field is exposed.
- [ ] GET collection, GET single, POST response, PUT response, campaign
      list/detail/embedded sending-profile responses, test-email responses, and
      every other normal HTTP response contain no plaintext password,
      ciphertext, key ID, envelope magic, secret mask, or `has_password` flag.
- [ ] Raw response bodies are tested against distinctive synthetic plaintext and
      ciphertext values.
- [ ] This is an intentional security breaking API change: clients can no
      longer retrieve SMTP passwords. Do not add an opt-in legacy endpoint or
      query parameter.
- [ ] Explicitly reject `password: null` for create and update without logging
      request bodies or relying on incidental ID-validation failures.
- [ ] Authentication/authorization for GET/PUT/DELETE and existing-profile test
      operations uses the authenticated user ID and profile ID/name; a client
      cannot preserve, rotate, test, move, or delete another user's profile.

### Frontend behavior

- [ ] Edit and copy flows never prefill the password input; it is empty when a
      stored profile is loaded.
- [ ] Creating an authenticated profile accepts the newly entered password.
- [ ] Creating a valid unauthenticated profile with empty username/password
      remains supported and produces no ciphertext.
- [ ] Editing non-secret fields with empty/absent password preserves an existing
      ciphertext byte-for-byte.
- [ ] Entering a non-empty password rotates the stored ciphertext.
- [ ] No SMTP plaintext, ciphertext, key ID, or envelope magic appears in GET
      network responses, DOM, storage, console, or serialized profile objects.
- [ ] Regenerate only canonical affected frontend assets. Two clean builds are
      byte-identical and no unrelated asset changes remain.

### Runtime create and update

- [ ] Create with a non-empty password runs in one transaction:
      insert the profile with both secret columns empty, obtain immutable ID,
      encrypt in memory with `(user_id,id)` AAD, immediately decrypt/verify,
      update ciphertext while legacy remains empty, persist headers, and commit.
      No incomplete row is visible and plaintext is never persisted.
- [ ] Create with empty/absent password and empty username remains a valid
      no-auth profile with both secret columns empty.
- [ ] If username is non-empty and password is empty, preserve the measured
      legacy allowance; runtime may attempt AUTH with an empty password but must
      not invent ciphertext or plaintext fallback.
- [ ] Update with non-empty password encrypts with the active key, replaces
      ciphertext, keeps legacy empty, verifies within the transaction, and
      leaves the old secret unusable for that record.
- [ ] Update with empty/absent password and an existing ciphertext preserves
      that ciphertext byte-for-byte without decrypt/re-encrypt.
- [ ] Update with empty/absent password on a no-auth profile keeps both columns
      empty.
- [ ] Username, name, host, port, from-address, header, and boolean changes do
      not affect AAD; a preserved ciphertext remains decryptable.
- [ ] No clear-password field/action is added in this PR.
- [ ] SMTP row and header creation/update/delete are atomic where touched.
- [ ] Model write and delete paths directly scope mutations by authenticated
      `(user_id,id)` rather than relying only on controller pre-fetch.

### Runtime send and test-email boundary

- [ ] Normal runtime never falls back to legacy `password`.
- [ ] Decrypt into a local non-serializable value immediately before
      `GetDialer()` constructs/authenticates the SMTP client. Do not attach
      plaintext to a durable/shared/API model when avoidable.
- [ ] Campaign/background sends use the correct profile owner's ciphertext and
      exact `(user_id,id)` AAD.
- [ ] Test email for an existing authorized profile can use its stored
      ciphertext when browser input is empty/absent, without returning the
      secret to the browser.
- [ ] Test email with a newly supplied non-empty password uses it only in memory
      and does not persist or echo it.
- [ ] No-auth profiles skip SMTP AUTH as in the pinned gomail behavior.
- [ ] Unknown key ID, wrong keyring, malformed/tampered ciphertext, wrong owner,
      wrong record ID, and invalid authentication tag fail with a controlled
      non-sensitive error before any SMTP connection attempt.
- [ ] No plaintext, ciphertext, key, or sensitive request body appears in logs,
      errors, templates, traces, or API responses.
- [ ] Tests use the existing mock mailer/dialer or a strictly local fake SMTP;
      external SMTP traffic is impossible.

### Explicit offline migration

- [ ] Add `--migrate-smtp-credentials`, consistent with the existing Kingpin
      flags and PR #54 offline action architecture; do not introduce a subcommand
      framework.
- [ ] Documentation requires stopped writers, a restorable DB backup, and a
      separate keyring backup before migration.
- [ ] Load the existing keyring once; validate supported backend/schema; validate
      non-zero unique IDs, owner IDs, and all current row states before writing.
- [ ] Valid already-encrypted rows are verified with their exact AAD and left
      byte-for-byte unchanged. No-secret rows remain empty.
- [ ] Legacy rows with non-empty plaintext and empty ciphertext are encrypted
      with `(user_id,id)` AAD, immediately decrypted and compared in memory,
      written to ciphertext, and cleared from legacy storage.
- [ ] Rows containing both legacy and ciphertext, invalid IDs/owners, malformed
      ciphertext, wrong-key ciphertext, or other ambiguous states block the
      whole migration; never choose a canonical value arbitrarily.
- [ ] The complete table migration is transactional. Any error at row N rolls
      back all earlier row changes.
- [ ] Re-running after success updates zero rows, changes no ciphertext, and
      succeeds with a clear count-only no-op result.
- [ ] Mixed valid encrypted, valid no-secret, and legacy rows can complete only
      the legacy rows safely.
- [ ] Migration logs only non-secret counts/row identifiers; never plaintext,
      key bytes, full envelopes, or request payloads.

### Explicit data rollback

- [ ] Add `--rollback-smtp-credentials` using the same backend/keyring checks and
      a single transaction.
- [ ] Valid ciphertext rows are decrypted with exact `(user_id,id)` AAD,
      immediately verified, restored to legacy plaintext, and ciphertext is
      cleared. No-secret rows remain empty.
- [ ] Ambiguous/invalid states, wrong keys, tampering, or per-row failures roll
      back the entire data rollback.
- [ ] Repetition is idempotent or a clear safe no-op.
- [ ] Data rollback never invokes schema Down. After successful rollback, this
      migration's Down alone yields an old-binary-compatible SQLite/MySQL schema
      and preserves multiple profiles and headers.

### Tests and evidence

- [ ] Freeze the measured legacy baseline in regression tests: create with
      auth, create without auth, empty username, empty/absent update behavior,
      null handling, profile/header behavior, and current API leak removed by the
      final contract.
- [ ] Direct DB assertions prove fresh secret writes and migrated rows have empty
      legacy password, non-empty ciphertext, no readable plaintext substring,
      and decrypt to the expected synthetic secret with exact AAD.
- [ ] No-auth rows keep both columns empty throughout create, migration,
      runtime, rollback, and Down.
- [ ] Cover create ID/AAD atomicity, header rollback, update preservation,
      rotation, username/host changes, cross-user authorization, same-user
      record-copy AAD failure, wrong key, missing key ID, tamper, invalid state,
      and failure-before-network.
- [ ] Cover API collection/single/campaign/POST/PUT/test-email response secrecy,
      explicit null rejection, existing-profile test-email with blank browser
      password, and inline new password.
- [ ] Cover browser create auth/no-auth, blank edit, non-secret preserve,
      rotation, copy, delete, error/retry, and absence from network/DOM/storage/
      console.
- [ ] Cover campaign send and test-email with mock/local SMTP for no-auth,
      encrypted secret, rotated secret, wrong key, and tamper; zero external
      network.
- [ ] Real SQLite and MySQL lifecycle tests cover schema Up, fresh/existing DB,
      offline migration, idempotence, interruption rollback, data rollback,
      guarded Down, old-binary compatibility, headers, and multiple profiles per
      user.
- [ ] Re-run the important IMAP encrypted lifecycle/API/browser/container tests;
      SMTP work must not regress PR #54.
- [ ] Existing PR #53 unit/race/fuzz tests remain green and crypto format is
      unchanged.

### Validation, documentation, and delivery

- [ ] Add an operational SMTP credential-encryption guide covering threat,
      optional auth, breaking API behavior, keyring, backups, stopped writers,
      schema upgrade, offline migration, verification, startup, rollback,
      old-binary compatibility, key loss, and future bulk rotation.
- [ ] Update user-facing/API documentation so clients know passwords are no
      longer returned and empty/absent update preserves an existing secret.
- [ ] Document PostgreSQL accurately as unsupported for this credential
      migration and no-auth profiles as preserved.
- [ ] `go test ./...`, `go test -race ./...`, `go vet ./...`,
      `go build ./...`, and `./scripts/verify.sh` pass.
- [ ] Run targeted real MySQL, browser, mock/fake SMTP, Docker, bounded PR #53
      fuzz targets, `govulncheck`, `gosec`, Gitleaks, actionlint, zizmor, and
      relevant frontend Yarn/Dependabot/Retire checks when assets change.
- [ ] Docker image and tests reuse the same read-only external keyring, prove
      fresh encrypted SMTP writes, legacy migration, preserve, rotate, send/test
      boundary behavior, wrong-key/tamper fail-closed, and contain no keyring.
- [ ] Dependencies remain unchanged; frontend clean builds are reproducible.
- [ ] Formal self-review documents every security/behavior criterion and all
      pre-existing scanner findings versus new regressions.
- [ ] Every commit is a signed Conventional Commit with reason in the body and a
      verified GOOD signature. Stop on signing failure.
- [ ] Push only after full local gates. PR description documents threat, measured
      legacy behavior, no-auth profiles, breaking API contract, AAD, schema,
      create-ID sequence, migration/rollback, sends/tests, backends, frontend,
      Docker, dependencies, tests, scanners, and self-review.
- [ ] Merge only with independent crypto/security PASS, fully green CI, verified
      signatures, no conflict, and no unresolved conversation.
- [ ] After merge, switch to `main`, fast-forward from `origin/main`, verify a
      clean tree, and stop without starting webhook encryption.

## Scope Boundaries

**In scope:**
- SMTP sending-profile passwords only.
- Security breaking removal of all SMTP password read exposure.
- Preservation of valid no-auth profiles.
- Security-safe empty/absent update preservation for existing ciphertext.
- Small directly coupled ownership and transaction fixes required to safely bind
  and mutate `(user_id,id)` ciphertext and headers.
- SQLite/MySQL schema, explicit offline migration/rollback, API/frontend/send
  boundary, tests, Docker, CI, and documentation.

**Out of scope:**
- IMAP behavior changes except regression tests.
- Webhook secrets/URLs, `events.details`, API keys, SMTP custom headers,
  DSN/config logging, temporary admin password logs, TLS keys.
- KMS/Vault, new cryptographic primitives, external crypto dependencies,
  PostgreSQL schema creation, generic secret-storage frameworks.
- Bulk rotation orchestration, frontend modernization, unrelated dependency
  upgrades, or a new clear-password mechanism.

## Applicable Project Conventions

**Quality gate command:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- Targeted SQLite/MySQL, browser, mock SMTP, Docker, fuzz, and security scanner
  commands required above.

**Commit convention:**
- Signed Conventional Commits using `git commit -S`, with the reason in the body.
- Builder/Inspector `[B]`/`[I]` markers required by the Goal workflow.
- `Assisted-by: Claude:Sonnet-4.6` for Builder and
  `Assisted-by: Claude:Haiku-4.5` for Inspector.
- `Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.
- Verify every signature with WSL-native Git/GPG and require `GOOD`.

**Guidelines:**
- `CLAUDE.md`
- `docs/CREDENTIAL_ENCRYPTION_FOUNDATION.md`
- `docs/IMAP_CREDENTIAL_ENCRYPTION.md`
- `.goals/encrypt-imap-credentials/goal.md`
- No `AGENTS.md`, `CONSTITUTION.md`, `.agents/guidelines`, or
  `.github/guidelines` exist.

**Rules:**
- Never commit or push directly to `main`; one security branch and PR.
- No secrets in code, tests, fixtures, examples, images, logs, or workflows.
- Use synthetic fake secrets only; tests cannot send real email externally.
- Never weaken security or hide scanner/test failures.
- Explicit versioned reversible migrations; stop on ambiguity/data-loss risk.
- No new dependency is needed or allowed for this goal.
- Preserve Action pinning, minimal permissions, timeouts, and CI protections.
- Full local gate, independent review, green CI, formal self-review, verified
  signatures, and no open threads before merge.
