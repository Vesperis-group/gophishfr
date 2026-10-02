# Goal: encrypt-event-details

## Status
IMMUTABLE after creation. Sole acceptance contract for Builder and Inspector.
Neither may alter it.

## Repository
/home/killian/gophishfr
Branch: security/encrypt-event-details
Base: main @ (see status.json initial_sha = d54fd4ea8c55008f204138f8239cbc8529db94ed)

## Context (confirmed facts from prior read-only audit, not to be re-litigated)

- `models.Event` (`models/campaign.go`): `Id int64` (DB autoincrement primary
  key), `CampaignId int64`, `Email string`, `Time time.Time`, `Message
  string`, `Details string`. `Details` is a JSON-encoded
  `models.EventDetails{Payload url.Values, Browser map[string]string}` for
  opened/clicked/submitted/reported events, a JSON-encoded `EventError{Error
  string}` for send-failure events, or empty/absent for a plain "Email Sent"
  event.
- DB column `events.details` is `BLOB` in BOTH SQLite
  (`db/db_sqlite3/migrations/20160131153104_0.1.2_add_event_details.sql`) and
  MySQL (`db/db_mysql/migrations/20160131153104_0.1.2_add_event_details.sql`).
  Standard MySQL `BLOB` caps at 65,535 bytes (64 KiB). No later migration
  changed this type.
- There is NO `db/db_postgres` migration directory anywhere in this
  repository. PostgreSQL has only a driver-level connection implementation
  (`models/models.go`, `postgresConnectionConfig`) and a synthetic-migration
  integration test (`models/database_test.go`,
  `TestPostgresDriverIntegration`) — NOT a real application schema. Event
  details encryption migration tooling must NOT claim PostgreSQL support.
- `models.AddEvent(e *Event, campaignID int64)` (`models/campaign.go`)
  currently: sets `CampaignId`/`Time`, loads active webhook endpoints,
  calls `webhook.SendAll(whEndPoints, e)` (delivers the full `*Event` —
  including plaintext `Details` — as JSON over HTTPS to configured webhook
  URLs), THEN `db.Save(e)`. Webhook delivery happens BEFORE persistence and
  is explicitly out of scope: this exact ordering and plaintext-to-webhook
  behavior must be preserved unchanged.
- `models.Result.createEvent` (`models/result.go`) is the sole constructor of
  every `Event` with non-nil details: `json.Marshal(details)` →
  `e.Details = string(dj)` → `AddEvent(e, r.CampaignId)`.
- No code anywhere (confirmed by exhaustive search) performs a SQL
  `WHERE`/`LIKE`/`JSON_EXTRACT` or any search/sort/aggregation on the
  `details` column. All campaign statistics use the separate `Result.Status`
  column. This is why whole-blob encryption is viable with zero query
  compatibility impact — do not introduce field-level encryption or a
  dedicated table; that was explicitly evaluated and rejected in the prior
  audit.
- `GET /api/campaigns/{id}` and `GET /api/campaigns/{id}/results` serialize
  `Campaign`/`CampaignResults` directly to JSON, including `Events []Event`
  (`"timeline"` JSON field) with `Details string` verbatim. No redaction
  exists today and none is to be added — the API response SHAPE must remain
  byte-for-byte identical (same fields, same meaning: `Details` must still
  contain the plaintext JSON string after decryption, transparently).
- Frontend (`static/js/src/app/campaign_results.js`) does `JSON.parse(event.
  details)` directly, with no try/catch visible, to render the timeline, to
  power the "Replay Credentials" feature (reconstructs and submits a hidden
  HTML form from `details.payload`, field by field, in plaintext — an active
  product feature requiring exact reversible plaintext), and to build a CSV
  export (`exportAsCSV` with scope `"timeline"` via Papa.unparse, including
  raw `Details`). If the API ever returned a non-JSON ciphertext envelope
  string in `Details`, this would throw and break timeline rendering
  entirely. Decryption MUST happen transparently server-side before JSON
  serialization; the frontend contract must not change.
- `internal/credentials` (already in production for IMAP/SMTP/webhook
  secrets) provides AES-256-GCM via `Cipher.Encrypt(ctx Context, plaintext
  []byte) (Envelope, error)` / `Cipher.Decrypt(ctx Context, env Envelope)
  ([]byte, error)`. `Envelope` is a self-describing text string
  (`gophishfr-cred:v1:keyID:nonceB64:ciphertextB64`), safe to store in any
  TEXT/BLOB column, bounded to 10 MiB by the package itself (not the binding
  constraint here — MySQL's historical 64 KiB BLOB is). `Context{Kind, Table,
  Column, OwnerID, RecordID}` is generic AAD binding, already reusable
  without modification. The key ID is readable from the envelope's plaintext
  prefix without decrypting, enabling old-key inventory by string parsing
  alone.
- `GOPHISHFR_CREDENTIAL_KEYRING_FILE` is the existing keyring environment
  variable already used for IMAP/SMTP/webhook secrets. Reusing the same
  physical keyring for event-details with a distinct `Context.Kind` is
  domain separation / context binding via AAD — it is NOT independent
  cryptographic key separation. A compromise of the keyring file compromises
  every domain using it. This distinction must be stated accurately in code
  comments, commit messages, and docs — never overstate the isolation this
  provides.
- No retention/purge policy exists anywhere in the product (confirmed by
  exhaustive search). Encrypting at rest does not solve indefinite retention
  of captured credentials. This PR does not implement retention; it only
  creates a backlog note (see Documentation requirements).
- Prior precedent in this repository for exactly this kind of migration
  (legacy-plaintext-column + new-ciphertext-column + migration tool +
  rollback + rotation) exists for IMAP, SMTP, and webhook secrets
  (`db/db_sqlite3/migrations/20260903000000_encrypt_imap_credentials.sql`,
  `20260903010000_encrypt_smtp_credentials.sql`,
  `20260903020000_encrypt_webhook_secrets.sql`, and their MySQL
  counterparts, plus the corresponding `models/*_credentials*.go` and test
  files). Follow established repository conventions from those precedents
  wherever they still apply; deviate explicitly and only where `events`'
  different volume/online-writers profile genuinely requires it (see below).
- UNLIKE those prior migrations (dozens of rows, config-at-rest, safe to
  migrate in one transaction with writers effectively idle), `events` is
  written continuously by the live mailer worker and the phishing server
  24/7, and may hold a materially larger number of rows. A single global
  transaction over the whole table is explicitly rejected by this goal.
  Online migration, batched, with writers active throughout, is required.

## Objective (single, exact)

Encrypt `Event.Details` at rest for SQLite and MySQL, with zero functional
change to the API response shape, frontend contract, CSV export, "Replay
Credentials" feature, or webhook delivery (payload, timing, or ordering).

## Explicitly out of scope (must NOT be touched)

- Modifying "Replay Credentials" behavior.
- Minimizing, truncating, or removing captured password/username/form-field
  values (no data minimization decision is made by this PR).
- Modifying webhook delivery (payload shape, timing, ordering, or the fact
  that it still transmits plaintext — that boundary decision already exists
  and is unrelated to DB-at-rest protection).
- Changing the `GET /api/campaigns/*` response shape in any way visible to
  a client (no new fields such as `details_ciphertext`, `key_id`,
  `has_encrypted_details`, etc. ever appear in a response).
- Changing the frontend contract.
- Touching API-key authentication, session authentication, RBAC, or CSRF.
- Touching query/form/raw-Authorization API-key transport behavior or the
  0.13.0 removal timeline.
- Addressing the `moment`/`fast-uri` frontend advisories (separate backlog).
- Implementing a retention/purge policy (only note it as backlog).
- Implementing or deciding credential-capture data minimization (only note
  it as backlog).
- Adding any new Go or JS dependency. `go.mod`, `go.sum`, `package.json`,
  `yarn.lock` must be byte-identical to the base commit.
- Claiming or implementing real PostgreSQL application-schema support for
  this migration. State explicitly, in code/docs/tests, that PostgreSQL is
  unsupported for this feature because no real application migration exists
  for it at all.

## Required design (binding, not a menu of options)

### Crypto

Reuse `internal/credentials` entirely unmodified unless a genuine blocker is
found and explicitly justified in the self-review (not assumed). AES-256-GCM,
existing `GOPHISHFR_CREDENTIAL_KEYRING_FILE` keyring (same physical keyring as
IMAP/SMTP/webhook — explicitly document this as domain separation via AAD
context binding, NOT independent key separation; a keyring compromise
compromises every domain sharing it).

`Context` for every event-details envelope:
```
Kind     = "event-details"
Table    = "events"
Column   = "details_ciphertext"
RecordID = event ID (decimal string; the DB-assigned, immutable primary key)
OwnerID  = campaign ID, ONLY if you can prove it is immutable for the
           lifetime of the row (an Event's CampaignId is set once in AddEvent
           and never updated afterward — confirm this directly in code before
           relying on it; if any doubt remains, leave OwnerID empty rather
           than binding to a value that is not provably immutable)
```
Never place in AAD: application version, event type/message, timestamp,
email address, campaign name, or any payload content — these are either
mutable-adjacent or redundant with RecordID/OwnerID and this goal forbids
widening the AAD surface.

### Schema

SQLite: add `details_ciphertext BLOB NULL`. Keep legacy `details` column,
nullable, throughout the transition/migration/rollback window.

MySQL: add `details_ciphertext MEDIUMBLOB NULL` — explicitly NOT a standard
`BLOB`. Standard `BLOB` caps at 64 KiB; the base64-encoded, framed envelope
of a legacy plaintext near that historical cap can exceed it. This must be
tested explicitly with a payload sized close to the legacy 64 KiB limit,
proving the resulting envelope fits in `MEDIUMBLOB` and round-trips exactly.

PostgreSQL: no migration file, no schema change, no claim of support
anywhere in code/docs/tests for this feature.

### Plaintext size bound (binding, for rollback compatibility)

New `Event.Details` plaintext written AFTER this feature ships must remain
bounded to the historical legacy MySQL `BLOB` capacity (64 KiB) — even though
the new column is `MEDIUMBLOB` and could hold far more. This is required
specifically so that `--rollback-event-details` can always write any
post-migration event back into the legacy `BLOB` column without truncation.
An oversized new `Details` payload must be REJECTED before persistence (no
silent truncation), with a clear, bounded error — determine and document the
exact enforcement point (likely in `createEvent` or `AddEvent`) and the exact
bound used (informed by the credentials-package envelope overhead and the
historical 64 KiB legacy cap — the plaintext bound must leave room so the
resulting envelope itself still fits comfortably, with margin, under 64 KiB
so a rollback-written legacy BLOB never overflows).

### New-write path (binding, exact sequence)

Because `Event.Id` is DB-autoincrement and the AAD binds to it, a two-phase
write is required for every new Event with non-empty Details, inside one DB
transaction:
1. Insert the event row without plaintext `details` persisted (shell /
   `details` left NULL — never write new plaintext to the legacy column).
2. Obtain the assigned ID.
3. Build `Context` with `RecordID` = that ID.
4. Encrypt the JSON-marshaled details.
5. Write `details_ciphertext`.
6. Read back the exact bytes just written and decrypt them, comparing to the
   original plaintext, before committing (read-back verification, consistent
   with the IMAP/SMTP/webhook precedent).
7. Commit. Any failure at any step rolls back the whole transaction — no
   durable shell row with neither legacy details nor ciphertext may survive
   a failed write.
An event with nil/empty details (e.g. "Email Sent") must NOT have an
artificial empty-string ciphertext created — preserve the EMPTY state
(`details` NULL/empty AND `details_ciphertext` NULL) exactly as today's NULL/
empty contract, tested explicitly for both NULL and "" historical
representations.

### Webhook ordering (binding, unchanged)

`webhook.SendAll` must continue to receive the plaintext `*Event` exactly as
today, before the encrypted write — same payload shape, same timing, same
ordering relative to persistence. Do not encrypt before the webhook call. Do
not log `Details`/credentials anywhere in this path, migration tooling,
rotation tooling, or their error paths — on any failure, report only the
event ID and an error class, never the plaintext or the envelope.

### Read path (binding)

For a MIGRATED row: ciphertext → decrypt → `Event.Details` plaintext in
memory → identical API/JSON serialization as today. Frontend, CSV export,
and Replay must work with zero code changes on their side. The envelope
string, key ID, or any ciphertext-related metadata must NEVER be exposed via
API, logs, or any other channel.

If ciphertext is PRESENT but decryption FAILS (wrong key, unknown key id,
tampered bytes, wrong/stale AAD) this is a hard failure: NEVER fall back to
reading legacy plaintext in that scenario, even if legacy plaintext happens
to still be present in the row (a `BOTH`-like inconsistency at read time must
not silently prefer one side). Return an appropriate server error without
leaking crypto internals; document and test exactly what the API response
looks like in this case, and whether/how one corrupted event affects
`GetCampaign`'s ability to return the rest of the campaign's events
(availability — do not silently drop the row without it being an explicit,
tested, documented decision).

### Transition states (binding, exact taxonomy)

```
EMPTY    : details NULL/"" (preserve exact historical NULL vs "" semantics)
           AND details_ciphertext NULL
LEGACY   : details non-empty, details_ciphertext NULL
MIGRATED : details cleared (NULL), details_ciphertext non-empty
BOTH     : details non-empty AND details_ciphertext non-empty
INVALID  : details_ciphertext present but malformed/undecryptable, or any
           other inconsistent combination
```
`BOTH` must never be silently accepted as a stable runtime state. It may
exist only transiently during the batch migration window (see below) and
must be resolved (verified-equal → clear legacy; different → STOP/report,
never auto-choose) before finalization.

### Migration mode / cutover marker (binding)

Define an explicit, durable (restart-surviving, non-secret) migration-state
marker distinguishing: PRE-CUTOVER (no ciphertext writing yet — should not
occur once this binary is deployed, since new writes are ciphertext-only
from first deploy), MIGRATING (new writes ciphertext-only; legacy rows from
before this deploy may still exist and are read-compatible as a bounded,
detectable, finalizable transition — NOT a silent permanent fallback), and
FINALIZED (legacy plaintext read support is removed; a LEGACY row found at
runtime after finalization is treated as a fail-closed error condition, not
silently served). Implement this with a durable mechanism appropriate to
this repository's existing conventions (e.g. a schema/metadata marker table
or column analogous to patterns already used for the credential migrations)
— do not rely solely on a volatile CLI flag with no persistent record of
finalization status.

From the moment the new binary is deployed, ALL new writes must be
ciphertext-only (verify this directly — no code path may persist new
plaintext `details` after this PR, even before migration/finalization).
Document the required operator sequence explicitly: deploy new binary (new
writes ciphertext-only, legacy reads still supported) → run the batch
migration command → verify (preflight) → finalize. Document that all
writer processes/replicas must run the new binary before migration starts;
mixed old/new binaries writing concurrently during migration is explicitly
unsupported in v1 — no automatic distributed coordination is required, just
clear documentation of the constraint operators must enforce themselves.

### Batch migration tool (binding)

A command (name it clearly, e.g. `--migrate-event-details` on whatever
existing CLI/tooling convention this repo uses for the credential
migrations) that:
- Processes rows ordered by `events.id ASC`.
- Uses a bounded, internally-reasonable batch size (do not invent an
  arbitrary precise number without justification; pick something sane and
  explain the reasoning — e.g. balancing transaction size against lock
  duration on a live, actively-written table).
- Commits one transaction per batch (never the whole table in one
  transaction).
- Is idempotent and resumable: the authoritative source of truth for "is
  this row still pending" must be the actual row state (`details_ciphertext
  IS NULL AND details IS NOT NULL/non-empty`), not merely an ID cursor
  checkpoint, since batches can be partial. A cursor MAY be used as a
  resume-position optimization but must never be trusted alone to imply
  "everything below this ID is migrated" without the row-state check — be
  explicit about this and test an interruption/resume scenario.
- Never loads the whole table into memory.
- For each LEGACY row in a batch: read exact plaintext bytes → validate
  state → encrypt with that row's own ID as `RecordID` → write ciphertext →
  read back the exact ciphertext bytes just written → decrypt → compare to
  the original plaintext exactly → only then clear the legacy `details`
  column → commit the batch. Any single-row failure rolls back that batch
  (or that row's portion per your transaction design — document precisely
  which) and is reported, not silently skipped and not fatal to the whole
  migration run.
- Malformed JSON in a legacy row must NOT block encryption of the row: the
  goal explicitly distinguishes CRYPTO-MIGRATABLE BYTES (any non-empty byte
  sequence that fits the size bound) from APPLICATION-VALID JSON. Encrypt
  the legacy bytes faithfully regardless of JSON validity, while separately
  flagging/reporting that specific event ID as application-malformed for
  operator visibility. Never destroy or silently drop a row's historical
  data because its JSON happens to be invalid.
- Encountering `BOTH` for a row mid-migration: decrypt the ciphertext and
  compare byte-for-byte (after decoding/re-encoding equivalently) to the
  legacy plaintext. If they represent the same logical content, the row may
  be finalized (legacy cleared). If they differ, STOP processing that row,
  report its ID, and do not guess which value is authoritative.
- A row whose `details_ciphertext` is present but fails to decrypt
  (INVALID) must never be "fixed" by falling back to legacy plaintext; it
  must be reported and left for operator investigation.
- Progress output is limited to: batch count, rows processed, last ID
  processed, remaining-count/estimate. NEVER plaintext, Details, or envelope
  content in any migration output, on success or failure.
- Finalization preflight: before marking FINALIZED, verify globally (a real
  query, not an assumption) that zero rows remain in LEGACY, zero in BOTH,
  zero in INVALID. Only then set the durable marker to FINALIZED. Document
  precisely how an operator (or a test) can verify "no legacy plaintext
  remains" after finalization (an explicit, reproducible query/check).

### Rollback tool (binding)

A command (e.g. `--rollback-event-details`), batched like the migration tool,
that for each MIGRATED row: decrypts the ciphertext → writes the plaintext
back into the legacy `details` column → reads back and compares exactly →
only then clears `details_ciphertext` → commits per batch. This allows
reverting to a binary that only understands legacy plaintext. This is
precisely why the plaintext size bound above (rollback compatibility) is
binding, not optional.

### Down migration guard (binding)

Refuse to `DROP details_ciphertext` (goose Down) while any row has a
non-NULL `details_ciphertext`, and refuse it if the migration marker
indicates FINALIZED without rollback having been run first (i.e., don't let
a schema rollback silently discard the only remaining copy of migrated
data). No silent data loss path.

### Bulk key rotation (binding)

A command (e.g. `--rotate-event-details-credentials`) that, batched:
identifies rows whose envelope key ID is not the active key (inventoried by
parsing the envelope's plaintext key-ID prefix — NEVER by decrypting first
just to count) → for each such row: decrypt with the old key → re-encrypt
with the active key (same `Context`/AAD) → read back and decrypt-compare →
commit per batch. Provide a read-only inventory/preflight capability (count
by key ID, via envelope parsing only, no decryption) so an operator can
verify "count for old key ID = 0" before considering that key retired.
Lazy-rotate-on-read MAY be added in addition (optional, keep simple if
added) but must NOT be the only mechanism — bulk rotation is mandatory
because event rows are read far less often than, say, API-key verifier rows,
so lazy-only could leave an old key referenced indefinitely.

### Availability / error behavior (binding)

Re-confirm and preserve existing owner-scoped access control
(`GetCampaign`/`GetCampaignResults` already filter by `user_id`) — encryption
must not widen or narrow who can read events. Determine and document/test
precisely what `GetCampaign` does today if one event row is unreadable
(decrypt failure) — does the whole campaign request fail, or is the row
skipped/replaced with an error marker? Do not silently drop a row without
this being an explicit, tested, documented behavior; do not let one
corrupted row silently take down visibility of an entire campaign's history
if a narrower failure mode is reasonable — but do not invent new behavior
beyond what's needed to safely handle this case without a plaintext
fallback.

## Non-functional constraints

- No new Go or JS dependency; `go.mod`, `go.sum`, `package.json`, `yarn.lock`
  byte-identical to base.
- No real-time sleeps in tests; use realistic small datasets and direct
  function calls / short-lived batches rather than simulating huge volume
  literally, but DO test the interruption/resume scenario concretely (e.g.
  migrate N rows, simulate a crash/stop, restart the command, verify
  correct resumed completion and final state).
- Must keep the full existing test/lint/scanner suite green: gofmt,
  golangci-lint, go vet, go build, go test, go test -race, govulncheck,
  gosec, gitleaks, actionlint, zizmor, yarn audit, retire, browser tests,
  Docker compatibility tests, Ansible bootstrap test, the full
  `./scripts/verify.sh`.
- MySQL must be tested in both strict and non-strict SQL modes (consistent
  with the existing SMTP credential encryption precedent in this repo) to
  prove no silent truncation ever passes undetected; read-back byte-exact
  verification is the enforcement mechanism, not reliance on strict mode
  alone.
- No RBAC/session/CSRF/API-key-transport behavior change anywhere in the
  diff.

## Git / commit requirements (binding, same as prior PRs in this repo)

- Every commit's Author and Committer must be exactly
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- Every commit must be GPG-signed GOOD with key
  `EB3E931B95880FB8931299E59DFC16E2E234C67B`.
- Zero occurrences of "copilot" (case-insensitive) anywhere in any commit
  message, trailer, author, or committer field.
- Only trailer allowed: `Assisted-by: Claude:<model>`.
- Conventional commit messages with `[B]`/`[I]` markers per this
  repository's established `.goals/` convention.

## Documentation requirements

Create/update an appropriate doc under `docs/` covering: threat model,
why whole-blob encryption (not field-level, not a dedicated table), the
exact `Context`/AAD fields and explicit domain-separation-not-independent-
key-separation framing, MySQL `MEDIUMBLOB` rationale and the legacy-size
plaintext bound for rollback compatibility, the full transition-state
taxonomy and migration/finalization/rollback/rotation operator procedure,
the explicit mixed-binary-version constraint during migration, the
PostgreSQL-unsupported statement, webhook ordering/plaintext preservation
(unchanged, documented as a pre-existing, separate decision), and two
explicit backlog notes (not implemented here): (1) captured-credential
retention/purge policy, (2) review of Replay Credentials / captured-password
retention as a potential future data-minimization decision.

## Acceptance criteria (the Inspector verifies ALL of these)

1. `EVENT_DETAILS_AT_REST`: new events are stored ciphertext-only; legacy
   rows become ciphertext-only after migration + finalization.
2. `NEW_WRITE_PLAINTEXT` = NONE: no code path persists new plaintext
   `details` after this PR, proven by test.
3. `WHOLE_BLOB`: the entire `Details` JSON blob is encrypted as a unit; no
   field-level encryption, no dedicated table introduced.
4. `MYSQL_CIPHERTEXT_CAPACITY`: `details_ciphertext` is `MEDIUMBLOB`;
   proven by test with a payload near the historical 64 KiB legacy cap
   round-tripping exactly through migration.
5. `AAD_RECORD_BINDING`: `Context.RecordID` = event ID; a copy-attack test
   (ciphertext from event A written into event B's row/decrypt attempt)
   fails AAD authentication; a tamper test (1 byte flipped) fails.
6. `TRANSITION_BOUNDED`: migration mode is explicit, durable across
   restart, and finalization is a real, checked gate (not assumed).
7. `INVALID_CIPHERTEXT_FALLBACK` = NONE: a present-but-undecryptable
   ciphertext never results in a legacy-plaintext read, proven by test.
8. `BATCH_MIGRATION` / `RESUME`: batched, resumable (proven via an
   interruption/restart test), idempotent, ordered by ID, row-state is the
   authoritative pending/done signal (not cursor alone).
9. `FINALIZATION_GATE`: a real preflight check (zero LEGACY/BOTH/INVALID)
   gates the FINALIZED marker; proven by test.
10. `ROLLBACK`: batched rollback tool restores legacy plaintext correctly
    and clears ciphertext only after verified round-trip; Down migration
    refuses to drop the column while ciphertext rows remain.
11. `BULK_ROTATION` / `OLD_KEY_RETIREMENT`: bulk re-encryption works,
    old-key inventory is provable via envelope-prefix parsing without
    decryption, proven by test.
12. `API_REGRESSION` / `REPLAY_REGRESSION` / `CSV_REGRESSION` /
    `WEBHOOK_REGRESSION` = NONE: proven by end-to-end tests using synthetic
    data only (captured username/password fixtures must be synthetic,
    never real-looking production-style secrets beyond what's needed for a
    test).
13. `PLAINTEXT_LOGGING` = NONE: no application log, migration tool output,
    rotation tool output, or error message anywhere contains Details
    content, the plaintext credential, or the envelope string. Only event
    ID and an error/state class are ever reported.
14. `DEPENDENCY_DIFF` = NONE: `go.mod`/`go.sum`/`package.json`/`yarn.lock`
    byte-identical to base.
15. `FULL_VERIFY` = PASS: the full gate suite passes, including SQLite,
    MySQL strict, and MySQL non-strict scenarios.
16. PostgreSQL is explicitly, honestly documented and tested as unsupported
    for this feature (no fabricated migration, no false claim anywhere).
17. Git identity/signature requirements are met for every commit in the
    range.
18. Documentation is added per the Documentation requirements section,
    including the two explicit backlog notes (retention, minimization) —
    noted, not implemented.
19. Concurrent-writer-during-migration behavior is proven safe: a new event
    inserted while migration is running is ciphertext-only from the start
    and is never mistaken for a pending-migration LEGACY row.
20. `Event.Details` mutability after creation is explicitly checked in code
    (not assumed) and documented; if it is in practice immutable after
    creation, state that plainly; if any mutation path exists, the
    migration/rollback/rotation tooling must account for it safely (e.g. via
    a state-consistent read-modify-write) rather than assuming immutability
    blindly.
