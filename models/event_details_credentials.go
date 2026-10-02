package models

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/jinzhu/gorm"
)

const (
	eventDetailsKind   = "event-details"
	eventDetailsTable  = "events"
	eventDetailsColumn = "details_ciphertext"

	// eventDetailsLegacyBlobCapacityBytes is the historical MySQL `BLOB`
	// column cap (64 KiB - 1 byte) that db/db_mysql/migrations/
	// 20160131153104_0.1.2_add_event_details.sql has used since 2016 and
	// that no later migration changed. SQLite's BLOB affinity has never
	// enforced any such limit, but the legacy `details` column -- the
	// rollback target -- is schema-identical across both backends, so this
	// bound is treated as binding for both.
	eventDetailsLegacyBlobCapacityBytes = 65535

	// eventDetailsPlaintextMaxBytes bounds every Event.Details value this
	// binary writes from the moment this feature ships (enforced in
	// persistEventWithEncryptedDetails, the sole write path -- see
	// AddEvent). It is deliberately far smaller than what the new
	// `details_ciphertext MEDIUMBLOB` column could hold, for exactly one
	// reason: `--rollback-event-details` must always be able to write a
	// post-migration event's decrypted plaintext back into the legacy
	// `details BLOB` column (on both backends) without truncation, so a
	// downgrade to a binary that only understands legacy plaintext never
	// loses data.
	//
	// 44 KiB (45056 bytes) was chosen, instead of simply using the full
	// 65535-byte legacy cap, to leave margin in *two* directions at once:
	//
	//  1. Rollback-write margin: 65535-45056 = 20479 bytes (~31%) of slack
	//     between the bound and the legacy BLOB cap itself.
	//  2. Envelope margin: the v1 envelope format base64-encodes the
	//     ciphertext (plaintext+16-byte GCM tag), which inflates size by
	//     ~4/3. At exactly 65535 bytes of plaintext the resulting envelope
	//     would be roughly 87 KiB -- already larger than a standard 64 KiB
	//     BLOB. 44 KiB keeps the *envelope itself* (computed precisely
	//     below, with a worst-case 64-byte key ID) comfortably under 64 KiB
	//     too, as defense in depth: even if a future maintainer mistakenly
	//     stored an event-details envelope in a standard BLOB column
	//     instead of the MEDIUMBLOB this PR adds, it would still fit.
	//
	// This bound applies only to *new* writes. Legacy rows that already
	// exist (written by older binaries with no such bound, up to the
	// historical 65535-byte MySQL cap) are migrated as-is: see
	// event_details_migration.go, which never rejects a legacy row for
	// being "too big", only internal/credentials' own 10 MiB envelope limit
	// and the MEDIUMBLOB column's 16 MiB capacity apply there.
	eventDetailsPlaintextMaxBytes = 44 * 1024 // 45056 bytes

	// eventDetailsEnvelopeMaxBytesForNewWrites mirrors the compile-time-
	// checked derivation already used by webhookSecretEnvelopeMaxBytes /
	// smtpPasswordEnvelopeMaxBytes: magic + separators + version + a
	// worst-case 64-byte key ID + base64(12-byte nonce) +
	// base64(plaintext+16-byte GCM tag).
	eventDetailsEnvelopeMaxBytesForNewWrites = len("gophishfr-cred") + 4 + len("v1") + 64 +
		((12 + 2) / 3 * 4) +
		((eventDetailsPlaintextMaxBytes + 16 + 2) / 3 * 4)

	// Compile-time proofs of the two margins described above: a negative
	// operand here fails to compile (overflows uint), so these can never
	// silently regress.
	_ = uint(eventDetailsLegacyBlobCapacityBytes - eventDetailsPlaintextMaxBytes)
	_ = uint(eventDetailsLegacyBlobCapacityBytes - eventDetailsEnvelopeMaxBytesForNewWrites)

	// eventDetailsUnavailableSentinel is what GetCampaign/GetCampaignResults
	// substitute for Details when a single event's ciphertext cannot be
	// decrypted (INVALID) or a LEGACY row is encountered after finalization.
	// It is valid JSON (so the unchanged frontend's JSON.parse never
	// throws) and deliberately shaped like the pre-existing EventError
	// struct ({"error": "..."}) rather than inventing a new shape. It never
	// contains the plaintext, the envelope, or any crypto internals.
	eventDetailsUnavailableSentinel = `{"error":"event details unavailable"}`
)

// Sentinel errors for event-details encryption. Every caller-visible failure
// maps to exactly one of these, checkable with errors.Is, and none of them
// (nor anything wrapped around them in this package) ever includes
// plaintext, the envelope string, or key material -- only an event ID and an
// error class, consistent with the PLAINTEXT_LOGGING = NONE requirement.
var (
	ErrEventDetailsKeyringRequired   = errors.New("event details keyring is required")
	ErrEventDetailsUnavailable       = errors.New("event details are unavailable")
	ErrEventDetailsInvalidState      = errors.New("event details columns are in an invalid state")
	ErrInvalidEventDetailsIdentity   = errors.New("event has an invalid record ID")
	ErrUnsupportedEventDetailsDB     = errors.New("event details migration is unsupported for this database")
	ErrEventDetailsTooLarge          = errors.New("event details exceed the storage-safe limit")
	ErrEventDetailsStorageMismatch   = errors.New("event details storage verification failed")
	ErrEventDetailsInvalidCiphertext = errors.New("event details ciphertext failed to decrypt")
	ErrEventDetailsFinalized         = errors.New("event details migration is finalized; legacy plaintext is no longer supported")
	ErrEventDetailsNotFinalizable    = errors.New("event details finalization preflight found unmigrated or inconsistent rows")
	ErrEventDetailsMismatchedBoth    = errors.New("event has both legacy plaintext and ciphertext that do not match")
)

// eventDetailsCipher is the shared application credential cipher used both
// at the AddEvent write boundary and at the GetCampaign/GetCampaignResults
// read boundary. It is set exactly once at startup by SetEventDetailsCipher
// -- see main.go -- from the *same* credentials.Cipher instance (same
// physical GOPHISHFR_CREDENTIAL_KEYRING_FILE keyring) already used for
// IMAP/SMTP/webhook secrets. Context.Kind = "event-details" is what
// separates this domain from those others: this is domain separation via
// AAD context binding, NOT independent cryptographic key separation. A
// compromise of the shared keyring file compromises every domain using it;
// see docs/event-details-encryption.md.
var eventDetailsCipher *credentials.Cipher

// SetEventDetailsCipher installs the shared application credential cipher
// used for Event.Details encryption/decryption. Callers must pass the exact
// same cipher instance already loaded for IMAP/SMTP/webhook secrets; this
// function does not construct a second keyring or parser.
func SetEventDetailsCipher(cipher *credentials.Cipher) {
	eventDetailsCipher = cipher
}

// activeEventDetailsCipher returns the cipher installed by
// SetEventDetailsCipher. AddEvent (no per-call cipher available) and the
// GetCampaign/GetCampaignResults read paths both use this package-level
// accessor; every offline/batch tool instead receives its cipher explicitly
// as a parameter.
func activeEventDetailsCipher() *credentials.Cipher {
	return eventDetailsCipher
}

// eventDetailsContext returns the exact, stable AAD for one event's
// details: Kind identifies this credential family, Table/Column pin it to
// events.details_ciphertext, OwnerID is the event's CampaignId (bound here
// specifically because it is provably immutable after creation -- see the
// doc comment on the Event struct and AddEvent in campaign.go, and
// acceptance criterion 20), and RecordID is the event's own immutable,
// database-assigned primary key. No other field (Email, Time, Message, or
// any payload content) is ever included: every one of those is either
// mutable-adjacent or redundant with RecordID/OwnerID, and widening the AAD
// surface is explicitly forbidden by goal.md.
func eventDetailsContext(campaignID, eventID int64) credentials.Context {
	return credentials.Context{
		Kind:     eventDetailsKind,
		Table:    eventDetailsTable,
		Column:   eventDetailsColumn,
		OwnerID:  strconv.FormatInt(campaignID, 10),
		RecordID: strconv.FormatInt(eventID, 10),
	}
}

// ValidateEventDetailsBackend rejects PostgreSQL and every backend for
// which this repository has no real event-details schema. There is no
// db/db_postgres migration directory anywhere in this repository (only a
// driver-level connection implementation and a synthetic integration test);
// claiming PostgreSQL support for this feature would be a false claim.
func ValidateEventDetailsBackend(databaseName string) error {
	switch databaseName {
	case "sqlite3", "mysql":
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedEventDetailsDB, databaseName)
	}
}

func validateEventDetailsPlaintextSize(details string) error {
	if len(details) > eventDetailsPlaintextMaxBytes {
		return fmt.Errorf(
			"%w: %d bytes exceeds the %d byte limit for new writes",
			ErrEventDetailsTooLarge, len(details), eventDetailsPlaintextMaxBytes,
		)
	}
	return nil
}

// storedEventDetailsRow is the raw, COALESCE-free view of one events row's
// detail columns used for read-back verification. Details is read as
// sql.NullString because, post-write, the legacy column is always
// explicitly NULL (never ""), and preserving that distinction is part of
// what read-back verification proves.
type storedEventDetailsRow struct {
	ID                int64          `gorm:"column:id"`
	CampaignID        int64          `gorm:"column:campaign_id"`
	Details           sql.NullString `gorm:"column:details"`
	DetailsCiphertext []byte         `gorm:"column:details_ciphertext"`
}

func readEventDetailsRow(transaction *gorm.DB, id int64) (storedEventDetailsRow, error) {
	var row storedEventDetailsRow
	if err := transaction.Raw(`
		SELECT id, campaign_id, details, details_ciphertext
		FROM events
		WHERE id = ?
	`, id).Scan(&row).Error; err != nil {
		return storedEventDetailsRow{}, err
	}
	if row.ID != id {
		return storedEventDetailsRow{}, gorm.ErrRecordNotFound
	}
	return row, nil
}

// persistEventWithEncryptedDetails implements the binding two-phase
// new-write sequence from goal.md, as the sole body of AddEvent's database
// write:
//
//  1. Insert the event row with both legacy detail columns forced to SQL
//     NULL (never the legacy column receiving new plaintext).
//  2. Obtain the DB-assigned autoincrement ID.
//  3. Build Context with that ID as RecordID (and CampaignId as OwnerID).
//  4. Encrypt the JSON-marshaled Details.
//  5. Write details_ciphertext.
//  6. Read back the exact bytes just written and decrypt them, comparing to
//     the original plaintext, before committing.
//  7. Commit; any failure at any step rolls back the whole transaction, so
//     no durable shell row with neither legacy details nor ciphertext ever
//     survives a failed write.
//
// An Event with nil/empty Details (e.g. "Email Sent") never has an
// artificial ciphertext created: both detail columns are left NULL and the
// function returns after step 1.
//
// Design note on step 1: gorm's ORM Create is what already reliably returns
// the cross-driver (SQLite/MySQL) autoincrement ID the same way the
// pre-existing db.Save(e) did; jinzhu/gorm's Exec helper does not expose the
// underlying sql.Result, so a raw INSERT would require bespoke,
// backend-specific last-insert-id handling for no additional safety, since
// nothing outside this same, still-uncommitted transaction can ever observe
// the transient empty-string value ORM Create writes before the immediate
// follow-up UPDATE forces both columns to NULL. This is the one place this
// PR deviates from a literal raw-SQL insert, and it is explicitly
// justified here per goal.md's instruction to document any such deviation.
func persistEventWithEncryptedDetails(e *Event) error {
	plaintext := e.Details
	if err := validateEventDetailsPlaintextSize(plaintext); err != nil {
		return err
	}
	cipher := activeEventDetailsCipher()
	if plaintext != "" && cipher == nil {
		return ErrEventDetailsKeyringRequired
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

	insertable := *e
	insertable.Details = ""
	insertable.DetailsCiphertext = nil
	if err := transaction.Create(&insertable).Error; err != nil {
		return fmt.Errorf("insert event shell: %w", err)
	}
	e.Id = insertable.Id

	// Never let a newly written event durably expose plaintext through the
	// legacy column: force both legacy columns to SQL NULL immediately,
	// inside this same, still-uncommitted transaction.
	if err := transaction.Exec(`
		UPDATE events SET details = NULL, details_ciphertext = NULL WHERE id = ?
	`, e.Id).Error; err != nil {
		return fmt.Errorf("clear event shell columns: %w", err)
	}

	if plaintext == "" {
		if err := transaction.Commit().Error; err != nil {
			return err
		}
		committed = true
		return nil
	}

	ctx := eventDetailsContext(e.CampaignId, e.Id)
	envelope, err := cipher.Encrypt(ctx, []byte(plaintext))
	if err != nil {
		return fmt.Errorf("%w for event %d", ErrEventDetailsUnavailable, e.Id)
	}
	envelopeBytes := []byte(string(envelope))

	update := transaction.Exec(`
		UPDATE events SET details_ciphertext = ?
		WHERE id = ? AND details IS NULL AND details_ciphertext IS NULL
	`, envelopeBytes, e.Id)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("event %d changed unexpectedly while writing encrypted details", e.Id)
	}

	stored, err := readEventDetailsRow(transaction, e.Id)
	if err != nil {
		return fmt.Errorf("read back event %d: %w", e.Id, err)
	}
	verifiedPlaintext, err := cipher.Decrypt(ctx, credentials.Envelope(stored.DetailsCiphertext))
	if err != nil || !bytes.Equal(verifiedPlaintext, []byte(plaintext)) {
		return fmt.Errorf("%w for event %d", ErrEventDetailsStorageMismatch, e.Id)
	}

	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	e.DetailsCiphertext = envelopeBytes
	return nil
}

// decryptEventDetails returns the plaintext Details for one already-loaded
// Event, applying the binding transition-state taxonomy from goal.md:
//
//	EMPTY    -> ("", nil)
//	LEGACY   -> (e.Details, nil) while not finalized; a fail-closed error
//	            once finalized (a LEGACY row found after finalization is not
//	            silently served)
//	MIGRATED -> decrypt and return the plaintext; a decrypt failure is
//	            ErrEventDetailsInvalidCiphertext and NEVER falls back to any
//	            legacy plaintext that might still be present in the row
//	BOTH     -> ErrEventDetailsInvalidState (never silently prefer one side)
func decryptEventDetails(e Event, cipher *credentials.Cipher, finalized bool) (string, error) {
	hasCiphertext := len(e.DetailsCiphertext) > 0
	hasLegacy := e.Details != ""

	switch {
	case !hasCiphertext && !hasLegacy:
		return "", nil
	case !hasCiphertext:
		if finalized {
			return "", fmt.Errorf("%w for event %d", ErrEventDetailsFinalized, e.Id)
		}
		return e.Details, nil
	case hasLegacy:
		// BOTH at read time: never silently prefer one side.
		return "", fmt.Errorf("%w for event %d", ErrEventDetailsInvalidState, e.Id)
	default:
		if cipher == nil {
			return "", ErrEventDetailsKeyringRequired
		}
		ctx := eventDetailsContext(e.CampaignId, e.Id)
		plaintext, err := cipher.Decrypt(ctx, credentials.Envelope(e.DetailsCiphertext))
		if err != nil {
			return "", fmt.Errorf("%w for event %d", ErrEventDetailsInvalidCiphertext, e.Id)
		}
		return string(plaintext), nil
	}
}

// decryptEventsInPlace decrypts every element of events in place and is the
// sole availability decision point for GetCampaign/GetCampaignResults: a
// single corrupted, inconsistent, or (post-finalization) legacy event never
// prevents the rest of the campaign's timeline from being returned. The
// affected event's Details is replaced with eventDetailsUnavailableSentinel
// (never the plaintext, the envelope, or any crypto internals) and only the
// event ID plus a short error class are logged.
//
// This is an explicit, documented, tested availability decision (see
// TestDecryptEventsInPlaceIsolatesCorruptedRow): one bad row degrades
// gracefully instead of failing the entire request, because the existing
// owner-scoped GetCampaign/GetCampaignResults access control is unaffected
// either way, and a narrower failure mode (one event, not the whole
// campaign) is reasonable and safer for operators investigating an
// incident.
func decryptEventsInPlace(events []Event) {
	cipher := activeEventDetailsCipher()
	finalized := eventDetailsMigrationFinalized()
	for i := range events {
		plaintext, err := decryptEventDetails(events[i], cipher, finalized)
		if err != nil {
			log.Errorf("event %d details unavailable: %s", events[i].Id, classifyEventDetailsError(err))
			plaintext = eventDetailsUnavailableSentinel
		}
		events[i].Details = plaintext
		events[i].DetailsCiphertext = nil
	}
}

// classifyEventDetailsError maps an event-details error to a short,
// non-leaking class string suitable for logs and migration/rotation tool
// output. It never includes the dynamic error text (which could, in
// principle, later grow to embed more context) -- only a fixed label.
func classifyEventDetailsError(err error) string {
	switch {
	case errors.Is(err, ErrEventDetailsInvalidCiphertext):
		return "invalid-ciphertext"
	case errors.Is(err, ErrEventDetailsMismatchedBoth):
		return "mismatched-both"
	case errors.Is(err, ErrEventDetailsStorageMismatch):
		return "storage-mismatch"
	case errors.Is(err, ErrEventDetailsUnavailable):
		return "cipher-unavailable"
	case errors.Is(err, ErrEventDetailsTooLarge):
		return "too-large"
	case errors.Is(err, ErrEventDetailsInvalidState):
		return "invalid-state"
	case errors.Is(err, ErrEventDetailsFinalized):
		return "finalized-legacy-row"
	case errors.Is(err, ErrEventDetailsKeyringRequired):
		return "keyring-required"
	default:
		return "error"
	}
}
