package models

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
)

// setupEventDetailsDatabase provisions a fresh, file-backed SQLite database
// (not :memory:, so a second Setup call against the same path can exercise
// "restart" scenarios) with every migration applied, matching the existing
// setupIMAPCredentialDatabase helper's conventions.
func setupEventDetailsDatabase(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv(InitialAdminPassword, "synthetic-test-admin-password")
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         filepath.Join(t.TempDir(), "event-details.db"),
		MigrationsPath: "../db/db_sqlite3/migrations",
	}
	if err := Setup(conf); err != nil {
		t.Fatalf("setup event details database: %v", err)
	}
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
	})
	return conf
}

func withEventDetailsCipher(t *testing.T, cipher *credentials.Cipher) {
	t.Helper()
	previous := activeEventDetailsCipher()
	SetEventDetailsCipher(cipher)
	t.Cleanup(func() { SetEventDetailsCipher(previous) })
}

func syntheticEventDetailsJSON(t *testing.T, payloadSize int) string {
	t.Helper()
	details := EventDetails{
		Payload: url.Values{
			"username": {"synthetic-user"},
			"password": {strings.Repeat("P", payloadSize)},
		},
		Browser: map[string]string{"user-agent": "synthetic-agent"},
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal synthetic event details: %v", err)
	}
	return string(encoded)
}

// --- NEW_WRITE_PLAINTEXT = NONE / EVENT_DETAILS_AT_REST ---------------------

func TestAddEventNeverPersistsNewPlaintext(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x11}, 32),
	})
	withEventDetailsCipher(t, cipher)

	cases := []struct {
		name    string
		details string
	}{
		{"empty", ""},
		{"non-empty", syntheticEventDetailsJSON(t, 32)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := &Event{Message: "Email Opened", Details: tc.details}
			if err := AddEvent(event, 7); err != nil {
				t.Fatalf("AddEvent: %v", err)
			}
			if event.Id == 0 {
				t.Fatal("AddEvent did not assign an ID")
			}
			var legacy sql.NullString
			var ciphertext []byte
			if err := db.Raw(
				"SELECT details, details_ciphertext FROM events WHERE id = ?", event.Id,
			).Row().Scan(&legacy, &ciphertext); err != nil {
				t.Fatalf("read persisted row: %v", err)
			}
			if legacy.Valid {
				t.Fatalf("legacy details column is non-NULL after a new write: %q", legacy.String)
			}
			if tc.details == "" {
				if ciphertext != nil {
					t.Fatal("empty Details produced an artificial ciphertext")
				}
				return
			}
			if len(ciphertext) == 0 {
				t.Fatal("non-empty Details was not encrypted")
			}
			plaintext, err := cipher.Decrypt(eventDetailsContext(event.CampaignId, event.Id), credentials.Envelope(ciphertext))
			if err != nil || string(plaintext) != tc.details {
				t.Fatalf("round-trip decrypt failed: err=%v", err)
			}
		})
	}
}

func TestAddEventRejectsOversizedDetailsBeforePersistence(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x12}, 32),
	})
	withEventDetailsCipher(t, cipher)

	oversized := strings.Repeat("A", eventDetailsPlaintextMaxBytes+1)
	event := &Event{Message: "Submitted Data", Details: oversized}
	if err := AddEvent(event, 1); !errors.Is(err, ErrEventDetailsTooLarge) {
		t.Fatalf("oversized AddEvent error = %v, want ErrEventDetailsTooLarge", err)
	}

	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM events").Row().Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Fatal("a rejected oversized write left a durable shell row")
	}
}

func TestAddEventRequiresCipherForNonEmptyDetails(t *testing.T) {
	setupEventDetailsDatabase(t)
	withEventDetailsCipher(t, nil)

	event := &Event{Message: "Email Opened", Details: syntheticEventDetailsJSON(t, 8)}
	if err := AddEvent(event, 1); !errors.Is(err, ErrEventDetailsKeyringRequired) {
		t.Fatalf("no-cipher AddEvent error = %v, want ErrEventDetailsKeyringRequired", err)
	}

	// An event with empty Details never needs a cipher.
	empty := &Event{Message: "Email Sent"}
	if err := AddEvent(empty, 1); err != nil {
		t.Fatalf("empty-details AddEvent without cipher: %v", err)
	}
}

// --- AAD_RECORD_BINDING ------------------------------------------------------

func TestEventDetailsCopyAttackAndTamperFailAuthentication(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x13}, 32),
	})
	withEventDetailsCipher(t, cipher)

	eventA := &Event{Message: "Clicked Link", Details: syntheticEventDetailsJSON(t, 8)}
	if err := AddEvent(eventA, 1); err != nil {
		t.Fatalf("create event A: %v", err)
	}
	eventB := &Event{Message: "Clicked Link", Details: syntheticEventDetailsJSON(t, 8)}
	if err := AddEvent(eventB, 1); err != nil {
		t.Fatalf("create event B: %v", err)
	}

	var envelopeA []byte
	if err := db.Raw("SELECT details_ciphertext FROM events WHERE id = ?", eventA.Id).
		Row().Scan(&envelopeA); err != nil {
		t.Fatalf("read event A ciphertext: %v", err)
	}

	// Copy-attack: event A's ciphertext decrypted under event B's context
	// (same campaign, different RecordID) must fail AAD authentication.
	if _, err := cipher.Decrypt(eventDetailsContext(eventB.CampaignId, eventB.Id), credentials.Envelope(envelopeA)); !errors.Is(err, credentials.ErrAuthenticationFailed) {
		t.Fatalf("copy-attack decrypt error = %v, want authentication failure", err)
	}

	// Tamper: flip one byte of the envelope and confirm decryption under the
	// correct, original context still fails.
	tampered := append([]byte(nil), envelopeA...)
	for i := len(tampered) - 1; i >= 0; i-- {
		if tampered[i] != '=' && tampered[i] != ':' {
			if tampered[i] == 'A' {
				tampered[i] = 'B'
			} else {
				tampered[i] = 'A'
			}
			break
		}
	}
	if _, err := cipher.Decrypt(eventDetailsContext(eventA.CampaignId, eventA.Id), credentials.Envelope(tampered)); !errors.Is(err, credentials.ErrAuthenticationFailed) {
		t.Fatalf("tampered decrypt error = %v, want authentication failure", err)
	}
}

// --- INVALID_CIPHERTEXT_FALLBACK = NONE / availability ----------------------

func TestDecryptEventsInPlaceIsolatesCorruptedRow(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x14}, 32),
	})
	withEventDetailsCipher(t, cipher)

	good := &Event{Message: "Clicked Link", Details: syntheticEventDetailsJSON(t, 8)}
	if err := AddEvent(good, 1); err != nil {
		t.Fatalf("create good event: %v", err)
	}
	corrupt := &Event{Message: "Clicked Link", Details: syntheticEventDetailsJSON(t, 8)}
	if err := AddEvent(corrupt, 1); err != nil {
		t.Fatalf("create corrupt event: %v", err)
	}
	if err := db.Exec(
		"UPDATE events SET details_ciphertext = ? WHERE id = ?",
		[]byte("gophishfr-cred:v1:event-key:AAAAAAAAAAAAAAAAAAAA:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
		corrupt.Id,
	).Error; err != nil {
		t.Fatalf("corrupt ciphertext: %v", err)
	}

	events := []Event{*good, *corrupt}
	// Reload from storage so the in-memory DetailsCiphertext reflects the
	// corruption above (AddEvent's return value does not).
	if err := db.Raw("SELECT id, campaign_id, details, details_ciphertext FROM events WHERE id IN (?, ?) ORDER BY id", good.Id, corrupt.Id).
		Scan(&events).Error; err != nil {
		t.Fatalf("reload events: %v", err)
	}

	decryptEventsInPlace(events)

	if events[0].Details == eventDetailsUnavailableSentinel {
		t.Fatal("the good event was marked unavailable")
	}
	if events[1].Details != eventDetailsUnavailableSentinel {
		t.Fatalf("corrupted event Details = %q, want the unavailable sentinel", events[1].Details)
	}
	// The sentinel must itself be valid JSON so the unchanged frontend's
	// JSON.parse(event.details) never throws.
	if !json.Valid([]byte(events[1].Details)) {
		t.Fatal("unavailable sentinel is not valid JSON")
	}
	if events[1].DetailsCiphertext != nil {
		t.Fatal("ciphertext/envelope bytes leaked into the serialized event")
	}
}

func TestDecryptEventDetailsNeverFallsBackFromInvalidCiphertext(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x15}, 32),
	})
	wrongCipher := testCredentialCipher(t, "other-key", map[string][]byte{
		"other-key": bytes.Repeat([]byte{0x16}, 32),
	})

	envelope, err := cipher.Encrypt(eventDetailsContext(1, 99), []byte("synthetic-plaintext"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	event := Event{Id: 99, CampaignId: 1, DetailsCiphertext: []byte(string(envelope))}
	if _, err := decryptEventDetails(event, wrongCipher, false); !errors.Is(err, ErrEventDetailsInvalidCiphertext) {
		t.Fatalf("wrong-key decrypt error = %v, want ErrEventDetailsInvalidCiphertext", err)
	}
}

func TestDecryptEventDetailsBothStateIsInvalid(t *testing.T) {
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x17}, 32),
	})
	envelope, err := cipher.Encrypt(eventDetailsContext(1, 1), []byte("synthetic"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	event := Event{Id: 1, CampaignId: 1, Details: "legacy-still-present", DetailsCiphertext: []byte(string(envelope))}
	if _, err := decryptEventDetails(event, cipher, false); !errors.Is(err, ErrEventDetailsInvalidState) {
		t.Fatalf("BOTH-state decrypt error = %v, want ErrEventDetailsInvalidState", err)
	}
}

func TestDecryptEventDetailsFinalizedRejectsLegacyRow(t *testing.T) {
	event := Event{Id: 1, CampaignId: 1, Details: "legacy-plaintext-json"}
	if _, err := decryptEventDetails(event, nil, false); err != nil {
		t.Fatalf("pre-finalization LEGACY read: %v", err)
	}
	if _, err := decryptEventDetails(event, nil, true); !errors.Is(err, ErrEventDetailsFinalized) {
		t.Fatalf("post-finalization LEGACY read error = %v, want ErrEventDetailsFinalized", err)
	}
}

// --- MYSQL_CIPHERTEXT_CAPACITY (backend-agnostic crypto/size proof) --------

func TestEnvelopeNearLegacyBlobCapExceedsStandardBlob(t *testing.T) {
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x18}, 32),
	})
	// A legacy row written by an older binary, with no application-level
	// bound, could be as large as the historical MySQL BLOB cap itself.
	nearCapPlaintext := strings.Repeat("Z", eventDetailsLegacyBlobCapacityBytes-64)
	envelope, err := cipher.Encrypt(eventDetailsContext(1, 1), []byte(nearCapPlaintext))
	if err != nil {
		t.Fatalf("encrypt near-cap legacy payload: %v", err)
	}
	if len(envelope) <= eventDetailsLegacyBlobCapacityBytes {
		t.Fatalf("envelope length %d does not exceed the standard BLOB cap %d -- the MEDIUMBLOB column choice would not be exercised", len(envelope), eventDetailsLegacyBlobCapacityBytes)
	}
	plaintext, err := cipher.Decrypt(eventDetailsContext(1, 1), envelope)
	if err != nil || string(plaintext) != nearCapPlaintext {
		t.Fatalf("round-trip of a near-cap legacy payload failed: err=%v", err)
	}
}

// --- backend validation / PostgreSQL unsupported ---------------------------

func TestValidateEventDetailsBackendRejectsPostgres(t *testing.T) {
	for _, backend := range []string{"postgres", "postgresql", "oracle", ""} {
		if err := ValidateEventDetailsBackend(backend); !errors.Is(err, ErrUnsupportedEventDetailsDB) {
			t.Fatalf("backend %q error = %v, want ErrUnsupportedEventDetailsDB", backend, err)
		}
	}
	for _, backend := range []string{"sqlite3", "mysql"} {
		if err := ValidateEventDetailsBackend(backend); err != nil {
			t.Fatalf("backend %q unexpectedly rejected: %v", backend, err)
		}
	}
}

// --- Event.Details / CampaignId immutability (acceptance criterion 20) -----

func TestEventColumnsAreNeverUpdatedAfterCreation(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x19}, 32),
	})
	withEventDetailsCipher(t, cipher)

	event := &Event{Message: "Clicked Link", Details: syntheticEventDetailsJSON(t, 8)}
	if err := AddEvent(event, 11); err != nil {
		t.Fatalf("create event: %v", err)
	}
	var campaignID int64
	if err := db.Raw("SELECT campaign_id FROM events WHERE id = ?", event.Id).Row().Scan(&campaignID); err != nil {
		t.Fatalf("read campaign_id: %v", err)
	}
	if campaignID != 11 {
		t.Fatalf("campaign_id = %d, want 11", campaignID)
	}

	// A second AddEvent call always assigns a brand-new autoincrement ID: it
	// never updates the first event's row. This is the exhaustive-search
	// property documented on the Event struct: AddEvent is the only writer,
	// and it only ever inserts.
	second := &Event{Message: "Clicked Link", Details: syntheticEventDetailsJSON(t, 8)}
	if err := AddEvent(second, 11); err != nil {
		t.Fatalf("create second event: %v", err)
	}
	if second.Id == event.Id {
		t.Fatal("AddEvent reused an existing event ID instead of inserting a new row")
	}
	var stillCampaignID int64
	if err := db.Raw("SELECT campaign_id FROM events WHERE id = ?", event.Id).Row().Scan(&stillCampaignID); err != nil {
		t.Fatalf("re-read campaign_id: %v", err)
	}
	if stillCampaignID != campaignID {
		t.Fatal("the first event's campaign_id changed after a later, unrelated AddEvent call")
	}
}
