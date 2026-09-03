package models

import (
	"bytes"
	"testing"

	"github.com/jinzhu/gorm"
)

// TestWebhookUpdateSecretMatrix freezes the exact tri-state update contract
// confirmed for this goal: absent/null preserves any existing secret
// byte-for-byte, an explicitly present empty string clears it, and a
// non-empty string replaces it -- for both a webhook that already has a
// secret and one that does not.
func TestWebhookUpdateSecretMatrix(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x31}, 32),
	})

	tests := []struct {
		name            string
		existingSecret  string // "" means the webhook starts with no secret
		intent          WebhookSecretIntent
		incomingSecret  string
		wantSecretAfter string // "" means no secret is expected afterward
	}{
		{
			name:            "existing secret, replace",
			existingSecret:  testWebhookSecret,
			intent:          WebhookSecretReplace,
			incomingSecret:  testWebhookReplacement,
			wantSecretAfter: testWebhookReplacement,
		},
		{
			name:            "existing secret, explicit clear",
			existingSecret:  testWebhookSecret,
			intent:          WebhookSecretClear,
			wantSecretAfter: "",
		},
		{
			name:            "existing secret, absent/null preserves",
			existingSecret:  testWebhookSecret,
			intent:          WebhookSecretPreserve,
			wantSecretAfter: testWebhookSecret,
		},
		{
			name:            "no existing secret, replace",
			existingSecret:  "",
			intent:          WebhookSecretReplace,
			incomingSecret:  testWebhookReplacement,
			wantSecretAfter: testWebhookReplacement,
		},
		{
			name:            "no existing secret, explicit clear stays empty",
			existingSecret:  "",
			intent:          WebhookSecretClear,
			wantSecretAfter: "",
		},
		{
			name:            "no existing secret, absent/null stays empty",
			existingSecret:  "",
			intent:          WebhookSecretPreserve,
			wantSecretAfter: "",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			wh := validModelWebhook()
			wh.Name = "Matrix: " + test.name
			seedIntent := WebhookSecretPreserve
			if test.existingSecret != "" {
				seedIntent = WebhookSecretReplace
			}
			if err := PostWebhook(&wh, seedIntent, test.existingSecret, cipher); err != nil {
				t.Fatalf("seed webhook: %v", err)
			}

			update := wh
			if err := PutWebhook(&update, test.intent, test.incomingSecret, cipher); err != nil {
				t.Fatalf("update: %v", err)
			}

			stored := loadOnlyWebhook(t, wh.Id)
			if stored.Secret != "" {
				t.Fatal("legacy secret column left non-empty after update")
			}
			if test.wantSecretAfter == "" {
				if stored.SecretCiphertext != "" {
					t.Fatal("expected no-secret state after update, found ciphertext")
				}
				return
			}
			plaintext, err := DecryptWebhookSecret(stored, cipher)
			if err != nil || plaintext != test.wantSecretAfter {
				t.Fatalf("decrypt after update: matched=%t err=%v", plaintext == test.wantSecretAfter, err)
			}
		})
	}
}

// TestWebhookValidateIgnoresSecret freezes the legacy contract that a
// no-secret webhook is a valid webhook: Validate only requires a name and a
// URL.
func TestWebhookValidateIgnoresSecret(t *testing.T) {
	wh := Webhook{Name: "No secret required", URL: "https://webhook.invalid/x"}
	if err := wh.Validate(); err != nil {
		t.Fatalf("no-secret webhook failed validation: %v", err)
	}
}

func TestGetWebhooksAndActiveWebhooks(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x32}, 32),
	})
	active := validModelWebhook()
	active.Name = "Active webhook"
	active.IsActive = true
	if err := PostWebhook(&active, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("create active webhook: %v", err)
	}
	inactive := validModelWebhook()
	inactive.Name = "Inactive webhook"
	inactive.IsActive = false
	if err := PostWebhook(&inactive, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("create inactive webhook: %v", err)
	}

	all, err := GetWebhooks()
	if err != nil || len(all) != 2 {
		t.Fatalf("GetWebhooks() = %d webhooks, err=%v, want 2", len(all), err)
	}
	activeOnly, err := GetActiveWebhooks()
	if err != nil || len(activeOnly) != 1 || activeOnly[0].Id != active.Id {
		t.Fatalf("GetActiveWebhooks() = %+v, err=%v, want only %d", activeOnly, err, active.Id)
	}
}

func TestDeleteWebhookIsTransactionalAndIdempotentlySafe(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x33}, 32),
	})
	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if err := DeleteWebhook(wh.Id); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if _, err := GetWebhook(wh.Id); err != gorm.ErrRecordNotFound {
		t.Fatalf("post-delete lookup error = %v, want not found", err)
	}
	// Deleting an already-deleted webhook fails closed rather than silently
	// succeeding a second time.
	if err := DeleteWebhook(wh.Id); err != gorm.ErrRecordNotFound {
		t.Fatalf("repeat delete error = %v, want not found", err)
	}
}
