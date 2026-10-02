//go:build browser && linux

package controllers

import (
	"bytes"
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/Vesperis-group/gophishfr/models"
)

const (
	purgeBrowserTestUsername = "purge-browser-admin"
	purgeBrowserTestPassword = "purge-browser-test-password"
)

// TestBrowserEventDetailsPurge is the real-browser regression test required
// by goal.md item 8: it creates a synthetic "Submitted Data" event with a
// non-empty payload, confirms Replay is offered and functional (existing
// pre-purge behavior), runs the real Go-side purge
// (models.PurgeEventDetailsBefore) directly against that event, reloads the
// results page, and asserts Replay is gone, no console error occurs, and
// the event's Message/Time survive unchanged -- all with ZERO changes to
// static/js/src/app/campaign_results.js, proving goal.md item 0's
// hypothesis (an empty Details string is already indistinguishable, by the
// existing `if (event.details)` guard, from an event that never had any).
//
// This runs as a separate Go test (not folded into TestBrowserSmoke) with
// its own isolated SQLite database and httptest server, and its own
// dedicated Playwright config/spec pair
// (tests/browser/playwright.purge.config.ts /
// tests/browser/event-details-purge.spec.ts) so it can run Playwright
// TWICE against the SAME running server -- once before the purge, once
// after -- with the real, direct Go-side purge call executed in between.
// tests/browser/playwright.config.ts (the config TestBrowserSmoke uses)
// explicitly ignores event-details-purge.spec.ts so the two Go tests'
// Playwright invocations never collide.
func TestBrowserEventDetailsPurge(t *testing.T) {
	root := browserProjectRoot(t)
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("change to project root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	t.Setenv(models.InitialAdminPassword, purgeBrowserTestPassword)
	t.Setenv(models.InitialAdminApiToken, "purge-browser-test-api-token-not-a-secret")
	installTestAPIKeyVerifier(t)

	databaseDirectory := t.TempDir()
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         filepath.Join(databaseDirectory, "browser-purge.db"),
		MigrationsPath: filepath.Join(root, "db", "db_sqlite3", "migrations"),
	}
	if err := models.Setup(conf); err != nil {
		t.Fatalf("set up browser purge test database: %v", err)
	}

	user, err := models.GetUser(1)
	if err != nil {
		t.Fatalf("get browser purge test user: %v", err)
	}
	user.Username = purgeBrowserTestUsername
	user.PasswordChangeRequired = false
	if err := models.PutUser(&user); err != nil {
		t.Fatalf("configure browser purge test user: %v", err)
	}

	// The event-details cipher must be installed before the fixture event is
	// recorded -- see browser_smoke_test.go's identical ordering note.
	keyring, err := credentials.NewKeyring(
		"purge-browser-test-key",
		map[string][]byte{"purge-browser-test-key": bytes.Repeat([]byte{0x72}, 32)},
	)
	if err != nil {
		t.Fatalf("create purge browser credential keyring: %v", err)
	}
	credentialCipher, err := credentials.New(keyring)
	if err != nil {
		t.Fatalf("create purge browser credential cipher: %v", err)
	}
	models.SetEventDetailsCipher(credentialCipher)

	campaignID, submittedEvent := seedPurgeBrowserFixtures(t, user.Id)

	adminServer := NewAdminServer(
		config.AdminServer{},
		WithCredentialCipher(credentialCipher),
	)
	server := httptest.NewServer(adminServer.server.Handler)
	t.Cleanup(server.Close)

	runPurgeBrowserSuite := func(spec string, phase string) {
		t.Helper()

		deadline, ok := t.Deadline()
		if !ok {
			deadline = time.Now().Add(2 * time.Minute)
		}
		commandContext, cancelCommand := context.WithDeadline(
			context.Background(),
			deadline.Add(-5*time.Second),
		)
		defer cancelCommand()

		outputDirectory := t.TempDir()
		command := exec.CommandContext(
			commandContext,
			"corepack",
			"yarn",
			"playwright",
			"test",
			"--config=tests/browser/playwright.purge.config.ts",
			spec,
		)
		command.Dir = root
		command.Env = append(os.Environ(),
			"GOPHISHFR_BROWSER_BASE_URL="+server.URL,
			"GOPHISHFR_BROWSER_USERNAME="+purgeBrowserTestUsername,
			"GOPHISHFR_BROWSER_PASSWORD="+purgeBrowserTestPassword,
			"GOPHISHFR_BROWSER_OUTPUT_DIR="+outputDirectory,
			"GOPHISHFR_BROWSER_PURGE_PHASE="+phase,
			"GOPHISHFR_BROWSER_PURGE_CAMPAIGN_ID="+formatCampaignID(campaignID),
		)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Cancel = func() error {
			return terminateBrowserProcessTree(command.Process.Pid)
		}
		command.WaitDelay = 2 * time.Second
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			t.Fatalf("run browser purge suite (phase=%s): %v", phase, err)
		}
	}

	// Phase 1: BEFORE the purge -- Replay must be offered and functional.
	runPurgeBrowserSuite("tests/browser/event-details-purge.spec.ts", "before")

	// The real, direct Go-side purge: cutoff is set safely in the future so
	// it unconditionally includes the just-created event regardless of
	// exact timing, and includeActiveCampaigns is true so this test never
	// depends on the fixture campaign's Status -- that eligibility nuance
	// is already covered exhaustively by models' own Go unit tests.
	cutoff := time.Now().UTC().Add(time.Hour)
	result, err := models.PurgeEventDetailsBefore(cutoff, true)
	if err != nil {
		t.Fatalf("PurgeEventDetailsBefore: %v", err)
	}
	if result.Purged < 1 {
		t.Fatalf("PurgeEventDetailsBefore purged %d rows, want at least 1 (the Submitted Data fixture event)", result.Purged)
	}

	// Phase 2: AFTER the purge -- Replay must be gone, with no exception,
	// and the event's Message/Time must survive.
	runPurgeBrowserSuite("tests/browser/event-details-purge.spec.ts", "after")

	// Independent Go-side confirmation alongside the browser assertions:
	// Message/Time/Email/CampaignId survive, and Details is now empty.
	afterCampaign, err := models.GetCampaign(campaignID, user.Id)
	if err != nil {
		t.Fatalf("reload campaign after purge: %v", err)
	}
	found := false
	for _, event := range afterCampaign.Events {
		if event.Id != submittedEvent.Id {
			continue
		}
		found = true
		if event.Message != submittedEvent.Message {
			t.Fatalf("purged event Message = %q, want %q", event.Message, submittedEvent.Message)
		}
		if !event.Time.Equal(submittedEvent.Time) {
			t.Fatalf("purged event Time = %v, want %v", event.Time, submittedEvent.Time)
		}
		if event.Details != "" {
			t.Fatalf("purged event Details = %q, want empty string", event.Details)
		}
	}
	if !found {
		t.Fatal("purged event disappeared from the campaign entirely; only its Details should have been cleared")
	}
}

// seedPurgeBrowserFixtures creates one minimal campaign (one group, one
// target, one template, one landing page, one sending profile) with exactly
// one Result, and records a real "Submitted Data" event on it with a
// non-empty, realistic captured-credentials payload -- unlike
// browser_smoke_test.go's shared fixtures, which deliberately use an empty
// EventDetails{} for every event. Returns the campaign's ID and the
// recorded Event, for the Playwright suite (via env vars) and the direct
// Go-side purge call respectively.
func seedPurgeBrowserFixtures(t *testing.T, userID int64) (int64, *models.Event) {
	t.Helper()

	const landingPageHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Purge browser landing page</title></head><body data-fixture="purge-regression"><form class="browser-form" data-purpose="synthetic" action="/ignored"><label for="browser-user">User</label><input id="browser-user" name="username" value="{{.FirstName}}"><label for="browser-password">Password</label><input id="browser-password" name="password" type="password"><button type="submit">Continue</button></form></body></html>`
	const emailTemplateHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.FirstName}} purge browser fixture</title></head><body class="email-body" data-fixture="purge-regression"><p>Hello <strong>{{.FirstName}}</strong></p><a id="browser-cta" data-rid="{{.RId}}" href="{{.URL}}?rid={{.RId}}">Open</a>{{.Tracker}}</body></html>`

	group := models.Group{
		Name:   "Purge Browser Fixture Group",
		UserId: userID,
		Targets: []models.Target{{
			BaseRecipient: models.BaseRecipient{
				Email:     "purge-fixture@localhost.invalid",
				FirstName: "Purge",
				LastName:  "Fixture",
			},
		}},
	}
	if err := models.PostGroup(&group); err != nil {
		t.Fatalf("create purge browser test group: %v", err)
	}

	emailTemplate := models.Template{
		Name:    "Purge Browser Fixture Template",
		Subject: "Synthetic purge browser fixture",
		Text:    "Synthetic purge browser fixture",
		HTML:    emailTemplateHTML,
		UserId:  userID,
	}
	if err := models.PostTemplate(&emailTemplate); err != nil {
		t.Fatalf("create purge browser test template: %v", err)
	}

	landingPage := models.Page{
		Name:               "Purge Browser Fixture Landing Page",
		HTML:               landingPageHTML,
		CaptureCredentials: true,
		UserId:             userID,
	}
	if err := models.PostPage(&landingPage); err != nil {
		t.Fatalf("create purge browser test landing page: %v", err)
	}

	sendingProfile := models.SMTP{
		Name:        "Purge Browser Fixture Sending Profile",
		FromAddress: "purge-browser@localhost.invalid",
		Host:        "127.0.0.1:1",
		UserId:      userID,
	}
	if err := models.PostSMTP(&sendingProfile); err != nil {
		t.Fatalf("create purge browser test sending profile: %v", err)
	}

	campaign := models.Campaign{
		Name:     "Purge Browser Fixture Campaign",
		UserId:   userID,
		Template: emailTemplate,
		Page:     landingPage,
		SMTP:     sendingProfile,
		Groups:   []models.Group{group},
		URL:      "http://127.0.0.1:1",
	}
	if err := models.PostCampaign(&campaign, userID); err != nil {
		t.Fatalf("create purge browser test campaign: %v", err)
	}
	if len(campaign.Results) != 1 {
		t.Fatalf("purge browser test campaign created %d results, want 1", len(campaign.Results))
	}

	result := &campaign.Results[0]
	if err := result.HandleEmailSent(); err != nil {
		t.Fatalf("record purge browser fixture Email Sent event: %v", err)
	}

	// A real, non-empty captured-credentials payload -- exactly the shape
	// Replay's rendering guard (`if (event.details)` in
	// campaign_results.js) and its payload table/Replay button depend on.
	submittedDetails := models.EventDetails{
		Payload: url.Values{
			"username": {"purge-replay-user"},
			"password": {"purge-replay-password-not-a-real-secret"},
			"rid":      {result.RId},
		},
		Browser: map[string]string{"user-agent": "purge-browser-fixture-agent"},
	}
	if err := result.HandleClickedLink(submittedDetails); err != nil {
		t.Fatalf("record purge browser fixture Clicked Link event: %v", err)
	}
	if err := result.HandleFormSubmit(submittedDetails); err != nil {
		t.Fatalf("record purge browser fixture Submitted Data event: %v", err)
	}

	var submittedEvent *models.Event
	// campaign.Events is populated at PostCampaign time, before the events
	// above were recorded, so it never actually contains them: re-fetch.
	refreshed, err := models.GetCampaign(campaign.Id, userID)
	if err != nil {
		t.Fatalf("reload purge browser test campaign: %v", err)
	}
	for i := range refreshed.Events {
		if refreshed.Events[i].Message == models.EventDataSubmit {
			submittedEvent = &refreshed.Events[i]
		}
	}
	if submittedEvent == nil {
		t.Fatal("purge browser fixture did not record a Submitted Data event")
	}

	return campaign.Id, submittedEvent
}

func formatCampaignID(id int64) string {
	return strconv.FormatInt(id, 10)
}
