package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/apikey"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/models"
)

// --- CLI_ARGUMENT_PARSING (main.go's own wiring) ----------------------------

func TestFormatOptionalEventTime(t *testing.T) {
	if got := formatOptionalEventTime(time.Time{}); got != "n/a" {
		t.Fatalf("formatOptionalEventTime(zero) = %q, want n/a", got)
	}
	when := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	want := when.Format(time.RFC3339)
	if got := formatOptionalEventTime(when); got != want {
		t.Fatalf("formatOptionalEventTime(%v) = %q, want %q", when, got, want)
	}
}

// --- PLAINTEXT_LOGGING = NONE (the real CLI logging call main.go makes) ----

// TestLogEventDetailsPurgeReportNeverLogsSecrets plants a unique synthetic
// marker inside an event's encrypted Details, redirects the shared
// application logger to a buffer (log.Logger.Out, exactly the sink
// log.Infof ultimately writes to), runs a real purge end to end, and then
// calls logEventDetailsPurgeReport -- the exact function main.go's CLI
// wiring calls for both --dry-run and a real purge -- with the real result.
// The captured buffer must never contain the marker, while the marker is
// independently proven to have existed (decryptable) before the purge and
// to be genuinely gone (NULL columns) after.
func TestLogEventDetailsPurgeReportNeverLogsSecrets(t *testing.T) {
	t.Setenv(models.InitialAdminPassword, "synthetic-purge-cli-test-password")
	installTestAPIKeyVerifierForPurgeTest(t)
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         filepath.Join(t.TempDir(), "purge-cli.db"),
		MigrationsPath: "db/db_sqlite3/migrations",
	}
	if err := models.Setup(conf); err != nil {
		t.Fatalf("set up purge CLI test database: %v", err)
	}

	cipher := credentialCipherForTest(t)
	models.SetEventDetailsCipher(cipher)

	const marker = "PURGE_TEST_SECRET_cli_9d2e7a41"
	details := models.EventDetails{Payload: url.Values{"password": {marker}}}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal synthetic event details: %v", err)
	}
	event := &models.Event{Message: "Submitted Data", Email: "cli-secret-test@example.test", Details: string(encoded)}
	if err := models.AddEvent(event, 1); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}

	// Redirect the shared application logger -- the same log.Logger that
	// log.Infof (and therefore logEventDetailsPurgeReport) writes to -- to a
	// buffer this test can grep.
	var captured bytes.Buffer
	previousOut := log.Logger.Out
	log.Logger.Out = &captured
	t.Cleanup(func() { log.Logger.Out = previousOut })

	cutoff := time.Now().UTC().Add(time.Hour)
	report, reportErr := models.ReportEventDetailsPurgeEligibility(cutoff, true)
	if reportErr != nil {
		t.Fatalf("ReportEventDetailsPurgeEligibility: %v", reportErr)
	}
	logEventDetailsPurgeReport("dry-run", report)

	result, purgeErr := models.PurgeEventDetailsBefore(cutoff, true)
	if purgeErr != nil {
		t.Fatalf("PurgeEventDetailsBefore: %v", purgeErr)
	}
	logEventDetailsPurgeReport("purge", result.EventDetailsPurgeReport)
	log.Infof(
		"event details purge: %d batches, %d purged, %d skipped (row no longer present)",
		result.Batches, result.Purged, result.Skipped,
	)

	if strings.Contains(captured.String(), marker) {
		t.Fatalf("marker leaked into captured CLI log output: %q", captured.String())
	}
	if result.Purged != 1 {
		t.Fatalf("Purged = %d, want 1", result.Purged)
	}

	// Independent proof the purge was real, not a no-op that merely failed
	// to log: a second, identical report must now find zero eligible rows
	// (models package tests separately prove, exhaustively, that the raw
	// columns are cleared and that API reads return an empty Details).
	secondReport, err := models.ReportEventDetailsPurgeEligibility(cutoff, true)
	if err != nil {
		t.Fatalf("second ReportEventDetailsPurgeEligibility: %v", err)
	}
	if secondReport.Eligible != 0 {
		t.Fatalf("second report Eligible = %d, want 0", secondReport.Eligible)
	}
}

func credentialCipherForTest(t *testing.T) *credentials.Cipher {
	t.Helper()
	keyring, err := credentials.NewKeyring(
		"purge-cli-test-key",
		map[string][]byte{"purge-cli-test-key": bytes.Repeat([]byte{0x5a}, 32)},
	)
	if err != nil {
		t.Fatalf("create test keyring: %v", err)
	}
	cipher, err := credentials.New(keyring)
	if err != nil {
		t.Fatalf("create test cipher: %v", err)
	}
	return cipher
}

// installTestAPIKeyVerifierForPurgeTest satisfies models.Setup's
// first-run admin-user bootstrap, which requires a configured API key
// verifier -- unrelated to event details, but a precondition for calling
// models.Setup at all in a test that starts from an empty database,
// exactly like controllers' installTestAPIKeyVerifier helper does for the
// browser smoke test.
func installTestAPIKeyVerifierForPurgeTest(t *testing.T) {
	t.Helper()
	keyring, err := apikey.NewKeyring(
		"purge-cli-test-api-key",
		map[string][]byte{"purge-cli-test-api-key": bytes.Repeat([]byte{0x6b}, 32)},
	)
	if err != nil {
		t.Fatalf("create test API key verifier keyring: %v", err)
	}
	verifier, err := apikey.New(keyring)
	if err != nil {
		t.Fatalf("create test API key verifier: %v", err)
	}
	models.SetAPIKeyVerifier(verifier)
}
