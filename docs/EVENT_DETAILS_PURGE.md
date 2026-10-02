# Event details purge operations

This document covers `--purge-event-details-before`, the operator-triggered,
manual, irreversible tool for clearing `events.details` /
`events.details_ciphertext` for old events. It is a direct follow-on to
[`EVENT_DETAILS_ENCRYPTION.md`](EVENT_DETAILS_ENCRYPTION.md) (the PR that
introduced the `details_ciphertext` column and the encrypted-at-rest write
path) and assumes familiarity with that document's threat model, schema, and
transition-state taxonomy.

**GophishFR does not automatically purge Event.Details in this version.
There is no built-in 30/60/90-day default. The operator selects a cutoff
explicitly on every invocation.** Automatic/scheduled retention and
credential-capture minimization are both deferred to a separate future PR;
neither is implemented here.

## What this is -- and is not

This is **logical deletion**, not forensic/secure erasure. Running the real
purge issues a plain `UPDATE events SET details = NULL, details_ciphertext =
NULL WHERE ...` for every eligible row. It does **not** guarantee immediate
physical removal from SQLite free pages, MySQL storage pages, filesystem
snapshots, disks, or backups:

- A backup or snapshot taken **before** a purge may still contain the
  cleared ciphertext. As long as the corresponding credential keyring file
  still exists, that backup remains decryptable if restored. This tool does
  not and cannot retroactively affect any backup already taken.
- This tool never rotates, deletes, or otherwise retires any credential
  key, and makes no "crypto-shredding" claim. The credential keyring is
  shared across IMAP/SMTP/webhook/event-details domains
  (`Context.Kind`-separated, not key-separated -- see
  `EVENT_DETAILS_ENCRYPTION.md`'s "Domain separation, not key separation"
  section) -- purging data here has no effect whatsoever on that keyring or
  on any other domain's ciphertext.
- Webhooks: purging the local database has **zero retroactive effect** on
  data already delivered to webhook receivers. `webhook.SendAll` is called
  with the plaintext `*Event` at write time, before encryption, exactly as
  documented in `EVENT_DETAILS_ENCRYPTION.md`; this tool cannot reach back
  into a receiver's own storage.
- Downloaded CSV exports: purging the database does not revoke or affect
  any CSV file an operator has already downloaded via "Export Events" (see
  "API / CSV contract" below).

## CLI contract

```
gophishfr --purge-event-details-before <RFC3339 timestamp> [--dry-run | --yes] [--include-active-campaigns]
```

- **`--purge-event-details-before`**: a single RFC3339 timestamp, e.g.
  `2026-07-01T00:00:00Z`. Parsing uses Go's `time.RFC3339`, which requires
  an explicit numeric offset or a trailing `Z` -- an offset-less value such
  as `2026-07-01T00:00:00` is rejected (`log.Fatal`) rather than silently
  assumed to be UTC or local time. The parsed cutoff is immediately
  normalized to UTC, matching `Event.Time`'s own storage convention
  (`time.Now().UTC()` in `AddEvent`), so the eligibility comparison is
  always apples-to-apples regardless of what offset the operator typed.
- **`--dry-run`**: runs the exact same eligibility query as a real purge,
  writes nothing, and reports only non-secret aggregate counters (see
  "Output" below).
- **`--yes`**: required for any real (non-dry-run) purge.
- Exactly one of `--dry-run` or `--yes` is required whenever
  `--purge-event-details-before` is given:
  - Neither flag: hard refusal (`log.Fatal`, "one of --dry-run or --yes is
    required"). There is no silent default to either mode.
  - Both flags: also a hard refusal (ambiguous operator intent is never
    silently resolved in either direction).
  - `--purge-event-details-before` absent, `--dry-run`/`--yes` given alone:
    simply "no purge requested", not an error -- these flags are only
    meaningful together with a cutoff.
- **`--include-active-campaigns`**: optional, off by default. See
  "Eligibility" below.
- This action participates in the same mutual-exclusivity check as every
  other credential/event-details CLI action in `main.go`
  (`--migrate-event-details`, `--rollback-event-details`, etc.): it is
  irreversible and destructive, exactly like `--rollback-event-details`, so
  combining it with any other action in one invocation is a hard refusal.
- `ValidateEventDetailsBackend(conf.DBName)` is called (and `log.Fatal`s on
  error) whenever `--purge-event-details-before` is set, for both dry-run
  and a real purge -- PostgreSQL and any other backend are rejected exactly
  like every other event-details tool. **This tool needs no credential
  cipher/keyring at all**: it never decrypts anything (see "Never
  decrypts" below), so it runs even with no
  `GOPHISHFR_CREDENTIAL_KEYRING_FILE` configured.
- This adds **no new HTTP route**. It is operator CLI tooling only, run
  directly on the host with filesystem/process access, exactly like
  `--migrate-event-details` and friends. No new RBAC surface is introduced.

### Output

Both a dry run and a real purge report only non-secret aggregate counters:
eligible/purged/skipped counts, oldest/newest eligible `Event.Time`, min/max
eligible `Event.Id`, distinct campaign count, and batch count. **`Details`,
`DetailsCiphertext`, any decoded/decrypted value, any envelope byte, and any
key ID are never logged** -- the same `PLAINTEXT_LOGGING = NONE` discipline
`EVENT_DETAILS_ENCRYPTION.md` already established.

## Eligibility

An `Event` row is eligible for purge if and only if **all** of:

1. `events.time < cutoff` (strict).
2. It currently has something to purge: `(details IS NOT NULL AND details
   <> '') OR details_ciphertext IS NOT NULL`. An already-empty row (both
   columns NULL, or legacy `''`/NULL and ciphertext NULL) is never counted
   as eligible and never touched -- re-running an identical purge after a
   successful run finds zero additional rows.
3. Unless `--include-active-campaigns` is set: the owning `Campaign`'s
   `Status == "Completed"`. This is a single
   `campaign_id IN (SELECT id FROM campaigns WHERE status = ?)` SQL
   condition, not a per-row decrypt-and-check loop. With
   `--include-active-campaigns`, events belonging to a non-Completed
   campaign become eligible too -- but `--yes` is still independently
   required; there is no confirmation bypass.
4. **BOTH-state rows (both legacy plaintext and ciphertext present) and
   INVALID-ciphertext rows (undecryptable) are eligible exactly like any
   other row meeting 1-3.** Purge never needs to decrypt or classify
   LEGACY vs MIGRATED vs BOTH vs INVALID to decide eligibility or to
   execute -- it always just clears both columns unconditionally for every
   row meeting 1-3.

**Dry-run uses EXACTLY the same eligibility query as the real purge** --
both call the single, shared `eventDetailsPurgeEligibilityClause` function
in `models/event_details_purge.go`; there is no second, parallel
implementation that could drift.

## Purge mechanics

- **Never decrypts.** The purge executes a plain `UPDATE events SET details
  = NULL, details_ciphertext = NULL WHERE id = ?` for every eligible row.
  `PurgeEventDetailsBefore`'s signature accepts no `*credentials.Cipher` at
  all -- decryption is structurally impossible in this code path, not
  merely avoided by convention.
- Only `details`/`details_ciphertext` are ever written. `Id`, `CampaignId`,
  `Email`, `Time`, and `Message` are never touched.
- **Idempotent and resumable**: every batch re-derives "what is still
  eligible" directly from current row state -- there is no remembered
  cursor anywhere. Re-running the exact same command with the exact same
  cutoff after a successful (or interrupted) run purges only whatever rows
  still match the eligibility query, exactly like
  `--migrate-event-details`'s established pattern.
- **Concurrency-safe**: safe to run online, concurrently with live
  `AddEvent` writes. A new event created during a purge run has
  `Time >= cutoff` (the cutoff is fixed before the run starts, and new
  events get `time.Now().UTC()` at or after that point) and is therefore
  never eligible. If a campaign (and its events) is deleted concurrently, a
  zero-rows-affected `UPDATE` for that row is tolerated cleanly and counted
  as "skipped", never as a hard failure.
- **Batching**: reuses the exact `eventDetailsMigrationBatchSize` (500)
  constant `--migrate-event-details` already validated, rather than
  inventing a second batch-size constant. Rows are read ordered `id ASC`
  with `LIMIT`; the whole eligible set is never loaded into memory at once.
  Unlike the migration tool's one-transaction-per-row design (justified
  there by its encrypt-then-verify round trip), this tool uses **one
  transaction per batch**: there is no encrypt/verify step here at all,
  only a conditionless column clear, so batching the transaction is
  simpler with no loss of row-level failure isolation -- a row that
  disappears concurrently just yields `RowsAffected = 0` for its own
  `UPDATE` within the batch, counted as skipped, while every other row in
  the same batch still commits normally.

## Query performance (no new index)

`events.time` has no index today, and this PR does **not** add one
speculatively. This is an occasional, operator-triggered, explicitly-
irreversible command -- not a hot/frequent/user-facing request path like a
per-request API query -- so an occasional full scan of `events` is
acceptable for this use case, and a schema change is not justified by this
tool alone. If real-world testing against a large dataset ever demonstrates
a genuine, measured blocking performance problem, that would need to be
measured and reported before any index is added -- not added preemptively
"just in case."

## Stats / API / Replay / CSV contract

- **Stats are provably unaffected.** `getCampaignStats` /
  `GetCampaignSummaries` / `GetCampaignSummary` query only the `results`
  table (`Status`, `Reported`) and never read `Event`/`Details` at all, so
  purging a campaign's event details cannot change any stats query's
  inputs. This is covered by a dedicated regression test
  (`TestPurgeEventDetailsBeforeDoesNotChangeCampaignStats`).
- **API**: after a purge, a purged event's `Details` serializes as `""`
  (empty string) in JSON -- **exactly** the same shape as any pre-existing
  event that legitimately never had `Details` (e.g. "Email Sent"). This
  tool introduces no `"purged": true` field, no new sentinel string, and no
  other new JSON shape: `Event`'s existing fields and `GetCampaign`/
  `GetCampaignResults`'s existing response shape are both unchanged.
- **Purged-state ambiguity is intentional, not a defect.** An empty
  `details` value is, by design, indistinguishable between "this event
  never had details" and "this event's details were purged". This
  deliberately avoids adding any new DB column or DTO field purely to
  disambiguate a state that has no functional consequence for the API,
  Replay, or CSV export -- all three already treat an empty `Details`
  identically regardless of why it is empty.
- **Replay ("Replay Credentials" button)**: `static/js/src/app/
  campaign_results.js`'s entire details-rendering block (device info, the
  Replay button, and the parameter table) is gated behind a single
  `if (event.details) { ... }` truthy check in `renderTimeline`. An empty
  string is falsy in JavaScript, so a purged event (`Details == ""`) hits
  exactly the same code path as a never-had-details event: the block does
  not execute, no `JSON.parse("")` call ever happens, no Replay button is
  ever rendered, and no exception is possible. **This was verified with a
  real, headless-Chromium browser test** (not just static analysis) --
  see "Frontend: zero code changes" below -- proving **no frontend code
  change was required** for this PR.
- **CSV export**: `exportAsCSV`'s `Papa.unparse(campaign.timeline, ...)`
  call serializes the raw event objects with no field filtering. A purged
  event's `details` field simply serializes as an empty CSV cell for that
  row; every other column (email, time, message) is intact. Verified by
  the same real-browser test, which captures the exact rows passed to
  `Papa.unparse` and asserts this directly, both before and after a purge.

## Frontend: zero code changes (empirically verified)

Goal item 0's hypothesis -- that the existing frontend already correctly
hides Replay and the details table for a purged event, with zero code
changes -- was verified with a real, headless-Chromium Playwright test
(`tests/browser/event-details-purge.spec.ts`, driven twice, before and
after a real purge, by `controllers/
browser_event_details_purge_test.go`'s `TestBrowserEventDetailsPurge`), not
merely re-derived from reading the source. The test:

1. Creates a synthetic campaign with one target and records a real
   "Submitted Data" event with a non-empty captured-credentials payload.
2. **Before** any purge: confirms the Replay button is rendered and
   functional (clicking it, confirming the URL prompt, and asserting the
   resulting form submission carries the real captured username).
3. Runs the real, direct Go-side purge (`models.PurgeEventDetailsBefore`)
   against that event.
4. **After** the purge: reloads the results page and asserts (a) no Replay
   button is rendered for that event, (b) no uncaught JS exception or
   console error occurs, (c) the event still appears in the timeline with
   its original `Message`/`Time`, and (d) the CSV export's row for that
   event has an empty `details` cell with every other column intact.

Both phases pass with `static/js/src/app/campaign_results.js` and every
other frontend file completely untouched. No frontend rebuild-and-diff
review (as would be required had a code change been necessary) applies to
this PR.

## Recommended operator procedure

This is documentation only -- none of the following steps are coupled in
code:

1. Take a backup per your organization's policy. (Remember: a backup taken
   before the purge may still retain the data -- see "What this is -- and
   is not" above.)
2. Run `--dry-run` with your intended cutoff and inspect the reported
   counts and oldest/newest eligible dates.
3. Run the real purge with `--yes` and the same cutoff.
4. Run `--dry-run` again with the same cutoff: it should now report zero
   eligible rows remaining.

As a purely operational suggestion (not a functional dependency, and not
coupled in code): running this purge **before** a large bulk key-rotation
run (`--rotate-event-details-credentials`, from
`EVENT_DETAILS_ENCRYPTION.md`) can reduce the number of rows that rotation
needs to re-encrypt, since purged rows no longer need rotation at all.

## Database backends

SQLite and MySQL only, both tested for real: a SQLite test exercises
dry-run, a real purge, idempotent re-run, simulated batch interruption and
resume, the stats regression, and the API-metadata-retained check; a real
MySQL integration test
(`TestMySQLEventDetailsPurgeDryRunAndRealPurgeStrictAndNonStrict`) exercises
the same dry-run/purge/idempotence flow against a real MySQL 8.4 instance
(disposable Docker container), in both the default strict SQL session and
an explicitly disabled non-strict session -- the same precedent/pattern as
`TestMySQLEventDetailsNearLegacyCapRoundTripsStrictAndNonStrict`.
PostgreSQL is rejected by `ValidateEventDetailsBackend` exactly like every
other event-details tool; this PR adds no PostgreSQL code path and makes no
PostgreSQL support claim. No schema migration was needed: `details`/
`details_ciphertext` already exist, and `NULL` is already a valid state for
both.

## Out of scope (deferred backlog)

- Automatic/scheduled retention (no cron, ticker, goroutine, startup purge,
  or any background trigger of any kind) -- and no default retention
  duration (30/60/90 days or otherwise). The cutoff is always
  operator-supplied and always explicit.
- Credential-capture minimization at the source (`controllers/phish.go`).
- Any "secure erase" / forensic-wipe implementation.
- `moment`/`fast-uri` dependency remediation, and removing query/form
  API-key transports (both separate, pre-existing backlog items).
- Any PostgreSQL support.
