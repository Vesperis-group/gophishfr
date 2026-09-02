package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Vesperis-group/gophishfr/models"
)

const (
	storedIMAPPassword      = "synthetic-stored-imap-password"
	replacementIMAPPassword = "synthetic-replacement-imap-password"
)

func validIMAPSettings(password *string) map[string]interface{} {
	settings := map[string]interface{}{
		"enabled":                        false,
		"host":                           "localhost",
		"port":                           "993",
		"username":                       "imap-user",
		"tls":                            true,
		"ignore_cert_errors":             false,
		"folder":                         "INBOX",
		"restrict_domain":                "example.test",
		"delete_reported_campaign_email": false,
		"imap_freq":                      "60",
	}
	if password != nil {
		settings["password"] = *password
	}
	return settings
}

func marshalIMAPSettings(t *testing.T, settings map[string]interface{}) []byte {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("error marshaling IMAP settings: %v", err)
	}
	return body
}

func performIMAPRequest(t *testing.T, testCtx *testContext, method string, body []byte, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/api/imap/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	}
	response := httptest.NewRecorder()
	testCtx.apiServer.ServeHTTP(response, request)
	return response
}

func storeIMAPSettings(t *testing.T, userID int64, username, password string) {
	t.Helper()
	settings := models.IMAP{
		UserId:                      userID,
		Enabled:                     false,
		Host:                        "localhost",
		Port:                        993,
		Username:                    username,
		Password:                    password,
		TLS:                         true,
		IgnoreCertErrors:            true,
		Folder:                      "Reports",
		RestrictDomain:              "example.test",
		DeleteReportedCampaignEmail: true,
		IMAPFreq:                    60,
	}
	if err := models.PostIMAP(&settings, userID); err != nil {
		t.Fatalf("error saving IMAP settings: %v", err)
	}
}

func assertStoredIMAPPassword(t *testing.T, userID int64, expected string) {
	t.Helper()
	settings, err := models.GetIMAP(userID)
	if err != nil {
		t.Fatalf("error loading IMAP settings: %v", err)
	}
	if len(settings) != 1 {
		t.Fatalf("unexpected IMAP settings count: got %d want 1", len(settings))
	}
	if settings[0].Password != expected {
		t.Fatal("stored IMAP password did not match the expected value")
	}
}

func TestIMAPGetOmitsStoredPassword(t *testing.T) {
	testCtx := setupTest(t)
	storeIMAPSettings(t, testCtx.admin.Id, "imap-user", storedIMAPPassword)
	response := performIMAPRequest(t, testCtx, http.MethodGet, nil, testCtx.apiKey)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", response.Code, http.StatusOK)
	}
	responseBody := response.Body.Bytes()
	if bytes.Contains(responseBody, []byte(storedIMAPPassword)) {
		t.Fatal("GET /api/imap/ response body exposed the stored password")
	}

	var settings []map[string]interface{}
	if err := json.Unmarshal(responseBody, &settings); err != nil {
		t.Fatalf("error decoding IMAP settings: %v", err)
	}
	if len(settings) != 1 {
		t.Fatalf("unexpected IMAP settings count: got %d want 1", len(settings))
	}
	if _, exposed := settings[0]["password"]; exposed {
		t.Fatal("GET /api/imap/ exposed the stored password")
	}
	expectedFields := map[string]interface{}{
		"delete_reported_campaign_email": true,
		"enabled":                        false,
		"folder":                         "Reports",
		"host":                           "localhost",
		"ignore_cert_errors":             true,
		"imap_freq":                      "60",
		"port":                           "993",
		"restrict_domain":                "example.test",
		"tls":                            true,
		"username":                       "imap-user",
	}
	for field, expected := range expectedFields {
		if settings[0][field] != expected {
			t.Fatalf("GET /api/imap/ changed or omitted the %q field", field)
		}
	}
	assertStoredIMAPPassword(t, testCtx.admin.Id, storedIMAPPassword)
}

func TestIMAPGetRequiresAuthentication(t *testing.T) {
	testCtx := setupTest(t)
	response := performIMAPRequest(t, testCtx, http.MethodGet, nil, "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status code: got %d want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestIMAPGetIsScopedToAuthenticatedUser(t *testing.T) {
	testCtx := setupTest(t)
	otherUser := createUnpriviledgedUser(t, models.RoleUser)
	storeIMAPSettings(t, testCtx.admin.Id, "admin-imap-user", storedIMAPPassword)
	storeIMAPSettings(t, otherUser.Id, "other-imap-user", replacementIMAPPassword)

	response := performIMAPRequest(t, testCtx, http.MethodGet, nil, testCtx.apiKey)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", response.Code, http.StatusOK)
	}
	var settings []map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil {
		t.Fatalf("error decoding IMAP settings: %v", err)
	}
	if len(settings) != 1 || settings[0]["username"] != "admin-imap-user" {
		t.Fatal("GET /api/imap/ returned another user's settings")
	}
}

func TestIMAPPostPasswordSemantics(t *testing.T) {
	tests := []struct {
		name             string
		existingPassword string
		requestPassword  *string
		expectedStatus   int
		expectedPassword string
	}{
		{
			name:             "empty password preserves existing secret",
			existingPassword: storedIMAPPassword,
			requestPassword:  stringPointer(""),
			expectedStatus:   http.StatusCreated,
			expectedPassword: storedIMAPPassword,
		},
		{
			name:             "absent password preserves existing secret",
			existingPassword: storedIMAPPassword,
			expectedStatus:   http.StatusCreated,
			expectedPassword: storedIMAPPassword,
		},
		{
			name:             "non-empty password replaces existing secret",
			existingPassword: storedIMAPPassword,
			requestPassword:  stringPointer(replacementIMAPPassword),
			expectedStatus:   http.StatusCreated,
			expectedPassword: replacementIMAPPassword,
		},
		{
			name:             "non-empty password creates settings",
			requestPassword:  stringPointer(storedIMAPPassword),
			expectedStatus:   http.StatusCreated,
			expectedPassword: storedIMAPPassword,
		},
		{
			name:            "empty password cannot create settings",
			requestPassword: stringPointer(""),
			expectedStatus:  http.StatusInternalServerError,
		},
		{
			name:           "absent password cannot create settings",
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testCtx := setupTest(t)
			if test.existingPassword != "" {
				storeIMAPSettings(t, testCtx.admin.Id, "imap-user", test.existingPassword)
			}
			body := marshalIMAPSettings(t, validIMAPSettings(test.requestPassword))
			response := performIMAPRequest(t, testCtx, http.MethodPost, body, testCtx.apiKey)
			if response.Code != test.expectedStatus {
				t.Fatalf("unexpected status code: got %d want %d", response.Code, test.expectedStatus)
			}

			settings, err := models.GetIMAP(testCtx.admin.Id)
			if err != nil {
				t.Fatalf("error loading IMAP settings: %v", err)
			}
			if test.expectedPassword == "" {
				if len(settings) != 0 {
					t.Fatal("invalid IMAP settings were persisted")
				}
				return
			}
			assertStoredIMAPPassword(t, testCtx.admin.Id, test.expectedPassword)
		})
	}
}

func TestIMAPPostRejectsNullPassword(t *testing.T) {
	testCtx := setupTest(t)
	storeIMAPSettings(t, testCtx.admin.Id, "imap-user", storedIMAPPassword)
	settings := validIMAPSettings(nil)
	settings["password"] = nil

	response := performIMAPRequest(
		t,
		testCtx,
		http.MethodPost,
		marshalIMAPSettings(t, settings),
		testCtx.apiKey,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: got %d want %d", response.Code, http.StatusBadRequest)
	}
	assertStoredIMAPPassword(t, testCtx.admin.Id, storedIMAPPassword)
}

func TestIMAPPostIgnoresClientUserID(t *testing.T) {
	testCtx := setupTest(t)
	otherUser := createUnpriviledgedUser(t, models.RoleUser)
	storeIMAPSettings(t, otherUser.Id, "other-imap-user", storedIMAPPassword)

	settings := validIMAPSettings(stringPointer(replacementIMAPPassword))
	settings["user_id"] = otherUser.Id
	response := performIMAPRequest(
		t,
		testCtx,
		http.MethodPost,
		marshalIMAPSettings(t, settings),
		testCtx.apiKey,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("unexpected status code: got %d want %d", response.Code, http.StatusCreated)
	}
	assertStoredIMAPPassword(t, testCtx.admin.Id, replacementIMAPPassword)
	assertStoredIMAPPassword(t, otherUser.Id, storedIMAPPassword)
}

func stringPointer(value string) *string {
	return &value
}
