package models

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
)

// --- CLI_ARGUMENT_PARSING ---------------------------------------------------

func TestParseEventDetailsPurgeCutoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    time.Time
		wantErr bool
	}{
		{
			name:  "Z offset accepted and already UTC",
			value: "2026-07-01T00:00:00Z",
			want:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "explicit positive offset normalized to UTC",
			value: "2026-07-01T02:00:00+02:00",
			want:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "explicit negative offset normalized to UTC",
			value: "2026-06-30T19:00:00-05:00",
			want:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{name: "missing offset is rejected, never assumed UTC or local", value: "2026-07-01T00:00:00", wantErr: true},
		{name: "date-only is rejected", value: "2026-07-01", wantErr: true},
		{name: "garbage is rejected", value: "not-a-timestamp", wantErr: true},
		{name: "empty string is rejected", value: "", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseEventDetailsPurgeCutoff(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseEventDetailsPurgeCutoff(%q) = nil error, want error", test.value)
				}
				if !errors.Is(err, ErrEventDetailsPurgeCutoffUnparseable) {
					t.Fatalf("error = %v, want it to wrap ErrEventDetailsPurgeCutoffUnparseable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEventDetailsPurgeCutoff(%q) error = %v", test.value, err)
			}
			if !got.Equal(test.want) {
				t.Fatalf("ParseEventDetailsPurgeCutoff(%q) = %v, want %v", test.value, got, test.want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("ParseEventDetailsPurgeCutoff(%q) location = %v, want UTC", test.value, got.Location())
			}
		})
	}
}

func TestValidateEventDetailsPurgeMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dryRun, yes bool
		wantErr     error
	}{
		{name: "dry-run alone is valid", dryRun: true, yes: false, wantErr: nil},
		{name: "yes alone is valid", dryRun: false, yes: true, wantErr: nil},
		{name: "neither is a hard refusal, never a silent default", dryRun: false, yes: false, wantErr: ErrEventDetailsPurgeModeRequired},
		{name: "both is a hard refusal, ambiguous intent is never resolved silently", dryRun: true, yes: true, wantErr: ErrEventDetailsPurgeModeAmbiguous},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := ValidateEventDetailsPurgeMode(test.dryRun, test.yes)
			if test.wantErr == nil {
				if got != nil {
					t.Fatalf("ValidateEventDetailsPurgeMode(%t, %t) = %v, want nil", test.dryRun, test.yes, got)
				}
				return
			}
			if !errors.Is(got, test.wantErr) {
				t.Fatalf("ValidateEventDetailsPurgeMode(%t, %t) = %v, want %v", test.dryRun, test.yes, got, test.wantErr)
			}
		})
	}
}

// --- TEST FIXTURES -----------------------------------------------------------

// seedPurgeCampaign inserts a minimal campaigns row directly (bypassing
// PostCampaign's template/page/smtp/group requirements, which the purge
// eligibility join never reads: it only ever reads campaigns.id and
// campaigns.status). user_id is fixed at 1 so GetCampaign/GetCampaignResults
// (which filter by user_id) can read it back directly in tests that need to.
func seedPurgeCampaign(t *testing.T, status string) int64 {
	t.Helper()
	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin seed campaign transaction: %v", transaction.Error)
	}
	if err := transaction.Exec(
		"INSERT INTO campaigns (user_id, name, status) VALUES (?, ?, ?)",
		1, "purge-test-campaign", status,
	).Error; err != nil {
		transaction.Rollback()
		t.Fatalf("seed purge test campaign: %v", err)
	}
	var id int64
	if err := transaction.Raw("SELECT last_insert_rowid()").Row().Scan(&id); err != nil {
		transaction.Rollback()
		t.Fatalf("read seeded campaign id: %v", err)
	}
	if err := transaction.Commit().Error; err != nil {
		t.Fatalf("commit seed campaign transaction: %v", err)
	}
	return id
}

// purgeEventSeed is the raw-row shape seedPurgeEvent writes directly,
// deliberately bypassing AddEvent so every transition state from goal.md
// (LEGACY/MIGRATED/BOTH/INVALID) and an arbitrary Event.Time (AddEvent
// always forces time.Now().UTC(), which cannot express an "old" event) can
// be constructed directly, exactly like seedLegacyEventRow/
// seedMySQLLegacyEventRow already do for the migration tests.
type purgeEventSeed struct {
	CampaignID int64
	Email      string
	Message    string
	Time       time.Time
	Details    sql.NullString
	Ciphertext []byte
}

func seedPurgeEvent(t *testing.T, seed purgeEventSeed) int64 {
	t.Helper()
	if seed.Email == "" {
		seed.Email = "purge-fixture@example.test"
	}
	if seed.Message == "" {
		seed.Message = EventClicked
	}
	var detailsArg interface{}
	if seed.Details.Valid {
		detailsArg = seed.Details.String
	}

	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin seed event transaction: %v", transaction.Error)
	}
	if err := transaction.Exec(
		"INSERT INTO events (campaign_id, email, time, message, details, details_ciphertext) VALUES (?, ?, ?, ?, ?, ?)",
		seed.CampaignID, seed.Email, seed.Time, seed.Message, detailsArg, seed.Ciphertext,
	).Error; err != nil {
		transaction.Rollback()
		t.Fatalf("seed purge test event: %v", err)
	}
	var id int64
	if err := transaction.Raw("SELECT last_insert_rowid()").Row().Scan(&id); err != nil {
		transaction.Rollback()
		t.Fatalf("read seeded event id: %v", err)
	}
	if err := transaction.Commit().Error; err != nil {
		t.Fatalf("commit seed event transaction: %v", err)
	}
	return id
}

func validLegacyDetails(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

type purgeEventDetailsColumns struct {
	Details    sql.NullString
	Ciphertext []byte
}

func readPurgeEventDetailsColumns(t *testing.T, id int64) purgeEventDetailsColumns {
	t.Helper()
	var row purgeEventDetailsColumns
	if err := db.Raw("SELECT details, details_ciphertext FROM events WHERE id = ?", id).
		Row().Scan(&row.Details, &row.Ciphertext); err != nil {
		t.Fatalf("read event %d columns: %v", id, err)
	}
	return row
}

func assertEventDetailsPurged(t *testing.T, id int64) {
	t.Helper()
	row := readPurgeEventDetailsColumns(t, id)
	if row.Details.Valid {
		t.Fatalf("event %d: details still set after purge: %q", id, row.Details.String)
	}
	if row.Ciphertext != nil {
		t.Fatalf("event %d: details_ciphertext still set after purge", id)
	}
}

func assertEventDetailsUnchanged(t *testing.T, id int64, want purgeEventDetailsColumns) {
	t.Helper()
	got := readPurgeEventDetailsColumns(t, id)
	if got.Details != want.Details {
		t.Fatalf("event %d: details = %+v, want %+v (changed by a supposedly read-only or non-matching operation)", id, got.Details, want.Details)
	}
	if !bytes.Equal(got.Ciphertext, want.Ciphertext) {
		t.Fatalf("event %d: details_ciphertext changed unexpectedly", id)
	}
}

// --- DRY_RUN = READ_ONLY / SAME_ELIGIBILITY_LOGIC ---------------------------

func TestReportEventDetailsPurgeEligibilityCountsAndWritesNothing(t *testing.T) {
	setupEventDetailsDatabase(t)
	completedCampaign := seedPurgeCampaign(t, CampaignComplete)
	activeCampaign := seedPurgeCampaign(t, CampaignInProgress)

	cutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-48 * time.Hour)
	newer := cutoff.Add(48 * time.Hour)

	eligibleID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: completedCampaign, Message: EventDataSubmit, Time: old,
		Details: validLegacyDetails("legacy-details"),
	})
	// Too new: never eligible.
	seedPurgeEvent(t, purgeEventSeed{
		CampaignID: completedCampaign, Message: EventClicked, Time: newer,
		Details: validLegacyDetails("legacy-details"),
	})
	// Already empty: never eligible, never counted.
	seedPurgeEvent(t, purgeEventSeed{
		CampaignID: completedCampaign, Message: EventSent, Time: old,
	})
	// Old enough, but the owning campaign is not Completed: excluded by default.
	seedPurgeEvent(t, purgeEventSeed{
		CampaignID: activeCampaign, Message: EventDataSubmit, Time: old,
		Details: validLegacyDetails("legacy-details"),
	})

	before := readPurgeEventDetailsColumns(t, eligibleID)

	report, err := ReportEventDetailsPurgeEligibility(cutoff, false)
	if err != nil {
		t.Fatalf("ReportEventDetailsPurgeEligibility: %v", err)
	}
	if report.Eligible != 1 {
		t.Fatalf("Eligible = %d, want 1", report.Eligible)
	}
	if report.MinEventID != eligibleID || report.MaxEventID != eligibleID {
		t.Fatalf("MinEventID/MaxEventID = %d/%d, want %d/%d", report.MinEventID, report.MaxEventID, eligibleID, eligibleID)
	}
	if !report.OldestEventTime.Equal(old) || !report.NewestEventTime.Equal(old) {
		t.Fatalf("OldestEventTime/NewestEventTime = %v/%v, want %v", report.OldestEventTime, report.NewestEventTime, old)
	}
	if report.DistinctCampaigns != 1 {
		t.Fatalf("DistinctCampaigns = %d, want 1", report.DistinctCampaigns)
	}

	// Dry-run reads must never write: assert the exact same row is still
	// byte-identical after the "same eligibility logic" read-only query ran.
	assertEventDetailsUnchanged(t, eligibleID, before)
}

func TestReportEventDetailsPurgeEligibilityWithNoEligibleRows(t *testing.T) {
	setupEventDetailsDatabase(t)
	report, err := ReportEventDetailsPurgeEligibility(time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("ReportEventDetailsPurgeEligibility: %v", err)
	}
	if report.Eligible != 0 || report.MinEventID != 0 || report.MaxEventID != 0 || report.DistinctCampaigns != 0 {
		t.Fatalf("report over an empty table = %+v, want all zero", report)
	}
	if !report.OldestEventTime.IsZero() || !report.NewestEventTime.IsZero() {
		t.Fatalf("report over an empty table has non-zero times: %+v", report)
	}
}

// --- ELIGIBILITY RULES -------------------------------------------------------

func TestPurgeEventDetailsBeforeEligibilityRules(t *testing.T) {
	setupEventDetailsDatabase(t)
	completedCampaign := seedPurgeCampaign(t, CampaignComplete)
	activeCampaign := seedPurgeCampaign(t, CampaignInProgress)

	cutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-time.Hour)
	newer := cutoff.Add(time.Hour)

	oldCompletedID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: completedCampaign, Message: EventDataSubmit, Time: old,
		Details: validLegacyDetails("legacy"),
	})
	newCompletedID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: completedCampaign, Message: EventDataSubmit, Time: newer,
		Details: validLegacyDetails("legacy"),
	})
	emptyOldID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: completedCampaign, Message: EventSent, Time: old,
	})
	oldActiveID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: activeCampaign, Message: EventDataSubmit, Time: old,
		Details: validLegacyDetails("legacy"),
	})

	emptyBefore := readPurgeEventDetailsColumns(t, emptyOldID)
	newBefore := readPurgeEventDetailsColumns(t, newCompletedID)
	activeBefore := readPurgeEventDetailsColumns(t, oldActiveID)

	result, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("PurgeEventDetailsBefore: %v", err)
	}
	if result.Purged != 1 {
		t.Fatalf("Purged = %d, want 1", result.Purged)
	}

	assertEventDetailsPurged(t, oldCompletedID)
	assertEventDetailsUnchanged(t, newCompletedID, newBefore)
	assertEventDetailsUnchanged(t, emptyOldID, emptyBefore)
	assertEventDetailsUnchanged(t, oldActiveID, activeBefore)

	// The active campaign's old event becomes eligible, and ONLY with
	// --include-active-campaigns -- a second, separate call is required,
	// exactly like goal.md's binding "no confirmation bypass" rule.
	result2, err := PurgeEventDetailsBefore(cutoff, true)
	if err != nil {
		t.Fatalf("PurgeEventDetailsBefore with includeActiveCampaigns: %v", err)
	}
	if result2.Purged != 1 {
		t.Fatalf("Purged with includeActiveCampaigns = %d, want 1", result2.Purged)
	}
	assertEventDetailsPurged(t, oldActiveID)
}

// --- PURGE MECHANICS: NEVER_DECRYPT / ALL_TRANSITION_STATES ----------------

// TestPurgeEventDetailsBeforeClearsEveryTransitionStateWithoutDecrypting
// proves goal.md's binding "never decrypt" requirement BY CONSTRUCTION:
// PurgeEventDetailsBefore's signature (time.Time, bool) accepts no
// *credentials.Cipher at all, so there is no way for its call graph to ever
// decrypt anything -- not "avoided by discipline", but structurally
// impossible. A deliberately-undecryptable "INVALID" ciphertext row is
// purged exactly like every other row, with zero special-casing and zero
// decrypt attempt.
func TestPurgeEventDetailsBeforeClearsEveryTransitionStateWithoutDecrypting(t *testing.T) {
	setupEventDetailsDatabase(t)
	campaign := seedPurgeCampaign(t, CampaignComplete)
	cutoff := time.Now().UTC().Add(time.Hour)
	eventTime := time.Now().UTC()

	legacyID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: campaign, Time: eventTime,
		Details: validLegacyDetails("legacy-plaintext"),
	})
	migratedID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: campaign, Time: eventTime,
		Ciphertext: []byte("not-a-real-envelope-but-purge-never-parses-it"),
	})
	bothID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: campaign, Time: eventTime,
		Details:    validLegacyDetails("legacy-plaintext"),
		Ciphertext: []byte("also-never-decrypted-or-compared"),
	})
	// Genuinely undecryptable: too short to even be a plausible envelope,
	// and contains bytes no valid envelope could ever decode to.
	invalidID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: campaign, Time: eventTime,
		Ciphertext: []byte{0x00, 0x01, 0x02, 0xff, 0xfe},
	})

	result, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("PurgeEventDetailsBefore: %v", err)
	}
	if result.Purged != 4 {
		t.Fatalf("Purged = %d, want 4", result.Purged)
	}

	for _, id := range []int64{legacyID, migratedID, bothID, invalidID} {
		assertEventDetailsPurged(t, id)
	}
}

// --- BATCHING / IDEMPOTENCE / RESUMABILITY ----------------------------------

func TestPurgeEventDetailsBeforeHandlesMultipleBatches(t *testing.T) {
	setupEventDetailsDatabase(t)
	campaign := seedPurgeCampaign(t, CampaignComplete)
	cutoff := time.Now().UTC().Add(time.Hour)
	eventTime := time.Now().UTC()

	total := eventDetailsPurgeBatchSize + 50
	ids := make([]int64, 0, total)
	for i := 0; i < total; i++ {
		ids = append(ids, seedPurgeEvent(t, purgeEventSeed{
			CampaignID: campaign, Time: eventTime,
			Details: validLegacyDetails("legacy"),
		}))
	}

	result, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("PurgeEventDetailsBefore: %v", err)
	}
	if result.Purged != int64(total) {
		t.Fatalf("Purged = %d, want %d", result.Purged, total)
	}
	if result.Batches < 2 {
		t.Fatalf("Batches = %d, want at least 2 for %d rows with a batch size of %d", result.Batches, total, eventDetailsPurgeBatchSize)
	}
	for _, id := range ids {
		assertEventDetailsPurged(t, id)
	}
}

func TestPurgeEventDetailsBeforeIsIdempotent(t *testing.T) {
	setupEventDetailsDatabase(t)
	campaign := seedPurgeCampaign(t, CampaignComplete)
	cutoff := time.Now().UTC().Add(time.Hour)
	id := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: campaign, Time: time.Now().UTC(),
		Details: validLegacyDetails("legacy"),
	})

	first, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("first purge: %v", err)
	}
	if first.Purged != 1 {
		t.Fatalf("first purge Purged = %d, want 1", first.Purged)
	}
	assertEventDetailsPurged(t, id)

	second, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("second (re-run) purge: %v", err)
	}
	if second.Purged != 0 || second.Batches != 0 {
		t.Fatalf("re-running an identical purge = %+v, want zero purged and zero batches", second)
	}
}

// TestPurgeEventDetailsBeforeResumesAfterInterruption simulates a process
// killed mid-run by invoking purgeEventDetailsOneBatch -- the exact same
// building block PurgeEventDetailsBefore's loop uses -- exactly once, then
// proves a wholly separate, later call to the public PurgeEventDetailsBefore
// (as an operator's re-run of the same command would be) completes the
// remaining eligible rows correctly, with no checkpoint/cursor of any kind
// passed between the two calls.
func TestPurgeEventDetailsBeforeResumesAfterInterruption(t *testing.T) {
	setupEventDetailsDatabase(t)
	campaign := seedPurgeCampaign(t, CampaignComplete)
	cutoff := time.Now().UTC().Add(time.Hour)
	eventTime := time.Now().UTC()

	total := eventDetailsPurgeBatchSize + 20
	ids := make([]int64, 0, total)
	for i := 0; i < total; i++ {
		ids = append(ids, seedPurgeEvent(t, purgeEventSeed{
			CampaignID: campaign, Time: eventTime,
			Details: validLegacyDetails("legacy"),
		}))
	}

	clause, args := eventDetailsPurgeEligibilityClause(cutoff, false)
	fetched, purged, skipped, err := purgeEventDetailsOneBatch(clause, args, eventDetailsPurgeBatchSize)
	if err != nil {
		t.Fatalf("simulated interrupted first batch: %v", err)
	}
	if fetched != eventDetailsPurgeBatchSize || purged != int64(eventDetailsPurgeBatchSize) || skipped != 0 {
		t.Fatalf("simulated interrupted batch = fetched %d purged %d skipped %d, want fetched/purged %d skipped 0", fetched, purged, skipped, eventDetailsPurgeBatchSize)
	}

	// The process is now "killed". A wholly independent, later invocation
	// (modeling an operator's re-run of the identical command) must finish
	// the remaining rows without any state passed from the call above.
	result, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("resumed purge: %v", err)
	}
	want := int64(total - eventDetailsPurgeBatchSize)
	if result.Purged != want {
		t.Fatalf("resumed purge Purged = %d, want %d", result.Purged, want)
	}
	for _, id := range ids {
		assertEventDetailsPurged(t, id)
	}
}

// --- STATS / API CONTRACT REGRESSION ----------------------------------------

func TestPurgeEventDetailsBeforeDoesNotChangeCampaignStats(t *testing.T) {
	setupEventDetailsDatabase(t)
	campaign := seedPurgeCampaign(t, CampaignComplete)

	// getCampaignStats reads only the results table (status/reported) and
	// never Event/Details at all -- see goal.md section 0 -- so an
	// unchanged value here is a pure regression guard, not new behavior.
	if err := db.Exec(
		"INSERT INTO results (campaign_id, status, reported) VALUES (?, ?, ?)",
		campaign, EventDataSubmit, false,
	).Error; err != nil {
		t.Fatalf("seed result row: %v", err)
	}
	eventID := seedPurgeEvent(t, purgeEventSeed{
		CampaignID: campaign, Message: EventDataSubmit, Time: time.Now().UTC(),
		Details: validLegacyDetails("legacy"),
	})

	before, err := getCampaignStats(campaign)
	if err != nil {
		t.Fatalf("getCampaignStats before purge: %v", err)
	}

	cutoff := time.Now().UTC().Add(time.Hour)
	if _, err := PurgeEventDetailsBefore(cutoff, false); err != nil {
		t.Fatalf("PurgeEventDetailsBefore: %v", err)
	}
	assertEventDetailsPurged(t, eventID)

	after, err := getCampaignStats(campaign)
	if err != nil {
		t.Fatalf("getCampaignStats after purge: %v", err)
	}
	if before != after {
		t.Fatalf("campaign stats changed by purge: before=%+v after=%+v", before, after)
	}
}

func TestPurgeEventDetailsBeforeRetainsAPIMetadata(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x33}, 32),
	})
	withEventDetailsCipher(t, cipher)

	campaignID := seedPurgeCampaign(t, CampaignComplete)
	event := &Event{
		Message: EventDataSubmit,
		Email:   "api-metadata@example.test",
		Details: syntheticEventDetailsJSON(t, 32),
	}
	if err := AddEvent(event, campaignID); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	wantTime := event.Time

	cutoff := time.Now().UTC().Add(time.Hour)
	result, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("PurgeEventDetailsBefore: %v", err)
	}
	if result.Purged != 1 {
		t.Fatalf("Purged = %d, want 1", result.Purged)
	}

	results, err := GetCampaignResults(campaignID, 1)
	if err != nil {
		t.Fatalf("GetCampaignResults: %v", err)
	}
	if len(results.Events) != 1 {
		t.Fatalf("GetCampaignResults events = %d, want 1", len(results.Events))
	}
	got := results.Events[0]
	if got.Id != event.Id {
		t.Fatalf("Id = %d, want %d", got.Id, event.Id)
	}
	if got.CampaignId != campaignID {
		t.Fatalf("CampaignId = %d, want %d", got.CampaignId, campaignID)
	}
	if got.Email != "api-metadata@example.test" {
		t.Fatalf("Email = %q, want api-metadata@example.test", got.Email)
	}
	if got.Message != EventDataSubmit {
		t.Fatalf("Message = %q, want %q", got.Message, EventDataSubmit)
	}
	if !got.Time.Equal(wantTime) {
		t.Fatalf("Time = %v, want %v", got.Time, wantTime)
	}
	if got.Details != "" {
		t.Fatalf("Details = %q, want empty string (same EMPTY shape as a never-had-details event)", got.Details)
	}

	// GetCampaign (the other read path) must agree.
	campaign, err := GetCampaign(campaignID, 1)
	if err != nil {
		t.Fatalf("GetCampaign: %v", err)
	}
	if len(campaign.Events) != 1 || campaign.Events[0].Details != "" {
		t.Fatalf("GetCampaign events = %+v, want exactly one event with empty Details", campaign.Events)
	}
}

// --- PLAINTEXT_LOGGING = NONE ------------------------------------------------

// TestPurgeEventDetailsBeforeNeverLeaksSecretsInResultsOrErrors plants a
// unique synthetic marker inside an event's encrypted Details, confirms it
// genuinely exists (decrypts successfully) before the purge, runs both
// ReportEventDetailsPurgeEligibility and PurgeEventDetailsBefore, and greps
// every value either function returns (formatted exactly as a careless
// future caller might log it) for that marker. It must never appear --
// these functions return only counts, IDs, and timestamps.
func TestPurgeEventDetailsBeforeNeverLeaksSecretsInResultsOrErrors(t *testing.T) {
	setupEventDetailsDatabase(t)
	cipher := testCredentialCipher(t, "event-key", map[string][]byte{
		"event-key": bytes.Repeat([]byte{0x44}, 32),
	})
	withEventDetailsCipher(t, cipher)

	const marker = "PURGE_TEST_SECRET_7f3a9c21b6e4"
	campaignID := seedPurgeCampaign(t, CampaignComplete)
	details := EventDetails{Payload: url.Values{"password": {marker}}}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal synthetic event details: %v", err)
	}
	event := &Event{Message: EventDataSubmit, Email: "secret-test@example.test", Details: string(encoded)}
	if err := AddEvent(event, campaignID); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}

	// Confirm the marker genuinely exists, encrypted, before purging it.
	ciphertextBefore := readPurgeEventDetailsColumns(t, event.Id).Ciphertext
	if len(ciphertextBefore) == 0 {
		t.Fatal("expected ciphertext to be present before purge")
	}
	plaintextBefore, err := cipher.Decrypt(eventDetailsContext(campaignID, event.Id), credentials.Envelope(ciphertextBefore))
	if err != nil || !bytes.Contains(plaintextBefore, []byte(marker)) {
		t.Fatalf("marker not present pre-purge (decrypt err=%v)", err)
	}

	cutoff := time.Now().UTC().Add(time.Hour)
	report, reportErr := ReportEventDetailsPurgeEligibility(cutoff, false)
	result, purgeErr := PurgeEventDetailsBefore(cutoff, false)

	rendered := []string{
		fmt.Sprintf("%+v", report),
		fmt.Sprintf("%+v", result),
		fmt.Sprintf("%v", reportErr),
		fmt.Sprintf("%v", purgeErr),
	}
	for _, text := range rendered {
		if strings.Contains(text, marker) {
			t.Fatalf("marker leaked into a models-package return value: %q", text)
		}
	}

	assertEventDetailsPurged(t, event.Id)
}

// --- BACKEND VALIDATION ------------------------------------------------------

func TestEventDetailsPurgeValidatesBackend(t *testing.T) {
	setupEventDetailsDatabase(t)
	previous := conf
	t.Cleanup(func() { conf = previous })
	conf = &config.Config{DBName: "postgres"}

	if _, err := ReportEventDetailsPurgeEligibility(time.Now().UTC(), false); !errors.Is(err, ErrUnsupportedEventDetailsDB) {
		t.Fatalf("ReportEventDetailsPurgeEligibility with unsupported backend error = %v", err)
	}
	if _, err := PurgeEventDetailsBefore(time.Now().UTC(), false); !errors.Is(err, ErrUnsupportedEventDetailsDB) {
		t.Fatalf("PurgeEventDetailsBefore with unsupported backend error = %v", err)
	}
}

// --- REAL MYSQL (strict and non-strict), same Docker precedent as PR #68 ---

// TestMySQLEventDetailsPurgeDryRunAndRealPurgeStrictAndNonStrict is the real
// MySQL counterpart to the SQLite-only tests above, following the exact
// GOPHISHFR_MYSQL_TEST_DSN-gated / testingMySQLDSN(t) / single-connection /
// SET SESSION sql_mode convention already used by
// TestMySQLEventDetailsNearLegacyCapRoundTripsStrictAndNonStrict. It proves
// the dry-run report and the real purge both work correctly against a real
// `events` MEDIUMBLOB/TEXT column pair, once under the default strict SQL
// session and once under an explicitly disabled non-strict session.
func TestMySQLEventDetailsPurgeDryRunAndRealPurgeStrictAndNonStrict(t *testing.T) {
	connectionString := testingMySQLDSN(t)
	if connectionString == "" {
		return
	}
	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatalf("open MySQL database: %v", err)
	}
	db = database
	db.LogMode(false)
	// Pin the pool to a single connection so SET SESSION sql_mode below (and
	// every statement that follows it) is guaranteed to observe the same
	// session, exactly as the other MySQL-gated tests in this package do.
	db.DB().SetMaxOpenConns(1)
	db.DB().SetMaxIdleConns(1)
	conf = &config.Config{
		DBName:         "mysql",
		DBPath:         connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatalf("migrate MySQL database: %v", err)
	}
	if err := db.Exec("DELETE FROM events").Error; err != nil {
		t.Fatalf("clear MySQL events rows: %v", err)
	}
	if err := db.Exec("DELETE FROM campaigns").Error; err != nil {
		t.Fatalf("clear MySQL campaigns rows: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM events").Error
		_ = db.Exec("DELETE FROM campaigns").Error
		_ = database.Close()
	})

	runMySQLEventDetailsPurgeScenario(t, "strict session")

	var originalSQLMode string
	if err := db.Raw("SELECT @@SESSION.sql_mode").Row().Scan(&originalSQLMode); err != nil {
		t.Fatalf("read MySQL session SQL mode: %v", err)
	}
	if err := db.Exec("SET SESSION sql_mode = ''").Error; err != nil {
		t.Fatalf("disable MySQL strict mode: %v", err)
	}
	var nonStrictSQLMode string
	if err := db.Raw("SELECT @@SESSION.sql_mode").Row().Scan(&nonStrictSQLMode); err != nil {
		t.Fatalf("verify MySQL session SQL mode: %v", err)
	}
	if nonStrictSQLMode != "" {
		t.Fatalf("MySQL test requires non-strict mode, got %q", nonStrictSQLMode)
	}
	t.Cleanup(func() {
		_ = db.Exec("SET SESSION sql_mode = ?", originalSQLMode).Error
	})

	runMySQLEventDetailsPurgeScenario(t, "non-strict session")
}

func runMySQLEventDetailsPurgeScenario(t *testing.T, label string) {
	t.Helper()

	campaignID := seedMySQLPurgeCampaign(t, CampaignComplete)
	cutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-time.Hour)
	newer := cutoff.Add(time.Hour)

	eligibleID := seedMySQLPurgeEvent(t, campaignID, old, "legacy-details", nil)
	ineligibleNewID := seedMySQLPurgeEvent(t, campaignID, newer, "legacy-details", nil)

	report, err := ReportEventDetailsPurgeEligibility(cutoff, false)
	if err != nil {
		t.Fatalf("[%s] ReportEventDetailsPurgeEligibility: %v", label, err)
	}
	if report.Eligible != 1 || report.MinEventID != eligibleID || report.MaxEventID != eligibleID {
		t.Fatalf("[%s] report = %+v, want exactly event %d eligible", label, report, eligibleID)
	}

	result, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("[%s] PurgeEventDetailsBefore: %v", label, err)
	}
	if result.Purged != 1 {
		t.Fatalf("[%s] Purged = %d, want 1", label, result.Purged)
	}
	assertEventDetailsPurged(t, eligibleID)

	var untouchedDetails sql.NullString
	if err := db.Raw("SELECT details FROM events WHERE id = ?", ineligibleNewID).Row().Scan(&untouchedDetails); err != nil {
		t.Fatalf("[%s] read untouched MySQL event: %v", label, err)
	}
	if !untouchedDetails.Valid || untouchedDetails.String != "legacy-details" {
		t.Fatalf("[%s] untouched event details = %+v, want unchanged \"legacy-details\"", label, untouchedDetails)
	}

	// Idempotence: a second, identical dry-run and purge over the same
	// cutoff must now report/purge zero rows.
	secondReport, err := ReportEventDetailsPurgeEligibility(cutoff, false)
	if err != nil {
		t.Fatalf("[%s] second ReportEventDetailsPurgeEligibility: %v", label, err)
	}
	if secondReport.Eligible != 0 {
		t.Fatalf("[%s] second report Eligible = %d, want 0", label, secondReport.Eligible)
	}
	secondResult, err := PurgeEventDetailsBefore(cutoff, false)
	if err != nil {
		t.Fatalf("[%s] second PurgeEventDetailsBefore: %v", label, err)
	}
	if secondResult.Purged != 0 {
		t.Fatalf("[%s] second purge Purged = %d, want 0", label, secondResult.Purged)
	}
}

func seedMySQLPurgeCampaign(t *testing.T, status string) int64 {
	t.Helper()
	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin MySQL seed campaign transaction: %v", transaction.Error)
	}
	if err := transaction.Exec(
		"INSERT INTO campaigns (user_id, name, status) VALUES (?, ?, ?)",
		1, "mysql-purge-test-campaign", status,
	).Error; err != nil {
		transaction.Rollback()
		t.Fatalf("seed MySQL purge test campaign: %v", err)
	}
	var id int64
	if err := transaction.Raw("SELECT LAST_INSERT_ID()").Row().Scan(&id); err != nil {
		transaction.Rollback()
		t.Fatalf("read seeded MySQL campaign id: %v", err)
	}
	if err := transaction.Commit().Error; err != nil {
		t.Fatalf("commit MySQL seed campaign transaction: %v", err)
	}
	return id
}

func seedMySQLPurgeEvent(t *testing.T, campaignID int64, eventTime time.Time, details string, ciphertext []byte) int64 {
	t.Helper()
	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin MySQL seed event transaction: %v", transaction.Error)
	}
	var detailsArg interface{}
	if details != "" {
		detailsArg = details
	}
	if err := transaction.Exec(
		"INSERT INTO events (campaign_id, email, time, message, details, details_ciphertext) VALUES (?, ?, ?, ?, ?, ?)",
		campaignID, "mysql-purge@example.test", eventTime, EventClicked, detailsArg, ciphertext,
	).Error; err != nil {
		transaction.Rollback()
		t.Fatalf("seed MySQL purge test event: %v", err)
	}
	var id int64
	if err := transaction.Raw("SELECT LAST_INSERT_ID()").Row().Scan(&id); err != nil {
		transaction.Rollback()
		t.Fatalf("read seeded MySQL event id: %v", err)
	}
	if err := transaction.Commit().Error; err != nil {
		t.Fatalf("commit MySQL seed event transaction: %v", err)
	}
	return id
}
