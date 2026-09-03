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
	storedWebhookSecret      = "synthetic-stored-webhook-secret"
	replacementWebhookSecret = "synthetic-replacement-webhook-secret"
)

func validWebhookPayload(name string, secret interface{}) map[string]interface{} {
	payload := map[string]interface{}{
		"name":      name,
		"url":       "https://webhook.invalid/endpoint",
		"is_active": true,
	}
	if secret != nil {
		payload["secret"] = secret
	}
	return payload
}

func marshalWebhookPayload(t *testing.T, payload map[string]interface{}) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal webhook payload: %v", err)
	}
	return body
}

func performWebhookRequest(
	t *testing.T,
	server *Server,
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
	server.ServeHTTP(response, request)
	return response
}

func assertWebhookResponseSecretFree(t *testing.T, body []byte, ciphertext string) {
	t.Helper()
	for _, forbidden := range []string{
		storedWebhookSecret,
		replacementWebhookSecret,
		ciphertext,
		"secret_ciphertext",
		"gophishfr-cred:",
		"api-test-active-key",
		`"secret"`,
	} {
		if forbidden != "" && bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("webhook response exposed credential material %q in %s", forbidden, body)
		}
	}
}

func TestWebhookAPICredentialLifecycleAndSecrecy(t *testing.T) {
	testCtx := setupTest(t)

	response := performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
		marshalWebhookPayload(t, validWebhookPayload("Encrypted webhook", storedWebhookSecret)),
		testCtx.apiKey,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", response.Code, response.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	stored, err := models.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("load created webhook: %v", err)
	}
	if stored.Secret != "" || stored.SecretCiphertext == "" || strings.Contains(stored.SecretCiphertext, storedWebhookSecret) {
		t.Fatal("API create did not store only opaque webhook ciphertext")
	}
	if plaintext, err := models.DecryptWebhookSecret(stored, testCtx.credentialCipher); err != nil || plaintext != storedWebhookSecret {
		t.Fatalf("decrypt API-created webhook secret: matched=%t err=%v", plaintext == storedWebhookSecret, err)
	}
	assertWebhookResponseSecretFree(t, response.Body.Bytes(), stored.SecretCiphertext)

	for _, path := range []string{"/api/webhooks/", fmt.Sprintf("/api/webhooks/%d", created.ID)} {
		response = performWebhookRequest(t, testCtx.apiServer, http.MethodGet, path, nil, testCtx.apiKey)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
		assertWebhookResponseSecretFree(t, response.Body.Bytes(), stored.SecretCiphertext)
	}

	// Absent "secret" preserves the stored ciphertext byte-for-byte.
	preservePayload := validWebhookPayload("Renamed webhook", nil)
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPut, fmt.Sprintf("/api/webhooks/%d", created.ID),
		marshalWebhookPayload(t, preservePayload), testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("preserve update status = %d, body=%s", response.Code, response.Body.String())
	}
	assertWebhookResponseSecretFree(t, response.Body.Bytes(), stored.SecretCiphertext)
	afterPreserve, err := models.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("load webhook after preserve: %v", err)
	}
	if afterPreserve.SecretCiphertext != stored.SecretCiphertext || afterPreserve.Name != "Renamed webhook" {
		t.Fatal("absent-secret update did not preserve ciphertext or apply the metadata change")
	}

	// Explicit JSON null has the same preserve meaning as an absent field --
	// this is the explicit tri-state resolution for webhooks (unlike SMTP/IMAP,
	// which reject null outright because they have no clear semantics).
	nullPayload := validWebhookPayload("Still renamed webhook", nil)
	nullPayload["secret"] = nil
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPut, fmt.Sprintf("/api/webhooks/%d", created.ID),
		marshalWebhookPayload(t, nullPayload), testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("null-secret update status = %d, body=%s", response.Code, response.Body.String())
	}
	afterNull, err := models.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("load webhook after null update: %v", err)
	}
	if afterNull.SecretCiphertext != stored.SecretCiphertext {
		t.Fatal("JSON-null secret did not preserve ciphertext")
	}

	// A non-empty secret replaces the ciphertext.
	replacePayload := validWebhookPayload("Rotated webhook", replacementWebhookSecret)
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPut, fmt.Sprintf("/api/webhooks/%d", created.ID),
		marshalWebhookPayload(t, replacePayload), testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("replace update status = %d, body=%s", response.Code, response.Body.String())
	}
	assertWebhookResponseSecretFree(t, response.Body.Bytes(), "")
	rotated, err := models.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("load webhook after rotation: %v", err)
	}
	if rotated.SecretCiphertext == afterNull.SecretCiphertext {
		t.Fatal("non-empty secret did not rotate the ciphertext")
	}
	if plaintext, err := models.DecryptWebhookSecret(rotated, testCtx.credentialCipher); err != nil || plaintext != replacementWebhookSecret {
		t.Fatalf("decrypt rotated webhook secret: matched=%t err=%v", plaintext == replacementWebhookSecret, err)
	}

	// An explicitly present empty string clears the secret atomically.
	clearPayload := validWebhookPayload("Cleared webhook", "")
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPut, fmt.Sprintf("/api/webhooks/%d", created.ID),
		marshalWebhookPayload(t, clearPayload), testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("clear update status = %d, body=%s", response.Code, response.Body.String())
	}
	cleared, err := models.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("load webhook after clear: %v", err)
	}
	if cleared.Secret != "" || cleared.SecretCiphertext != "" {
		t.Fatal("explicit empty secret did not clear both secret columns")
	}

	response = performWebhookRequest(t, testCtx.apiServer, http.MethodDelete, fmt.Sprintf("/api/webhooks/%d", created.ID), nil, testCtx.apiKey)
	if response.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", response.Code, response.Body.String())
	}
	if _, err := models.GetWebhook(created.ID); err == nil {
		t.Fatal("webhook still present after delete")
	}
}

func TestWebhookAPICreateWithoutSecret(t *testing.T) {
	testCtx := setupTest(t)
	for _, test := range []struct {
		name   string
		secret interface{}
	}{
		{name: "absent", secret: nil},
		{name: "null", secret: json.RawMessage("null")},
		{name: "empty", secret: ""},
	} {
		payload := validWebhookPayload("No secret "+test.name, nil)
		if test.secret != nil {
			payload["secret"] = test.secret
		}
		response := performWebhookRequest(
			t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
			marshalWebhookPayload(t, payload), testCtx.apiKey,
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("%s: create status = %d, body=%s", test.name, response.Code, response.Body.String())
		}
		var created struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
			t.Fatalf("%s: decode response: %v", test.name, err)
		}
		stored, err := models.GetWebhook(created.ID)
		if err != nil {
			t.Fatalf("%s: load webhook: %v", test.name, err)
		}
		if stored.Secret != "" || stored.SecretCiphertext != "" {
			t.Fatalf("%s: create left a non-empty secret column", test.name)
		}
	}
}

func TestWebhookAPIRejectsInvalidRequests(t *testing.T) {
	testCtx := setupTest(t)

	response := performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
		[]byte("{not json"), testCtx.apiKey,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON status = %d, want 400", response.Code)
	}
	if strings.Contains(response.Body.String(), "not json") {
		t.Fatal("malformed-JSON error echoed the raw request body")
	}

	oversized := validWebhookPayload("Too long", strings.Repeat("a", 256))
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
		marshalWebhookPayload(t, oversized), testCtx.apiKey,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized secret status = %d, want 400, body=%s", response.Code, response.Body.String())
	}

	noURL := map[string]interface{}{"name": "No URL"}
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
		marshalWebhookPayload(t, noURL), testCtx.apiKey,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing URL status = %d, want 400", response.Code)
	}
}

func TestWebhookAPIRequiresModifySystemPermission(t *testing.T) {
	testCtx := setupTest(t)
	response := performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
		marshalWebhookPayload(t, validWebhookPayload("Owner webhook", storedWebhookSecret)),
		testCtx.apiKey,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d, body=%s", response.Code, response.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode seed response: %v", err)
	}

	unprivileged := createUnpriviledgedUser(t, models.RoleUser)
	for _, probe := range []struct {
		method string
		path   string
		body   []byte
	}{
		{method: http.MethodGet, path: "/api/webhooks/"},
		{method: http.MethodGet, path: fmt.Sprintf("/api/webhooks/%d", created.ID)},
		{method: http.MethodPost, path: "/api/webhooks/", body: marshalWebhookPayload(t, validWebhookPayload("Denied", nil))},
		{method: http.MethodPut, path: fmt.Sprintf("/api/webhooks/%d", created.ID), body: marshalWebhookPayload(t, validWebhookPayload("Denied", nil))},
		{method: http.MethodDelete, path: fmt.Sprintf("/api/webhooks/%d", created.ID)},
		{method: http.MethodPost, path: fmt.Sprintf("/api/webhooks/%d/validate", created.ID)},
	} {
		response := performWebhookRequest(t, testCtx.apiServer, probe.method, probe.path, probe.body, unprivileged.ApiKey)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", probe.method, probe.path, response.Code)
		}
	}

	stillStored, err := models.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("load webhook after denied requests: %v", err)
	}
	if plaintext, err := models.DecryptWebhookSecret(stillStored, testCtx.credentialCipher); err != nil || plaintext != storedWebhookSecret {
		t.Fatalf("unprivileged requests changed the webhook secret: matched=%t err=%v", plaintext == storedWebhookSecret, err)
	}
}

func TestWebhookValidateUsesStoredSecretAndSupportsInlineOverride(t *testing.T) {
	testCtx := setupTest(t)

	var captured struct {
		hits      int
		signature string
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.hits++
		captured.signature = r.Header.Get("X-Gophish-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	response := performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
		marshalWebhookPayload(t, map[string]interface{}{
			"name": "Validate target", "url": target.URL, "is_active": true, "secret": storedWebhookSecret,
		}),
		testCtx.apiKey,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", response.Code, response.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// Absent body (what the stock UI's "ping" sends): use the stored secret.
	response = performWebhookRequest(t, testCtx.apiServer, http.MethodPost, fmt.Sprintf("/api/webhooks/%d/validate", created.ID), []byte("{}"), testCtx.apiKey)
	if response.Code != http.StatusOK {
		t.Fatalf("validate (stored) status = %d, body=%s", response.Code, response.Body.String())
	}
	assertWebhookResponseSecretFree(t, response.Body.Bytes(), "")
	if captured.hits != 1 {
		t.Fatalf("validate (stored) hits = %d, want 1", captured.hits)
	}

	// A non-empty inline secret is used only for this request, in memory.
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, fmt.Sprintf("/api/webhooks/%d/validate", created.ID),
		marshalWebhookPayload(t, map[string]interface{}{"secret": replacementWebhookSecret}), testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("validate (inline) status = %d, body=%s", response.Code, response.Body.String())
	}
	inlineSignature := captured.signature
	stillStored, err := models.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("load webhook after inline validate: %v", err)
	}
	if plaintext, err := models.DecryptWebhookSecret(stillStored, testCtx.credentialCipher); err != nil || plaintext != storedWebhookSecret {
		t.Fatalf("inline validate secret leaked into storage: matched=%t err=%v", plaintext == storedWebhookSecret, err)
	}

	// Explicit empty string validates the no-secret (empty-key HMAC) behavior.
	response = performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, fmt.Sprintf("/api/webhooks/%d/validate", created.ID),
		marshalWebhookPayload(t, map[string]interface{}{"secret": ""}), testCtx.apiKey,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("validate (explicit empty) status = %d, body=%s", response.Code, response.Body.String())
	}
	if captured.signature == inlineSignature {
		t.Fatal("explicit-empty validate reused the inline-secret signature")
	}
	if captured.hits != 3 {
		t.Fatalf("validate hits = %d, want 3", captured.hits)
	}
}

func TestWebhookValidateFailsBeforeNetworkOnCredentialFailure(t *testing.T) {
	testCtx := setupTest(t)

	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	response := performWebhookRequest(
		t, testCtx.apiServer, http.MethodPost, "/api/webhooks/",
		marshalWebhookPayload(t, map[string]interface{}{
			"name": "Wrong key target", "url": target.URL, "is_active": true, "secret": storedWebhookSecret,
		}),
		testCtx.apiKey,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", response.Code, response.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// A server sharing the same database but holding a keyring that never
	// contained the encrypting key simulates a missing/rotated-away key. The
	// webhook's stored ciphertext cannot be authenticated, so validation must
	// fail before any outbound HTTP request.
	wrongKeyring, err := credentials.NewKeyring(
		"wrong-active-key",
		map[string][]byte{"wrong-active-key": bytes.Repeat([]byte{0x99}, 32)},
	)
	if err != nil {
		t.Fatalf("create wrong-key keyring: %v", err)
	}
	wrongCipher, err := credentials.New(wrongKeyring)
	if err != nil {
		t.Fatalf("create wrong-key cipher: %v", err)
	}
	wrongKeyServer := NewServer(WithCredentialCipher(wrongCipher))

	response = performWebhookRequest(t, wrongKeyServer, http.MethodPost, fmt.Sprintf("/api/webhooks/%d/validate", created.ID), []byte("{}"), testCtx.apiKey)
	if response.Code == http.StatusOK {
		t.Fatal("validate with an unrecognized key unexpectedly succeeded")
	}
	if hits != 0 {
		t.Fatalf("target server hits = %d, want 0 (must fail before any HTTP request)", hits)
	}
	if strings.Contains(response.Body.String(), storedWebhookSecret) {
		t.Fatal("credential-failure response leaked the stored secret")
	}
}
