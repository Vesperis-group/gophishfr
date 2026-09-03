package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/apikey"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/Vesperis-group/gophishfr/models"
)

type testContext struct {
	apiKey           string
	config           *config.Config
	apiServer        *Server
	admin            models.User
	credentialCipher *credentials.Cipher
}

func setupTest(t *testing.T) *testContext {
	t.Setenv(models.InitialAdminPassword, "synthetic-api-test-password")
	t.Setenv(models.InitialAdminApiToken, "synthetic-api-suite-token")
	installTestAPIKeyVerifier(t)
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         ":memory:",
		MigrationsPath: "../../db/db_sqlite3/migrations/",
	}
	err := models.Setup(conf)
	if err != nil {
		t.Fatalf("Failed creating database: %v", err)
	}
	ctx := &testContext{}
	ctx.config = conf
	keyring, err := credentials.NewKeyring(
		"api-test-active-key",
		map[string][]byte{"api-test-active-key": bytes.Repeat([]byte{0x41}, 32)},
	)
	if err != nil {
		t.Fatalf("create test credential keyring: %v", err)
	}
	ctx.credentialCipher, err = credentials.New(keyring)
	if err != nil {
		t.Fatalf("create test credential cipher: %v", err)
	}
	// Get the API key to use for these tests
	u, err := models.GetUser(1)
	if err != nil {
		t.Fatalf("error getting admin user: %v", err)
	}
	ctx.apiKey = "synthetic-api-suite-token"
	ctx.admin = u
	ctx.apiServer = NewServer(WithCredentialCipher(ctx.credentialCipher))
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

func TestSiteImportBaseHref(t *testing.T) {
	ctx := setupTest(t)
	h := "<html><head></head><body><img src=\"/test.png\"/></body></html>"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, h)
	}))
	expected := fmt.Sprintf("<html><head><base href=\"%s\"/></head><body><img src=\"/test.png\"/>\n</body></html>", ts.URL)
	defer ts.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/import/site",
		bytes.NewBuffer([]byte(fmt.Sprintf(`
			{
				"url" : "%s",
				"include_resources" : false
			}
		`, ts.URL))))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx.apiServer.ImportSite(response, req)
	cs := cloneResponse{}
	err := json.NewDecoder(response.Body).Decode(&cs)
	if err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if cs.HTML != expected {
		t.Fatalf("unexpected response received. expected %s got %s", expected, cs.HTML)
	}
}
