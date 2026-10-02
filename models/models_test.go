package models

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/apikey"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"gopkg.in/check.v1"
)

const modelsSuiteAPIKey = "synthetic-model-suite-api-token"

func TestMain(m *testing.M) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}

	keyring, err := apikey.NewKeyring("test-active", map[string][]byte{"test-active": key})
	if err != nil {
		panic(err)
	}
	verifier, err := apikey.New(keyring)
	if err != nil {
		panic(err)
	}
	SetAPIKeyVerifier(verifier)

	// Every test in this package that creates a campaign event (directly or
	// indirectly, e.g. via the mailer's MailLog.Backoff/HandleEmailError
	// path) now goes through AddEvent's encrypted write path, which requires
	// a configured cipher for any event with non-empty Details -- exactly
	// like the pre-existing IMAP/SMTP/webhook credential paths already
	// require one. This installs a synthetic default cipher for the whole
	// suite, mirroring the API-key verifier setup immediately above. Tests
	// that specifically exercise the "no cipher configured" fail-closed
	// behavior save and restore this package-level variable around their own
	// assertions (see TestAddEventRequiresCipherForNonEmptyDetails).
	credentialKeyBytes := bytes.Repeat([]byte{0x5a}, 32)
	credentialKeyring, err := credentials.NewKeyring("model-suite-active", map[string][]byte{
		"model-suite-active": credentialKeyBytes,
	})
	if err != nil {
		panic(err)
	}
	credentialCipher, err := credentials.New(credentialKeyring)
	if err != nil {
		panic(err)
	}
	SetEventDetailsCipher(credentialCipher)

	os.Exit(m.Run())
}

func restoreTestUsersForAPIVerifierDown(t *testing.T) {
	t.Helper()
	rows, err := db.DB().Query("SELECT id FROM users ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := db.DB().Exec(
			"UPDATE users SET api_key = ?, api_key_verifier = NULL, api_key_verifier_key_id = NULL WHERE id = ?",
			fmt.Sprintf("synthetic-schema-down-%d", id), id,
		); err != nil {
			t.Fatal(err)
		}
	}
}

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) {
	if err := os.Setenv(InitialAdminPassword, "synthetic-model-test-password"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv(InitialAdminApiToken, modelsSuiteAPIKey); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Unsetenv(InitialAdminPassword)
		_ = os.Unsetenv(InitialAdminApiToken)
	}()
	check.TestingT(t)
}

type ModelsSuite struct {
	config *config.Config
}

var _ = check.Suite(&ModelsSuite{})

func (s *ModelsSuite) SetUpSuite(c *check.C) {
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         ":memory:",
		MigrationsPath: "../db/db_sqlite3/migrations/",
	}
	s.config = conf
	err := Setup(conf)
	if err != nil {
		c.Fatalf("Failed creating database: %v", err)
	}
}

func (s *ModelsSuite) TearDownTest(c *check.C) {
	// Clear database tables between each test. If new tables are
	// used in this test suite they will need to be cleaned up here.
	db.Delete(Group{})
	db.Delete(Target{})
	db.Delete(GroupTarget{})
	db.Delete(SMTP{})
	db.Delete(Page{})
	db.Delete(Result{})
	db.Delete(MailLog{})
	db.Delete(Campaign{})

	// Reset users table to default state.
	db.Not("id", 1).Delete(User{})
	db.Model(User{}).Update("username", "admin")
}

func (s *ModelsSuite) createCampaignDependencies(ch *check.C, optional ...string) Campaign {
	// we use the optional parameter to pass an alternative subject
	group := Group{Name: "Test Group"}
	group.Targets = []Target{
		{BaseRecipient: BaseRecipient{Email: "test1@example.com", FirstName: "First", LastName: "Example"}},
		{BaseRecipient: BaseRecipient{Email: "test2@example.com", FirstName: "Second", LastName: "Example"}},
		{BaseRecipient: BaseRecipient{Email: "test3@example.com", FirstName: "Second", LastName: "Example"}},
		{BaseRecipient: BaseRecipient{Email: "test4@example.com", FirstName: "Second", LastName: "Example"}},
	}
	group.UserId = 1
	ch.Assert(PostGroup(&group), check.Equals, nil)

	// Add a template
	t := Template{Name: "Test Template"}
	if len(optional) > 0 {
		t.Subject = optional[0]
	} else {
		t.Subject = "{{.RId}} - Subject"
	}
	t.Text = "{{.RId}} - Text"
	t.HTML = "{{.RId}} - HTML"
	t.UserId = 1
	ch.Assert(PostTemplate(&t), check.Equals, nil)

	// Add a landing page
	p := Page{Name: "Test Page"}
	p.HTML = "<html>Test</html>"
	p.UserId = 1
	ch.Assert(PostPage(&p), check.Equals, nil)

	// Add a sending profile
	smtp := SMTP{Name: "Test Page"}
	smtp.UserId = 1
	smtp.Host = "example.com"
	smtp.FromAddress = "test@test.com"
	ch.Assert(PostSMTP(&smtp), check.Equals, nil)

	c := Campaign{Name: "Test campaign"}
	c.UserId = 1
	c.Template = t
	c.Page = p
	c.SMTP = smtp
	c.Groups = []Group{group}
	return c
}

func (s *ModelsSuite) createCampaign(ch *check.C) Campaign {
	c := s.createCampaignDependencies(ch)
	// Setup and "launch" our campaign
	ch.Assert(PostCampaign(&c, c.UserId), check.Equals, nil)

	// For comparing the dates, we need to fetch the campaign again. This is
	// to solve an issue where the campaign object right now has time down to
	// the microsecond, while in MySQL it's rounded down to the second.
	c, _ = GetCampaign(c.Id, c.UserId)
	return c
}

func setupBenchmark(b *testing.B) {
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         ":memory:",
		MigrationsPath: "../db/db_sqlite3/migrations/",
	}
	err := Setup(conf)
	if err != nil {
		b.Fatalf("Failed creating database: %v", err)
	}
}

func tearDownBenchmark(b *testing.B) {
	err := db.Close()
	if err != nil {
		b.Fatalf("error closing database: %v", err)
	}
}

func resetBenchmark(b *testing.B) {
	db.Delete(Group{})
	db.Delete(Target{})
	db.Delete(GroupTarget{})
	db.Delete(SMTP{})
	db.Delete(Page{})
	db.Delete(Result{})
	db.Delete(MailLog{})
	db.Delete(Campaign{})

	// Reset users table to default state.
	db.Not("id", 1).Delete(User{})
	db.Model(User{}).Update("username", "admin")
}
