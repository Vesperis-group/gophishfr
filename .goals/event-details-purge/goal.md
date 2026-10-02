# Goal: operator-triggered manual purge of Event.Details (security/event-details-purge)

This file is the immutable binding acceptance contract for this PR. The
Builder implements against it; the Inspector judges strictly against it and
nothing else. If any acceptance criterion conflicts with a convenience
shortcut, the criterion wins. If a criterion is found to be factually wrong
after re-validation against the current codebase, STOP and report the
conflict instead of silently reinterpreting it.

## 0. Context (confirmed facts from prior audits, PR #68, and this PR's
   pre-implementation re-validation)

- `Event` (models/campaign.go) has `Id`, `CampaignId`, `Email`, `Time`,
  `Message`, `Details string` (legacy column `details`), and
  `DetailsCiphertext []byte` (`gorm:"column:details_ciphertext", json:"-"`).
- `Event.Time` is set exactly once, in `AddEvent`
  (`persistEventWithEncryptedDetails`), at insert time, to
  `time.Now().UTC()`. No code path anywhere in this repository issues an
  `UPDATE events SET time = ...`. It is the correct, stable age signal for
  this PR.
- `events.details`/`events.details_ciphertext` are the exact two columns
  written by PR #68. Confirmed column names:
  `eventDetailsColumn = "details_ciphertext"` (event_details_credentials.go).
  Legacy column name is `details` (same struct tag / raw SQL throughout
  event_details_credentials.go and event_details_migration.go).
- Campaign status constants (models/models.go):
  `CampaignInProgress = "In progress"`, `CampaignQueued = "Queued"`,
  `CampaignCreated = "Created"`, `CampaignEmailsSent = "Emails Sent"`,
  `CampaignComplete = "Completed"`. **"Active" for this PR means
  `Campaign.Status != CampaignComplete`** (i.e. any of the other four
  states). There is no "archived" state anywhere in this codebase.
- `DeleteCampaign` (models/campaign.go) deletes `Result`, `Event`, `MailLog`
  rows by `campaign_id`, then the `Campaign` row itself. No DB-level FK
  cascades exist anywhere (plain integer columns only since the initial
  2016 schema). This PR does not touch `DeleteCampaign`.
- `getCampaignStats` (models/campaign.go) queries ONLY the `results` table
  (`Status`, `Reported` columns). It never reads `Event`/`Details` at all.
  This is the basis for the binding "stats must be bit-identical after
  purge" requirement below — purging `Details` provably cannot change any
  stats query's inputs.
- `decryptEventDetails`/`decryptEventsInPlace`
  (models/event_details_credentials.go) implement the EMPTY/LEGACY/
  MIGRATED/BOTH/INVALID taxonomy from PR #68. EMPTY (`!hasCiphertext &&
  !hasLegacy`) returns `("", nil)` with zero error and zero logging. A
  purged row (both columns cleared to SQL NULL) is indistinguishable, by
  construction, from a row that legitimately never had any Details (e.g.
  "Email Sent", "Email Opened", "Campaign Created" events) — this is
  intentional and accepted (see "Purged state ambiguity" below); it is NOT
  a defect to fix in this PR.
- Frontend: `static/js/src/app/campaign_results.js`'s `renderTimeline`
  function gates its ENTIRE details-rendering block (device info, the
  "Submitted Data" Replay button, and the parameter table) behind a single
  `if (event.details) { ... }` truthy check. An empty string
  (`event.details === ""`) is falsy in JavaScript, so this block does not
  execute at all when `Details` is empty: no `JSON.parse("")` call ever
  happens, no Replay button is ever rendered, and no exception is possible
  for that case. **Pre-implementation hypothesis**: because a purged event
  has `Details == ""` (the EMPTY state, exactly the same shape as a
  never-had-details event), the existing frontend ALREADY correctly hides
  Replay and the details table for purged events, with zero code changes
  required. **This hypothesis MUST be empirically verified by the Builder
  with a real browser test (not just re-read as static analysis) before
  being relied upon.** If verification disproves it (e.g. some other code
  path renders Replay without this guard), implement the explicitly-scoped
  minimal fix from goal.md item 10 below, with a full clean frontend
  rebuild and asset diff review per item 14.
- `exportAsCSV` (same file) calls `Papa.unparse(campaign.timeline, ...)`
  directly on the raw event objects with no field filtering. A purged
  event's `details` field will simply serialize as an empty CSV cell for
  that row — again, provably requiring no code change, to be confirmed by
  test.
- `GetCampaign` (line ~225) and `GetCampaignResults` (line ~462) both call
  `decryptEventsInPlace(events)` before returning. Both are therefore
  already correctly exercised by the existing EMPTY-state path for a
  purged event; this PR adds no new API shape.
- The batching pattern already validated and shipped in PR #68
  (`models/event_details_migration.go`): `eventDetailsMigrationBatchSize =
  500`, rows fetched ordered `id ASC` with `LIMIT`, one transaction PER ROW
  (not per batch), the batch loop re-queries "what is still eligible"
  directly from row state every iteration (no remembered cursor is the
  resumability authority) and stops when a full pass makes zero progress.
  This PR's purge tool reuses this exact pattern and, where reasonable,
  the existing `eventDetailsMigrationBatchSize` constant itself (do not
  invent a second, different batch size constant without a stated reason).
- `main.go` wires offline/online credential tools as mutually-exclusive
  `kingpin.Flag(...).Bool()` flags plus `conf.DBName` validation via
  `models.ValidateEventDetailsBackend` (rejects anything but `sqlite3`/
  `mysql`). This PR's new flags follow the same `kingpin` idiom.
- **The purge tool needs NO credential cipher/keyring at all.** It never
  decrypts anything (explicit requirement below). It must still call
  `models.ValidateEventDetailsBackend(conf.DBName)` to reject PostgreSQL
  honestly, exactly like every other event-details tool.
- `RequireAPIKey`/`EnforceViewOnly` middleware and `PermissionModifyObjects`/
  `PermissionModifySystem` RBAC exist for HTTP routes only. This PR adds NO
  new HTTP route — it is operator CLI tooling only, run directly on the
  host with filesystem/process access, exactly like
  `--migrate-event-details` etc. No new RBAC surface is introduced or
  needed.

If re-validation during implementation finds ANY of the above facts to now
be false, STOP and report the discrepancy before writing code that depends
on it.

## 1. Scope (binding)

### In scope
- A new CLI flag `--purge-event-details-before <RFC3339 timestamp>` that
  irreversibly clears `details` and `details_ciphertext` (to SQL NULL,
  matching the exact convention already used everywhere else in this
  column pair) for every `Event` whose `Time < cutoff` AND which currently
  has non-empty `Details` (legacy) and/or non-NULL `DetailsCiphertext`.
- `--dry-run`: runs the exact same eligibility query/logic, writes nothing,
  reports only non-secret counters (counts, oldest/newest eligible
  `Event.Time`, min/max eligible `Event.Id`, distinct campaign count).
- `--yes`: required for ANY real (non-dry-run) purge. Omitting both
  `--dry-run` and `--yes` when `--purge-event-details-before` is given is a
  hard refusal (`log.Fatal`), never a silent default to either mode.
- `--include-active-campaigns`: optional, off by default. When absent,
  events belonging to a campaign whose `Status != CampaignComplete` are
  excluded from eligibility entirely (not purged, not counted as eligible
  in dry-run). When present, those events become eligible too, but `--yes`
  is still separately required for a real run (no confirmation bypass).
- Batched purge using the PR #68 pattern: ordered `id ASC`, bounded page
  size (reuse `eventDetailsMigrationBatchSize` unless a documented reason
  requires a new constant), one transaction per row OR per small batch
  (pick one and justify it in a doc comment exactly as PR #68 did — either
  is acceptable here since there is no encrypt/verify round-trip per row,
  only a conditional UPDATE, so a per-batch transaction is likely both
  simpler and safe; decide and document).
- A frontend regression test (Playwright or equivalent, following existing
  `tests/browser/*.spec.ts` conventions) proving Replay/CSV behavior
  before and after a purge, per item 0's hypothesis. Implement the minimal
  JS fix from item 10 ONLY if the hypothesis is empirically disproven.
- Documentation: a new `docs/EVENT_DETAILS_PURGE.md` (or an addition to
  the existing `docs/EVENT_DETAILS_ENCRYPTION.md` backlog section — pick
  whichever keeps the existing doc coherent; a new file is likely cleaner
  given its own operator procedure) covering CLI usage, eligibility rules,
  the logical-deletion-vs-forensic-erasure distinction, backup/keyring
  caveats, webhook/CSV irrevocability, and the explicit "no default
  retention duration exists" statement.
- Tests: see item 12.

### Out of scope — DO NOT implement any of the following
- Automatic/scheduled retention (no cron, ticker, goroutine, startup
  purge, or any background trigger of any kind).
- Any default retention duration (30/60/90 days or otherwise) — the
  cutoff is ALWAYS operator-supplied and ALWAYS explicit.
- Deleting `Event` rows, `Result` rows, or `Campaign` rows.
- Field-level/partial parsing of the `Details` JSON payload (no new
  Markdown/JSON "understanding" logic — the purge treats `Details`/
  `DetailsCiphertext` as opaque blobs to be cleared, never inspected or
  parsed).
- Any change to credential capture (`controllers/phish.go` form handling).
- Any change to Replay's behavior BEFORE a purge (only the specific,
  minimal post-purge guard from item 10, and only if empirically needed).
- Any change to the webhook delivery contract or code.
- Any change to the AES-256-GCM algorithm, envelope format, or
  `internal/credentials` package.
- Destroying, rotating, or retiring any credential key.
- Any "secure erase" / forensic-wipe implementation or claim.
- `moment`/`fast-uri` remediation (separate P2/P3 backlog).
- Removing query/form API-key transports (deferred to 0.13.0 per existing
  deprecation doc).
- Any PostgreSQL support claim or migration.
- Any new Go or JS dependency. `go.mod`/`go.sum`/`package.json`/
  `yarn.lock` must be byte-identical to base after this PR.
- Any new DB schema/migration file, UNLESS implementation proves one is
  truly unavoidable — in which case STOP and report why before adding one
  (expected outcome: zero migrations, since `details`/`details_ciphertext`
  already exist and NULL is already a valid state for both).

## 2. CLI contract (binding)

- Flag name: `--purge-event-details-before`. Value: a single RFc3339
  timestamp string (e.g. `2026-07-01T00:00:00Z`). Reject (via `log.Fatal`,
  consistent with every other CLI validation failure in `main.go`):
  missing value when any purge-related flag is given but the cutoff flag
  itself is absent should simply mean "purge not requested" (kingpin
  `.String()` with no `.Default` so an empty/unset value is distinguishable
  from a provided empty string); an unparseable timestamp (`time.Parse`
  with `time.RFC3339` — reject formats lacking an explicit offset/`Z`, do
  not silently assume local time or UTC for an ambiguous/offset-less
  input).
- Mutual exclusivity: this new action must participate in the SAME
  mutual-exclusivity check `main.go` already applies to the other
  credential/event-details actions (`credentialActions > 1` →
  `log.Fatal`). Decide, and document in a code comment, whether the purge
  flag counts toward that shared counter (it almost certainly should,
  since it is irreversible and destructive, exactly like
  `--rollback-event-details`).
- `--dry-run` and `--yes` are independent boolean flags. Valid
  combinations when `--purge-event-details-before` is set:
  - `--dry-run` alone → read-only report, exit 0, nothing written.
  - `--yes` alone → real purge executes.
  - neither → `log.Fatal` with a clear message ("one of --dry-run or --yes
    is required").
  - both → treat as an operator error and `log.Fatal` (ambiguous intent;
    do not silently prefer one). Document this choice.
- `--include-active-campaigns` is a boolean flag, independent of the
  above, meaningful only when `--purge-event-details-before` is set.
- `ValidateEventDetailsBackend(conf.DBName)` must be called and must
  `log.Fatal` on error, exactly like the other event-details actions,
  whenever `--purge-event-details-before` is set (dry-run or real).
- Output (both dry-run and real purge): structured `log.Infof` lines with
  ONLY: eligible/purged/skipped counts, oldest/newest eligible
  `Event.Time`, min/max eligible `Event.Id`, distinct campaign count,
  batch count, and (for failures) event ID + a short non-leaking error
  class string (reuse `classifyEventDetailsError`-style conventions —
  this tool does not need every one of those classes, but must follow the
  SAME never-log-content discipline). NEVER log `Details`,
  `DetailsCiphertext`, any decoded/decrypted value, any envelope bytes, or
  any key ID.

## 3. Eligibility (binding)

An `Event` row is eligible for purge if and only if ALL of:
1. `events.time < cutoff` (strict, using the already-UTC `Event.Time`
   semantics established in PR #68 — the cutoff must be interpreted/
   compared consistently, document how timezone normalization of the
   parsed cutoff is handled, e.g. `.UTC()` immediately after
   `time.Parse`).
2. `(details IS NOT NULL AND details <> '') OR details_ciphertext IS NOT
   NULL` — i.e. the row currently has SOMETHING to purge. An
   already-empty row (both NULL, or legacy `''`/NULL and ciphertext NULL)
   is never counted as eligible, never touched, and never appears in
   dry-run counts.
3. Unless `--include-active-campaigns` is set: the owning `Campaign`'s
   `Status == CampaignComplete`. Implement this as a SQL join/subquery
   condition (`events.campaign_id IN (SELECT id FROM campaigns WHERE
   status = ?)` or an equivalent join), not a decrypt-and-check loop, and
   not a per-row Go-side lookup inside the hot loop unless profiling in
   this PR truly requires it (a single join condition is simplest and
   almost certainly sufficient at this scale — state the choice).
4. BOTH-state rows (both legacy and ciphertext present) and INVALID
   ciphertext rows (undecryptable) ARE eligible exactly like any other
   row meeting 1–3 — purge never needs to decrypt or classify BOTH vs
   LEGACY vs MIGRATED vs INVALID to decide eligibility or to execute: it
   always just clears both columns unconditionally for every row meeting
   1–3.

## 4. Purge mechanics (binding)

- NEVER decrypt. The purge executes a plain `UPDATE events SET details =
  NULL, details_ciphertext = NULL WHERE id = ?` (or an equivalent batched
  form) for every eligible row. No `cipher.Decrypt` call anywhere in this
  tool's code path.
- Preserve, unmodified: `Id`, `CampaignId`, `Email`, `Time`, `Message`.
  Only the two detail columns are ever written.
- Idempotence: re-running the exact same command with the exact same
  cutoff after a successful run must find and purge zero additional rows
  (the eligibility query directly re-derives "still has something to
  purge" from current row state — no separate, rememberable "already
  processed" marker is needed or should be added).
- Resumability: if interrupted mid-run (process killed, DB connection
  lost), a re-run with the same arguments must resume correctly, because
  the eligibility query is re-evaluated fresh each batch from row state,
  exactly like `MigrateEventDetailsBatch`'s established pattern — no new
  checkpoint/cursor table.
- Concurrency: the purge may run online, concurrently with live
  `AddEvent` writes and the mailer/phishing server. A new event created
  during a purge run has `Time >= cutoff` (since cutoff is an operator-
  supplied timestamp fixed before the run starts and new events get
  `time.Now().UTC()` which is necessarily at or after process start) and
  is therefore never eligible. If a campaign is deleted concurrently
  (removing an event this tool was about to process), tolerate a
  zero-rows-affected UPDATE for that ID cleanly (log it as "skipped: row
  no longer present" or fold it into the normal progress counters — do
  not treat it as a hard failure).
- Batching: reuse `eventDetailsMigrationBatchSize` (500) unless there is a
  documented reason not to. Never load the whole eligible set into memory
  at once.

## 5. Query performance (binding — explicit instruction: do not add an
   index speculatively)

- `events.time` has no index today (confirmed: no `CREATE INDEX` on that
  column in any migration). This PR must NOT add one speculatively.
- The Builder must explicitly reason about (and document in a code
  comment near the eligibility query, plus summarize in the PR
  description) whether a full scan of `events` for this operator-
  triggered, occasional, explicitly-irreversible command is acceptable
  given this is NOT a hot/frequent/user-facing request path (unlike, say,
  a per-request API query). The expected conclusion, absent contrary
  evidence found during implementation, is that an occasional full scan
  is acceptable for this use case and schema changes are not justified by
  this PR alone. If real testing (e.g. against a large synthetic dataset)
  demonstrates a genuine, measured blocking performance problem, STOP and
  report the measurement before adding any index or schema change —
  do not add one preemptively "just in case."

## 6. Stats/API/Replay/CSV contract (binding)

- `getCampaignStats`/`GetCampaignSummaries`/`GetCampaignSummary`: a test
  must prove byte-for-byte/value-for-value identical `CampaignStats`
  before and after a purge of a synthetic campaign's submitted-data
  events (since that query never reads `Details`, this is a regression
  guard, not new behavior).
- API (`GetCampaign`/`GetCampaignResults`): after purge, a purged event's
  `Details` must serialize as `""` (empty string) in JSON, EXACTLY the
  same as any pre-existing event that legitimately never had Details
  (e.g. "Email Sent"). Do NOT introduce `"purged": true`, a new field, a
  new sentinel string, or any other new JSON shape. Document explicitly,
  in the new doc file, that an empty `details` value is ambiguous between
  "never had details" and "was purged" by design, and that this is an
  accepted, intentional trade-off (not a defect) given the explicit
  "AUCUN nouveau champ DB" and "ne crée pas de nouveau DTO" constraints.
- Replay: see item 0's hypothesis and item 10's conditional fallback.
- CSV: see item 0's hypothesis; a test must prove a purged event's CSV
  row has an empty `details` cell and all other columns (email, time,
  message) intact.
- Webhooks: no code change. Add exactly one doc sentence (in the new doc
  file) stating that purging the local DB has zero retroactive effect on
  data already delivered to webhook receivers.
- Downloaded CSV exports: no code change. One doc sentence stating that
  purging the DB does not revoke or affect any CSV file an operator has
  already downloaded.

## 7. Security documentation requirements (binding — must appear in the
   new doc file, in plain unambiguous language, not just implied)

- This is **logical deletion**, not forensic/secure erasure. It does not
  guarantee immediate physical removal from SQLite free pages, MySQL
  storage pages, filesystem snapshots, disks, or backups.
- Old backups/snapshots taken before a purge may still contain the
  cleared ciphertext; as long as the corresponding credential keyring
  file still exists, those backups remain decryptable if restored. This
  PR does not and cannot retroactively affect existing backups.
- Do NOT rotate, delete, or otherwise retire any credential key as part
  of this PR, and do NOT claim any form of "crypto-shredding" benefit
  from this purge tool. The credential keyring is shared across IMAP/
  SMTP/webhook/event-details domains (Context.Kind-separated, not
  key-separated) — this must be stated explicitly, again, in this PR's
  own doc (even though PR #68 already said it) because this PR
  specifically interacts with destructive operator tooling where the
  temptation to conflate "purge data" with "destroy key" is highest.
- Recommended operator procedure (documentation only — do not implement
  any of these as code beyond what's already specified): backup per
  operator policy → `--dry-run` → inspect counts/dates → real purge with
  `--yes` → a second `--dry-run` with the same cutoff should report zero
  eligible rows remaining. State explicitly that a backup taken before
  the purge may still retain the data.
- State explicitly, in the new doc: "GophishFR does not automatically
  purge Event.Details in this version. There is no built-in 30/60/90-day
  default. The operator selects a cutoff explicitly on every invocation."
- State explicitly that automatic/scheduled retention and credential-
  capture minimization are both deferred to a separate future PR/backlog
  item, not implemented here.
- Recommend (documentation only, do not couple the commands in code):
  running this purge before a large bulk key-rotation run
  (`--rotate-event-details-credentials` from PR #68) can reduce the
  number of rows that rotation needs to re-encrypt, since purged rows no
  longer need rotation at all — purely an operational suggestion, not a
  functional dependency between the two tools.

## 8. Frontend (binding — conditional)

- Step 1 (mandatory): write a real browser test that creates a synthetic
  "Submitted Data" event with a non-empty payload, confirms Replay is
  offered and functional (existing pre-purge behavior, matching current
  `tests/browser/*` conventions), then runs the Go-side purge against
  that event (directly, or via the CLI tool under test — pick whichever
  is more consistent with how `browser_smoke_test.go` already drives
  server-side setup), reloads the results page, and asserts: (a) no
  Replay button is rendered for that event, (b) no uncaught JS
  exception/console error occurs, (c) the event still appears in the
  timeline with its original `Message`/`Time`.
- Step 2: if that test PASSES with ZERO frontend code changes (i.e. the
  item 0 hypothesis holds), do not modify
  `static/js/src/app/campaign_results.js` or any other frontend file, do
  not touch `static/js/dist/**`, and state explicitly in the PR
  description that no frontend change was required, with the test as
  proof.
- Step 3 (only if step 1's test FAILS without a code change): implement
  the smallest possible guard change to
  `static/js/src/app/campaign_results.js`'s Replay-button rendering so
  that the button only renders when `event.message == "Submitted Data"`
  AND `event.details` is a non-empty, successfully-`JSON.parse`-able
  string AND the parsed object has a non-empty `payload`. Do not refactor
  unrelated parts of `renderTimeline`/`replay`. If this path is taken, a
  full clean frontend rebuild is mandatory (see item 14) and the asset
  diff must be reviewed to confirm it touches only the expected bundle(s).

## 9. Database backends (binding)

- SQLite and MySQL only, both tested for real (same Docker-based real
  MySQL precedent as PR #68 — strict AND non-strict `sql_mode`).
- PostgreSQL: `ValidateEventDetailsBackend` already rejects it; the purge
  action must go through that same validation and `log.Fatal` cleanly for
  `postgres`/any other DB name. No new PostgreSQL code path, no new
  claim of support.
- No schema migration expected (see item 1's "out of scope" schema
  rule). If implementation proves one is truly required, STOP first.

## 10. Testing (binding minimum — all must exist and pass)

Go tests (models package, following existing `event_details_*_test.go`
conventions and naming):
- CLI-level argument parsing: valid RFC3339 cutoff accepted; invalid/
  unparseable cutoff rejected; missing cutoff flag alongside `--dry-run`/
  `--yes` alone is simply "no purge requested" (not an error); cutoff
  present with neither `--dry-run` nor `--yes` is a hard refusal; both
  present is a hard refusal.
- Dry-run performs zero writes (assert via a direct row read before/after
  that `details`/`details_ciphertext` are byte-identical) and reports
  correct counts/min/max ID/time/campaign-count against a known synthetic
  dataset.
- Eligibility: an old (`Time` before cutoff) event in a COMPLETED
  campaign with non-empty Details is purged; a new event (`Time` at/after
  cutoff) is never touched; an already-empty-Details event is not counted
  and not touched; an old event in a non-completed (active) campaign is
  excluded by default and included only with
  `--include-active-campaigns`.
- Purge mechanics: legacy-only row purged; ciphertext-only (MIGRATED) row
  purged; BOTH-state row purged (both columns cleared); a row with
  deliberately corrupted/INVALID ciphertext is purged WITHOUT any decrypt
  attempt (assert via a cipher that would error/panic on decrypt, or by
  constructing genuinely undecryptable bytes, and confirming the purge
  function never calls `cipher.Decrypt` — e.g. by not providing a cipher
  to the purge function at all, proving by construction that decryption
  is structurally impossible in this code path).
- Batch size > `eventDetailsMigrationBatchSize` rows purged correctly
  across multiple batches/transactions.
- Re-run idempotence: running the same purge twice with the same cutoff
  purges the same rows once, then purges zero rows the second time.
- Interruption/resume: simulate a partial run (e.g. process only one
  batch) and confirm a second invocation completes the remaining
  eligible rows correctly.
- Stats regression: `getCampaignStats`/`GetCampaignSummary` return
  identical values before and after purging a synthetic campaign's
  submitted-data event details.
- API metadata retained: `GetCampaign`/`GetCampaignResults` still return
  the purged event with correct `Id`/`CampaignId`/`Email`/`Time`/
  `Message`, and `Details == ""`.
- A real MySQL integration test (strict AND non-strict `sql_mode`, same
  Docker precedent/pattern as
  `TestMySQLEventDetailsNearLegacyCapRoundTripsStrictAndNonStrict`) that
  exercises the purge end-to-end against a real MySQL instance.
- A SQLite test exercising: dry-run, real purge, re-run (idempotent),
  simulated batch interruption/resume, stats check, API check.
- `go test -race` clean for every new/modified test in this PR.
- No plaintext/ciphertext/secret ever appears in any test assertion
  string, log capture, or error message produced by this feature (grep
  test output/log capture for a unique synthetic marker, e.g.
  `PURGE_TEST_SECRET_<random>`, confirming it never leaks to stdout/
  stderr/log output while still existing in the ciphertext before purge
  and being genuinely gone after).

Browser test:
- The Replay/CSV before-and-after test described in item 8.

Docker (if practical within this environment; if genuinely impractical,
state why in the PR instead of silently skipping):
- Run the CLI purge (dry-run then real) inside a real container build,
  confirming no credential content appears in container stdout/stderr/
  logs.

## 11. Security review checklist (binding — the final security review in
   this PR must explicitly confirm every one of these)

- Explicit cutoff is always required; there is no default.
- No default retention duration exists anywhere in code, config, or docs
  implying one.
- Dry-run uses EXACTLY the same eligibility logic as the real purge (not
  a parallel/divergent implementation that could drift).
- `--yes` is required for every real purge; no silent execution.
- Active campaigns are excluded by default; inclusion requires an
  explicit, separate flag, and `--yes` is still independently required.
- No decryption occurs anywhere in the purge code path.
- Both `details` and `details_ciphertext` are always cleared together.
- `Id`/`CampaignId`/`Email`/`Time`/`Message` are never modified.
- Stats are provably unaffected (test-backed).
- Replay is not broken for any row that hasn't been purged; Replay is
  safely absent (no exception) for purged rows.
- CSV export is safe (no exception, correct metadata, empty captured
  values) after purge.
- The operation is idempotent and resumable.
- No "secure erase"/forensic-wipe claim appears anywhere.
- Backup residual-data caveat is documented.
- No credential key is destroyed, rotated, or retired by this tool.
- No secret/plaintext/ciphertext/key-id ever appears in logs or CLI
  output.
- No scheduler/background/automatic trigger exists.
- No schema/migration diff (or a STOP was reported if one was found
  truly necessary).
- No new dependency anywhere (`go.mod`/`go.sum`/`package.json`/
  `yarn.lock` byte-identical to base).

## 12. Git/commit discipline (binding, same as every prior PR this
   session)

- Every commit signed (`git commit -S`), author AND committer
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`, verified after
  EVERY commit with `git log -1 --format=fuller --show-signature`
  showing `Good signature`.
- If GPG signing fails, appears unavailable, or requires an unlock that
  is not available: STOP immediately and report to the user. NEVER
  commit unsigned "temporarily." NEVER claim GPG is unavailable without
  independently proving it (this exact failure mode happened once
  already in this session's PR #68 — an Inspector falsely claimed "GPG
  unavailable" and committed unsigned; that must never happen again).
- Zero Copilot authorship/committer/co-authored-by/metadata anywhere in
  any commit.

## 13. Definition of done / merge gates

All of section 11's checklist items true, PLUS:
- `./scripts/verify.sh` green.
- `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...`
  green.
- `govulncheck`, `gosec`, `gitleaks`, `actionlint`, `zizmor` green (no new
  findings vs. the established baseline from PR #68).
- Yarn/npm audit baseline unchanged (no new frontend dependency
  introduced to shift it) — only run/report if a frontend change was
  actually made per item 8's step 3; otherwise state "no frontend change,
  dependency surface untouched."
- Full CI green on the PR.
- Independent security review: no findings (or all findings resolved).
- Independent code review: no findings (or all findings resolved).
- Goal Inspector PASS against this exact file.
- `git status`/`git diff --exit-code` clean before every commit and
  before merge.

Do not start, in this PR: automatic/scheduled retention, credential-
capture minimization, frontend dependency remediation
(`moment`/`fast-uri`), or query/form API-key-transport removal. These
remain explicitly deferred backlog items for separate future PRs.
