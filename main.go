package main

/*
GophishFR - Security Awareness Simulation Framework

Derived from Gophish - Open-Source Phishing Framework

The MIT License (MIT)

Copyright (c) 2013 Jordan Wright

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/controllers"
	"github.com/Vesperis-group/gophishfr/dialer"
	"github.com/Vesperis-group/gophishfr/imap"
	"github.com/Vesperis-group/gophishfr/internal/apikey"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/middleware"
	"github.com/Vesperis-group/gophishfr/models"
	"github.com/Vesperis-group/gophishfr/webhook"
	"github.com/Vesperis-group/gophishfr/worker"
)

const (
	modeAll   string = "all"
	modeAdmin string = "admin"
	modePhish string = "phish"
)

var (
	configPath             = kingpin.Flag("config", "Location of config.json.").Default("./config.json").String()
	disableMailer          = kingpin.Flag("disable-mailer", "Disable the mailer (for use with multi-system deployments)").Bool()
	migrateIMAPCredentials = kingpin.Flag(
		"migrate-imap-credentials",
		"Offline: encrypt legacy IMAP passwords after stopping all application writers.",
	).Bool()
	rollbackIMAPCredentials = kingpin.Flag(
		"rollback-imap-credentials",
		"Offline: decrypt IMAP passwords before downgrading the schema or binary.",
	).Bool()
	migrateSMTPCredentials = kingpin.Flag(
		"migrate-smtp-credentials",
		"Offline: encrypt legacy SMTP passwords after stopping all application writers.",
	).Bool()
	rollbackSMTPCredentials = kingpin.Flag(
		"rollback-smtp-credentials",
		"Offline: decrypt SMTP passwords before downgrading the schema or binary.",
	).Bool()
	migrateWebhookSecrets = kingpin.Flag(
		"migrate-webhook-secrets",
		"Offline: encrypt legacy webhook secrets after stopping all application writers.",
	).Bool()
	rollbackWebhookSecrets = kingpin.Flag(
		"rollback-webhook-secrets",
		"Offline: decrypt webhook secrets before downgrading the schema or binary.",
	).Bool()
	migrateAPIKeys = kingpin.Flag(
		"migrate-api-keys",
		"Offline irreversible migration: replace legacy API token plaintext with HMAC verifiers after stopping all writers and testing a backup.",
	).Bool()
	migrateEventDetails = kingpin.Flag(
		"migrate-event-details",
		"Online: batch-encrypt legacy event details while the mailer and phishing server keep writing. Safe to interrupt and re-run.",
	).Bool()
	rollbackEventDetails = kingpin.Flag(
		"rollback-event-details",
		"Online: batch-restore legacy event details plaintext before downgrading to a binary that only understands it.",
	).Bool()
	finalizeEventDetails = kingpin.Flag(
		"finalize-event-details",
		"Verify zero legacy/inconsistent event details rows remain, then durably mark the migration FINALIZED.",
	).Bool()
	rotateEventDetailsCredentials = kingpin.Flag(
		"rotate-event-details-credentials",
		"Online: bulk re-encrypt event details ciphertext currently using a non-active credential key.",
	).Bool()
	inventoryEventDetailsKeys = kingpin.Flag(
		"inventory-event-details-keys",
		"Read-only: count event details ciphertext rows by key ID, by parsing the envelope prefix only (no decryption).",
	).Bool()
	purgeEventDetailsBefore = kingpin.Flag(
		"purge-event-details-before",
		"Operator-triggered, irreversible: clear Event.Details/DetailsCiphertext (never decrypted) for every event older than this RFC3339 cutoff (e.g. 2026-07-01T00:00:00Z). There is no default cutoff and no automatic schedule. Requires exactly one of --dry-run or --yes.",
	).String()
	purgeEventDetailsDryRun = kingpin.Flag(
		"dry-run",
		"With --purge-event-details-before: report eligible counts only (oldest/newest time, min/max ID, distinct campaigns); writes nothing.",
	).Bool()
	purgeEventDetailsYes = kingpin.Flag(
		"yes",
		"With --purge-event-details-before: confirm and execute the real, irreversible purge. Required for any non-dry-run purge.",
	).Bool()
	purgeEventDetailsIncludeActiveCampaigns = kingpin.Flag(
		"include-active-campaigns",
		"With --purge-event-details-before: also consider events whose owning campaign is not yet Completed (excluded by default).",
	).Bool()
	mode = kingpin.Flag("mode", fmt.Sprintf("Run the binary in one of the modes (%s, %s or %s)", modeAll, modeAdmin, modePhish)).
		Default("all").Enum(modeAll, modeAdmin, modePhish)
)

func main() {
	// Load the version

	version, err := os.ReadFile("./VERSION")
	if err != nil {
		log.Fatal(err)
	}
	kingpin.Version(string(version))

	// Parse the CLI flags and load the config
	kingpin.CommandLine.HelpFlag.Short('h')
	kingpin.Parse()

	// Load the config
	conf, err := config.LoadConfig(*configPath)
	// Just warn if a contact address hasn't been configured
	if err != nil {
		log.Fatal(err)
	}
	if conf.ContactAddress == "" {
		log.Warnf("No contact address has been configured.")
		log.Warnf("Please consider adding a contact_address entry in your config.json")
	}
	config.Version = string(version)

	// purgeEventDetailsRequested is computed once, from the raw flag value,
	// rather than re-checking `*purgeEventDetailsBefore != ""` throughout
	// this function: an empty/absent value means "purge not requested" --
	// goal.md is explicit that this must never be a silent error, unlike a
	// genuinely unparseable non-empty cutoff below.
	purgeEventDetailsRequested := *purgeEventDetailsBefore != ""
	var purgeEventDetailsCutoff time.Time
	if purgeEventDetailsRequested {
		if err := models.ValidateEventDetailsPurgeMode(*purgeEventDetailsDryRun, *purgeEventDetailsYes); err != nil {
			log.Fatal(err)
		}
		purgeEventDetailsCutoff, err = models.ParseEventDetailsPurgeCutoff(*purgeEventDetailsBefore)
		if err != nil {
			log.Fatal(err)
		}
	}

	credentialKeyringAction := *migrateIMAPCredentials ||
		*rollbackIMAPCredentials ||
		*migrateSMTPCredentials ||
		*rollbackSMTPCredentials ||
		*migrateWebhookSecrets ||
		*rollbackWebhookSecrets ||
		*migrateEventDetails ||
		*rollbackEventDetails ||
		*finalizeEventDetails ||
		*rotateEventDetailsCredentials
	credentialActions := 0
	for _, selected := range []bool{
		*migrateIMAPCredentials,
		*rollbackIMAPCredentials,
		*migrateSMTPCredentials,
		*rollbackSMTPCredentials,
		*migrateWebhookSecrets,
		*rollbackWebhookSecrets,
		*migrateAPIKeys,
		*migrateEventDetails,
		*rollbackEventDetails,
		*finalizeEventDetails,
		*rotateEventDetailsCredentials,
		*inventoryEventDetailsKeys,
		// The purge action counts toward this shared mutual-exclusivity
		// counter -- it is irreversible and destructive, exactly like
		// --rollback-event-details, and must never run combined with any
		// other offline/online credential action in the same invocation.
		purgeEventDetailsRequested,
	} {
		if selected {
			credentialActions++
		}
	}
	if credentialActions > 1 {
		log.Fatal("offline migration and rollback actions are mutually exclusive")
	}
	if *migrateIMAPCredentials || *rollbackIMAPCredentials {
		if err := models.ValidateIMAPCredentialBackend(conf.DBName); err != nil {
			log.Fatal(err)
		}
	}
	if *migrateSMTPCredentials || *rollbackSMTPCredentials {
		if err := models.ValidateSMTPCredentialBackend(conf.DBName); err != nil {
			log.Fatal(err)
		}
	}
	if *migrateWebhookSecrets || *rollbackWebhookSecrets {
		if err := models.ValidateWebhookCredentialBackend(conf.DBName); err != nil {
			log.Fatal(err)
		}
	}
	if *migrateAPIKeys {
		if err := models.ValidateAPIKeyVerifierBackend(conf.DBName); err != nil {
			log.Fatal(err)
		}
	}
	if *migrateEventDetails || *rollbackEventDetails || *finalizeEventDetails ||
		*rotateEventDetailsCredentials || *inventoryEventDetailsKeys || purgeEventDetailsRequested {
		if err := models.ValidateEventDetailsBackend(conf.DBName); err != nil {
			log.Fatal(err)
		}
	}

	// Configure our various upstream clients to make sure that we restrict
	// outbound connections as needed.
	//
	// A malformed entry here means the operator intended a restriction that we
	// cannot honour, so refuse to start rather than run with an allowlist that
	// does not match the configuration file.
	if err := dialer.SetAllowedHosts(conf.AdminConf.AllowedInternalHosts); err != nil {
		log.Fatal(err)
	}
	webhook.SetTransport(&http.Transport{
		DialContext: dialer.Dialer().DialContext,
	})

	err = log.Setup(conf.Logging)
	if err != nil {
		log.Fatal(err)
	}

	credentialCipher, activeCredentialKeyID, err := loadCredentialCipher()
	if err != nil {
		log.Fatal(err)
	}
	apiKeyVerifier, err := loadAPIKeyVerifier()
	if err != nil {
		log.Fatal(err)
	}
	if *migrateAPIKeys && apiKeyVerifier == nil {
		log.Fatal(models.ErrAPIKeyVerifierUnavailable)
	}
	models.SetAPIKeyVerifier(apiKeyVerifier)
	if credentialKeyringAction && credentialCipher == nil {
		switch {
		case *migrateSMTPCredentials || *rollbackSMTPCredentials:
			log.Fatal(models.ErrSMTPCredentialKeyringRequired)
		case *migrateWebhookSecrets || *rollbackWebhookSecrets:
			log.Fatal(models.ErrWebhookCredentialKeyringRequired)
		case *migrateEventDetails || *rollbackEventDetails || *finalizeEventDetails || *rotateEventDetailsCredentials:
			log.Fatal(models.ErrEventDetailsKeyringRequired)
		default:
			log.Fatal(models.ErrIMAPCredentialKeyringRequired)
		}
	}

	// Provide the option to disable the built-in mailer
	// Setup the global variables and settings
	err = models.Setup(conf)
	if err != nil {
		log.Fatal(err)
	}
	// AddEvent and the GetCampaign/GetCampaignResults read path (no
	// per-request or per-call cipher available there) read this
	// package-level installation of the exact same shared cipher every
	// other credential path receives explicitly.
	models.SetWebhookCredentialCipher(credentialCipher)
	models.SetEventDetailsCipher(credentialCipher)
	if *migrateAPIKeys {
		result, err := models.MigrateAPIKeys(apiKeyVerifier)
		if err != nil {
			log.Fatal(err)
		}
		log.Infof("API verifier migration complete: %d rows updated, %d rows unchanged", result.Updated, result.Unchanged)
		return
	}
	if *migrateIMAPCredentials || *rollbackIMAPCredentials {
		result, err := runIMAPCredentialAction(credentialCipher, *rollbackIMAPCredentials)
		if err != nil {
			log.Fatal(err)
		}

		action := "migration"
		if *rollbackIMAPCredentials {
			action = "rollback"
		}
		log.Infof("IMAP credential %s complete: %d rows updated, %d rows unchanged", action, result.Updated, result.Unchanged)
		return
	}
	if *migrateSMTPCredentials || *rollbackSMTPCredentials {
		result, err := runSMTPCredentialAction(credentialCipher, *rollbackSMTPCredentials)
		if err != nil {
			log.Fatal(err)
		}
		action := "migration"
		if *rollbackSMTPCredentials {
			action = "rollback"
		}
		log.Infof("SMTP credential %s complete: %d rows updated, %d rows unchanged", action, result.Updated, result.Unchanged)
		return
	}
	if *migrateWebhookSecrets || *rollbackWebhookSecrets {
		result, err := runWebhookCredentialAction(credentialCipher, *rollbackWebhookSecrets)
		if err != nil {
			log.Fatal(err)
		}
		action := "migration"
		if *rollbackWebhookSecrets {
			action = "rollback"
		}
		log.Infof("Webhook credential %s complete: %d rows updated, %d rows unchanged", action, result.Updated, result.Unchanged)
		return
	}
	if *migrateEventDetails {
		result, err := models.MigrateEventDetailsBatch(credentialCipher)
		logEventDetailsBatchResult("migration", result.Batches, result.RowsEncrypted, result.RowsReconciled, result.LastID, result.Failed, result.MalformedJSON)
		if err != nil {
			log.Fatal(err)
		}
		if len(result.Failed) > 0 {
			log.Fatalf("event details migration completed with %d unresolved row failures; see log output above", len(result.Failed))
		}
		return
	}
	if *rollbackEventDetails {
		result, err := models.RollbackEventDetailsBatch(credentialCipher)
		logEventDetailsBatchResult("rollback", result.Batches, result.RowsRestored, 0, result.LastID, result.Failed, nil)
		if err != nil {
			log.Fatal(err)
		}
		if len(result.Failed) > 0 {
			log.Fatalf("event details rollback completed with %d unresolved row failures; see log output above", len(result.Failed))
		}
		return
	}
	if *finalizeEventDetails {
		counts, err := models.FinalizeEventDetailsMigration(credentialCipher)
		log.Infof(
			"event details finalization preflight: legacy=%d both=%d migrated=%d invalid=%d",
			counts.Legacy, counts.Both, counts.Migrated, counts.Invalid,
		)
		if err != nil {
			log.Fatal(err)
		}
		log.Info("event details migration marked FINALIZED")
		return
	}
	if *rotateEventDetailsCredentials {
		result, err := models.RotateEventDetailsCredentials(credentialCipher, activeCredentialKeyID)
		log.Infof(
			"event details key rotation: %d batches, %d rotated, %d already on the active key, %d failed, last id %d",
			result.Batches, result.Rotated, result.Unchanged, len(result.Failed), result.LastID,
		)
		for _, failure := range result.Failed {
			log.Errorf("event %d rotation failure: %s", failure.EventID, failure.Reason)
		}
		if err != nil {
			log.Fatal(err)
		}
		if len(result.Failed) > 0 {
			log.Fatalf("event details rotation completed with %d unresolved row failures; see log output above", len(result.Failed))
		}
		return
	}
	if *inventoryEventDetailsKeys {
		inventory, err := models.InventoryEventDetailsKeys()
		if err != nil {
			log.Fatal(err)
		}
		for keyID, count := range inventory {
			log.Infof("event details key %q: %d rows", keyID, count)
		}
		return
	}
	if purgeEventDetailsRequested {
		// NEVER decrypt: this action needs no credentialCipher at all --
		// see models.PurgeEventDetailsBefore's doc comment. It is never
		// passed one, so decryption is structurally impossible here, not
		// merely avoided by convention.
		if *purgeEventDetailsDryRun {
			report, err := models.ReportEventDetailsPurgeEligibility(purgeEventDetailsCutoff, *purgeEventDetailsIncludeActiveCampaigns)
			if err != nil {
				log.Fatal(err)
			}
			logEventDetailsPurgeReport("dry-run", report)
			return
		}
		result, err := models.PurgeEventDetailsBefore(purgeEventDetailsCutoff, *purgeEventDetailsIncludeActiveCampaigns)
		logEventDetailsPurgeReport("purge", result.EventDetailsPurgeReport)
		log.Infof(
			"event details purge: %d batches, %d purged, %d skipped (row no longer present)",
			result.Batches, result.Purged, result.Skipped,
		)
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	// Unlock any maillogs that may have been locked for processing
	// when GophishFR was last shut down.
	err = models.UnlockAllMailLogs()
	if err != nil {
		log.Fatal(err)
	}

	// Create our servers
	adminOptions := []controllers.AdminServerOption{}
	adminOptions = append(adminOptions, controllers.WithCredentialCipher(credentialCipher))
	if *disableMailer {
		adminOptions = append(adminOptions, controllers.WithWorker(nil))
	} else {
		mailWorker, err := worker.New(worker.WithCredentialCipher(credentialCipher))
		if err != nil {
			log.Fatal(err)
		}
		adminOptions = append(adminOptions, controllers.WithWorker(mailWorker))
	}
	adminConfig := conf.AdminConf
	adminServer := controllers.NewAdminServer(adminConfig, adminOptions...)
	middleware.Store.Options.Secure = adminConfig.UseTLS

	phishConfig := conf.PhishConf
	phishServer := controllers.NewPhishingServer(phishConfig)

	imapMonitor := imap.NewMonitor(credentialCipher)
	if *mode == "admin" || *mode == "all" {
		go adminServer.Start()
		if err := imapMonitor.Start(); err != nil {
			log.Fatal(err)
		}
	}
	if *mode == "phish" || *mode == "all" {
		go phishServer.Start()
	}

	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	log.Info("CTRL+C Received... Gracefully shutting down servers")
	if *mode == modeAdmin || *mode == modeAll {
		if err := adminServer.Shutdown(); err != nil {
			log.Error(err)
		}
		if err := imapMonitor.Shutdown(); err != nil {
			log.Error(err)
		}
	}
	if *mode == modePhish || *mode == modeAll {
		if err := phishServer.Shutdown(); err != nil {
			log.Error(err)
		}
	}

}

func loadAPIKeyVerifier() (*apikey.Service, error) {
	path := os.Getenv(apikey.KeyringEnvironment)
	if path == "" {
		return nil, nil
	}
	keyring, err := apikey.LoadKeyringFile(path)
	if err != nil {
		return nil, fmt.Errorf("load API verifier keyring: %w", err)
	}
	verifier, err := apikey.New(keyring)
	if err != nil {
		return nil, fmt.Errorf("initialize API verifier: %w", err)
	}
	return verifier, nil
}

// loadCredentialCipher loads the shared application credential cipher used
// for IMAP/SMTP/webhook secrets and -- via the same instance,
// Context.Kind-separated -- event details. It also returns the keyring's
// active key ID: internal/credentials.Cipher deliberately does not expose
// its own active key ID (see cipher.go), so RotateEventDetailsCredentials
// takes it as an explicit parameter sourced here, from the same Keyring
// already loaded, rather than this file reading the keyring file or parsing
// a second time.
func loadCredentialCipher() (*credentials.Cipher, string, error) {
	path := os.Getenv(models.IMAPCredentialKeyringEnvironment)
	if path == "" {
		return nil, "", nil
	}
	keyring, err := credentials.LoadKeyringFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("load IMAP credential keyring: %w", err)
	}
	credentialCipher, err := credentials.New(keyring)
	if err != nil {
		return nil, "", fmt.Errorf("initialize IMAP credential cipher: %w", err)
	}
	return credentialCipher, keyring.ActiveKeyID(), nil
}

// logEventDetailsBatchResult reports only non-secret progress counters --
// batch count, rows processed, last ID processed, and failure IDs with
// their error class -- never Details, the envelope, or any other content,
// consistent with PLAINTEXT_LOGGING = NONE. reconciled is only meaningful
// for the migration action (BOTH-state rows resolved); pass 0 for rollback.
func logEventDetailsBatchResult(
	action string,
	batches, rowsProcessed, reconciled int,
	lastID int64,
	failed []models.EventDetailsRowFailure,
	malformedJSON []int64,
) {
	log.Infof(
		"event details %s: %d batches, %d rows processed, %d BOTH-state rows reconciled, %d failed, last id %d",
		action, batches, rowsProcessed, reconciled, len(failed), lastID,
	)
	if len(malformedJSON) > 0 {
		log.Warnf("event details %s: %d row(s) encrypted despite application-malformed JSON: %v", action, len(malformedJSON), malformedJSON)
	}
	for _, failure := range failed {
		log.Errorf("event %d %s failure: %s", failure.EventID, action, failure.Reason)
	}
}

// logEventDetailsPurgeReport reports ONLY the non-secret aggregate counters
// goal.md's CLI contract specifies -- eligible count, oldest/newest
// eligible Event.Time, min/max eligible Event.Id, and distinct campaign
// count -- for both --dry-run and the real purge's own pre-purge summary.
// It never logs Details, DetailsCiphertext, or any other row content.
func logEventDetailsPurgeReport(action string, report models.EventDetailsPurgeReport) {
	log.Infof(
		"event details %s: %d eligible, oldest=%s newest=%s min_id=%d max_id=%d campaigns=%d",
		action,
		report.Eligible,
		formatOptionalEventTime(report.OldestEventTime),
		formatOptionalEventTime(report.NewestEventTime),
		report.MinEventID,
		report.MaxEventID,
		report.DistinctCampaigns,
	)
}

// formatOptionalEventTime renders a zero time.Time (meaning "no eligible
// rows, so there is no oldest/newest to report") as "n/a" instead of Go's
// default zero-value string, and every other value as RFC3339 -- operator-
// readable and unambiguous about the UTC normalization every eligible
// Event.Time already uses.
func formatOptionalEventTime(t time.Time) string {
	if t.IsZero() {
		return "n/a"
	}
	return t.Format(time.RFC3339)
}

func runSMTPCredentialAction(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (models.SMTPCredentialMigrationResult, error) {
	if rollback {
		return models.RollbackSMTPCredentials(credentialCipher)
	}
	return models.MigrateSMTPCredentials(credentialCipher)
}

func runIMAPCredentialAction(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (models.IMAPCredentialMigrationResult, error) {
	if rollback {
		return models.RollbackIMAPCredentials(credentialCipher)
	}
	return models.MigrateIMAPCredentials(credentialCipher)
}

func runWebhookCredentialAction(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (models.WebhookCredentialMigrationResult, error) {
	if rollback {
		return models.RollbackWebhookSecrets(credentialCipher)
	}
	return models.MigrateWebhookSecrets(credentialCipher)
}
