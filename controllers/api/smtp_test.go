package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/Vesperis-group/gophishfr/models"
)

const (
	storedSMTPPassword      = "synthetic-stored-smtp-password"
	replacementSMTPPassword = "synthetic-replacement-smtp-password"
)

func validSMTPPayload(name string, password interface{}) map[string]interface{} {
	payload := map[string]interface{}{
		"from_address":       "sender@example.test",
		"headers":            []map[string]string{{"key": "X-Synthetic", "value": "api"}},
		"host":               "127.0.0.1:2525",
		"ignore_cert_errors": false,
		"interface_type":     "SMTP",
		"name":               name,
		"username":           "smtp-user",
	}
	if password != nil {
		payload["password"] = password
	}
	return payload
}

func marshalSMTPPayload(t *testing.T, payload map[string]interface{}) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal SMTP payload: %v", err)
	}
	return body
}

func performSMTPRequest(
	t *testing.T,
	testCtx *testContext,
	method, path string,
	body []byte,
	apiKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	}
	response := httptest.NewRecorder()
	testCtx.apiServer.ServeHTTP(response, request)
	return response
}

func assertSMTPResponseSecretFree(t *testing.T, body []byte, ciphertext string) {
	t.Helper()
	for _, forbidden := range []string{
		storedSMTPPassword,
		replacementSMTPPassword,
		ciphertext,
		"password_ciphertext",
		"gophishfr-cred:",
		"api-test-active-key",
		`"password"`,
	} {
		if forbidden != "" && bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("SMTP response exposed credential material %q", forbidden)
		}
	}
}

func TestSMTPAPICredentialLifecycleAndSecrecy(t *testing.T) {
	testCtx := setupTest(t)
	payload := validSMTPPayload("Encrypted SMTP API", storedSMTPPassword)
	response := performSMTPRequest(
		t,
		testCtx,
		http.MethodPost,
		"/api/smtp/",
		marshalSMTPPayload(t, payload),
		testCtx.apiKey,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", response.Code, response.Body.String())
	}

	profiles, err := models.GetSMTPs(testCtx.admin.Id)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("load created SMTP profile: count=%d err=%v", len(profiles), err)
	}
	stored := profiles[0]
	if stored.Password != "" || stored.PasswordCiphertext == "" ||
		strings.Contains(stored.PasswordCiphertext, storedSMTPPassword) {
		t.Fatal("API create did not store only opaque SMTP ciphertext")
	}
	if plaintext, err := models.DecryptSMTPPassword(stored, testCtx.credentialCipher); err != nil || plaintext != storedSMTPPassword {
		t.Fatalf("decrypt API-created SMTP password: matched=%t err=%v", plaintext == storedSMTPPassword, err)
	}
	assertSMTPResponseSecretFree(t, response.Body.Bytes(), stored.PasswordCiphertext)

	for _, path := range []string{"/api/smtp/", fmt.Sprintf("/api/smtp/%d", stored.Id)} {
		response = performSMTPRequest(t, testCtx, http.MethodGet, path, nil, testCtx.apiKey)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
		assertSMTPResponseSecretFree(t, response.Body.Bytes(), stored.PasswordCiphertext)
	}
	group := models.Group{
		Name:   "SMTP response secrecy group",
		UserId: testCtx.admin.Id,
		Targets: []models.Target{{BaseRecipient: models.BaseRecipient{
			Email: "recipient@example.test",
		}}},
	}
	if err := models.PostGroup(&group); err != nil {
		t.Fatalf("create campaign group: %v", err)
	}
	template := models.Template{
		Name: "SMTP response secrecy template", Subject: "Synthetic", Text: "Synthetic", UserId: testCtx.admin.Id,
	}
	if err := models.PostTemplate(&template); err != nil {
		t.Fatalf("create campaign template: %v", err)
	}
	page := models.Page{Name: "SMTP response secrecy page", HTML: "<html></html>", UserId: testCtx.admin.Id}
	if err := models.PostPage(&page); err != nil {
		t.Fatalf("create campaign page: %v", err)
	}
	campaign := models.Campaign{
		Name: "SMTP response secrecy campaign", UserId: testCtx.admin.Id,
		Groups: []models.Group{group}, Template: template, Page: page, SMTP: stored,
		URL: "http://localhost.invalid",
	}
	if err := models.PostCampaign(&campaign, testCtx.admin.Id); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	for _, path := range []string{"/api/campaigns/", fmt.Sprintf("/api/campaigns/%d", campaign.Id)} {
		response = performSMTPRequest(t, testCtx, http.MethodGet, path, nil, testCtx.apiKey)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body=%s", path, response.Code, response.Body.String())
		}
		assertSMTPResponseSecretFree(t, response.Body.Bytes(), stored.PasswordCiphertext)
	}

	originalCiphertext := stored.PasswordCiphertext
	payload["id"] = stored.Id
	payload["name"] = "Preserved SMTP API"
	payload["from_address"] = "updated@example.test"
	payload["headers"] = []map[string]string{{"key": "X-Synthetic", "value": "preserved"}}
	payload["password"] = ""
	response = performSMTPRequest(
		t,
		testCtx,
		http.MethodPut,
		fmt.Sprintf("/api/smtp/%d", stored.Id),
		marshalSMTPPayload(t, payload),
		testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("preserve status = %d, body=%s", response.Code, response.Body.String())
	}
	preserved, err := models.GetSMTP(stored.Id, testCtx.admin.Id)
	if err != nil {
		t.Fatalf("load preserved SMTP profile: %v", err)
	}
	if preserved.PasswordCiphertext != originalCiphertext {
		t.Fatal("empty API update did not preserve ciphertext byte-for-byte")
	}
	assertSMTPResponseSecretFree(t, response.Body.Bytes(), preserved.PasswordCiphertext)

	delete(payload, "password")
	response = performSMTPRequest(
		t,
		testCtx,
		http.MethodPut,
		fmt.Sprintf("/api/smtp/%d", stored.Id),
		marshalSMTPPayload(t, payload),
		testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("absent-password update status = %d", response.Code)
	}
	absentPreserved, _ := models.GetSMTP(stored.Id, testCtx.admin.Id)
	if absentPreserved.PasswordCiphertext != originalCiphertext {
		t.Fatal("absent API update did not preserve ciphertext byte-for-byte")
	}

	for name, change := range map[string]interface{}{
		"host":               "localhost:2526",
		"username":           "redirected-user",
		"ignore_cert_errors": true,
		"interface_type":     "SMTPS",
	} {
		redirect := make(map[string]interface{}, len(payload)+1)
		for key, value := range payload {
			redirect[key] = value
		}
		redirect[name] = change
		redirect["password"] = ""
		response = performSMTPRequest(
			t,
			testCtx,
			http.MethodPut,
			fmt.Sprintf("/api/smtp/%d", stored.Id),
			marshalSMTPPayload(t, redirect),
			testCtx.apiKey,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s redirect status = %d, want 400; body=%s", name, response.Code, response.Body.String())
		}
		unchanged, loadErr := models.GetSMTP(stored.Id, testCtx.admin.Id)
		if loadErr != nil ||
			unchanged.PasswordCiphertext != originalCiphertext ||
			unchanged.Host != absentPreserved.Host ||
			unchanged.Username != absentPreserved.Username ||
			unchanged.IgnoreCertErrors != absentPreserved.IgnoreCertErrors ||
			unchanged.Interface != absentPreserved.Interface {
			t.Fatalf("rejected %s redirect changed the stored profile: err=%v", name, loadErr)
		}
	}

	payload["host"] = "localhost:2526"
	payload["username"] = "rotated-user"
	payload["ignore_cert_errors"] = true
	payload["password"] = replacementSMTPPassword
	response = performSMTPRequest(
		t,
		testCtx,
		http.MethodPut,
		fmt.Sprintf("/api/smtp/%d", stored.Id),
		marshalSMTPPayload(t, payload),
		testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body=%s", response.Code, response.Body.String())
	}
	rotated, _ := models.GetSMTP(stored.Id, testCtx.admin.Id)
	if rotated.PasswordCiphertext == originalCiphertext {
		t.Fatal("non-empty API update did not rotate ciphertext")
	}
	if plaintext, err := models.DecryptSMTPPassword(rotated, testCtx.credentialCipher); err != nil || plaintext != replacementSMTPPassword {
		t.Fatalf("decrypt API-rotated SMTP password: matched=%t err=%v", plaintext == replacementSMTPPassword, err)
	}
	assertSMTPResponseSecretFree(t, response.Body.Bytes(), rotated.PasswordCiphertext)
}

func TestSMTPAPINoAuthNullAndCrossUser(t *testing.T) {
	testCtx := setupTest(t)
	payload := validSMTPPayload("No-auth SMTP API", "")
	payload["username"] = ""
	response := performSMTPRequest(
		t,
		testCtx,
		http.MethodPost,
		"/api/smtp/",
		marshalSMTPPayload(t, payload),
		testCtx.apiKey,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("no-auth create status = %d, body=%s", response.Code, response.Body.String())
	}
	profiles, _ := models.GetSMTPs(testCtx.admin.Id)
	if len(profiles) != 1 || profiles[0].Password != "" || profiles[0].PasswordCiphertext != "" {
		t.Fatal("no-auth API create persisted credential data")
	}
	assertSMTPResponseSecretFree(t, response.Body.Bytes(), "")

	oversizedPassword := strings.Repeat("é", 128)
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		oversizedPayload := validSMTPPayload("Oversized SMTP", oversizedPassword)
		path := "/api/smtp/"
		if method == http.MethodPut {
			oversizedPayload["id"] = profiles[0].Id
			path = fmt.Sprintf("/api/smtp/%d", profiles[0].Id)
		}
		response = performSMTPRequest(
			t,
			testCtx,
			method,
			path,
			marshalSMTPPayload(t, oversizedPayload),
			testCtx.apiKey,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s oversized-password status = %d, want 400", method, response.Code)
		}
	}

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		nullPayload := validSMTPPayload("Null SMTP", "")
		nullPayload["password"] = nil
		path := "/api/smtp/"
		if method == http.MethodPut {
			nullPayload["id"] = profiles[0].Id
			path = fmt.Sprintf("/api/smtp/%d", profiles[0].Id)
		}
		response = performSMTPRequest(t, testCtx, method, path, marshalSMTPPayload(t, nullPayload), testCtx.apiKey)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s null-password status = %d, want 400", method, response.Code)
		}
	}

	otherUser := createUnpriviledgedUser(t, models.RoleUser)
	foreign := validSMTPPayload("Foreign update", replacementSMTPPassword)
	foreign["id"] = profiles[0].Id
	response = performSMTPRequest(
		t,
		testCtx,
		http.MethodPut,
		fmt.Sprintf("/api/smtp/%d", profiles[0].Id),
		marshalSMTPPayload(t, foreign),
		otherUser.ApiKey,
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-user update status = %d, want 404", response.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		response = performSMTPRequest(
			t,
			testCtx,
			method,
			fmt.Sprintf("/api/smtp/%d", profiles[0].Id),
			nil,
			otherUser.ApiKey,
		)
		if response.Code != http.StatusNotFound {
			t.Fatalf("cross-user %s status = %d, want 404", method, response.Code)
		}
	}
	testEmail := map[string]interface{}{
		"email": "recipient@example.test",
		"smtp": map[string]interface{}{
			"id":           profiles[0].Id,
			"name":         "No-auth SMTP API",
			"from_address": "sender@example.test",
			"host":         "127.0.0.1:2525",
			"username":     "",
			"password":     "",
		},
	}
	response = performSMTPRequest(
		t,
		testCtx,
		http.MethodPost,
		"/api/util/send_test_email",
		marshalSMTPPayload(t, testEmail),
		otherUser.ApiKey,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("cross-user test-email status = %d, want 400", response.Code)
	}
	if _, err := models.GetSMTP(profiles[0].Id, testCtx.admin.Id); err != nil {
		t.Fatalf("cross-user requests changed owner profile: %v", err)
	}
}

type smtpCredentialTestWorker struct {
	cipher   *credentials.Cipher
	password string
	request  *models.EmailRequest
}

func (w *smtpCredentialTestWorker) Start() {}

func (w *smtpCredentialTestWorker) LaunchCampaign(models.Campaign) {}

func (w *smtpCredentialTestWorker) SendTestEmail(request *models.EmailRequest) error {
	w.request = request
	dialer, err := request.GetDialer(w.cipher)
	if err != nil {
		return err
	}
	w.password = dialer.(*models.Dialer).Password
	return nil
}

func TestSendTestEmailUsesStoredOrInlineCredentialWithoutEcho(t *testing.T) {
	testCtx := setupTest(t)
	nullBody := map[string]interface{}{
		"email": "recipient@example.test",
		"smtp": map[string]interface{}{
			"name":         "Null password test profile",
			"host":         "127.0.0.1:2525",
			"from_address": "sender@example.test",
			"password":     nil,
		},
	}
	response := performSMTPRequest(t, testCtx, http.MethodPost, "/api/util/send_test_email", marshalSMTPPayload(t, nullBody), testCtx.apiKey)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("test-email null-password status = %d, want 400", response.Code)
	}
	overLimitBody := map[string]interface{}{
		"email": "recipient@example.test",
		"smtp": map[string]interface{}{
			"name":         "Oversized password test profile",
			"host":         "127.0.0.1:2525",
			"from_address": "sender@example.test",
			"password":     strings.Repeat("é", 128),
		},
	}
	response = performSMTPRequest(
		t,
		testCtx,
		http.MethodPost,
		"/api/util/send_test_email",
		marshalSMTPPayload(t, overLimitBody),
		testCtx.apiKey,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("test-email oversized-password status = %d, want 400", response.Code)
	}

	profile := models.SMTP{
		UserId:           testCtx.admin.Id,
		Interface:        "SMTP",
		Name:             "Stored test profile",
		Host:             "127.0.0.1:2525",
		Username:         "smtp-user",
		Password:         storedSMTPPassword,
		FromAddress:      "sender@example.test",
		IgnoreCertErrors: false,
		Headers:          []models.Header{{Key: "X-Stored", Value: "authorized"}},
	}
	if err := models.PostSMTP(&profile, testCtx.credentialCipher); err != nil {
		t.Fatalf("store encrypted SMTP profile: %v", err)
	}
	worker := &smtpCredentialTestWorker{cipher: testCtx.credentialCipher}
	testCtx.apiServer.worker = worker

	body := map[string]interface{}{
		"email": "recipient@example.test",
		"smtp": map[string]interface{}{
			"id":                 profile.Id,
			"interface_type":     "SMTPS",
			"name":               "Attacker-controlled profile",
			"from_address":       "attacker@example.test",
			"host":               "127.0.0.1:65534",
			"username":           "redirected-user",
			"ignore_cert_errors": true,
			"headers": []map[string]string{{
				"key": "X-Redirected", "value": "true",
			}},
			"password": "",
		},
	}
	for _, passwordMode := range []string{"empty", "absent"} {
		if passwordMode == "empty" {
			body["smtp"].(map[string]interface{})["password"] = ""
		} else {
			delete(body["smtp"].(map[string]interface{}), "password")
		}
		response = performSMTPRequest(t, testCtx, http.MethodPost, "/api/util/send_test_email", marshalSMTPPayload(t, body), testCtx.apiKey)
		if response.Code != http.StatusOK {
			t.Fatalf("stored test-email (%s) status = %d, body=%s", passwordMode, response.Code, response.Body.String())
		}
		if worker.password != storedSMTPPassword || worker.request.SMTP.Password != "" {
			t.Fatalf("stored test email (%s) did not decrypt only at the dialer boundary", passwordMode)
		}
		if worker.request.SMTP.Host != profile.Host ||
			worker.request.SMTP.Username != profile.Username ||
			worker.request.SMTP.IgnoreCertErrors != profile.IgnoreCertErrors ||
			worker.request.SMTP.Interface != profile.Interface ||
			worker.request.SMTP.FromAddress != profile.FromAddress ||
			len(worker.request.SMTP.Headers) != 1 ||
			worker.request.SMTP.Headers[0].Key != "X-Stored" {
			t.Fatalf("stored test email (%s) combined ciphertext with request-controlled connection context", passwordMode)
		}
		assertSMTPResponseSecretFree(t, response.Body.Bytes(), profile.PasswordCiphertext)
	}

	body["smtp"].(map[string]interface{})["password"] = replacementSMTPPassword
	response = performSMTPRequest(t, testCtx, http.MethodPost, "/api/util/send_test_email", marshalSMTPPayload(t, body), testCtx.apiKey)
	if response.Code != http.StatusOK {
		t.Fatalf("replacement test-email status = %d, body=%s", response.Code, response.Body.String())
	}
	if worker.password != replacementSMTPPassword ||
		worker.request.SMTP.Host != "127.0.0.1:65534" ||
		worker.request.SMTP.Username != "redirected-user" ||
		!worker.request.SMTP.IgnoreCertErrors ||
		worker.request.SMTP.Password != "" ||
		worker.request.SMTP.PasswordCiphertext != "" {
		t.Fatal("replacement test email did not use only the explicit request context and password")
	}

	body["smtp"].(map[string]interface{})["id"] = 0
	body["smtp"].(map[string]interface{})["name"] = "Inline test profile"
	body["smtp"].(map[string]interface{})["password"] = replacementSMTPPassword
	response = performSMTPRequest(t, testCtx, http.MethodPost, "/api/util/send_test_email", marshalSMTPPayload(t, body), testCtx.apiKey)
	if response.Code != http.StatusOK {
		t.Fatalf("inline test-email status = %d, body=%s", response.Code, response.Body.String())
	}
	if worker.password != replacementSMTPPassword || worker.request.SMTP.Password != "" {
		t.Fatal("inline test email did not keep the supplied password request-only")
	}
	assertSMTPResponseSecretFree(t, response.Body.Bytes(), profile.PasswordCiphertext)
	after, err := models.GetSMTP(profile.Id, testCtx.admin.Id)
	if err != nil {
		t.Fatalf("reload SMTP profile after inline test: %v", err)
	}
	if after.Password != "" || after.PasswordCiphertext != profile.PasswordCiphertext {
		t.Fatal("inline test email persisted or changed credential data")
	}
}
