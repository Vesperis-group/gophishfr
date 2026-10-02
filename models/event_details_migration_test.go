package models

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/pressly/goose/v3"
)

func seedLegacyEventRow(t *testing.T, campaignID int64, details string) int64 {
	t.Helper()
	// INSERT and the follow-up last_insert_rowid() must run on the exact
	// same underlying connection, which a shared *gorm.DB connection pool
	// does not otherwise guarantee between two separate calls. A single
	// transaction pins both statements to one connection.
	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin seed transaction: %v", transaction.Error)
	}
	if err := transaction.Exec(
		"INSERT INTO events (campaign_id, email, time, message, details) VALUES (?, ?, CURRENT_TIMESTAMP, ?, ?)",
		campaignID, "legacy@example.test", "Clicked Link", details,
	).Error; err != nil {
		transaction.Rollback()
		t.Fatalf("seed legacy event row: %v", err)
	}
	var id int64
	if err := transaction.Raw("SELECT last_insert_rowid()").Row().Scan(&id); err != nil {
		transaction.Rollback()
		t.Fatalf("read seeded event id: %v", err)
	}
	if err := transaction.Commit().Error; err != nil {
		t.Fatalf("commit seed transaction: %v", err)
	}
	return id
}

func syntheticDetailsOfSize(t *testing.T, size int) string {
	t.Helper()
	details := EventDetails{
		Payload: url.Values{"password": {strings.Repeat("Q", size)}},
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// --- BATCH_MIGRATION / RESUME / TRANSITION_BOUNDED -------------------------

func TestMigrateEventDetailsBatchIsIdempotentAndRowStateDriven(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x21}, 32),
	})

	var legacyIDs []int64
	for i := 0; i < 5; i++ {
		legacyIDs = append(legacyIDs, seedLegacyEventRow(t, 1, syntheticDetailsOfSize(t, 16)))
	}
	// Malformed JSON must still be crypto-migratable.
	malformedID := seedLegacyEventRow(t, 1, "{not valid json")

	result, err := MigrateEventDetailsBatch(cipher)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if result.RowsEncrypted != 6 {
		t.Fatalf("rows encrypted = %d, want 6", result.RowsEncrypted)
	}
	if len(result.MalformedJSON) != 1 || result.MalformedJSON[0] != malformedID {
		t.Fatalf("malformed JSON report = %v, want [%d]", result.MalformedJSON, malformedID)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("unexpected failures: %+v", result.Failed)
	}

	for _, id := range append(legacyIDs, malformedID) {
		var legacy sql.NullString
		var ciphertext []byte
		if err := db.Raw("SELECT details, details_ciphertext FROM events WHERE id = ?", id).
			Row().Scan(&legacy, &ciphertext); err != nil {
			t.Fatalf("read migrated row %d: %v", id, err)
		}
		if legacy.Valid {
			t.Fatalf("row %d retained legacy plaintext after migration", id)
		}
		if len(ciphertext) == 0 {
			t.Fatalf("row %d was not encrypted", id)
		}
	}

	// Idempotent re-run: the authoritative pending signal is row state, not
	// a cursor, so a second run finds nothing pending.
	second, err := MigrateEventDetailsBatch(cipher)
	if err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}
	if second.RowsEncrypted != 0 {
		t.Fatalf("idempotent re-run encrypted %d rows, want 0", second.RowsEncrypted)
	}
}

func TestMigrateEventDetailsBatchResumesAfterInterruption(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x22}, 32),
	})

	var ids []int64
	for i := 0; i < 4; i++ {
		ids = append(ids, seedLegacyEventRow(t, 1, syntheticDetailsOfSize(t, 16)))
	}

	// Simulate a crash partway through migration: encrypt and clear the
	// first two rows "by hand" (bypassing MigrateEventDetailsBatch), leaving
	// the rest LEGACY, then verify a fresh MigrateEventDetailsBatch call
	// finds and finishes exactly the remaining rows -- proving row state,
	// not a remembered ID cursor, drives what is "still pending".
	for _, id := range ids[:2] {
		var details string
		if err := db.Raw("SELECT details FROM events WHERE id = ?", id).Row().Scan(&details); err != nil {
			t.Fatal(err)
		}
		envelope, err := cipher.Encrypt(eventDetailsContext(1, id), []byte(details))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(
			"UPDATE events SET details = NULL, details_ciphertext = ? WHERE id = ?",
			[]byte(envelope), id,
		).Error; err != nil {
			t.Fatal(err)
		}
	}

	result, err := MigrateEventDetailsBatch(cipher)
	if err != nil {
		t.Fatalf("resumed migration: %v", err)
	}
	if result.RowsEncrypted != 2 {
		t.Fatalf("resumed migration encrypted %d rows, want exactly the 2 still-pending rows", result.RowsEncrypted)
	}

	var remainingLegacy int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM events WHERE details_ciphertext IS NULL AND details IS NOT NULL AND details <> ''",
	).Row().Scan(&remainingLegacy); err != nil {
		t.Fatal(err)
	}
	if remainingLegacy != 0 {
		t.Fatalf("remaining LEGACY rows = %d, want 0", remainingLegacy)
	}
}

func TestMigrateEventDetailsReconcilesBothState(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x23}, 32),
	})

	matchingDetails := syntheticDetailsOfSize(t, 16)
	matchingID := seedLegacyEventRow(t, 1, matchingDetails)
	matchingEnvelope, err := cipher.Encrypt(eventDetailsContext(1, matchingID), []byte(matchingDetails))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE events SET details_ciphertext = ? WHERE id = ?", []byte(matchingEnvelope), matchingID,
	).Error; err != nil {
		t.Fatal(err)
	}

	mismatchedID := seedLegacyEventRow(t, 1, syntheticDetailsOfSize(t, 16))
	mismatchedEnvelope, err := cipher.Encrypt(eventDetailsContext(1, mismatchedID), []byte(syntheticDetailsOfSize(t, 24)))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE events SET details_ciphertext = ? WHERE id = ?", []byte(mismatchedEnvelope), mismatchedID,
	).Error; err != nil {
		t.Fatal(err)
	}

	result, err := MigrateEventDetailsBatch(cipher)
	if err != nil {
		t.Fatalf("migrate with BOTH-state rows: %v", err)
	}
	if result.RowsReconciled != 1 {
		t.Fatalf("rows reconciled = %d, want 1", result.RowsReconciled)
	}
	if len(result.Failed) != 1 || result.Failed[0].EventID != mismatchedID {
		t.Fatalf("failed rows = %+v, want exactly mismatched row %d reported", result.Failed, mismatchedID)
	}
	if result.Failed[0].Reason != "mismatched-both" {
		t.Fatalf("failure reason = %q, want mismatched-both (and never plaintext content)", result.Failed[0].Reason)
	}

	// The matching row was finalized (legacy cleared); the mismatched row is
	// left exactly as found for operator investigation -- never guessed at.
	var matchingLegacy sql.NullString
	if err := db.Raw("SELECT details FROM events WHERE id = ?", matchingID).Row().Scan(&matchingLegacy); err != nil {
		t.Fatal(err)
	}
	if matchingLegacy.Valid {
		t.Fatal("matching BOTH row was not cleared")
	}
	var mismatchedLegacy sql.NullString
	if err := db.Raw("SELECT details FROM events WHERE id = ?", mismatchedID).Row().Scan(&mismatchedLegacy); err != nil {
		t.Fatal(err)
	}
	if !mismatchedLegacy.Valid {
		t.Fatal("mismatched BOTH row's legacy copy was destroyed instead of left for investigation")
	}
}

// --- CONCURRENT_WRITER_DURING_MIGRATION ------------------------------------

func TestConcurrentWriteDuringMigrationIsNeverMistakenForLegacy(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x24}, 32),
	})
	withEventDetailsCipher(t, cipher)

	legacyID := seedLegacyEventRow(t, 1, syntheticDetailsOfSize(t, 16))

	// A new event is written by the "new binary" (AddEvent) while the
	// migration has not run yet.
	newEvent := &Event{Message: "Clicked Link", Details: syntheticDetailsOfSize(t, 16)}
	if err := AddEvent(newEvent, 1); err != nil {
		t.Fatalf("concurrent new write: %v", err)
	}

	result, err := MigrateEventDetailsBatch(cipher)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Only the pre-existing LEGACY row should have been processed; the new,
	// already-ciphertext-only event must never be counted as encrypted by
	// the migration (it was never LEGACY).
	if result.RowsEncrypted != 1 {
		t.Fatalf("rows encrypted = %d, want exactly the 1 pre-existing legacy row", result.RowsEncrypted)
	}

	var newEventLegacy sql.NullString
	var newEventCiphertext []byte
	if err := db.Raw("SELECT details, details_ciphertext FROM events WHERE id = ?", newEvent.Id).
		Row().Scan(&newEventLegacy, &newEventCiphertext); err != nil {
		t.Fatal(err)
	}
	if newEventLegacy.Valid {
		t.Fatal("the concurrently written new event ended up with legacy plaintext")
	}
	if len(newEventCiphertext) == 0 {
		t.Fatal("the concurrently written new event lost its ciphertext")
	}

	var legacyCiphertext []byte
	if err := db.Raw("SELECT details_ciphertext FROM events WHERE id = ?", legacyID).Row().Scan(&legacyCiphertext); err != nil {
		t.Fatal(err)
	}
	if len(legacyCiphertext) == 0 {
		t.Fatal("the pre-existing legacy row was not migrated")
	}
}

// --- FINALIZATION_GATE ------------------------------------------------------

func TestFinalizeEventDetailsMigrationGatesOnRealPreflight(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x25}, 32),
	})

	seedLegacyEventRow(t, 1, syntheticDetailsOfSize(t, 16))

	if _, err := FinalizeEventDetailsMigration(cipher); !errors.Is(err, ErrEventDetailsNotFinalizable) {
		t.Fatalf("premature finalize error = %v, want ErrEventDetailsNotFinalizable", err)
	}
	state, err := eventDetailsMigrationState()
	if err != nil {
		t.Fatal(err)
	}
	if state != EventDetailsStateMigrating {
		t.Fatalf("marker state = %q after a rejected finalize, want unchanged %q", state, EventDetailsStateMigrating)
	}

	if _, err := MigrateEventDetailsBatch(cipher); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	counts, err := FinalizeEventDetailsMigration(cipher)
	if err != nil {
		t.Fatalf("finalize after clean migration: %v", err)
	}
	if counts.Legacy != 0 || counts.Both != 0 || counts.Invalid != 0 {
		t.Fatalf("finalize counts = %+v, want all zero", counts)
	}
	state, err = eventDetailsMigrationState()
	if err != nil {
		t.Fatal(err)
	}
	if state != EventDetailsStateFinalized {
		t.Fatalf("marker state = %q after finalize, want %q", state, EventDetailsStateFinalized)
	}
}

func TestEventDetailsMigrationMarkerSurvivesRestart(t *testing.T) {
	conf := setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x26}, 32),
	})

	if _, err := FinalizeEventDetailsMigration(cipher); err != nil {
		t.Fatalf("finalize on empty database: %v", err)
	}

	// Simulate a process restart: close and reopen the same on-disk database
	// via a fresh Setup call (goose Up is a no-op the second time, but the
	// marker row -- and whatever state was last set -- must still be there).
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Setup(conf); err != nil {
		t.Fatalf("re-setup (simulated restart): %v", err)
	}

	state, err := eventDetailsMigrationState()
	if err != nil {
		t.Fatalf("read marker after restart: %v", err)
	}
	if state != EventDetailsStateFinalized {
		t.Fatalf("marker state after restart = %q, want %q (durable across restart)", state, EventDetailsStateFinalized)
	}
}

// --- ROLLBACK ---------------------------------------------------------------

func TestRollbackEventDetailsBatchRestoresPlaintextAndUnfinalizes(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x27}, 32),
	})

	details := make([]string, 3)
	ids := make([]int64, 3)
	for i := range details {
		details[i] = syntheticDetailsOfSize(t, 16+i)
		ids[i] = seedLegacyEventRow(t, 1, details[i])
	}
	if _, err := MigrateEventDetailsBatch(cipher); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := FinalizeEventDetailsMigration(cipher); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	result, err := RollbackEventDetailsBatch(cipher)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if result.RowsRestored != 3 {
		t.Fatalf("rows restored = %d, want 3", result.RowsRestored)
	}
	for i, id := range ids {
		var legacy sql.NullString
		var ciphertext []byte
		if err := db.Raw("SELECT details, details_ciphertext FROM events WHERE id = ?", id).
			Row().Scan(&legacy, &ciphertext); err != nil {
			t.Fatal(err)
		}
		if !legacy.Valid || legacy.String != details[i] {
			t.Fatalf("row %d restored plaintext = %q, want %q", id, legacy.String, details[i])
		}
		if ciphertext != nil {
			t.Fatalf("row %d retained ciphertext after rollback", id)
		}
	}

	// A completed rollback that empties MIGRATED/BOTH un-finalizes the
	// durable marker: FINALIZED asserts "zero legacy rows exist", which this
	// rollback deliberately makes false again.
	state, err := eventDetailsMigrationState()
	if err != nil {
		t.Fatal(err)
	}
	if state != EventDetailsStateMigrating {
		t.Fatalf("marker state after full rollback = %q, want %q", state, EventDetailsStateMigrating)
	}

	// Idempotent re-run.
	second, err := RollbackEventDetailsBatch(cipher)
	if err != nil || second.RowsRestored != 0 {
		t.Fatalf("idempotent rollback re-run: result=%+v err=%v", second, err)
	}
}

// --- Down migration guard ----------------------------------------------------

func TestDownMigrationRefusesWhileCiphertextOrFinalizedRemains(t *testing.T) {
	conf := setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x28}, 32),
	})

	seedLegacyEventRow(t, 1, syntheticDetailsOfSize(t, 16))
	if _, err := MigrateEventDetailsBatch(cipher); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("schema Down accepted a row with remaining ciphertext")
	}

	if _, err := RollbackEventDetailsBatch(cipher); err != nil {
		t.Fatalf("rollback before schema Down: %v", err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("schema Down after a clean rollback: %v", err)
	}

	var hasCiphertextColumn int
	if err := db.Raw("SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'details_ciphertext'").
		Row().Scan(&hasCiphertextColumn); err != nil {
		t.Fatal(err)
	}
	if hasCiphertextColumn != 0 {
		t.Fatal("schema Down did not drop details_ciphertext")
	}
}

func TestDownMigrationRefusesWhileFinalizedEvenWithoutCiphertextRows(t *testing.T) {
	conf := setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x29}, 32),
	})

	// An empty database (no events at all) still satisfies the finalization
	// preflight (zero LEGACY/BOTH/INVALID), but the marker itself must still
	// block an un-rolled-back schema Down.
	if _, err := FinalizeEventDetailsMigration(cipher); err != nil {
		t.Fatalf("finalize empty database: %v", err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("schema Down accepted a FINALIZED marker without an explicit rollback")
	}
}

// --- BULK_ROTATION / OLD_KEY_RETIREMENT -------------------------------------

func TestRotateEventDetailsCredentialsAndInventory(t *testing.T) {
	setupEventDetailsDatabase(t)
	oldCipher := testCredentialCipher(t, "event-key-old", map[string][]byte{
		"event-key-old": bytes.Repeat([]byte{0x2a}, 32),
		"event-key-new": bytes.Repeat([]byte{0x2b}, 32),
	})
	newCipher := testCredentialCipher(t, "event-key-new", map[string][]byte{
		"event-key-old": bytes.Repeat([]byte{0x2a}, 32),
		"event-key-new": bytes.Repeat([]byte{0x2b}, 32),
	})

	withEventDetailsCipher(t, oldCipher)
	var oldKeyEvents []*Event
	for i := 0; i < 3; i++ {
		event := &Event{Message: "Clicked Link", Details: syntheticDetailsOfSize(t, 16)}
		if err := AddEvent(event, 1); err != nil {
			t.Fatalf("create event under old key: %v", err)
		}
		oldKeyEvents = append(oldKeyEvents, event)
	}

	inventory, err := InventoryEventDetailsKeys()
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if inventory["event-key-old"] != 3 {
		t.Fatalf("inventory for old key = %d, want 3 (envelope-prefix parsing only, no decryption)", inventory["event-key-old"])
	}

	result, err := RotateEventDetailsCredentials(newCipher, "event-key-new")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if result.Rotated != 3 || len(result.Failed) != 0 {
		t.Fatalf("rotation result = %+v", result)
	}

	postInventory, err := InventoryEventDetailsKeys()
	if err != nil {
		t.Fatalf("post-rotation inventory: %v", err)
	}
	if postInventory["event-key-old"] != 0 {
		t.Fatalf("old key inventory after full rotation = %d, want 0 (safe to retire)", postInventory["event-key-old"])
	}
	if postInventory["event-key-new"] != 3 {
		t.Fatalf("new key inventory after rotation = %d, want 3", postInventory["event-key-new"])
	}

	for _, event := range oldKeyEvents {
		var ciphertext []byte
		if err := db.Raw("SELECT details_ciphertext FROM events WHERE id = ?", event.Id).Row().Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		plaintext, err := newCipher.Decrypt(eventDetailsContext(event.CampaignId, event.Id), credentials.Envelope(ciphertext))
		if err != nil || string(plaintext) != event.Details {
			t.Fatalf("rotated ciphertext for event %d does not decrypt to the original plaintext: err=%v", event.Id, err)
		}
	}

	// Idempotent re-run: already-active-key rows are reported Unchanged, not
	// re-rotated.
	second, err := RotateEventDetailsCredentials(newCipher, "event-key-new")
	if err != nil || second.Rotated != 0 || second.Unchanged != 3 {
		t.Fatalf("idempotent rotation re-run: result=%+v err=%v", second, err)
	}
}

// --- backend/keyring validation for the batch tools -------------------------

func TestEventDetailsBatchToolsValidateBackendAndKeyring(t *testing.T) {
	setupEventDetailsDatabase(t)
	if _, err := MigrateEventDetailsBatch(nil); !errors.Is(err, ErrEventDetailsKeyringRequired) {
		t.Fatalf("migrate without cipher error = %v", err)
	}
	if _, err := RollbackEventDetailsBatch(nil); !errors.Is(err, ErrEventDetailsKeyringRequired) {
		t.Fatalf("rollback without cipher error = %v", err)
	}
	if _, err := FinalizeEventDetailsMigration(nil); !errors.Is(err, ErrEventDetailsKeyringRequired) {
		t.Fatalf("finalize without cipher error = %v", err)
	}
	if _, err := RotateEventDetailsCredentials(nil, "k"); !errors.Is(err, ErrEventDetailsKeyringRequired) {
		t.Fatalf("rotate without cipher error = %v", err)
	}

	previousConf := conf
	conf = &config.Config{DBName: "postgres"}
	t.Cleanup(func() { conf = previousConf })
	cipher := testCredentialCipher(t, "k", map[string][]byte{"k": bytes.Repeat([]byte{0x2c}, 32)})
	if _, err := MigrateEventDetailsBatch(cipher); !errors.Is(err, ErrUnsupportedEventDetailsDB) {
		t.Fatalf("migrate on unsupported backend error = %v", err)
	}
}

// --- envelope key-ID prefix parsing (used by rotation/inventory) -----------

func TestEnvelopeKeyIDPrefixNeverDecrypts(t *testing.T) {
	cipher := testCredentialCipher(t, "parse-key", map[string][]byte{
		"parse-key": bytes.Repeat([]byte{0x2d}, 32),
	})
	envelope, err := cipher.Encrypt(eventDetailsContext(1, 1), []byte("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	keyID, ok := envelopeKeyIDPrefix(string(envelope))
	if !ok || keyID != "parse-key" {
		t.Fatalf("envelopeKeyIDPrefix = (%q, %v), want (parse-key, true)", keyID, ok)
	}
	if _, ok := envelopeKeyIDPrefix("not-an-envelope"); ok {
		t.Fatal("unparseable input was reported as parseable")
	}
}
