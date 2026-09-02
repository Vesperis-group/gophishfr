//go:build browser && linux

package controllers

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/Vesperis-group/gophishfr/models"
)

const (
	browserTestUsername = "browser-admin"
	browserTestPassword = "browser-test-password"
)

func TestBrowserSmoke(t *testing.T) {
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

	t.Setenv(models.InitialAdminPassword, browserTestPassword)
	t.Setenv(models.InitialAdminApiToken, "browser-test-api-token-not-a-secret")

	databaseDirectory := t.TempDir()
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         filepath.Join(databaseDirectory, "browser-smoke.db"),
		MigrationsPath: filepath.Join(root, "db", "db_sqlite3", "migrations"),
	}
	if err := models.Setup(conf); err != nil {
		t.Fatalf("set up browser test database: %v", err)
	}

	user, err := models.GetUser(1)
	if err != nil {
		t.Fatalf("get browser test user: %v", err)
	}
	user.Username = browserTestUsername
	user.PasswordChangeRequired = false
	if err := models.PutUser(&user); err != nil {
		t.Fatalf("configure browser test user: %v", err)
	}

	seedBrowserFixtures(t, user.Id)

	keyring, err := credentials.NewKeyring(
		"browser-test-key",
		map[string][]byte{"browser-test-key": bytes.Repeat([]byte{0x58}, 32)},
	)
	if err != nil {
		t.Fatalf("create browser credential keyring: %v", err)
	}
	credentialCipher, err := credentials.New(keyring)
	if err != nil {
		t.Fatalf("create browser credential cipher: %v", err)
	}
	adminServer := NewAdminServer(
		config.AdminServer{},
		WithCredentialCipher(credentialCipher),
	)
	server := httptest.NewServer(adminServer.server.Handler)
	t.Cleanup(server.Close)

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
		"--config=tests/browser/playwright.config.ts",
	)
	command.Dir = root
	command.Env = append(os.Environ(),
		"GOPHISHFR_BROWSER_BASE_URL="+server.URL,
		"GOPHISHFR_BROWSER_USERNAME="+browserTestUsername,
		"GOPHISHFR_BROWSER_PASSWORD="+browserTestPassword,
		"GOPHISHFR_BROWSER_OUTPUT_DIR="+outputDirectory,
	)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return terminateBrowserProcessTree(command.Process.Pid)
	}
	command.WaitDelay = 2 * time.Second
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run browser smoke tests: %v", err)
	}
}

func browserProjectRoot(t *testing.T) string {
	t.Helper()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	root, err := filepath.Abs(filepath.Join(workingDirectory, ".."))
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	return root
}

func seedBrowserFixtures(t *testing.T, userID int64) {
	t.Helper()

	const emailTemplateHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.FirstName}} browser fixture</title><style>.browser-cta{color:#123456!important;background-image:url(https://preview.invalid/background.png)}</style></head><body class="email-body" data-fixture="editor-round-trip"><table role="presentation" style="border-collapse:collapse;width:100%"><tr><td><p>Hello <strong>{{.FirstName}}</strong></p><a id="browser-cta" class="browser-cta" data-rid="{{.RId}}" aria-label="Open for {{.FirstName}}" href="{{.URL}}?rid={{.RId}}">Open</a><img src="https://preview.invalid/pixel.png" onerror="window.__previewHandlerExecuted=true" alt="Tracking pixel"><script>window.__previewScriptExecuted=true</script>{{.Tracker}}</td></tr></table></body></html>`
	const landingPageHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Browser landing page</title><style>.browser-form{max-width:24rem}</style></head><body data-fixture="editor-round-trip"><form class="browser-form" data-purpose="synthetic" action="/ignored"><label for="browser-user">User</label><input id="browser-user" name="username" value="{{.FirstName}}"><label for="browser-password">Password</label><input id="browser-password" name="password" type="password"><button type="submit">Continue</button></form></body></html>`

	group := models.Group{
		Name:   "Browser Fixture Group",
		UserId: userID,
		Targets: []models.Target{{
			BaseRecipient: models.BaseRecipient{
				Email:     "fixture@localhost.invalid",
				FirstName: "Browser",
				LastName:  "Fixture",
			},
		}},
	}
	if err := models.PostGroup(&group); err != nil {
		t.Fatalf("create browser test group: %v", err)
	}

	emailTemplate := models.Template{
		Name:    "Browser Fixture Template",
		Subject: "Synthetic browser fixture",
		Text:    "Synthetic browser fixture",
		HTML:    emailTemplateHTML,
		UserId:  userID,
	}
	if err := models.PostTemplate(&emailTemplate); err != nil {
		t.Fatalf("create browser test template: %v", err)
	}

	landingPage := models.Page{
		Name:               "Browser Fixture Landing Page",
		HTML:               landingPageHTML,
		CaptureCredentials: true,
		UserId:             userID,
	}
	if err := models.PostPage(&landingPage); err != nil {
		t.Fatalf("create browser test landing page: %v", err)
	}

	sendingProfile := models.SMTP{
		Name:        "Browser Fixture Sending Profile",
		FromAddress: "browser@localhost.invalid",
		Host:        "127.0.0.1:1",
		UserId:      userID,
	}
	if err := models.PostSMTP(&sendingProfile); err != nil {
		t.Fatalf("create browser test sending profile: %v", err)
	}

	campaign := models.Campaign{
		Name:     "Browser Fixture Campaign",
		UserId:   userID,
		Template: emailTemplate,
		Page:     landingPage,
		SMTP:     sendingProfile,
		Groups:   []models.Group{group},
		URL:      "http://127.0.0.1:1",
	}
	if err := models.PostCampaign(&campaign, userID); err != nil {
		t.Fatalf("create browser test campaign: %v", err)
	}
	if len(campaign.Results) != 1 {
		t.Fatalf("browser test campaign created %d results, want 1", len(campaign.Results))
	}

	result := &campaign.Results[0]
	eventDetails := models.EventDetails{}
	events := []struct {
		name   string
		record func() error
	}{
		{name: models.EventSent, record: result.HandleEmailSent},
		{name: models.EventOpened, record: func() error { return result.HandleEmailOpened(eventDetails) }},
		{name: models.EventClicked, record: func() error { return result.HandleClickedLink(eventDetails) }},
		{name: models.EventDataSubmit, record: func() error { return result.HandleFormSubmit(eventDetails) }},
		{name: models.EventReported, record: func() error { return result.HandleEmailReport(eventDetails) }},
	}
	for _, event := range events {
		if err := event.record(); err != nil {
			t.Fatalf("record browser campaign event %q: %v", event.name, err)
		}
	}
	if err := campaign.UpdateStatus(models.CampaignInProgress); err != nil {
		t.Fatalf("activate browser test campaign: %v", err)
	}

	seedBrowserTableFixtures(t, userID)
}

// browserTableFixtureCount is the number of extra landing pages seeded so the
// landing-page table exceeds one page at the default length. It gives the
// browser suite a table it can sort, search and page through deterministically.
const browserTableFixtureCount = 12

// browserAttachmentFixtureCount is the number of attachments on the fixture
// template. It is deliberately larger than the attachment table's default page
// length, so a rendering strategy that only keeps the visible rows would lose
// the rest and the browser suite would notice.
const browserAttachmentFixtureCount = 12

// seedBrowserTableFixtures creates landing pages whose names sort in the exact
// reverse of their modification dates. A table ordered by name therefore cannot
// accidentally satisfy an assertion about ordering by date, which is what makes
// the date column a real test of the table's date type detection. It also
// creates the many-attachment template described above.
func seedBrowserTableFixtures(t *testing.T, userID int64) {
	t.Helper()

	base := time.Date(2031, time.March, 15, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= browserTableFixtureCount; i++ {
		page := models.Page{
			// The name carries a token that appears in no other fixture, so a
			// table search for it selects exactly this set. DataTables' default
			// search matches words in any order, so a token shared with another
			// fixture name would silently widen the result.
			Name:         fmt.Sprintf("Paginated Fixture %02d", i),
			HTML:         fmt.Sprintf("<html><body>Paginated fixture %02d</body></html>", i),
			UserId:       userID,
			ModifiedDate: base.AddDate(0, 0, -i),
		}
		if err := models.PostPage(&page); err != nil {
			t.Fatalf("create browser table fixture page %02d: %v", i, err)
		}
	}

	attachments := make([]models.Attachment, 0, browserAttachmentFixtureCount)
	for i := 1; i <= browserAttachmentFixtureCount; i++ {
		attachments = append(attachments, models.Attachment{
			Name: fmt.Sprintf("attachment-%02d.txt", i),
			Type: "text/plain",
			// "fixture" base64-encoded; the content only has to survive intact.
			Content: "Zml4dHVyZQ==",
		})
	}
	attachmentTemplate := models.Template{
		Name:        "Browser Attachment Fixture",
		Subject:     "Synthetic attachment fixture",
		Text:        "Synthetic attachment fixture",
		HTML:        "<html><body>Attachment fixture</body></html>",
		UserId:      userID,
		Attachments: attachments,
	}
	if err := models.PostTemplate(&attachmentTemplate); err != nil {
		t.Fatalf("create browser attachment fixture template: %v", err)
	}
}
