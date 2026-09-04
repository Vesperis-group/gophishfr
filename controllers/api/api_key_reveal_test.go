package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ctx "github.com/Vesperis-group/gophishfr/context"
	"github.com/Vesperis-group/gophishfr/models"
	"github.com/gorilla/mux"
)

func TestAPIKeyResponsesAreSecretFreeExceptImmediateReveal(t *testing.T) {
	testContext := setupTest(t)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/users/", nil)
	listRequest = ctx.Set(listRequest, "user", testContext.admin)
	listResponse := httptest.NewRecorder()
	testContext.apiServer.Users(listResponse, listRequest)
	assertNoAPIKeyMetadata(t, listResponse.Body.Bytes())

	singleRequest := httptest.NewRequest(http.MethodGet, "/api/users/1", nil)
	singleRequest = ctx.Set(singleRequest, "user", testContext.admin)
	singleRequest = mux.SetURLVars(singleRequest, map[string]string{"id": "1"})
	singleResponse := httptest.NewRecorder()
	testContext.apiServer.User(singleResponse, singleRequest)
	assertNoAPIKeyMetadata(t, singleResponse.Body.Bytes())

	payload, err := json.Marshal(userRequest{
		Username: "reveal-once-user", Password: "synthetic-reveal-password",
		Role: models.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	createRequest := httptest.NewRequest(http.MethodPost, "/api/users/", bytes.NewReader(payload))
	createRequest = ctx.Set(createRequest, "user", testContext.admin)
	createResponse := httptest.NewRecorder()
	testContext.apiServer.Users(createResponse, createRequest)
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", createResponse.Code, createResponse.Body.String())
	}
	var created userCreationResponse
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.APIKey == "" || strings.Count(createResponse.Body.String(), created.APIKey) != 1 {
		t.Fatal("creation did not reveal exactly one token value")
	}
	var createShape map[string]json.RawMessage
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createShape); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "username", "role", "password_change_required", "account_locked", "last_login", "api_key"} {
		if _, present := createShape[field]; !present {
			t.Fatalf("legacy create response omitted top-level %q", field)
		}
	}
	if _, nested := createShape["user"]; nested {
		t.Fatal("create response unexpectedly nested legacy user fields")
	}
	stored, err := models.GetUser(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	persistentJSON, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	assertNoAPIKeyMetadata(t, persistentJSON)
	if bytes.Contains(createResponse.Body.Bytes(), stored.APIKeyVerifier) ||
		bytes.Contains(createResponse.Body.Bytes(), []byte(stored.APIKeyVerifierKeyID)) {
		t.Fatal("creation response disclosed verifier metadata")
	}

	resetRequest := httptest.NewRequest(http.MethodPost, "/api/reset", nil)
	resetRequest = ctx.Set(resetRequest, "user", stored)
	resetResponse := httptest.NewRecorder()
	testContext.apiServer.Reset(resetResponse, resetRequest)
	if resetResponse.Code != http.StatusOK {
		t.Fatalf("reset failed: %d %s", resetResponse.Code, resetResponse.Body.String())
	}
	var reset models.Response
	if err := json.Unmarshal(resetResponse.Body.Bytes(), &reset); err != nil {
		t.Fatal(err)
	}
	var resetShape struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resetResponse.Body.Bytes(), &resetShape); err != nil {
		t.Fatal(err)
	}
	var rawResetToken string
	if err := json.Unmarshal(resetShape.Data, &rawResetToken); err != nil {
		t.Fatal("legacy reset data is not a raw JSON string")
	}
	resetToken, ok := reset.Data.(string)
	if !ok {
		t.Fatal("legacy reset data is not a token string")
	}
	if rawResetToken != resetToken || bytes.Contains(resetShape.Data, []byte(`"api_key"`)) {
		t.Fatal("reset response changed its legacy data shape")
	}
	if resetToken == "" || strings.Count(resetResponse.Body.String(), resetToken) != 1 {
		t.Fatal("reset did not reveal exactly one replacement token")
	}
	if _, err := models.GetUserByAPIKey(created.APIKey); !errors.Is(err, models.ErrInvalidAPICredential) {
		t.Fatal("reset response left old token valid")
	}
	if _, err := models.GetUserByAPIKey(resetToken); err != nil {
		t.Fatal("reset response token does not authenticate")
	}
}

func assertNoAPIKeyMetadata(t *testing.T, body []byte) {
	t.Helper()
	for _, forbidden := range [][]byte{
		[]byte(`"api_key"`),
		[]byte("api_key_verifier"),
		[]byte("api_key_verifier_key_id"),
		[]byte("has_key"),
		[]byte("last_four"),
	} {
		if bytes.Contains(body, forbidden) {
			t.Fatalf("ordinary API response disclosed API-key metadata: %s", forbidden)
		}
	}
}
