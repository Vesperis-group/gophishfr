# Goal: Encrypt webhook authentication secrets

## User Request

On branch `security/encrypt-webhook-secrets`, secure webhook authentication
secrets only: remove their API/frontend exposure, encrypt them at rest
exclusively with the existing `internal/credentials` foundation and shared
external keyring, preserve optional-secret and explicit-clear behavior, provide
transactional offline migration and rollback for SQLite/MySQL, decrypt only at
the HMAC signing boundary, and guarantee zero outbound HTTP requests when an
encrypted secret cannot be authenticated.

The complete user specification at
`pasted-text-30907653-fa9b-4922-9dd7-04a89f58bd1c.txt` is authoritative. This
immutable goal captures its required outcomes, the measured legacy behavior,
and the user's explicit resolution of the absent/empty/null update ambiguity.

## Refined Goal

Make webhook secrets write-only at every API/browser read boundary and store
non-empty secrets only as authenticated AES-256-GCM envelopes using the existing
application keyring. Bind each ciphertext to the immutable global webhook ID,
obtain generated IDs transactionally without ever persisting plaintext, and
decrypt only immediately before the unchanged HMAC-SHA256 operation. Preserve
legacy no-secret delivery and explicit revocation while making ordinary edits
safe: on update, absent or null preserves the existing ciphertext byte-for-byte,
an explicitly present empty string clears it atomically, and a non-empty string
replaces it.

## Measured Legacy Contracts

- Table: `webhooks`.
- Primary key: database-generated autoincrement/auto_increment `id`.
- Ownership: none. Webhooks are global administrator resources protected by
  `PermissionModifySystem`; there is no `user_id` or tenant owner.
- Persistent fields: `id`, `name varchar(255)`, `url varchar(1000)`,
  nullable `secret varchar(255)`, and `is_active`.
- There are no webhook child/event-subscription relation tables. Runtime event
  selection loads all active global webhooks.
- `name`, `url`, and `is_active` are mutable and cannot enter AAD.
- `Webhook.Validate()` requires only name and URL. A webhook without a secret is
  valid.
- Legacy create accepts secret non-empty, empty, absent, or JSON null. The last
  three create a no-secret webhook.
- Legacy update uses full-row GORM `Save`. Non-empty replaces; empty, absent,
  and JSON null all clear the secret.
- The stock UI always sends the secret, marks the input `required`, and prefills
  plaintext from GET. It therefore cannot create/clear no-secret webhooks and
  masks the full-replace API behavior by round-tripping the plaintext.
- The target update contract was explicitly confirmed by the user:
  absent/null = preserve; present `""` = explicit clear; non-empty = replace.
  Create absent/null/empty remains a no-secret webhook.
- An ordinary frontend edit must omit `secret` until the user explicitly asks
  to modify or remove it.
- GET collection, GET single, POST, PUT, and validate responses currently
  serialize plaintext through the persistence model.
- Delivery chain:
  `models.AddEvent` -> `GetActiveWebhooks` -> `webhook.SendAll` ->
  `defaultSender.Send`; validation calls `webhook.Send` synchronously.
- Current signing is `HMAC-SHA256(secret, json.Marshal(payload))`, rendered as
  `X-Gophish-Signature: sha256=<lowercase hex>`.
- A no-secret webhook still signs with the empty HMAC key and sends the signature
  header. This exact behavior must remain unchanged.
- `SendAll` launches one goroutine per endpoint, logs one failure, and has no
  retry/backoff mechanism. Validation returns delivery errors synchronously.
- HTTP status, timeout, redirect, logging, and retry behavior are otherwise out
  of scope.
- SQLite and MySQL have real webhook schemas. PostgreSQL has driver connectivity
  coverage but no application migration tree and must be rejected before data
  mutation.

## Acceptance Criteria

### Foundation, keyring, and AAD

- [ ] Reuse `internal/credentials` unchanged: AES-256-GCM, random nonce,
      `gophishfr-cred:v1` envelope, versioned key ID, canonical encoding, framed
      AAD, strict parser, and fail-closed errors.
- [ ] Reuse the one immutable cipher loaded from
      `GOPHISHFR_CREDENTIAL_KEYRING_FILE` for IMAP, SMTP, webhooks, and future
      credential kinds. Do not add a second parser, keyring, environment
      variable, or implicit key generation.
- [ ] Do not add dependencies or modify `go.mod`, `go.sum`, `package.json`, or
      `yarn.lock`.
- [ ] Webhook AAD is exactly and stably based on
      `Kind=webhook-secret`, `Table=webhooks`, `Column=secret`, empty OwnerID
      because no owner exists, and `RecordID=<immutable webhooks.id>`.
- [ ] AAD excludes application/build version, name, URL, active state, events,
      timestamps, HTTP status, headers, and every other mutable value.
- [ ] Copying ciphertext between two webhook IDs fails authentication.
- [ ] Old-key ciphertext remains decryptable while its key is retained; new
      or replacement secrets use the active key. Bulk re-encryption is future
      work.

### Schema, identity, and state model

- [ ] Add paired explicit Goose migrations for SQLite and MySQL only, retaining
      nullable legacy `secret` temporarily and adding a sufficiently sized
      `secret_ciphertext` column with a deterministic empty default.
- [ ] Add no owner, uniqueness, event relation, or business constraint.
- [ ] Preserve immutable database-generated IDs. Create with a secret must wait
      for the generated ID inside the same transaction; plaintext must never be
      inserted merely to obtain it.
- [ ] SQLite schema Down preserves the prior `sqlite_sequence` high-water mark,
      including after the highest webhook ID has been deleted, so historical
      references cannot be retargeted by ID reuse.
- [ ] Enforce valid runtime states:
      no-secret = legacy null/empty and ciphertext empty;
      with-secret = legacy null/empty and valid ciphertext;
      legacy non-empty after activation, both populated, malformed/oversized
      ciphertext, unknown key, or invalid AAD/tag are invalid.
- [ ] Never encrypt an empty string merely to populate ciphertext.
- [ ] Migration Up succeeds on fresh and existing SQLite/MySQL databases.
- [ ] Schema Down affects only this migration and refuses before destructive
      mutation if any ciphertext remains. It succeeds only after explicit data
      rollback and yields an old-binary-compatible schema.
- [ ] PostgreSQL webhook-secret migration is rejected before mutation; do not
      claim or create PostgreSQL application-schema support.

### Length and storage integrity

- [ ] Preserve the legacy MySQL `VARCHAR(255)` secret contract in characters,
      not by blindly copying SMTP's stricter 255-byte password limit.
- [ ] Establish and document a deterministic valid-Unicode maximum compatible
      with 255 database characters. Derive the maximum UTF-8 plaintext bytes,
      AES-GCM envelope bytes, and ciphertext-column capacity conservatively.
- [ ] Accept boundary-valid multibyte secrets that the legacy MySQL schema can
      store; reject the first over-limit value before encryption or mutation.
- [ ] Reject invalid/unsafe stored plaintext or ciphertext states without
      logging secret material.
- [ ] For create, replace, migration, and rollback, read secret columns back
      inside the transaction and compare exact bytes before clearing the prior
      copy or committing.
- [ ] Migration first stores and verifies complete ciphertext while retaining
      plaintext, then clears and verifies plaintext.
- [ ] Rollback first restores and verifies complete plaintext while retaining
      ciphertext, then clears and verifies ciphertext.
- [ ] Real MySQL tests with strict and non-strict behavior prove silent
      plaintext/ciphertext truncation cannot commit or destroy the only copy.

### API request and response contracts

- [ ] Separate persistence from request/response representations so write
      decoding distinguishes absent, null, empty, and non-empty secret values
      while responses cannot serialize secret storage.
- [ ] GET collection, GET single, POST response, PUT response, validation
      response, error responses, and every other normal webhook HTTP response
      contain no plaintext secret, ciphertext, envelope marker, key ID,
      `has_secret`, mask, or equivalent secret-presence oracle.
- [ ] Test raw bodies against distinctive synthetic plaintext, ciphertext,
      envelope, and key-ID values.
- [ ] This is an intentional security breaking API change: clients can no
      longer retrieve webhook secrets. Do not add a compatibility endpoint,
      query parameter, mask, or opt-in plaintext mode.
- [ ] Preserve global authorization: every list/read/create/update/delete/
      validate operation requires `PermissionModifySystem`; no unprivileged
      user can read metadata, preserve, replace, clear, validate, or delete.
- [ ] Invalid request types are rejected cleanly without request-body, SQL,
      ciphertext, or internal error disclosure.

### Exact create and update semantics

- [ ] CREATE with non-empty secret:
      begin transaction; insert row with legacy null/empty and ciphertext empty;
      obtain immutable ID; encrypt using exact ID AAD; immediately decrypt and
      compare in memory; write ciphertext; read back exact storage; commit.
      Plaintext is never persisted and no incomplete row is externally visible.
- [ ] CREATE with absent, JSON null, or present empty string succeeds as a valid
      no-secret webhook with both secret states empty.
- [ ] UPDATE with non-empty secret encrypts using active key and the existing
      immutable ID, verifies and replaces ciphertext atomically, and keeps
      legacy storage empty.
- [ ] UPDATE with secret absent or JSON null preserves existing ciphertext
      byte-for-byte. It does not decrypt/re-encrypt, and a no-secret webhook
      remains no-secret.
- [ ] UPDATE with explicitly present `secret:""` clears existing ciphertext
      atomically. After commit both secret columns are empty and subsequent
      delivery uses the historical empty-key HMAC behavior.
- [ ] Name, URL, and active-state updates preserve/clear/replace according to
      only the explicit secret tri-state above and cannot make ciphertext
      undecryptable.
- [ ] Model writes and deletes use explicit transactions, expected row counts,
      exact in-transaction state verification, and concurrency predicates so
      MySQL matched-but-unchanged updates succeed only after proving the row
      still matches while missing/conflicting rows fail closed.
- [ ] Concurrent preserve, clear, replace, delete, and metadata updates cannot
      resurrect an old secret, clear a replacement, move ciphertext, or commit
      an ambiguous state.

### Frontend behavior and explicit revocation

- [ ] Edit never prefills the secret input; the browser never fetches the
      plaintext or ciphertext.
- [ ] Ordinary create/edit submits no `secret` property until the user explicitly
      chooses to set or clear the secret.
- [ ] The UI supports create with a new secret and create without a secret.
- [ ] The UI provides an accessible, explicit revocation action/state that sends
      present empty `secret:""` only after deliberate user intent; blank untouched
      input alone must not silently clear.
- [ ] The UI supports replacing a secret with a non-empty value and clearly
      distinguishes untouched, replacement, and explicit removal.
- [ ] Failure/retry preserves user intent without accidentally converting
      preserve into clear or exposing secret values.
- [ ] No plaintext, ciphertext, key ID, or envelope marker appears in webhook
      GET responses, DOM, browser storage, console, serialized webhook objects,
      or reusable client state.
- [ ] Regenerate only canonical affected frontend assets. Two clean builds are
      byte-identical and no unrelated generated assets remain changed.

### HMAC and delivery boundary

- [ ] Keep the current HMAC algorithm and wire format exactly:
      HMAC-SHA256 over the exact existing JSON bytes and
      `X-Gophish-Signature: sha256=<lowercase hex>`.
- [ ] A migrated or freshly encrypted secret produces exactly the same signature
      as legacy plaintext for the same secret and payload.
- [ ] A no-secret webhook performs no decrypt and preserves the historical
      empty-key HMAC signature/header exactly; do not omit the header.
- [ ] Carry ciphertext/identity until immediately before HMAC use. Decrypt to a
      short-lived local value and avoid attaching plaintext to a persistent,
      serializable, shared, or loggable webhook model.
- [ ] Validation with absent/null secret uses the authorized stored secret;
      explicit empty validates no-secret behavior; non-empty uses the supplied
      value only in memory and never persists or echoes it.
- [ ] Successful replace/rotation changes signatures to the new secret; the old
      secret no longer signs that webhook.
- [ ] Unknown key ID, missing/wrong keyring, malformed/tampered/oversized
      ciphertext, wrong record ID, wrong tag, or invalid state fails before
      `http.Client.Do` or any outbound network activity.
- [ ] Deterministic tests assert outbound request count equals zero on every
      credential failure and that other active webhook deliveries remain
      correctly isolated according to existing `SendAll` behavior.
- [ ] Preserve existing timeout, redirect, HTTP status, goroutine delivery,
      response handling, and one-error logging behavior. There is no retry
      engine; do not add or broadly refactor one.
- [ ] Crypto errors are not misclassified as remote HTTP failures and cannot
      create loops or duplicate delivery attempts.
- [ ] Logs/errors never contain plaintext, full ciphertext/envelope, key bytes,
      sensitive request bodies, or newly exposed HMAC material.

### Explicit offline migration

- [ ] Add `--migrate-webhook-secrets` consistent with the existing Kingpin
      offline-action architecture. Do not add a subcommand framework.
- [ ] Documentation requires stopped writers, a restorable database backup, and
      a separately protected keyring backup before migration.
- [ ] Load the shared keyring once; validate supported backend/schema before
      mutation; then preflight all rows for valid unique positive IDs, length,
      null/empty handling, and unambiguous secret state.
- [ ] Valid already-encrypted rows decrypt with their exact ID AAD and remain
      byte-for-byte unchanged. No-secret rows remain without ciphertext.
- [ ] Legacy non-empty rows are encrypted with exact ID AAD, immediately
      decrypted/compared in memory, stored and read-back-verified, then cleared
      from legacy storage.
- [ ] A row with both legacy and ciphertext, invalid/duplicate ID, malformed or
      oversized data, wrong-key/tampered ciphertext, or any ambiguous state
      blocks the whole migration before writes.
- [ ] Valid encrypted, valid no-secret, and legacy rows may coexist across
      different rows; only legacy rows are transformed.
- [ ] The complete table transform is one transaction. Any failure at row N
      rolls back every preceding row and preserves recoverable source data.
- [ ] Re-running after success updates zero rows, changes no ciphertext, and
      reports only non-sensitive counts/identifiers.

### Explicit data rollback

- [ ] Add `--rollback-webhook-secrets` using the same backend/keyring checks and
      one table transaction.
- [ ] Valid ciphertext rows decrypt with exact ID AAD, then restore and verify
      the complete legacy secret before ciphertext is cleared.
- [ ] No-secret rows remain null/empty with no ciphertext.
- [ ] Ambiguous state, wrong/missing key, tamper, length/storage mismatch, or any
      row failure rolls back the entire operation without destroying ciphertext.
- [ ] Repetition is idempotent or a clear safe no-op.
- [ ] Data rollback never invokes Goose Down. After rollback, this migration's
      Down alone preserves all rows, IDs, sequence high-water mark, names, URLs,
      active states, and restored secrets for an old binary.

### Tests and evidence

- [ ] Freeze the legacy baseline in regression tests:
      schema/nullable secret, global permission model, create and update matrix
      for non-empty/empty/absent/null, API exposure baseline removed by target,
      frontend prefill removed, no-secret validity, empty-key HMAC, and absence
      of retry.
- [ ] Direct database assertions prove fresh/migrated secret rows have
      null/empty legacy storage, non-empty ciphertext without the plaintext
      substring, and exact decryption under ID-bound AAD.
- [ ] Direct database assertions prove create absent/null/empty and explicit
      update clear keep both secret states empty.
- [ ] Cover generated-ID atomicity, transaction interruption, preserve
      byte-identity, explicit clear, replace/rotation, metadata changes,
      concurrency conflicts, delete races, record-copy AAD failure, wrong key,
      missing key ID, malformed/tampered ciphertext, and fail-before-network.
- [ ] Cover all API methods and response secrecy, tri-state decoder behavior,
      global permission enforcement, validate preserve/clear/inline-secret
      behavior, and non-sensitive errors.
- [ ] Cover browser create with/without secret, blank edit, ordinary preserve,
      explicit clear, replacement, metadata changes, active state, delete,
      failure/retry intent, and absence from network/DOM/storage/console.
- [ ] Use only local deterministic `httptest` or loopback webhook servers.
      Verify legacy/encrypted/rotated/no-secret signatures and zero outbound
      requests on all credential failures. Tests must never contact external
      endpoints.
- [ ] Real SQLite lifecycle covers legacy DB, schema Up, migration, runtime,
      idempotence, failure rollback, data rollback, guarded Down, old-binary
      compatibility, fresh DB, and sequence preservation after deleted max ID.
- [ ] Real MySQL lifecycle covers the same data/schema behavior, strict and
      non-strict storage limits/truncation, no-op row counts, and atomic failure.
- [ ] Re-run important IMAP and SMTP encrypted lifecycle/API/browser/container
      tests; webhook work must not regress PRs #54/#55.
- [ ] Existing credential foundation unit/race/fuzz behavior and envelope format
      remain unchanged.

### Validation, documentation, and delivery

- [ ] Create `docs/WEBHOOK_SECRET_ENCRYPTION.md` covering threat, legacy and new
      API/update matrices, optional/clear behavior, HMAC contract, shared
      keyring, backups, stopped writers, schema upgrade, offline migration,
      verification, runtime, rollback ordering, old-binary compatibility,
      PostgreSQL exclusion, key loss, Docker, and future rotation.
- [ ] Update API/user documentation for the write-only secret, explicit
      preserve/null/clear/replace contract, no-secret HMAC behavior, and security
      breaking change.
- [ ] Keep webhook URL credentials/userinfo/query tokens out of scope and record
      their audit/redaction/policy as a separate backlog item.
- [ ] `./scripts/verify.sh`, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, and `go build ./...` pass.
- [ ] Run targeted SQLite and real MySQL lifecycle, API, browser, local webhook,
      Docker, bounded foundation fuzz, IMAP/SMTP regression, and frontend
      reproducibility checks.
- [ ] Run `govulncheck`, `gosec`, Gitleaks, actionlint, zizmor, Yarn audit,
      Retire.js, and dependency-delta review. Classify pre-existing findings
      honestly; add no blanket suppression or hidden failure.
- [ ] Docker image and tests reuse one read-only external keyring and prove fresh
      secret/no-secret writes, legacy migration, preserve/replace/clear,
      deterministic local HMAC delivery, wrong-key/tamper/AAD failure with zero
      outbound request, rollback, and absence of a baked keyring.
- [ ] Dependencies remain unchanged; affected frontend assets rebuild
      reproducibly.
- [ ] Formal self-review documents every security/behavior criterion, legacy
      matrix, scanner baseline, and any pre-existing finding versus regression.
- [ ] Every commit is a signed Conventional Commit with a why-focused body,
      required Goal role marker/trailers where applicable, and verified GOOD
      signature. Stop on signing failure.
- [ ] Push only after full local gates. PR description documents every field
      required in the user specification, including threat, matrices, HMAC,
      migration/rollback, zero-outbound evidence, backends, frontend, Docker,
      dependencies, scanners, and formal self-review.
- [ ] Merge only after Goal Inspector PASS, independent code and security PASS,
      all CI green, GitHub-verified signatures, no conflict, and no unresolved
      review conversation.
- [ ] After merge, switch to `main`, fast-forward from `origin/main`, verify a
      clean tree, and stop without starting `events.details` or API-key hashing.

## Scope Boundaries

**In scope:**
- `webhooks.secret` authentication material only.
- Security-breaking removal of webhook secret read exposure.
- User-confirmed tri-state update semantics: absent/null preserve, explicit empty
  clear, non-empty replace.
- Preservation of create-without-secret and empty-key HMAC delivery.
- Directly coupled transaction, concurrency, storage-integrity, permission, and
  schema-identity fixes required to encrypt and mutate the secret safely.
- SQLite/MySQL schema and lifecycle, API/frontend, HMAC boundary, tests, Docker,
  CI, documentation, and formal review.

**Out of scope:**
- Webhook URLs containing userinfo/query tokens; webhook headers; broader
  delivery/retry refactors.
- IMAP, SMTP, `events.details`, user API keys, DSN/config, temporary admin
  passwords, TLS key material, or any other secret family.
- KMS/Vault, new cryptographic primitives/dependencies, changes to
  `internal/credentials`, PostgreSQL schema creation, bulk key rotation.
- Starting the next secret-remediation PR after merge.

## Applicable Project Conventions

**Quality gate commands:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- `corepack yarn test:browser`
- Targeted SQLite/MySQL, Docker, fuzz, and security-scanner commands required
  above.

**Commit convention:**
- Signed Conventional Commits using `git commit -S`, with the reason in the body.
- Builder/Inspector `[B]`/`[I]` markers required by the Goal workflow.
- `Assisted-by: Claude:Sonnet-4.6` for Builder and
  `Assisted-by: Claude:Haiku-4.5` for Inspector.
- `Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.
- Verify every signature with WSL-native Git/GPG and require `GOOD`.

**Guidelines:**
- `CLAUDE.md`
- `SECURITY.md`
- `CONTRIBUTING.md`
- `.github/PULL_REQUEST_TEMPLATE.md`
- `docs/CREDENTIAL_ENCRYPTION_FOUNDATION.md`
- `docs/IMAP_CREDENTIAL_ENCRYPTION.md`
- `docs/SMTP_CREDENTIAL_ENCRYPTION.md`
- `.goals/encrypt-imap-credentials/goal.md`
- `.goals/encrypt-smtp-credentials/goal.md`
- No `AGENTS.md`, `CONSTITUTION.md`, `.agents/guidelines`, or
  `.github/guidelines` exist.

**Rules:**
- Never commit or push directly to `main`; one security branch and PR.
- No secret in code, tests, fixtures, examples, images, logs, or workflows;
  synthetic unmistakably fake test values only.
- Tests cannot send external HTTP requests or real email.
- Never weaken security or hide scanner/test failures.
- Explicit, reversible, fail-closed migrations; stop on ambiguity/data-loss risk.
- No new dependency is needed or allowed for this goal.
- Preserve Action SHA pinning, minimal permissions, timeouts, and CI protections.
- Full local gate, independent reviews, green CI, formal self-review, verified
  signatures, and no unresolved thread before merge.
