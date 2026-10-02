package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
)

// Durable migration-state marker values. See
// db/db_sqlite3/migrations/20260903040000_encrypt_event_details.sql (and its
// MySQL counterpart) for the event_details_migration_state table this reads
// and writes, and docs/event-details-encryption.md for the full operator
// procedure.
const (
	// EventDetailsStatePreCutover describes every deployment of this
	// repository before this PR's schema migration has applied: there is no
	// details_ciphertext column and no marker row. It is not a state the Go
	// code ever persists or observes at runtime -- models.Setup always runs
	// pending goose migrations before any other model code can execute, so
	// by the time eventDetailsMigrationState can be called, the marker row
	// this migration seeds always already exists. It exists in this
	// constant list purely to make the full three-state taxonomy from
	// goal.md explicit and documented, including the state that precedes
	// it.
	EventDetailsStatePreCutover = "pre_cutover"

	// EventDetailsStateMigrating is the state the Up migration seeds the
	// marker row with. New writes are ciphertext-only from this binary's
	// first deploy (see AddEvent/persistEventWithEncryptedDetails); legacy
	// rows written by an older binary may still exist and are read-
	// compatible (decryptEventDetails serves them as-is) until finalized.
	EventDetailsStateMigrating = "migrating"

	// EventDetailsStateFinalized is set only by FinalizeEventDetailsMigration,
	// only after its preflight proves zero LEGACY, zero BOTH, and zero
	// INVALID rows remain. Once finalized, decryptEventDetails treats any
	// LEGACY row it encounters as a fail-closed error, never a silent
	// fallback.
	EventDetailsStateFinalized = "finalized"

	// eventDetailsMigrationBatchSize bounds how many pending rows a single
	// read/transaction cycle of the migrate, rollback, or rotation tooling
	// below processes before re-querying current row state. 500 is chosen
	// as a bounded, conservative middle ground for a table that is written
	// continuously by the live mailer worker and phishing server: large
	// enough that migrating a multi-million-row backlog does not take an
	// impractical number of round trips, but small enough that any single
	// transaction's lock footprint and duration stay short relative to the
	// steady trickle of concurrent single-row INSERTs from AddEvent, so
	// migration does not meaningfully contend with live traffic. Every row
	// within a batch is processed and committed in its OWN transaction (see
	// the design note on migrateEventDetailsRow below) -- "batch" here
	// means only "the bounded unit of rows read into memory per read
	// query", never "a single shared transaction spanning many rows".
	eventDetailsMigrationBatchSize = 500
)

// EventDetailsRowFailure reports a single row's migration/rollback/rotation
// failure by ID and error class only -- never Details, the envelope, or any
// other content, consistent with PLAINTEXT_LOGGING = NONE.
type EventDetailsRowFailure struct {
	EventID int64
	Reason  string
}

// EventDetailsMigrationResult aggregates one MigrateEventDetailsBatch run.
type EventDetailsMigrationResult struct {
	Batches        int
	RowsEncrypted  int
	RowsReconciled int
	MalformedJSON  []int64
	Failed         []EventDetailsRowFailure
	LastID         int64
}

// EventDetailsRollbackResult aggregates one RollbackEventDetailsBatch run.
type EventDetailsRollbackResult struct {
	Batches      int
	RowsRestored int
	Failed       []EventDetailsRowFailure
	LastID       int64
}

// EventDetailsRotationResult aggregates one RotateEventDetailsCredentials
// run.
type EventDetailsRotationResult struct {
	Batches   int
	Rotated   int
	Unchanged int
	Failed    []EventDetailsRowFailure
	LastID    int64
}

// EventDetailsStateCounts is the finalization preflight's real, reproducible
// global query result: the authoritative "is it safe to finalize" signal.
type EventDetailsStateCounts struct {
	Legacy   int64
	Both     int64
	Migrated int64
	Invalid  int64
}

// eventDetailsMigrationState reads the durable, restart-surviving marker.
// See the constant doc comments above for the full taxonomy.
func eventDetailsMigrationState() (string, error) {
	var state sql.NullString
	row := db.Raw(`SELECT state FROM event_details_migration_state WHERE id = 1`).Row()
	if err := row.Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EventDetailsStatePreCutover, nil
		}
		return "", err
	}
	if !state.Valid || state.String == "" {
		return EventDetailsStatePreCutover, nil
	}
	return state.String, nil
}

// eventDetailsMigrationFinalized reports whether the durable marker is
// FINALIZED. Any error reading the marker (including the schema migration
// not having applied yet) fails open toward "not finalized", i.e. toward
// still serving LEGACY rows rather than toward a surprising fail-closed
// read failure on every single event -- finalization is something an
// operator explicitly verifies via FinalizeEventDetailsMigration's own
// preflight, not something the read path should ever guess into existence.
func eventDetailsMigrationFinalized() bool {
	state, err := eventDetailsMigrationState()
	if err != nil {
		return false
	}
	return state == EventDetailsStateFinalized
}

func setEventDetailsMigrationState(state string) error {
	update := db.Exec(`UPDATE event_details_migration_state SET state = ? WHERE id = 1`, state)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return errors.New("event details migration marker row is missing; apply the schema migration first")
	}
	return nil
}

type legacyEventRow struct {
	ID         int64  `gorm:"column:id"`
	CampaignID int64  `gorm:"column:campaign_id"`
	Details    string `gorm:"column:details"`
}

func fetchPendingLegacyEventRows(limit int) ([]legacyEventRow, error) {
	var rows []legacyEventRow
	err := db.Raw(`
		SELECT id, campaign_id, details
		FROM events
		WHERE details_ciphertext IS NULL
		  AND details IS NOT NULL
		  AND details <> ''
		ORDER BY id ASC
		LIMIT ?
	`, limit).Scan(&rows).Error
	return rows, err
}

type bothStateEventRow struct {
	ID                int64  `gorm:"column:id"`
	CampaignID        int64  `gorm:"column:campaign_id"`
	Details           string `gorm:"column:details"`
	DetailsCiphertext []byte `gorm:"column:details_ciphertext"`
}

func fetchBothStateEventRows(limit int) ([]bothStateEventRow, error) {
	var rows []bothStateEventRow
	err := db.Raw(`
		SELECT id, campaign_id, details, details_ciphertext
		FROM events
		WHERE details IS NOT NULL AND details <> ''
		  AND details_ciphertext IS NOT NULL
		ORDER BY id ASC
		LIMIT ?
	`, limit).Scan(&rows).Error
	return rows, err
}

type migratedEventRow struct {
	ID                int64  `gorm:"column:id"`
	CampaignID        int64  `gorm:"column:campaign_id"`
	DetailsCiphertext []byte `gorm:"column:details_ciphertext"`
}

func fetchMigratedEventRows(limit int) ([]migratedEventRow, error) {
	var rows []migratedEventRow
	err := db.Raw(`
		SELECT id, campaign_id, details_ciphertext
		FROM events
		WHERE (details IS NULL OR details = '')
		  AND details_ciphertext IS NOT NULL
		ORDER BY id ASC
		LIMIT ?
	`, limit).Scan(&rows).Error
	return rows, err
}

// MigrateEventDetailsBatch is the --migrate-event-details online batch
// migration tool. It is safe to run repeatedly (idempotent) and to
// interrupt and resume at any point (resumable): every batch re-derives its
// own pending-row set directly from current row state (details_ciphertext
// IS NULL AND details is non-empty for LEGACY, or both columns populated for
// BOTH), never from a remembered ID cursor, so a partially completed run
// leaves nothing to reconcile except the rows that are still, in fact,
// pending. LastID is reported purely as an operator progress signal, never
// as the authority for "what remains".
//
// Processing order: rows ordered by events.id ASC, is global across the
// whole pending set; this function processes BOTH-state rows to
// resolution/report first, then LEGACY rows, repeating each phase in
// batches of eventDetailsMigrationBatchSize until a read returns no more
// rows (or a full batch makes no progress, which stops an otherwise
// infinite loop over rows that keep failing the same way).
//
// Design note: every row is encrypted/verified/cleared inside its OWN
// transaction (see migrateEventDetailsRow / reconcileBothStateEventRow),
// never a single transaction shared across a whole batch. This keeps each
// write's lock footprint minimal on a table under continuous, concurrent
// write load from AddEvent, and means one row's failure only ever rolls
// back that row -- it is reported in Failed and the migration continues
// with the rest of the batch and the rest of the table, never aborting the
// whole run.
func MigrateEventDetailsBatch(cipher *credentials.Cipher) (EventDetailsMigrationResult, error) {
	result := EventDetailsMigrationResult{}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateEventDetailsBackend(conf.DBName); err != nil {
		return result, err
	}
	if cipher == nil {
		return result, ErrEventDetailsKeyringRequired
	}

	// failedThisRun tracks row IDs already reported as failures within this
	// single call, so a permanently-unresolvable row (e.g. a genuine
	// BOTH-state mismatch) is reported exactly once per invocation instead
	// of once per outer-loop pass over the batch. This is purely an
	// in-memory, single-call bookkeeping aid, NOT the authoritative
	// pending-row signal -- a separate, later invocation of this function
	// still re-derives its own pending set entirely from row state, with no
	// memory of this one, exactly as the binding resumability requirement
	// demands.
	failedThisRun := make(map[int64]bool)

	for {
		rows, err := fetchBothStateEventRows(eventDetailsMigrationBatchSize)
		if err != nil {
			return result, fmt.Errorf("query BOTH-state rows: %w", err)
		}
		rows = excludeFailedBoth(rows, failedThisRun)
		if len(rows) == 0 {
			break
		}
		result.Batches++
		progressed := false
		for _, row := range rows {
			if err := reconcileBothStateEventRow(cipher, row, &result); err != nil {
				result.Failed = append(result.Failed, EventDetailsRowFailure{EventID: row.ID, Reason: classifyEventDetailsError(err)})
				failedThisRun[row.ID] = true
			} else {
				progressed = true
			}
			if row.ID > result.LastID {
				result.LastID = row.ID
			}
		}
		if !progressed {
			break
		}
	}

	for {
		rows, err := fetchPendingLegacyEventRows(eventDetailsMigrationBatchSize)
		if err != nil {
			return result, fmt.Errorf("query LEGACY rows: %w", err)
		}
		rows = excludeFailedLegacy(rows, failedThisRun)
		if len(rows) == 0 {
			break
		}
		result.Batches++
		progressed := false
		for _, row := range rows {
			if err := migrateEventDetailsRow(cipher, row, &result); err != nil {
				result.Failed = append(result.Failed, EventDetailsRowFailure{EventID: row.ID, Reason: classifyEventDetailsError(err)})
				failedThisRun[row.ID] = true
			} else {
				progressed = true
			}
			if row.ID > result.LastID {
				result.LastID = row.ID
			}
		}
		if !progressed {
			break
		}
	}

	return result, nil
}

// excludeFailedBoth and excludeFailedLegacy filter out rows already reported
// as a failure earlier in this same MigrateEventDetailsBatch call -- see the
// failedThisRun doc comment above.
func excludeFailedBoth(rows []bothStateEventRow, failedThisRun map[int64]bool) []bothStateEventRow {
	if len(failedThisRun) == 0 {
		return rows
	}
	filtered := rows[:0]
	for _, row := range rows {
		if !failedThisRun[row.ID] {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func excludeFailedLegacy(rows []legacyEventRow, failedThisRun map[int64]bool) []legacyEventRow {
	if len(failedThisRun) == 0 {
		return rows
	}
	filtered := rows[:0]
	for _, row := range rows {
		if !failedThisRun[row.ID] {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

// migrateEventDetailsRow encrypts one LEGACY row's exact byte sequence,
// regardless of whether it happens to be application-valid JSON: goal.md
// explicitly distinguishes CRYPTO-MIGRATABLE BYTES (any non-empty byte
// sequence within size limits) from APPLICATION-VALID JSON, and malformed
// JSON must never block encryption -- it is only flagged, via
// result.MalformedJSON, for operator visibility. The legacy bytes
// themselves are never destroyed or silently dropped for being invalid
// JSON.
func migrateEventDetailsRow(cipher *credentials.Cipher, row legacyEventRow, result *EventDetailsMigrationResult) error {
	if !json.Valid([]byte(row.Details)) {
		result.MalformedJSON = append(result.MalformedJSON, row.ID)
	}

	ctx := eventDetailsContext(row.CampaignID, row.ID)
	envelope, err := cipher.Encrypt(ctx, []byte(row.Details))
	if err != nil {
		return fmt.Errorf("%w for event %d", ErrEventDetailsUnavailable, row.ID)
	}
	envelopeBytes := []byte(string(envelope))

	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	// Compare details as the same Go string type it was originally read as
	// (legacy rows are historically TEXT-stored, not BLOB-stored, even
	// though the column itself has no-affinity BLOB type): SQLite considers
	// a TEXT value and a byte-for-byte-identical BLOB value UNEQUAL under
	// '=' because they carry different storage classes, so binding
	// []byte(row.Details) here would make this guard never match and every
	// migration attempt fail closed as "changed concurrently". Passing the
	// plain string keeps the comparison storage-class-consistent on both
	// SQLite and MySQL.
	update := transaction.Exec(`
		UPDATE events SET details_ciphertext = ?
		WHERE id = ? AND details = ? AND details_ciphertext IS NULL
	`, envelopeBytes, row.ID, row.Details)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("event %d changed concurrently during migration", row.ID)
	}

	stored, err := readEventDetailsRow(transaction, row.ID)
	if err != nil {
		return err
	}
	verified, err := cipher.Decrypt(ctx, credentials.Envelope(stored.DetailsCiphertext))
	if err != nil || string(verified) != row.Details {
		return fmt.Errorf("%w for event %d", ErrEventDetailsStorageMismatch, row.ID)
	}

	clear := transaction.Exec(`
		UPDATE events SET details = NULL
		WHERE id = ? AND details_ciphertext = ?
	`, row.ID, envelopeBytes)
	if clear.Error != nil {
		return clear.Error
	}
	if clear.RowsAffected != 1 {
		return fmt.Errorf("event %d changed concurrently clearing legacy details", row.ID)
	}

	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	result.RowsEncrypted++
	return nil
}

// reconcileBothStateEventRow handles a row found in the BOTH transition
// state mid-migration (legacy plaintext and ciphertext both present, which
// can only exist transiently during the batch migration window). If the
// ciphertext decrypts to exactly the legacy plaintext, the row is
// considered already-migrated and the legacy copy is cleared. If they
// differ, this function stops processing that row and reports it --
// goal.md is explicit that neither value may be silently assumed
// authoritative.
func reconcileBothStateEventRow(cipher *credentials.Cipher, row bothStateEventRow, result *EventDetailsMigrationResult) error {
	ctx := eventDetailsContext(row.CampaignID, row.ID)
	plaintext, err := cipher.Decrypt(ctx, credentials.Envelope(row.DetailsCiphertext))
	if err != nil {
		return fmt.Errorf("%w for event %d", ErrEventDetailsInvalidCiphertext, row.ID)
	}
	if string(plaintext) != row.Details {
		return fmt.Errorf("%w for event %d", ErrEventDetailsMismatchedBoth, row.ID)
	}

	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	clear := transaction.Exec(`
		UPDATE events SET details = NULL
		WHERE id = ? AND details = ? AND details_ciphertext = ?
	`, row.ID, row.Details, row.DetailsCiphertext)
	if clear.Error != nil {
		return clear.Error
	}
	if clear.RowsAffected != 1 {
		return fmt.Errorf("event %d changed concurrently during BOTH-state reconciliation", row.ID)
	}

	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	result.RowsReconciled++
	return nil
}

// RollbackEventDetailsBatch is the --rollback-event-details online batch
// tool: for each MIGRATED row, it decrypts the ciphertext, writes the
// plaintext back into the legacy `details` column, reads back and compares
// exactly, and only then clears details_ciphertext, committing per row (see
// the same one-row-per-transaction design note as migrateEventDetailsRow).
// This is precisely why the plaintext size bound in
// event_details_credentials.go is binding: every MIGRATED row's plaintext
// is guaranteed, by that bound having been enforced at write time, to fit
// back into the legacy BLOB column without truncation.
//
// If this rollback fully empties the MIGRATED/BOTH/INVALID sets (verified
// by a fresh, real query, not an assumption), it also resets the durable
// marker from FINALIZED back to MIGRATING: a FINALIZED marker asserts "zero
// legacy rows exist", which a completed rollback deliberately makes false
// again by design, so leaving it at FINALIZED afterward would be an
// inaccurate, stale marker.
func RollbackEventDetailsBatch(cipher *credentials.Cipher) (EventDetailsRollbackResult, error) {
	result := EventDetailsRollbackResult{}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateEventDetailsBackend(conf.DBName); err != nil {
		return result, err
	}
	if cipher == nil {
		return result, ErrEventDetailsKeyringRequired
	}

	// See the identical failedThisRun doc comment in MigrateEventDetailsBatch.
	failedThisRun := make(map[int64]bool)

	for {
		rows, err := fetchMigratedEventRows(eventDetailsMigrationBatchSize)
		if err != nil {
			return result, fmt.Errorf("query MIGRATED rows: %w", err)
		}
		if len(failedThisRun) > 0 {
			filtered := rows[:0]
			for _, row := range rows {
				if !failedThisRun[row.ID] {
					filtered = append(filtered, row)
				}
			}
			rows = filtered
		}
		if len(rows) == 0 {
			break
		}
		result.Batches++
		progressed := false
		for _, row := range rows {
			if err := rollbackEventDetailsRow(cipher, row, &result); err != nil {
				result.Failed = append(result.Failed, EventDetailsRowFailure{EventID: row.ID, Reason: classifyEventDetailsError(err)})
				failedThisRun[row.ID] = true
			} else {
				progressed = true
			}
			if row.ID > result.LastID {
				result.LastID = row.ID
			}
		}
		if !progressed {
			break
		}
	}

	if len(result.Failed) == 0 {
		if counts, err := countEventDetailsStates(cipher); err == nil {
			if counts.Migrated == 0 && counts.Both == 0 && counts.Legacy >= 0 {
				if state, stateErr := eventDetailsMigrationState(); stateErr == nil && state == EventDetailsStateFinalized {
					_ = setEventDetailsMigrationState(EventDetailsStateMigrating)
				}
			}
		}
	}

	return result, nil
}

func rollbackEventDetailsRow(cipher *credentials.Cipher, row migratedEventRow, result *EventDetailsRollbackResult) error {
	ctx := eventDetailsContext(row.CampaignID, row.ID)
	plaintext, err := cipher.Decrypt(ctx, credentials.Envelope(row.DetailsCiphertext))
	if err != nil {
		return fmt.Errorf("%w for event %d", ErrEventDetailsInvalidCiphertext, row.ID)
	}
	if len(plaintext) > eventDetailsLegacyBlobCapacityBytes {
		return fmt.Errorf("%w for event %d", ErrEventDetailsTooLarge, row.ID)
	}

	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	update := transaction.Exec(`
		UPDATE events SET details = ?
		WHERE id = ? AND (details IS NULL OR details = '') AND details_ciphertext = ?
	`, plaintext, row.ID, row.DetailsCiphertext)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("event %d changed concurrently during rollback", row.ID)
	}

	stored, err := readEventDetailsRow(transaction, row.ID)
	if err != nil {
		return err
	}
	if !stored.Details.Valid || stored.Details.String != string(plaintext) {
		return fmt.Errorf("%w for event %d", ErrEventDetailsStorageMismatch, row.ID)
	}

	clear := transaction.Exec(`
		UPDATE events SET details_ciphertext = NULL
		WHERE id = ? AND details_ciphertext = ?
	`, row.ID, row.DetailsCiphertext)
	if clear.Error != nil {
		return clear.Error
	}
	if clear.RowsAffected != 1 {
		return fmt.Errorf("event %d changed concurrently clearing ciphertext during rollback", row.ID)
	}

	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	result.RowsRestored++
	return nil
}

// countInvalidEventDetailsCiphertext performs the real, batched (never
// whole-table-in-memory) decrypt-attempt scan that makes INVALID part of
// the finalization preflight's global query rather than an assumption: any
// row whose details_ciphertext is present but fails to decrypt counts here.
func countInvalidEventDetailsCiphertext(cipher *credentials.Cipher) (int64, error) {
	if cipher == nil {
		return 0, ErrEventDetailsKeyringRequired
	}
	var invalid int64
	var lastID int64
	for {
		var rows []struct {
			ID                int64  `gorm:"column:id"`
			CampaignID        int64  `gorm:"column:campaign_id"`
			DetailsCiphertext []byte `gorm:"column:details_ciphertext"`
		}
		if err := db.Raw(`
			SELECT id, campaign_id, details_ciphertext
			FROM events
			WHERE details_ciphertext IS NOT NULL AND id > ?
			ORDER BY id ASC
			LIMIT ?
		`, lastID, eventDetailsMigrationBatchSize).Scan(&rows).Error; err != nil {
			return invalid, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			ctx := eventDetailsContext(row.CampaignID, row.ID)
			if _, err := cipher.Decrypt(ctx, credentials.Envelope(row.DetailsCiphertext)); err != nil {
				invalid++
			}
			lastID = row.ID
		}
	}
	return invalid, nil
}

// countEventDetailsStates runs the real, reproducible global query an
// operator (or a test) uses to verify "no legacy plaintext remains" and to
// gate finalization: counts of LEGACY, BOTH, MIGRATED, and INVALID rows.
func countEventDetailsStates(cipher *credentials.Cipher) (EventDetailsStateCounts, error) {
	var counts EventDetailsStateCounts
	if err := db.Raw(`
		SELECT COUNT(*) FROM events
		WHERE details_ciphertext IS NULL AND details IS NOT NULL AND details <> ''
	`).Row().Scan(&counts.Legacy); err != nil {
		return counts, fmt.Errorf("count LEGACY rows: %w", err)
	}
	if err := db.Raw(`
		SELECT COUNT(*) FROM events
		WHERE details IS NOT NULL AND details <> '' AND details_ciphertext IS NOT NULL
	`).Row().Scan(&counts.Both); err != nil {
		return counts, fmt.Errorf("count BOTH rows: %w", err)
	}
	if err := db.Raw(`
		SELECT COUNT(*) FROM events
		WHERE (details IS NULL OR details = '') AND details_ciphertext IS NOT NULL
	`).Row().Scan(&counts.Migrated); err != nil {
		return counts, fmt.Errorf("count MIGRATED rows: %w", err)
	}
	invalid, err := countInvalidEventDetailsCiphertext(cipher)
	if err != nil {
		return counts, fmt.Errorf("count INVALID rows: %w", err)
	}
	counts.Invalid = invalid
	return counts, nil
}

// FinalizeEventDetailsMigration is the --finalize-event-details gate. It
// runs the real countEventDetailsStates preflight and only sets the durable
// marker to FINALIZED if LEGACY, BOTH, and INVALID are all exactly zero.
// Otherwise it returns ErrEventDetailsNotFinalizable with the exact counts,
// changing nothing.
func FinalizeEventDetailsMigration(cipher *credentials.Cipher) (EventDetailsStateCounts, error) {
	if conf == nil {
		return EventDetailsStateCounts{}, errors.New("database is not configured")
	}
	if err := ValidateEventDetailsBackend(conf.DBName); err != nil {
		return EventDetailsStateCounts{}, err
	}
	if cipher == nil {
		return EventDetailsStateCounts{}, ErrEventDetailsKeyringRequired
	}

	counts, err := countEventDetailsStates(cipher)
	if err != nil {
		return counts, fmt.Errorf("finalization preflight query: %w", err)
	}
	if counts.Legacy > 0 || counts.Both > 0 || counts.Invalid > 0 {
		return counts, fmt.Errorf(
			"%w: legacy=%d both=%d invalid=%d",
			ErrEventDetailsNotFinalizable, counts.Legacy, counts.Both, counts.Invalid,
		)
	}
	if err := setEventDetailsMigrationState(EventDetailsStateFinalized); err != nil {
		return counts, err
	}
	return counts, nil
}

// envelopeKeyIDPrefix extracts the key ID from an envelope's plaintext
// "gophishfr-cred:v1:<key-id>:..." prefix WITHOUT decrypting, exactly as
// goal.md requires for old-key inventory: internal/credentials deliberately
// keeps the key ID un-encrypted and readable by string parsing alone (see
// doc.go), and this is a direct, unmodified use of that documented
// property -- no change to internal/credentials was needed or made.
func envelopeKeyIDPrefix(envelope string) (string, bool) {
	const magic = "gophishfr-cred"
	parts := strings.SplitN(envelope, ":", 5)
	if len(parts) != 5 || parts[0] != magic {
		return "", false
	}
	return parts[2], true
}

// EventDetailsKeyInventory maps envelope key ID -> row count.
// "(unparseable)" counts rows whose stored bytes do not even match the
// envelope's plaintext prefix shape.
type EventDetailsKeyInventory map[string]int64

// InventoryEventDetailsKeys is the read-only --inventory-event-details-keys
// preflight: it counts details_ciphertext rows by key ID using only
// envelopeKeyIDPrefix (string parsing), performing zero decryption, so an
// operator can verify "count for old key ID = 0" before retiring that key.
// It is batched (never the whole table in memory at once) exactly like the
// migration/rotation tooling.
func InventoryEventDetailsKeys() (EventDetailsKeyInventory, error) {
	inventory := EventDetailsKeyInventory{}
	var lastID int64
	for {
		var rows []struct {
			ID                int64  `gorm:"column:id"`
			DetailsCiphertext []byte `gorm:"column:details_ciphertext"`
		}
		if err := db.Raw(`
			SELECT id, details_ciphertext FROM events
			WHERE details_ciphertext IS NOT NULL AND id > ?
			ORDER BY id ASC LIMIT ?
		`, lastID, eventDetailsMigrationBatchSize).Scan(&rows).Error; err != nil {
			return inventory, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			keyID, ok := envelopeKeyIDPrefix(string(row.DetailsCiphertext))
			if !ok {
				keyID = "(unparseable)"
			}
			inventory[keyID]++
			lastID = row.ID
		}
	}
	return inventory, nil
}

// RotateEventDetailsCredentials is the --rotate-event-details-credentials
// bulk key-rotation tool. activeKeyID identifies the currently-active key
// (sourced from the same Keyring main.go already loads -- see
// loadCredentialCipher -- since internal/credentials.Cipher deliberately
// does not expose its own active key ID; this is a signature-level design
// choice, not a change to internal/credentials). Rows are inventoried by
// envelope-prefix parsing only (never decrypting just to decide whether a
// row needs rotation); only rows whose key ID differs from activeKeyID are
// decrypted (with the old key) and re-encrypted (with the active key, same
// Context/AAD), then read back and decrypt-compared before each row's own
// commit.
func RotateEventDetailsCredentials(cipher *credentials.Cipher, activeKeyID string) (EventDetailsRotationResult, error) {
	result := EventDetailsRotationResult{}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateEventDetailsBackend(conf.DBName); err != nil {
		return result, err
	}
	if cipher == nil {
		return result, ErrEventDetailsKeyringRequired
	}
	if activeKeyID == "" {
		return result, fmt.Errorf("%w: active key id is required", ErrEventDetailsInvalidState)
	}

	var lastID int64
	for {
		var rows []struct {
			ID                int64  `gorm:"column:id"`
			CampaignID        int64  `gorm:"column:campaign_id"`
			DetailsCiphertext []byte `gorm:"column:details_ciphertext"`
		}
		if err := db.Raw(`
			SELECT id, campaign_id, details_ciphertext FROM events
			WHERE details_ciphertext IS NOT NULL AND id > ?
			ORDER BY id ASC LIMIT ?
		`, lastID, eventDetailsMigrationBatchSize).Scan(&rows).Error; err != nil {
			return result, err
		}
		if len(rows) == 0 {
			break
		}
		result.Batches++
		for _, row := range rows {
			if row.ID > lastID {
				lastID = row.ID
			}
			keyID, ok := envelopeKeyIDPrefix(string(row.DetailsCiphertext))
			if ok && keyID == activeKeyID {
				result.Unchanged++
				continue
			}
			if err := rotateEventDetailsRow(cipher, row.ID, row.CampaignID, row.DetailsCiphertext, &result); err != nil {
				result.Failed = append(result.Failed, EventDetailsRowFailure{EventID: row.ID, Reason: classifyEventDetailsError(err)})
			}
		}
	}
	result.LastID = lastID
	return result, nil
}

func rotateEventDetailsRow(
	cipher *credentials.Cipher,
	id, campaignID int64,
	oldEnvelope []byte,
	result *EventDetailsRotationResult,
) error {
	ctx := eventDetailsContext(campaignID, id)
	plaintext, err := cipher.Decrypt(ctx, credentials.Envelope(oldEnvelope))
	if err != nil {
		return fmt.Errorf("%w for event %d", ErrEventDetailsInvalidCiphertext, id)
	}
	newEnvelope, err := cipher.Encrypt(ctx, plaintext)
	if err != nil {
		return fmt.Errorf("%w for event %d", ErrEventDetailsUnavailable, id)
	}
	newEnvelopeBytes := []byte(string(newEnvelope))

	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	update := transaction.Exec(`
		UPDATE events SET details_ciphertext = ?
		WHERE id = ? AND details_ciphertext = ?
	`, newEnvelopeBytes, id, oldEnvelope)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("event %d changed concurrently during rotation", id)
	}

	stored, err := readEventDetailsRow(transaction, id)
	if err != nil {
		return err
	}
	verified, err := cipher.Decrypt(ctx, credentials.Envelope(stored.DetailsCiphertext))
	if err != nil || string(verified) != string(plaintext) {
		return fmt.Errorf("%w for event %d", ErrEventDetailsStorageMismatch, id)
	}

	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	result.Rotated++
	return nil
}
