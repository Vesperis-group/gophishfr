package models

import (
	"errors"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/webhook"
	"github.com/jinzhu/gorm"
)

// Webhook represents the webhook model.
//
// Secret and SecretCiphertext are never serialized directly: every API
// response uses a separate response DTO (see controllers/api/webhook.go) so
// that a future field added here cannot accidentally leak through JSON tags.
// Secret is the legacy plaintext column, retained only so the offline
// migration and rollback actions can read and clear it; a running server
// never reads it as a runtime fallback (see DecryptWebhookSecret).
type Webhook struct {
	Id               int64  `json:"id" gorm:"column:id; primary_key:yes"`
	Name             string `json:"name"`
	URL              string `json:"url"`
	Secret           string `json:"-" gorm:"column:secret"`
	SecretCiphertext string `json:"-" gorm:"column:secret_ciphertext"`
	IsActive         bool   `json:"is_active"`
}

// WebhookSecretIntent describes what a create or update request means for
// webhooks.secret. The zero value, WebhookSecretPreserve, matches an absent
// or JSON-null "secret" field so a caller that forgets to set an intent never
// accidentally clears or rotates a stored secret.
type WebhookSecretIntent int

const (
	// WebhookSecretPreserve leaves any existing secret (or no-secret state)
	// unchanged: on update this is a byte-for-byte ciphertext preserve with no
	// decrypt or re-encrypt, and on create it is simply a no-secret webhook.
	// This is the tri-state result of an absent or JSON-null "secret" field --
	// the shape of an ordinary metadata-only edit.
	WebhookSecretPreserve WebhookSecretIntent = iota
	// WebhookSecretClear atomically removes any existing secret, leaving both
	// the legacy and ciphertext columns empty. This is the tri-state result of
	// an explicitly present empty string, matching the historical
	// clear-on-empty API contract without inventing a new wire signal.
	WebhookSecretClear
	// WebhookSecretReplace encrypts the given non-empty plaintext value under
	// the active key (create) or the webhook's unchanged immutable ID
	// (update). This is the tri-state result of a non-empty "secret" value.
	WebhookSecretReplace
)

// ErrURLNotSpecified indicates there was no URL specified
var ErrURLNotSpecified = errors.New("URL can't be empty")

// ErrNameNotSpecified indicates there was no name specified
var ErrNameNotSpecified = errors.New("Name can't be empty")

// ErrWebhookConcurrentChange indicates that a webhook's stored secret state
// no longer matches what was read earlier in the same transaction, so a
// preserve, clear, or replace could not be safely applied.
var ErrWebhookConcurrentChange = errors.New("webhook changed concurrently")

// GetWebhooks returns the webhooks
func GetWebhooks() ([]Webhook, error) {
	whs := []Webhook{}
	err := db.Find(&whs).Error
	return whs, err
}

// GetActiveWebhooks returns the active webhooks
func GetActiveWebhooks() ([]Webhook, error) {
	whs := []Webhook{}
	err := db.Where("is_active=?", true).Find(&whs).Error
	return whs, err
}

// GetWebhook returns the webhook that the given id corresponds to.
// If no webhook is found, an error is returned.
func GetWebhook(id int64) (Webhook, error) {
	wh := Webhook{}
	err := db.Where("id=?", id).First(&wh).Error
	return wh, err
}

// ActiveWebhookEndpoints returns the delivery endpoints for every active
// webhook, decrypting each stored secret immediately before it is handed to
// the webhook package -- this is the only place a webhook secret exists as
// plaintext outside of a single encrypt/decrypt call, and it is never
// attached back to a persisted, loggable, or serializable Webhook value.
//
// A webhook whose stored credential cannot be authenticated (unknown key,
// missing keyring, tampered/malformed/oversized ciphertext, or an unmigrated
// legacy secret) is excluded from the result and logged once by ID only. It
// is never handed to webhook.Send, so it can never reach http.Client.Do, and
// its failure never prevents another active webhook's endpoint from being
// returned.
func ActiveWebhookEndpoints(credentialCipher *credentials.Cipher) ([]webhook.EndPoint, error) {
	whs, err := GetActiveWebhooks()
	if err != nil {
		return nil, err
	}
	endPoints := make([]webhook.EndPoint, 0, len(whs))
	for _, wh := range whs {
		secret, err := DecryptWebhookSecret(wh, credentialCipher)
		if err != nil {
			log.Errorf("webhook %d credential unavailable, excluding from delivery: %v", wh.Id, err)
			continue
		}
		endPoints = append(endPoints, webhook.EndPoint{URL: wh.URL, Secret: secret})
	}
	return endPoints, nil
}

// PostWebhook creates a new webhook in the database. intent and secret
// describe the incoming tri-state secret value (see WebhookSecretIntent).
// Only WebhookSecretReplace with a non-empty secret creates a stored secret;
// preserve and clear both mean "no secret" for a brand-new row, matching the
// legacy create contract where an empty, absent, or null secret always
// produced a valid no-secret webhook.
//
// A non-empty secret is encrypted only after the row exists and Gorm has
// populated wh.Id with the database-generated autoincrement value, so
// plaintext is never persisted -- not even transiently -- and no encryption
// is ever attempted with a zero record ID.
func PostWebhook(wh *Webhook, intent WebhookSecretIntent, secret string, credentialCipher *credentials.Cipher) error {
	if err := wh.Validate(); err != nil {
		log.Error(err)
		return err
	}
	wantsSecret := intent == WebhookSecretReplace
	if wantsSecret {
		if secret == "" {
			return ErrWebhookCredentialInvalidState
		}
		if err := ValidateWebhookSecret(secret); err != nil {
			return err
		}
	}
	wh.Secret = ""
	wh.SecretCiphertext = ""

	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()
	if err := transaction.Create(wh).Error; err != nil {
		return err
	}
	if wantsSecret {
		ciphertext, err := encryptWebhookSecret(credentialCipher, wh.Id, secret)
		if err != nil {
			return err
		}
		update := transaction.Model(&Webhook{}).
			Where("id = ? AND secret = '' AND secret_ciphertext = ''", wh.Id).
			Update("secret_ciphertext", ciphertext)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrWebhookConcurrentChange
		}
		wh.SecretCiphertext = ciphertext
	}
	if err := verifyWebhookCredentialStorage(
		transaction,
		wh.Id,
		"",
		wh.SecretCiphertext,
	); err != nil {
		return err
	}
	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}

// PutWebhook edits an existing webhook in the database. intent and secret
// describe the incoming tri-state secret value (see WebhookSecretIntent):
//
//   - WebhookSecretPreserve leaves the existing ciphertext byte-for-byte
//     unchanged. It never decrypts or re-encrypts, and a no-secret webhook
//     remains no-secret.
//   - WebhookSecretClear atomically empties both secret columns.
//   - WebhookSecretReplace encrypts the given non-empty secret under the
//     webhook's unchanged immutable ID and replaces any existing ciphertext.
//
// The existing row is read inside the same transaction that performs the
// update, and the update is guarded on the exact secret-column state just
// read: a concurrent preserve, clear, replace, or delete between the read and
// the write causes the guarded update to affect zero rows, which is reported
// as ErrWebhookConcurrentChange rather than silently applied over a state
// that was never validated.
func PutWebhook(wh *Webhook, intent WebhookSecretIntent, secret string, credentialCipher *credentials.Cipher) error {
	if err := wh.Validate(); err != nil {
		log.Error(err)
		return err
	}
	if wh.Id <= 0 {
		return ErrInvalidWebhookCredentialIdentity
	}
	if intent == WebhookSecretReplace {
		if secret == "" {
			return ErrWebhookCredentialInvalidState
		}
		if err := ValidateWebhookSecret(secret); err != nil {
			return err
		}
	}

	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	existing := Webhook{}
	existingQuery := transaction.Where("id = ?", wh.Id)
	if conf != nil && conf.DBName == "mysql" {
		existingQuery = existingQuery.Set("gorm:query_option", "FOR UPDATE")
	}
	if err := existingQuery.First(&existing).Error; err != nil {
		return err
	}
	if err := validateWebhookSecretColumns(existing); err != nil {
		return err
	}

	switch intent {
	case WebhookSecretPreserve:
		wh.SecretCiphertext = existing.SecretCiphertext
	case WebhookSecretClear:
		wh.SecretCiphertext = ""
	case WebhookSecretReplace:
		ciphertext, err := encryptWebhookSecret(credentialCipher, wh.Id, secret)
		if err != nil {
			return err
		}
		wh.SecretCiphertext = ciphertext
	}
	wh.Secret = ""

	// MySQL's default (non-CLIENT_FOUND_ROWS) RowsAffected reports rows that
	// actually *changed* value, not rows that matched the WHERE clause: an
	// ordinary no-op resubmission (preserve intent, no metadata change) can
	// therefore legitimately report 0 even though exactly one row matched.
	// Concurrency safety instead comes from the read-back verification below,
	// which compares the persisted secret columns against what this
	// transaction intended and fails closed on any mismatch; RowsAffected is
	// only used here to catch the impossible ">1" case for a primary-key
	// scoped update.
	update := transaction.Model(&Webhook{}).
		Where(
			"id = ? AND secret = '' AND secret_ciphertext = ?",
			wh.Id,
			existing.SecretCiphertext,
		).
		Updates(map[string]interface{}{
			"name":              wh.Name,
			"url":               wh.URL,
			"secret":            "",
			"secret_ciphertext": wh.SecretCiphertext,
			"is_active":         wh.IsActive,
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected > 1 {
		return ErrWebhookConcurrentChange
	}
	if err := verifyWebhookCredentialStorage(
		transaction,
		wh.Id,
		"",
		wh.SecretCiphertext,
	); err != nil {
		return err
	}
	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}

// DeleteWebhook deletes an existing webhook in the database.
// An error is returned if a webhook with the given id isn't found.
func DeleteWebhook(id int64) error {
	if id <= 0 {
		return ErrInvalidWebhookCredentialIdentity
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()
	deleted := transaction.Where("id = ?", id).Delete(&Webhook{})
	if deleted.Error != nil {
		return deleted.Error
	}
	if deleted.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}

func (wh *Webhook) Validate() error {
	if wh.URL == "" {
		return ErrURLNotSpecified
	}
	if wh.Name == "" {
		return ErrNameNotSpecified
	}
	return nil
}
