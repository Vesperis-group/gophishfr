package models

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/webhook"
)

// expectedSignature independently recomputes the documented HMAC-SHA256
// wire format (see webhook.sign) over the exact bytes a server received, to
// cross-check delivery without depending on the webhook package's internals.
func expectedSignature(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write(body); err != nil {
		t.Fatalf("compute expected signature: %v", err)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

type deliveryCapture struct {
	hits      int32
	signature string
	body      []byte
}

func newCaptureServer(t *testing.T, capture *deliveryCapture) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature := r.Header.Get(webhook.SignatureHeader)
		body, err := readAll(r)
		if err != nil {
			t.Errorf("read captured webhook body: %v", err)
		}
		// The plain fields are written before the atomic release-store on
		// hits, and every reader only ever inspects them after an atomic
		// load observes that store: that ordering (not the atomic op alone)
		// is what makes the plain writes visible across goroutines.
		capture.signature = signature
		capture.body = body
		atomic.AddInt32(&capture.hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server
}

func readAll(r *http.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

// TestActiveWebhookEndpointsMigratedSignatureMatchesLegacy proves a migrated
// (encrypted-at-rest) secret produces exactly the same HMAC-SHA256 signature
// as legacy plaintext would for the same secret and payload.
func TestActiveWebhookEndpointsMigratedSignatureMatchesLegacy(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x41}, 32),
	})
	capture := &deliveryCapture{}
	server := newCaptureServer(t, capture)

	wh := validModelWebhook()
	wh.URL = server.URL
	wh.IsActive = true
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("create encrypted active webhook: %v", err)
	}

	endpoints, err := ActiveWebhookEndpoints(cipher)
	if err != nil {
		t.Fatalf("ActiveWebhookEndpoints: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].Secret != testWebhookSecret {
		t.Fatalf("endpoints = %+v, want exactly one decrypted to %q", endpoints, testWebhookSecret)
	}

	payload := map[string]string{"event": "signature-regression"}
	if err := webhook.Send(endpoints[0], payload); err != nil {
		t.Fatalf("send webhook: %v", err)
	}
	if atomic.LoadInt32(&capture.hits) != 1 {
		t.Fatalf("server hits = %d, want 1", capture.hits)
	}
	want := expectedSignature(t, testWebhookSecret, capture.body)
	got := capture.signature[len(webhook.Sha256Prefix)+1:]
	if got != want {
		t.Fatalf("migrated-secret signature = %s, want %s (legacy-equivalent)", got, want)
	}
}

// TestActiveWebhookEndpointsNoSecretPreservesEmptyKeySignature proves a
// no-secret webhook is delivered with the unchanged historical behavior: no
// decrypt is attempted, and the signature header is still present, computed
// with an empty HMAC key exactly as before this goal.
func TestActiveWebhookEndpointsNoSecretPreservesEmptyKeySignature(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x42}, 32),
	})
	capture := &deliveryCapture{}
	server := newCaptureServer(t, capture)

	wh := validModelWebhook()
	wh.URL = server.URL
	wh.IsActive = true
	if err := PostWebhook(&wh, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("create no-secret active webhook: %v", err)
	}

	endpoints, err := ActiveWebhookEndpoints(cipher)
	if err != nil {
		t.Fatalf("ActiveWebhookEndpoints: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].Secret != "" {
		t.Fatalf("endpoints = %+v, want exactly one with an empty secret", endpoints)
	}

	payload := map[string]string{"event": "no-secret"}
	if err := webhook.Send(endpoints[0], payload); err != nil {
		t.Fatalf("send webhook: %v", err)
	}
	if capture.signature == "" {
		t.Fatal("no-secret webhook delivery omitted the signature header")
	}
	want := expectedSignature(t, "", capture.body)
	got := capture.signature[len(webhook.Sha256Prefix)+1:]
	if got != want {
		t.Fatalf("no-secret signature = %s, want %s (empty-key HMAC)", got, want)
	}
}

// TestActiveWebhookEndpointsExcludeCredentialFailures is the central
// zero-outbound gate: a webhook whose ciphertext cannot be authenticated
// (wrong/unknown key or tampered ciphertext) must never be handed to the
// webhook package, must never receive an HTTP request, and must never block
// another active webhook's correctly signed delivery.
func TestActiveWebhookEndpointsExcludeCredentialFailures(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x43}, 32),
	})
	unknownKeyCipher := testCredentialCipher(t, "webhook-key-unknown", map[string][]byte{
		"webhook-key-unknown": bytes.Repeat([]byte{0x44}, 32),
	})

	goodCapture := &deliveryCapture{}
	goodServer := newCaptureServer(t, goodCapture)
	wrongKeyCapture := &deliveryCapture{}
	wrongKeyServer := newCaptureServer(t, wrongKeyCapture)
	tamperedCapture := &deliveryCapture{}
	tamperedServer := newCaptureServer(t, tamperedCapture)

	good := validModelWebhook()
	good.URL = goodServer.URL
	good.IsActive = true
	if err := PostWebhook(&good, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("create good webhook: %v", err)
	}

	// A ciphertext encrypted under a key the real keyring does not contain
	// simulates a missing/rotated-away key.
	wrongKey := validModelWebhook()
	wrongKey.URL = wrongKeyServer.URL
	wrongKey.IsActive = true
	if err := PostWebhook(&wrongKey, WebhookSecretReplace, testWebhookReplacement, unknownKeyCipher); err != nil {
		t.Fatalf("create wrong-key webhook: %v", err)
	}

	tampered := validModelWebhook()
	tampered.URL = tamperedServer.URL
	tampered.IsActive = true
	if err := PostWebhook(&tampered, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("create tampered-source webhook: %v", err)
	}
	if err := db.Exec(
		"UPDATE webhooks SET secret_ciphertext = secret_ciphertext || 'tamper' WHERE id = ?",
		tampered.Id,
	).Error; err != nil {
		t.Fatalf("tamper ciphertext: %v", err)
	}

	endpoints, err := ActiveWebhookEndpoints(cipher)
	if err != nil {
		t.Fatalf("ActiveWebhookEndpoints: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].URL != goodServer.URL {
		t.Fatalf("endpoints = %+v, want exactly the good endpoint (credential failures excluded before any request)", endpoints)
	}

	webhook.SendAll(endpoints, map[string]string{"event": "isolation"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&goodCapture.hits) == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	if atomic.LoadInt32(&goodCapture.hits) != 1 {
		t.Fatalf("good server hits = %d, want 1 (isolation must not block a healthy webhook)", goodCapture.hits)
	}
	if atomic.LoadInt32(&wrongKeyCapture.hits) != 0 {
		t.Fatalf("wrong-key server hits = %d, want 0 (must fail before any HTTP request)", wrongKeyCapture.hits)
	}
	if atomic.LoadInt32(&tamperedCapture.hits) != 0 {
		t.Fatalf("tampered server hits = %d, want 0 (must fail before any HTTP request)", tamperedCapture.hits)
	}
}

// TestAddEventDeliversOnlyDecryptableEndpoints exercises the real campaign
// delivery boundary (AddEvent -> ActiveWebhookEndpoints -> SendAll) end to
// end, confirming the shared package-level cipher installed by
// SetWebhookCredentialCipher is what AddEvent actually uses.
func TestAddEventDeliversOnlyDecryptableEndpoints(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x45}, 32),
	})
	SetWebhookCredentialCipher(cipher)
	t.Cleanup(func() { SetWebhookCredentialCipher(nil) })

	goodCapture := &deliveryCapture{}
	goodServer := newCaptureServer(t, goodCapture)
	badCapture := &deliveryCapture{}
	badServer := newCaptureServer(t, badCapture)

	good := validModelWebhook()
	good.URL = goodServer.URL
	good.IsActive = true
	if err := PostWebhook(&good, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("create good webhook: %v", err)
	}
	bad := validModelWebhook()
	bad.URL = badServer.URL
	bad.IsActive = true
	if err := PostWebhook(&bad, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("create bad webhook: %v", err)
	}
	if err := db.Exec(
		"UPDATE webhooks SET secret_ciphertext = 'not-a-valid-envelope' WHERE id = ?",
		bad.Id,
	).Error; err != nil {
		t.Fatalf("corrupt ciphertext: %v", err)
	}

	event := &Event{Message: "Campaign event"}
	if err := AddEvent(event, 1); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if event.Id == 0 {
		t.Fatal("AddEvent did not persist the event")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&goodCapture.hits) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&goodCapture.hits) != 1 {
		t.Fatalf("good server hits = %d, want 1", goodCapture.hits)
	}
	if atomic.LoadInt32(&badCapture.hits) != 0 {
		t.Fatalf("bad server hits = %d, want 0", badCapture.hits)
	}

	var sentPayload map[string]interface{}
	if err := json.Unmarshal(goodCapture.body, &sentPayload); err != nil {
		t.Fatalf("unmarshal delivered event payload: %v", err)
	}
	if sentPayload["message"] != "Campaign event" {
		t.Fatalf("delivered payload = %+v, want the campaign event", sentPayload)
	}
}
