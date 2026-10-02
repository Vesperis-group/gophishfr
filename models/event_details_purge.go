package models

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// eventDetailsPurgeBatchSize reuses the exact batch size already validated by
// PR #68's migrate/rollback/rotation tooling (see eventDetailsMigrationBatchSize's
// doc comment in event_details_migration.go) rather than inventing a second
// constant: this tool's write is a strict subset of what that batch size was
// already proven safe for on a table under continuous, concurrent write load
// from AddEvent -- a single, conditionless column-clear UPDATE per row,
// instead of an encrypt-then-verify round trip per row. The same bound is,
// if anything, more conservative here, since there is strictly less work per
// row.
const eventDetailsPurgeBatchSize = eventDetailsMigrationBatchSize

// Sentinel errors for the --purge-event-details-before CLI contract. These
// live here, rather than in main.go, so they are unit-testable with the
// same conventions as every other event-details CLI validation helper in
// this package.
var (
	// ErrEventDetailsPurgeCutoffUnparseable is returned when
	// --purge-event-details-before's value fails strict RFC3339 parsing
	// (no default timezone is ever assumed -- see ParseEventDetailsPurgeCutoff).
	ErrEventDetailsPurgeCutoffUnparseable = errors.New("purge cutoff is not a valid RFC3339 timestamp")

	// ErrEventDetailsPurgeModeRequired is returned when a cutoff was given
	// but neither --dry-run nor --yes was set. There is no silent default
	// to either mode.
	ErrEventDetailsPurgeModeRequired = errors.New("one of --dry-run or --yes is required with --purge-event-details-before")

	// ErrEventDetailsPurgeModeAmbiguous is returned when both --dry-run and
	// --yes were set for the same invocation. Ambiguous operator intent is
	// never silently resolved in either direction.
	ErrEventDetailsPurgeModeAmbiguous = errors.New("--dry-run and --yes are mutually exclusive")
)

// ParseEventDetailsPurgeCutoff parses --purge-event-details-before's value
// with strict RFC3339 semantics: time.RFC3339 requires an explicit numeric
// offset or a trailing "Z" (see https://pkg.go.dev/time#pkg-constants), so a
// bare, offset-less string such as "2026-07-01T00:00:00" is rejected rather
// than silently assumed to be UTC or local time, exactly as goal.md
// requires. The parsed value is immediately normalized with .UTC(),
// matching Event.Time's own storage convention (time.Now().UTC() in
// AddEvent -- see campaign.go), so the eligibility comparison below is
// always apples-to-apples regardless of what offset the operator typed.
func ParseEventDetailsPurgeCutoff(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q: %v", ErrEventDetailsPurgeCutoffUnparseable, value, err)
	}
	return parsed.UTC(), nil
}

// ValidateEventDetailsPurgeMode enforces the binding CLI contract: exactly
// one of dryRun/yes must be true whenever a purge cutoff was supplied.
// Neither set is a hard refusal (no silent default); both set is also a
// hard refusal (ambiguous operator intent is never silently resolved).
func ValidateEventDetailsPurgeMode(dryRun, yes bool) error {
	switch {
	case dryRun && yes:
		return ErrEventDetailsPurgeModeAmbiguous
	case !dryRun && !yes:
		return ErrEventDetailsPurgeModeRequired
	default:
		return nil
	}
}

// eventDetailsPurgeEligibilityClause returns the single, shared SQL WHERE
// fragment (and its bind arguments) used by BOTH
// ReportEventDetailsPurgeEligibility (dry-run and the real purge's own
// pre-purge summary) and PurgeEventDetailsBefore's batch fetch query -- see
// goal.md's binding "dry-run uses EXACTLY the same eligibility logic as the
// real purge" requirement. There is exactly one copy of this fragment in
// the entire codebase; dry-run and the real purge can never drift apart
// because both call this same function.
//
// Condition 1 (time < cutoff): cutoff is already UTC-normalized by
// ParseEventDetailsPurgeCutoff before it ever reaches this function.
//
// Condition 2 (non-empty details): "(details IS NOT NULL AND details <>
// ”) OR details_ciphertext IS NOT NULL" -- a row that already has nothing
// to purge (both columns NULL, or legacy ” and ciphertext NULL) is never
// eligible, never touched, and never appears in a dry-run count.
//
// Condition 3 (active-campaign exclusion), when includeActiveCampaigns is
// false: expressed as a single `campaign_id IN (SELECT id FROM campaigns
// WHERE status = ?)` subquery, not a per-row Go-side lookup. At the scale
// this operator-triggered, occasional tool runs at, one extra subquery
// (which MySQL and SQLite both optimize against the campaigns table's
// primary key) per batch/report is simpler than, and avoids the N+1 query
// pattern of, a Go-side per-row campaign lookup inside the hot loop. See
// goal.md section 5 for the accompanying full-table-scan performance note:
// events.time has no index today, and this PR deliberately does not add
// one speculatively -- an occasional full scan is acceptable for this
// operator-triggered, non-hot-path command.
//
// Condition 4 (BOTH/INVALID rows): deliberately absent from this clause.
// This function, and therefore every caller, never classifies or decrypts
// a row to decide eligibility -- a row meeting conditions 1-3 is eligible
// regardless of whether it is LEGACY, MIGRATED, BOTH, or INVALID
// ciphertext. See PurgeEventDetailsBefore's own doc comment for why
// decryption is structurally impossible in this code path.
func eventDetailsPurgeEligibilityClause(cutoff time.Time, includeActiveCampaigns bool) (string, []interface{}) {
	clause := `time < ? AND ((details IS NOT NULL AND details <> '') OR details_ciphertext IS NOT NULL)`
	args := []interface{}{cutoff}
	if !includeActiveCampaigns {
		clause += ` AND campaign_id IN (SELECT id FROM campaigns WHERE status = ?)`
		args = append(args, CampaignComplete)
	}
	return clause, args
}

// flexibleSQLTime scans a possibly-NULL time value returned by a raw
// aggregate query, tolerating both backends this tool supports without
// requiring any DSN/config change (explicitly out of scope -- see
// goal.md's "no new dependency" / "no config change" constraints):
// SQLite's mattn/go-sqlite3 driver returns a text-formatted value (with a
// zone offset) for a raw aggregate SELECT, and MySQL's go-sql-driver/mysql
// returns either a native time.Time (when the operator's DSN includes
// parseTime=true, as this repository's own test/CI DSN does) or a raw
// text DATETIME value with no zone suffix (when it does not). This
// package's production code never requires parseTime=true -- it is purely
// an operator DSN choice -- so this type tolerates either shape rather
// than assuming one.
type flexibleSQLTime struct {
	Time  time.Time
	Valid bool
}

// sqlDateTimeTextLayouts are the text layouts this tool's two supported
// backends are known to emit for a DATETIME-affinity value, when the
// driver returns one as text instead of a native time.Time:
//
//   - MySQL's go-sql-driver/mysql, when parseTime is not enabled (this
//     repository's DSN never sets it): "YYYY-MM-DD HH:MM:SS[.ffffff]",
//     with no zone suffix at all (MySQL's DATETIME has no zone concept).
//   - SQLite's mattn/go-sqlite3, for a raw aggregate-query SELECT like this
//     one (as opposed to a column bound to a struct field, which that
//     driver converts natively): "YYYY-MM-DD HH:MM:SS[.fffffffff]±HH:MM",
//     always including a zone offset.
//
// Both are tried, in order, most-specific first, and ParseInLocation's
// default-zone argument only matters for the MySQL layouts (which have no
// zone to parse), since UTC is this entire feature's one storage
// convention end to end.
var sqlDateTimeTextLayouts = []string{
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

func (scanned *flexibleSQLTime) Scan(value interface{}) error {
	if value == nil {
		*scanned = flexibleSQLTime{}
		return nil
	}
	switch typed := value.(type) {
	case time.Time:
		*scanned = flexibleSQLTime{Time: typed.UTC(), Valid: true}
		return nil
	case []byte:
		return scanned.scanText(string(typed))
	case string:
		return scanned.scanText(typed)
	default:
		return fmt.Errorf("event details purge: unsupported time scan type %T", value)
	}
}

func (scanned *flexibleSQLTime) scanText(text string) error {
	for _, layout := range sqlDateTimeTextLayouts {
		if parsed, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
			*scanned = flexibleSQLTime{Time: parsed.UTC(), Valid: true}
			return nil
		}
	}
	return fmt.Errorf("event details purge: unrecognized time format %q", text)
}

// EventDetailsPurgeReport is the non-secret, aggregate-only summary produced
// by a dry run, and also computed by a real purge as its own pre-purge
// summary. It never contains Details, DetailsCiphertext, or any other row
// content -- only counts, IDs, and timestamps, consistent with the
// PLAINTEXT_LOGGING = NONE discipline PR #68 already established.
type EventDetailsPurgeReport struct {
	Eligible          int64
	OldestEventTime   time.Time
	NewestEventTime   time.Time
	MinEventID        int64
	MaxEventID        int64
	DistinctCampaigns int64
}

// ReportEventDetailsPurgeEligibility runs the read-only eligibility
// aggregate query used directly for --dry-run, and also by
// PurgeEventDetailsBefore to compute its own pre-purge summary before
// writing anything. It performs exactly one aggregate (COUNT/MIN/MAX)
// query, never loads individual eligible rows into memory, and writes
// nothing.
func ReportEventDetailsPurgeEligibility(cutoff time.Time, includeActiveCampaigns bool) (EventDetailsPurgeReport, error) {
	report := EventDetailsPurgeReport{}
	if conf == nil {
		return report, errors.New("database is not configured")
	}
	if err := ValidateEventDetailsBackend(conf.DBName); err != nil {
		return report, err
	}

	clause, args := eventDetailsPurgeEligibilityClause(cutoff, includeActiveCampaigns)
	query := fmt.Sprintf(`
		SELECT
			COUNT(*),
			MIN(time), MAX(time),
			MIN(id), MAX(id),
			COUNT(DISTINCT campaign_id)
		FROM events
		WHERE %s
	`, clause)

	var (
		eligible          int64
		oldest, newest    flexibleSQLTime
		minID, maxID      sql.NullInt64
		distinctCampaigns int64
	)
	row := db.Raw(query, args...).Row()
	if err := row.Scan(&eligible, &oldest, &newest, &minID, &maxID, &distinctCampaigns); err != nil {
		return report, fmt.Errorf("query purge eligibility: %w", err)
	}

	report.Eligible = eligible
	report.DistinctCampaigns = distinctCampaigns
	if oldest.Valid {
		report.OldestEventTime = oldest.Time
	}
	if newest.Valid {
		report.NewestEventTime = newest.Time
	}
	if minID.Valid {
		report.MinEventID = minID.Int64
	}
	if maxID.Valid {
		report.MaxEventID = maxID.Int64
	}
	return report, nil
}

// EventDetailsPurgeResult aggregates one PurgeEventDetailsBefore run.
type EventDetailsPurgeResult struct {
	EventDetailsPurgeReport

	Batches int
	Purged  int64

	// Skipped counts rows that matched the eligibility query at fetch time
	// but had already lost their eligibility (e.g. the owning Campaign, or
	// the Event row itself, was deleted concurrently by DeleteCampaign) by
	// the time this batch's UPDATE ran -- see goal.md's binding concurrency
	// tolerance requirement. It is reported as a normal progress counter,
	// never as a hard failure.
	Skipped int64
}

type eventPurgeIDRow struct {
	ID int64 `gorm:"column:id"`
}

// purgeEventDetailsOneBatch fetches and clears exactly one bounded batch of
// currently-eligible rows (re-deriving eligibility fresh from
// eventDetailsPurgeEligibilityClause every call -- there is no remembered
// cursor), and is the sole building block PurgeEventDetailsBefore's loop
// uses. It is also factored out, rather than inlined, specifically so a
// test can invoke a single batch directly to simulate an interrupted run
// (process killed mid-loop) and then prove a subsequent, independent call
// to PurgeEventDetailsBefore resumes and completes correctly -- exactly
// the resumability goal.md requires.
//
// fetched is the number of rows the eligibility query returned (0 means
// nothing left to do -- the caller's loop should stop). purged/skipped
// split that count by whether each row's UPDATE actually changed a row
// (RowsAffected == 1) or found the row already gone (RowsAffected == 0,
// e.g. a concurrent DeleteCampaign).
//
// Design note on transaction granularity: unlike
// migrateEventDetailsRow/reconcileBothStateEventRow in
// event_details_migration.go (one transaction per row, justified there by
// the encrypt-then-verify round trip each of those performs), this
// function uses ONE transaction per BATCH. There is no encrypt/verify step
// here at all -- every row in a batch receives the exact same
// unconditional `UPDATE events SET details = NULL, details_ciphertext =
// NULL WHERE id = ?` -- so batching the transaction is simpler and keeps
// the same total lock footprint, with no loss of row-level failure
// isolation: a row that disappears concurrently simply yields RowsAffected
// = 0 for its own UPDATE within the batch (counted in skipped), while
// every other row's UPDATE in the same batch still commits normally.
func purgeEventDetailsOneBatch(clause string, clauseArgs []interface{}, batchSize int) (fetched int, purged int64, skipped int64, err error) {
	fetchQuery := fmt.Sprintf(`
		SELECT id FROM events WHERE %s ORDER BY id ASC LIMIT ?
	`, clause)
	fetchArgs := append(append([]interface{}{}, clauseArgs...), batchSize)

	var rows []eventPurgeIDRow
	if err := db.Raw(fetchQuery, fetchArgs...).Scan(&rows).Error; err != nil {
		return 0, 0, 0, fmt.Errorf("query purge-eligible rows: %w", err)
	}
	if len(rows) == 0 {
		return 0, 0, 0, nil
	}

	transaction := db.Begin()
	if transaction.Error != nil {
		return 0, 0, 0, transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	for _, row := range rows {
		// NEVER decrypt: this is a plain, unconditional column clear. No
		// *credentials.Cipher is accepted or referenced anywhere in this
		// function's call graph (or PurgeEventDetailsBefore's, or
		// ReportEventDetailsPurgeEligibility's), which makes decryption
		// structurally impossible in this code path, not merely avoided by
		// convention -- see goal.md item 4 and the acceptance test that
		// purges a deliberately-INVALID ciphertext row by construction.
		update := transaction.Exec(`
			UPDATE events SET details = NULL, details_ciphertext = NULL WHERE id = ?
		`, row.ID)
		if update.Error != nil {
			return 0, 0, 0, fmt.Errorf("purge event %d: %w", row.ID, update.Error)
		}
		if update.RowsAffected == 1 {
			purged++
		} else {
			skipped++
		}
	}

	if err := transaction.Commit().Error; err != nil {
		return 0, 0, 0, fmt.Errorf("commit purge batch: %w", err)
	}
	committed = true
	return len(rows), purged, skipped, nil
}

// PurgeEventDetailsBefore executes the real, irreversible purge: every
// Event row matching eventDetailsPurgeEligibilityClause has BOTH `details`
// and `details_ciphertext` cleared to SQL NULL. Only those two columns are
// ever written; `Id`, `CampaignId`, `Email`, `Time`, and `Message` are
// never touched.
//
// Idempotence/resumability: every batch re-derives "what is still
// eligible" directly from eventDetailsPurgeEligibilityClause against
// current row state -- there is no remembered cursor anywhere. Re-running
// this function with the same cutoff after a prior successful (or
// interrupted) run purges only whatever rows still match that fresh
// query, exactly like MigrateEventDetailsBatch's established pattern.
//
// Concurrency: safe to run online, concurrently with live AddEvent writes
// and campaign deletion -- see purgeEventDetailsOneBatch's skipped-row
// handling and goal.md item 4.
func PurgeEventDetailsBefore(cutoff time.Time, includeActiveCampaigns bool) (EventDetailsPurgeResult, error) {
	result := EventDetailsPurgeResult{}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateEventDetailsBackend(conf.DBName); err != nil {
		return result, err
	}

	report, err := ReportEventDetailsPurgeEligibility(cutoff, includeActiveCampaigns)
	if err != nil {
		return result, err
	}
	result.EventDetailsPurgeReport = report

	clause, clauseArgs := eventDetailsPurgeEligibilityClause(cutoff, includeActiveCampaigns)
	for {
		fetched, purged, skipped, err := purgeEventDetailsOneBatch(clause, clauseArgs, eventDetailsPurgeBatchSize)
		if err != nil {
			return result, err
		}
		if fetched == 0 {
			break
		}
		result.Batches++
		result.Purged += purged
		result.Skipped += skipped
		if purged == 0 {
			// Every fetched row vanished concurrently (e.g. a bulk
			// DeleteCampaign raced this batch): stop rather than loop
			// forever re-fetching a now-stale ID list, exactly like
			// MigrateEventDetailsBatch's analogous "no progress" guard.
			break
		}
	}

	return result, nil
}
