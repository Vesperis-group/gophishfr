# Event details encryption operations

GophishFR encrypts `events.details` (the captured credential/browser payload
recorded for opened/clicked/submitted/reported events, and the error message
recorded for a failed send) at rest, on both SQLite and MySQL, using the same
AES-256-GCM envelope primitive ([`internal/credentials`](../internal/credentials))
already in production for IMAP/SMTP/webhook secrets. This document covers the
threat model, the exact design decisions, and the full operator procedure.

## Threat model

This protects a database file, dump, snapshot, or backup obtained without
the separately stored keyring from exposing captured credentials and browser
metadata. It does **not** protect:

- A compromised running process (the application itself must decrypt to
  serve the API and the "Replay Credentials" feature, so a live RCE can read
  plaintext the same way the application does).
- A host that can read both the database and the keyring file.
- The existing webhook delivery path: `webhook.SendAll` is called with the
  plaintext `*Event` before this feature's encrypted write, exactly as
  before this change, and still transmits `Details` in plaintext over
  HTTPS to configured webhook URLs. That boundary decision already existed
  and is unrelated to DB-at-rest protection; it is explicitly unchanged and
  out of scope here.
- Indefinite retention of captured credentials -- see "Backlog" below.

## Why whole-blob encryption, not field-level or a dedicated table

No code anywhere in this repository performs a SQL `WHERE`/`LIKE`/
`JSON_EXTRACT` or any search/sort/aggregation on the `details` column; every
campaign statistic uses the separate `Result.Status` column. Because the
column is never queried by content, encrypting the entire JSON blob as one
opaque unit has zero query-compatibility cost, while field-level encryption
or a dedicated table would add schema and application complexity for no
benefit. This was evaluated and rejected prior to implementation; this
feature intentionally does not introduce either.

## Context / AAD design

Every event-details envelope is built with:

```
Kind     = "event-details"
Table    = "events"
Column   = "details_ciphertext"
OwnerID  = CampaignId (decimal string)
RecordID = the event's own database-assigned, immutable primary key (decimal string)
```

No other field (Email, Time, Message, or any payload content) is ever
included in the associated data: each is either mutable-adjacent or
redundant with RecordID/OwnerID, and widening the AAD surface was explicitly
rejected.

**`CampaignId` immutability was verified, not assumed** (acceptance criterion
20): an exhaustive search of this repository shows the *only* write path
that ever touches the `events` table is `models.AddEvent`, which is always
called with a freshly constructed `*Event` whose `Id` is the Go zero value,
so every call performs a SQL `INSERT`, never an `UPDATE`. No other function
anywhere issues an `UPDATE`/`Save` against an existing event row. `CampaignId`
and `Details` are therefore both set exactly once, at creation, and never
mutated afterward -- see the doc comment on `models.Event` and on `AddEvent`
in `models/campaign.go`. This is what makes binding `CampaignId` into the AAD
as `OwnerID` safe: if any doubt had remained, `OwnerID` would have been left
empty per the binding goal, since a mutable field used as AAD would make a
row's ciphertext impossible to decrypt as soon as that field changed.

**Domain separation, not key separation.** Event details reuse the exact
same physical keyring as IMAP/SMTP/webhook secrets
(`GOPHISHFR_CREDENTIAL_KEYRING_FILE`). `Context.Kind = "event-details"` is
*domain separation via AAD context binding*: it ensures an event-details
envelope can never be mistaken for, or substituted into, an IMAP/SMTP/
webhook row (or vice versa), because the AAD would not match. It is **not**
independent cryptographic key separation -- a compromise of the shared
keyring file compromises every domain using it, including this one. This
distinction is stated here, in code comments on `eventDetailsContext`, and
in the commit message, deliberately, so it is never overstated.

## Schema

| Backend | Column | Rationale |
| --- | --- | --- |
| SQLite | `details_ciphertext BLOB NULL` | SQLite `BLOB` has no size cap. |
| MySQL | `details_ciphertext MEDIUMBLOB NULL` | **Not** a standard `BLOB`. |

The legacy `details` column is kept, nullable, on both backends throughout
the transition/migration/rollback window (migration
`db/db_sqlite3/migrations/20260903040000_encrypt_event_details.sql` and its
MySQL counterpart).

**Why `MEDIUMBLOB`, proven by test:** the historical MySQL `BLOB` cap is
65,535 bytes (`events.details` has used `BLOB` since 2016, unchanged since).
A legacy row written by an older binary could itself be close to that
65,535-byte cap, since no application-level bound existed before this
feature. Once base64-framed into a v1 envelope (`ciphertext+16-byte GCM tag`
inflated ~4/3, plus framing), the resulting envelope for such a row is
larger than 64 KiB -- too large for a standard `BLOB`, which would silently
truncate it during migration. `MEDIUMBLOB` (16 MiB) comfortably holds it.
`models/event_details_credentials_test.go`'s
`TestEnvelopeNearLegacyBlobCapExceedsStandardBlob` proves this precisely: it
encrypts a near-65,535-byte payload and asserts the resulting envelope length
*exceeds* the historical BLOB cap, then proves it round-trips exactly.

PostgreSQL has no migration file, no schema change, and no claim of support
anywhere in code, tests, or this document for this feature: there is no
`db/db_postgres` migration directory anywhere in this repository (only a
driver-level connection implementation and a synthetic integration test),
and `ValidateEventDetailsBackend` rejects every database name except
`sqlite3` and `mysql`.

## Plaintext size bound (new writes only, binding for rollback compatibility)

**New** `Event.Details` plaintext, written by this binary from the moment
this feature ships, is bounded to **44 KiB (45,056 bytes)** --
`eventDetailsPlaintextMaxBytes` in `models/event_details_credentials.go`. An
oversized value is rejected with `ErrEventDetailsTooLarge` *before*
persistence (no silent truncation, no partial write -- see the two-phase
write sequence below).

This bound exists specifically so `--rollback-event-details` can always
write *any* post-migration event's decrypted plaintext back into the legacy
`details BLOB` column (on both backends, since the legacy column's type
never changes) without truncation. 44 KiB was chosen with margin in two
directions, both verified by a compile-time assertion in the same file
(`_ = uint(eventDetailsLegacyBlobCapacityBytes - eventDetailsPlaintextMaxBytes)`
and an analogous one for the envelope):

1. **Rollback-write margin:** 65,535 − 45,056 = 20,479 bytes (~31%) of slack
   against the legacy `BLOB` cap the restored plaintext is written into.
2. **Envelope margin (defense in depth):** even though the new
   `details_ciphertext` column is `MEDIUMBLOB` (no practical size concern),
   the resulting *envelope* for a 44 KiB plaintext, with a worst-case
   64-byte key ID, is still computed to stay comfortably under 64 KiB too
   (~8% margin) -- so even a future accidental use of a standard `BLOB`
   column for an event-details envelope would not silently truncate.

This bound applies **only** to new writes (enforced in
`persistEventWithEncryptedDetails`, the sole body of `AddEvent`'s database
write -- see below). Legacy rows already in the database, written by an
older binary with no such bound, are migrated as-is: the migration tool
never rejects a legacy row for being "too big"; it is bounded only by
`internal/credentials`' own 10 MiB envelope limit and the `MEDIUMBLOB`
column's 16 MiB capacity.

## New-write path (exact two-phase sequence)

`Event.Id` is DB-autoincrement and the AAD binds to it, so every new event
with non-empty `Details` is written in one DB transaction
(`persistEventWithEncryptedDetails` in `models/event_details_credentials.go`,
the sole body of `AddEvent`'s write):

1. Insert the event row with **both** legacy detail columns forced to SQL
   `NULL` (never persisting new plaintext in the legacy column -- see the
   design note in the source for why this is a `Create` + immediate
   in-transaction `NULL`-out rather than a literal raw `INSERT`, which this
   repository's `jinzhu/gorm` version cannot combine with a portable
   last-insert-ID read).
2. Obtain the DB-assigned autoincrement ID.
3. Build `Context` with that ID as `RecordID` (`CampaignId` as `OwnerID`).
4. Encrypt the JSON-marshaled `Details`.
5. Write `details_ciphertext`.
6. Read back the exact bytes just written and decrypt them, comparing to the
   original plaintext, before committing (read-back verification, matching
   the IMAP/SMTP/webhook precedent).
7. Commit. Any failure at any step rolls back the whole transaction: no
   durable shell row with neither legacy details nor ciphertext survives a
   failed write.

An event with nil/empty `Details` (e.g. "Email Sent") never gets an
artificial ciphertext: both columns are left `NULL`, preserving the EMPTY
state exactly. (Historical rows may show `details` as `NULL` *or* `""`
depending on when they were created relative to the original 2016
`add_event_details` migration; both representations are treated identically
as EMPTY on read -- tested explicitly.)

Webhook delivery (`webhook.SendAll`) is still called with the exact
plaintext `*Event`, before this encrypted write, with the same payload
shape, timing, and ordering as before this feature. No code path logs
`Details`, the plaintext credential, or the envelope string anywhere in this
write path, the migration/rotation tooling, or their error paths -- only the
event ID and a short error class are ever reported
(`classifyEventDetailsError`).

## Transition-state taxonomy

```
EMPTY    : details NULL/"" AND details_ciphertext NULL
LEGACY   : details non-empty, details_ciphertext NULL
MIGRATED : details cleared (NULL), details_ciphertext non-empty
BOTH     : details non-empty AND details_ciphertext non-empty (transient only)
INVALID  : details_ciphertext present but malformed/undecryptable
```

`BOTH` may exist only transiently during the batch migration window (another
writer concurrently inserting while a migration or rollback run is also in
flight on older/other rows) and is always resolved before finalization --
verified-equal rows are finalized (legacy cleared); mismatched rows are
reported and left untouched for operator investigation, never guessed at.

## Migration mode / cutover marker

A durable, restart-surviving marker table,
`event_details_migration_state` (single row, `id = 1`), distinguishes:

- **PRE-CUTOVER**: before this PR's schema migration has applied (no marker
  row exists). This is not a state the running application code ever
  observes in practice, since `models.Setup` always applies pending
  migrations before any other model code runs; it exists in the documented
  taxonomy for completeness, since the row is seeded the moment the
  migration applies.
- **MIGRATING** (the seeded default): new writes are ciphertext-only from
  this binary's first deploy; legacy rows from before this deploy may still
  exist and are read-compatible.
- **FINALIZED**: set only by `--finalize-event-details`, only after its
  preflight proves zero LEGACY, zero BOTH, and zero INVALID rows remain
  (`FinalizeEventDetailsMigration` in `models/event_details_migration.go`).
  Once finalized, a LEGACY row encountered at read time is a fail-closed
  error (`ErrEventDetailsFinalized`), never silently served.

**From the moment the new binary is deployed, all new writes are
ciphertext-only** -- `AddEvent` is the sole write path for the `events`
table (verified by exhaustive search, see above), and
`persistEventWithEncryptedDetails` never leaves new plaintext in the legacy
column, proven by `TestAddEventNeverPersistsNewPlaintext`.

**Operator sequence:**

```sh
# 1. Deploy the new binary everywhere. New writes are ciphertext-only;
#    legacy reads are still supported. ALL writer processes/replicas must
#    be on the new binary before step 2 -- mixed old/new binaries writing
#    concurrently during migration is explicitly unsupported in v1; there
#    is no automatic distributed coordination, only this documented
#    operator constraint.
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
./gophishfr --config ./config.json   # normal startup, writers active

# 2. Run the batch migration (online: writers may remain active).
./gophishfr --config ./config.json --migrate-event-details

# 3. Verify (preflight) -- re-run step 2 until it reports zero rows
#    processed, then confirm with the reproducible query below.
./gophishfr --config ./config.json --migrate-event-details

# 4. Finalize.
./gophishfr --config ./config.json --finalize-event-details
```

**Reproducible "no legacy plaintext remains" check** (same query
`countEventDetailsStates` uses internally):

```sql
SELECT
  SUM(CASE WHEN details_ciphertext IS NULL AND details IS NOT NULL AND details <> '' THEN 1 ELSE 0 END) AS legacy_rows,
  SUM(CASE WHEN details IS NOT NULL AND details <> '' AND details_ciphertext IS NOT NULL THEN 1 ELSE 0 END) AS both_rows,
  SUM(CASE WHEN (details IS NULL OR details = '') AND details_ciphertext IS NOT NULL THEN 1 ELSE 0 END) AS migrated_rows
FROM events;
```

(INVALID additionally requires an actual decrypt attempt per ciphertext row,
which only the application -- holding the keyring -- can perform; this is
exactly what `--finalize-event-details`'s preflight does, batched, before
setting the marker.)

## Batch migration tool (`--migrate-event-details`)

Unlike the IMAP/SMTP/webhook credential precedents (dozens of rows,
config-at-rest, safe to migrate in one offline transaction), `events` is
written continuously by the live mailer worker and phishing server, and may
hold a materially larger number of rows. A single global transaction is
explicitly rejected; this tool is **online** and **batched**.

- **Processing order:** `events.id ASC`.
- **Batch size: 500 rows** per read query (`eventDetailsMigrationBatchSize`).
  This balances transaction/lock footprint against round-trip count for a
  live, actively-written table: large enough that a multi-million-row
  backlog does not take an impractical number of round trips, small enough
  that no single unit of work meaningfully contends with the steady trickle
  of concurrent single-row `AddEvent` inserts.
- **Transaction granularity:** every row is encrypted, verified, and
  committed in its **own** transaction -- never a transaction shared across
  a whole batch. "Batch" here means only the bounded unit of rows read into
  memory per read query; a single row's failure only ever rolls back that
  row, is reported (`EventDetailsRowFailure{EventID, Reason}`), and never
  aborts the rest of the batch or the run.
- **Idempotent and resumable:** the authoritative "is this row still
  pending" signal is the row's own current state (`details_ciphertext IS
  NULL AND details` non-empty for LEGACY; both columns populated for BOTH),
  re-queried fresh on every invocation -- never a remembered ID cursor.
  `LastID` is reported purely as an operator progress signal.
  `TestMigrateEventDetailsBatchResumesAfterInterruption` proves this
  concretely: it hand-encrypts two of four seeded legacy rows (simulating a
  crash mid-migration), then calls `MigrateEventDetailsBatch` fresh and
  confirms it finds and finishes exactly the two still-pending rows.
- **Never loads the whole table into memory**: every scan (pending rows,
  BOTH-state rows, the INVALID-ciphertext preflight scan, the key-ID
  inventory, rotation) is itself paginated in `eventDetailsMigrationBatchSize`
  chunks.
- **Malformed JSON never blocks encryption:** a legacy row's bytes are
  encrypted faithfully regardless of JSON validity (CRYPTO-MIGRATABLE BYTES
  is a strictly weaker condition than APPLICATION-VALID JSON); a malformed
  row is additionally reported via `MalformedJSON []int64` for operator
  visibility, and its historical data is never destroyed or dropped.
- **BOTH-state rows** are decrypted and compared byte-for-byte against the
  legacy plaintext: verified-equal rows are finalized (legacy cleared);
  mismatched rows are left exactly as found and reported
  (`mismatched-both`), never auto-resolved.
- **Progress output** is limited to batch count, rows processed, last ID,
  and failure IDs with a short error class -- never plaintext, `Details`, or
  envelope content, in success or failure (`logEventDetailsBatchResult` in
  `main.go`).

## Finalization gate (`--finalize-event-details`)

A real, global, reproducible query (`countEventDetailsStates`) -- not an
assumption -- counts LEGACY, BOTH, and INVALID rows (the last via a batched
decrypt-attempt scan). Only if all three are exactly zero does
`FinalizeEventDetailsMigration` set the durable marker to `FINALIZED`.
`TestFinalizeEventDetailsMigrationGatesOnRealPreflight` proves both the
rejection (one LEGACY row present) and the success path, including that the
marker is left unchanged by a rejected attempt.

## Rollback tool (`--rollback-event-details`)

Batched exactly like the migration tool (500-row read batches, one
transaction per row): for each MIGRATED row, decrypts the ciphertext, writes
the plaintext back into the legacy `details` column, reads back and compares
exactly, and only then clears `details_ciphertext`, committing per row. This
is precisely why the plaintext size bound above is binding, not optional:
every MIGRATED row's plaintext was bounded at write time (new writes) or is
whatever a legacy row already was (migrated rows, bounded in practice by the
historical MySQL cap), so the restored legacy `BLOB` write never overflows.

A rollback that fully empties the MIGRATED/BOTH sets (re-verified by a fresh
query) also resets a `FINALIZED` marker back to `MIGRATING`: `FINALIZED`
asserts "zero legacy rows exist", which a completed rollback deliberately
makes false again, so leaving a stale `FINALIZED` marker afterward would be
inaccurate.

## Down migration guard

The Goose Down for `20260903040000_encrypt_event_details.sql` refuses to
drop `details_ciphertext` while **any** row has non-`NULL`
`details_ciphertext`, and separately refuses it while the durable marker
reads `finalized` (forcing an explicit `--rollback-event-details` run
first). Both checks use the same collide-on-primary-key guard-table
mechanism as the existing webhook/SMTP/API-key-verifier Down migrations: a
duplicate-key `INSERT` aborts the whole Down statement sequence if either
condition is detected.

## Bulk key rotation (`--rotate-event-details-credentials`,
`--inventory-event-details-keys`)

Old-key rows are inventoried by **parsing the envelope's plaintext key-ID
prefix only** (`gophishfr-cred:v1:<key-id>:...`) -- **never by decrypting
first just to count** (`InventoryEventDetailsKeys` /
`envelopeKeyIDPrefix`). `--inventory-event-details-keys` is a read-only
preflight an operator uses to confirm "count for old key ID = 0" before
retiring that key.

`--rotate-event-details-credentials` then: for each row whose parsed key ID
is not the active key, decrypts with the old key, re-encrypts with the
active key (the same `Context`/AAD -- `Table`/`Column`/`OwnerID`/`RecordID`
are unchanged by rotation), reads back and decrypt-compares, and commits per
row. Rows already on the active key are reported `Unchanged`, not
re-rotated. `internal/credentials.Cipher` deliberately does not expose its
own active key ID (keeping its surface minimal), so the active key ID is
passed as an explicit parameter, sourced from the same `Keyring` `main.go`
already loads for `loadCredentialCipher` -- no change to
`internal/credentials` was needed.

Lazy-rotate-on-read was deliberately **not** added: event rows are read far
less often than, for example, API-key verifier rows, so a lazy-only
mechanism could leave an old key referenced indefinitely. Bulk rotation is
the mandatory mechanism; a future lazy-rotate-on-decrypt addition remains
possible without changing this.

## Availability / error behavior on read

`GetCampaign`/`GetCampaignResults` keep their existing owner-scoped access
control (`user_id` filtering) unchanged -- encryption neither widens nor
narrows who can read events.

**Decision (explicit, tested, documented):** if one event's ciphertext is
present but fails to decrypt (wrong key, unknown key ID, tampered bytes,
wrong/stale AAD -- including copy-attack across rows), or a row is found in
the invalid `BOTH` state, or a `LEGACY` row is found after finalization,
that single event's `Details` is replaced, in memory, with a bounded,
non-leaking JSON sentinel:

```json
{"error":"event details unavailable"}
```

(the same shape as the pre-existing `EventError` struct, so the unchanged
frontend's `JSON.parse(event.details)` never throws). The event ID and a
short error class are logged; **the rest of the campaign's events are still
returned** -- one corrupted row never takes down visibility of an entire
campaign's history. This is implemented once, centrally, in
`decryptEventsInPlace` (`models/event_details_credentials.go`), called from
both `Campaign.getDetails()` and `GetCampaignResults`, and proven by
`TestDecryptEventsInPlaceIsolatesCorruptedRow`.

A present-but-undecryptable ciphertext **never** falls back to reading
legacy plaintext that might still be in the row (the `BOTH`-state check in
`decryptEventDetails` rejects that combination outright) --
`TestDecryptEventDetailsNeverFallsBackFromInvalidCiphertext` and
`TestDecryptEventDetailsBothStateIsInvalid` prove this.

## API / frontend / Replay Credentials / CSV export (unchanged)

`GET /api/campaigns/{id}` and `GET /api/campaigns/{id}/results` continue to
serialize `Event.Details` as a plaintext JSON string, decrypted
transparently server-side -- the response shape is byte-for-byte identical
to before this feature (no `details_ciphertext`, `key_id`,
`has_encrypted_details`, or any other new field is ever added to a
response; `DetailsCiphertext` on the Go struct is tagged `json:"-"`). The
frontend's `JSON.parse(event.details)` for timeline rendering, the "Replay
Credentials" hidden-form-submission feature, and the CSV export via
Papa.unparse all continue to work with zero frontend code changes.

## Concurrent-writer-during-migration safety

A new event inserted by `AddEvent` while `--migrate-event-details` is
running is ciphertext-only from the instant it is created (its legacy
`details` column is `NULL`), so the migration's own pending-row query
(`details_ciphertext IS NULL AND details` non-empty) never selects it --
it is never mistaken for a pending LEGACY row.
`TestConcurrentWriteDuringMigrationIsNeverMistakenForLegacy` proves this by
seeding one pre-existing legacy row, writing one new event via `AddEvent`
mid-"migration-window", then confirming a migration run processes exactly
the one pre-existing legacy row and leaves the new event's ciphertext
untouched and intact.

## MySQL strict/non-strict testing

Consistent with the existing SMTP credential encryption precedent, read-back
byte-exact verification -- not reliance on strict SQL mode -- is the
enforcement mechanism that catches silent truncation.
`TestMySQLEventDetailsNearLegacyCapRoundTripsStrictAndNonStrict` (skipped
unless `GOPHISHFR_MYSQL_TEST_DSN` is set, matching the existing
IMAP/SMTP/webhook test convention) migrates a legacy-style event whose
plaintext is near the historical 65,535-byte MySQL `BLOB` cap into the real
`details_ciphertext` `MEDIUMBLOB` column and decrypt-compares the result
byte-for-byte, once under the default strict session and once under an
explicitly disabled non-strict session.

## Backlog (not implemented here)

1. **Captured-credential retention/purge policy.** No retention/purge policy
   exists anywhere in this product today (confirmed by exhaustive search).
   Encrypting at rest does not solve indefinite retention of captured
   credentials; this PR does not implement one.
2. **Replay Credentials / captured-password retention review.** Whether
   captured usernames/passwords/form fields should ever be minimized,
   truncated, or expired -- a potential future data-minimization decision --
   is explicitly out of scope here and noted only as backlog.
