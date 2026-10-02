package controllers

import (
	"crypto/rand"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Vesperis-group/gophishfr/auth"
	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/apikey"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/Vesperis-group/gophishfr/models"
)

// testContext is the data required to test API related functions
type testContext struct {
	apiKey      string
	config      *config.Config
	adminServer *httptest.Server
	phishServer *httptest.Server
	origPath    string
}

func setupTest(t *testing.T) *testContext {
	t.Setenv(models.InitialAdminPassword, "synthetic-controller-test-password")
	t.Setenv(models.InitialAdminApiToken, "synthetic-controller-api-token")
	installTestAPIKeyVerifier(t)
	installTestEventDetailsCipher(t)
	wd, _ := os.Getwd()
	fmt.Println(wd)
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         ":memory:",
		MigrationsPath: "../db/db_sqlite3/migrations/",
	}
	abs, _ := filepath.Abs("../db/db_sqlite3/migrations/")
	fmt.Printf("in controllers_test.go: %s\n", abs)
	err := models.Setup(conf)
	if err != nil {
		t.Fatalf("error setting up database: %v", err)
	}
	ctx := &testContext{}
	ctx.config = conf
	ctx.adminServer = httptest.NewUnstartedServer(NewAdminServer(ctx.config.AdminConf).server.Handler)
	ctx.adminServer.Config.Addr = ctx.config.AdminConf.ListenURL
	ctx.adminServer.Start()
	// Get the API key to use for these tests
	u, err := models.GetUser(1)
	if err != nil {
		t.Fatalf("error getting first user from database: %v", err)
	}
	// Reset the temporary password for the admin user to a value we control
	hash, err := auth.GeneratePasswordHash("gophish")
	if err != nil {
		t.Fatalf("error generating password hash: %v", err)
	}
	u.Hash = hash
	if err := models.PutUser(&u); err != nil {
		t.Fatalf("error updating first user: %v", err)
	}

	// Create a second user to test account locked status
	u2 := models.User{Username: "houdini", Hash: hash, AccountLocked: true}
	if _, err := models.CreateUserWithAPIKey(&u2); err != nil {
		t.Fatalf("error creating new user: %v", err)
	}

	ctx.apiKey = "synthetic-controller-api-token"
	// Start the phishing server
	ctx.phishServer = httptest.NewUnstartedServer(NewPhishingServer(ctx.config.PhishConf).server.Handler)
	ctx.phishServer.Config.Addr = ctx.config.PhishConf.ListenURL
	ctx.phishServer.Start()
	// Move our cwd up to the project root for help with resolving
	// static assets
	origPath, _ := os.Getwd()
	ctx.origPath = origPath
	err = os.Chdir("../")
	if err != nil {
		t.Fatalf("error changing directories to setup asset discovery: %v", err)
	}

	createTestData(t)
	return ctx
}

func installTestAPIKeyVerifier(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyring, err := apikey.NewKeyring("test-active", map[string][]byte{"test-active": key})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := apikey.New(keyring)
	if err != nil {
		t.Fatal(err)
	}
	models.SetAPIKeyVerifier(verifier)
}

// installTestEventDetailsCipher installs a synthetic cipher for the whole
// controllers test suite: like the pre-existing IMAP/SMTP/webhook credential
// paths, AddEvent now requires a configured cipher for any event with
// non-empty Details, so every end-to-end phishing-server test that records
// a click/open/submit event needs one, exactly as a real deployment needs
// GOPHISHFR_CREDENTIAL_KEYRING_FILE configured.
func installTestEventDetailsCipher(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyring, err := credentials.NewKeyring("controller-test-active", map[string][]byte{"controller-test-active": key})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := credentials.New(keyring)
	if err != nil {
		t.Fatal(err)
	}
	models.SetEventDetailsCipher(cipher)
}

func tearDown(t *testing.T, ctx *testContext) {
	// Tear down the admin and phishing servers
	ctx.adminServer.Close()
	ctx.phishServer.Close()
	// Reset the path for the next test
	if err := os.Chdir(ctx.origPath); err != nil {
		t.Fatalf("error restoring the working directory: %v", err)
	}
}

func createTestData(t *testing.T) {
	// Add a group
	group := models.Group{Name: "Test Group"}
	group.Targets = []models.Target{
		{BaseRecipient: models.BaseRecipient{Email: "test1@example.com", FirstName: "First", LastName: "Example"}},
		{BaseRecipient: models.BaseRecipient{Email: "test2@example.com", FirstName: "Second", LastName: "Example"}},
	}
	group.UserId = 1
	if err := models.PostGroup(&group); err != nil {
		t.Fatalf("error creating test group: %v", err)
	}

	// Add a template
	template := models.Template{Name: "Test Template"}
	template.Subject = "Test subject"
	template.Text = "Text text"
	template.HTML = "<html>Test</html>"
	template.UserId = 1
	if err := models.PostTemplate(&template); err != nil {
		t.Fatalf("error creating test template: %v", err)
	}

	// Add a landing page
	p := models.Page{Name: "Test Page"}
	p.HTML = "<html>Test</html>"
	p.UserId = 1
	if err := models.PostPage(&p); err != nil {
		t.Fatalf("error creating test page: %v", err)
	}

	// Add a sending profile
	smtp := models.SMTP{Name: "Test Page"}
	smtp.UserId = 1
	smtp.Host = "example.com"
	smtp.FromAddress = "test@test.com"
	if err := models.PostSMTP(&smtp); err != nil {
		t.Fatalf("error creating test sending profile: %v", err)
	}

	// Setup and "launch" our campaign
	// Set the status such that no emails are attempted
	c := models.Campaign{Name: "Test campaign"}
	c.UserId = 1
	c.Template = template
	c.Page = p
	c.SMTP = smtp
	c.Groups = []models.Group{group}
	if err := models.PostCampaign(&c, c.UserId); err != nil {
		t.Fatalf("error creating test campaign: %v", err)
	}
	if err := c.UpdateStatus(models.CampaignEmailsSent); err != nil {
		t.Fatalf("error updating test campaign status: %v", err)
	}
}
