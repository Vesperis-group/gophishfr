package models

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/jinzhu/gorm"
)

const (
	webhookCredentialKind   = "webhook-secret"
	webhookCredentialTable  = "webhooks"
	webhookCredentialColumn = "secret"

	// The legacy `secret varchar(255)` column is a MySQL *character* limit,
	// not a byte limit. Unlike an SMTP password (which this repository binds
	// to a 255-*byte* limit because it is effectively ASCII connection
	// credential material), a webhook secret is arbitrary caller-chosen
	// authentication material and must accept any 255-character valid-Unicode
	// string the legacy schema could store, including multibyte characters.
	// webhookSecretMaxRunes is therefore a rune (Unicode code point) count,
	// matching MySQL's character semantics for VARCHAR(255) regardless of the
	// column's character set, and webhookSecretMaxBytes is the conservative,
	// worst-case (every rune encoded as the maximum 4 UTF-8 bytes) plaintext
	// byte length used only to size the envelope bound below.
	webhookSecretMaxRunes = 255
	webhookSecretMaxBytes = webhookSecretMaxRunes * utf8.UTFMax

	// Byte limits are deliberately independent of database character-count
	// semantics. Keep this conservative derivation aligned with the immutable
	// v1 envelope format: magic, separators, version, max key ID, nonce,
	// ciphertext, and GCM tag. See smtpPasswordEnvelopeMaxBytes for the same
	// derivation applied to SMTP's smaller, byte-bounded plaintext; this is
	// intentionally not reused as-is because the plaintext bound differs.
	webhookSecretEnvelopeMaxBytes = len("gophishfr-cred") + 4 + len("v1") + 64 +
		((12 + 2) / 3 * 4) +
		((webhookSecretMaxBytes + 16 + 2) / 3 * 4)
	webhookSecretCiphertextColumnBytes = 2048
	_                                  = uint(webhookSecretCiphertextColumnBytes - webhookSecretEnvelopeMaxBytes)
)

var (
	ErrWebhookCredentialKeyringRequired = errors.New("webhook credential keyring is required")
	ErrWebhookCredentialUnavailable     = errors.New("webhook credential is unavailable")
	ErrWebhookCredentialInvalidState    = errors.New("webhook credential columns are in an invalid state")
	ErrWebhookCredentialNotMigrated     = errors.New("webhook credential plaintext migration is required")
	ErrInvalidWebhookCredentialIdentity = errors.New("webhook credential has an invalid record ID")
	ErrUnsupportedWebhookCredentialDB   = errors.New("webhook credential migration is unsupported for this database")
	ErrWebhookCredentialTooLong         = errors.New("webhook secret exceeds the storage-safe limit")
	ErrWebhookCredentialInvalidEncoding = errors.New("webhook secret is not valid UTF-8")
	ErrWebhookCredentialStorageMismatch = errors.New("webhook credential storage verification failed")
)

// WebhookCredentialMigrationResult reports non-secret row counts for an
// offline migration or rollback.
type WebhookCredentialMigrationResult struct {
	Updated   int
	Unchanged int
}

// webhookCredentialCipher is the shared application credential cipher used by
// ActiveWebhookEndpoints (via AddEvent) to decrypt webhook secrets
// immediately before HMAC signing. It is set exactly once at startup, from
// the same immutable cipher already used for IMAP and SMTP -- see
// SetWebhookCredentialCipher and main.go's loadCredentialCipher. Every other
// caller (the API server, the offline migration/rollback CLI actions)
// receives its cipher explicitly as a parameter instead of reading this
// variable, so that only the one delivery boundary that has no request- or
// call-scoped cipher available depends on package-level state.
var webhookCredentialCipher *credentials.Cipher

// SetWebhookCredentialCipher installs the shared application credential
// cipher used at the webhook delivery boundary. It does not create a second
// keyring, parser, or environment variable: callers must pass the exact
// cipher loaded from GOPHISHFR_CREDENTIAL_KEYRING_FILE that IMAP and SMTP
// already use.
func SetWebhookCredentialCipher(cipher *credentials.Cipher) {
	webhookCredentialCipher = cipher
}

// webhookCredentialCipherForDelivery returns the cipher installed by
// SetWebhookCredentialCipher, for use only by the campaign delivery path.
func webhookCredentialCipherForDelivery() *credentials.Cipher {
	return webhookCredentialCipher
}

// webhookCredentialContext returns the exact, stable AAD for a webhook
// secret: Kind identifies the credential family, Table/Column pin it to
// webhooks.secret, OwnerID is empty because webhooks are a global resource
// with no owner, and RecordID is the webhook's immutable database-generated
// ID. Every other field -- name, URL, is_active, event selection, timestamps,
// HTTP status/headers, and application/build version -- is mutable and
// therefore excluded: copying ciphertext to a different webhook ID, or
// mutating any of those fields, must never change what authenticates.
func webhookCredentialContext(recordID int64) credentials.Context {
	return credentials.Context{
		Kind:     webhookCredentialKind,
		Table:    webhookCredentialTable,
		Column:   webhookCredentialColumn,
		RecordID: strconv.FormatInt(recordID, 10),
	}
}

// ValidateWebhookSecret applies the same valid-Unicode, 255-character limit
// (matching the legacy MySQL VARCHAR(255) contract) to every webhook secret
// entry point: create, replace, and offline migration/rollback.
func ValidateWebhookSecret(secret string) error {
	if !utf8.ValidString(secret) {
		return ErrWebhookCredentialInvalidEncoding
	}
	if utf8.RuneCountInString(secret) > webhookSecretMaxRunes {
		return ErrWebhookCredentialTooLong
	}
	return nil
}

func validateWebhookCiphertext(ciphertext string) error {
	if !utf8.ValidString(ciphertext) ||
		len(ciphertext) > webhookSecretEnvelopeMaxBytes ||
		len(ciphertext) > webhookSecretCiphertextColumnBytes {
		return ErrWebhookCredentialInvalidState
	}
	return nil
}

func encryptWebhookSecret(credentialCipher *credentials.Cipher, recordID int64, secret string) (string, error) {
	if err := ValidateWebhookSecret(secret); err != nil {
		return "", err
	}
	if credentialCipher == nil {
		return "", ErrWebhookCredentialKeyringRequired
	}
	if recordID <= 0 {
		return "", ErrInvalidWebhookCredentialIdentity
	}
	if secret == "" {
		return "", ErrWebhookCredentialInvalidState
	}
	envelope, err := credentialCipher.Encrypt(
		webhookCredentialContext(recordID),
		[]byte(secret),
	)
	if err != nil {
		return "", ErrWebhookCredentialUnavailable
	}
	verified, err := credentialCipher.Decrypt(
		webhookCredentialContext(recordID),
		envelope,
	)
	if err != nil || !bytes.Equal(verified, []byte(secret)) {
		return "", ErrWebhookCredentialUnavailable
	}
	ciphertext := string(envelope)
	if err := validateWebhookCiphertext(ciphertext); err != nil {
		return "", err
	}
	return ciphertext, nil
}

func validateWebhookSecretColumns(wh Webhook) error {
	switch {
	case wh.Secret != "" && wh.SecretCiphertext != "":
		return ErrWebhookCredentialInvalidState
	case wh.Secret != "":
		if err := ValidateWebhookSecret(wh.Secret); err != nil {
			return err
		}
		return ErrWebhookCredentialNotMigrated
	case wh.SecretCiphertext != "":
		return validateWebhookCiphertext(wh.SecretCiphertext)
	default:
		return nil
	}
}

// DecryptWebhookSecret returns an empty secret (and no error) for a webhook
// with no stored ciphertext: that is the legacy, still-valid "no-secret"
// webhook state, not a failure. It never treats the legacy plaintext column
// as a runtime fallback -- a legacy non-empty secret without a matching
// ciphertext means the offline migration has not been run yet, which fails
// closed as ErrWebhookCredentialNotMigrated rather than silently signing with
// the plaintext.
func DecryptWebhookSecret(wh Webhook, credentialCipher *credentials.Cipher) (string, error) {
	if err := validateWebhookSecretColumns(wh); err != nil {
		return "", err
	}
	if wh.SecretCiphertext == "" {
		return "", nil
	}
	if wh.Id <= 0 {
		return "", ErrInvalidWebhookCredentialIdentity
	}
	if credentialCipher == nil {
		return "", ErrWebhookCredentialKeyringRequired
	}
	if err := validateWebhookCiphertext(wh.SecretCiphertext); err != nil {
		return "", err
	}
	plaintext, err := credentialCipher.Decrypt(
		webhookCredentialContext(wh.Id),
		credentials.Envelope(wh.SecretCiphertext),
	)
	if err != nil || len(plaintext) == 0 {
		return "", ErrWebhookCredentialUnavailable
	}
	secret := string(plaintext)
	if err := ValidateWebhookSecret(secret); err != nil {
		return "", err
	}
	return secret, nil
}

// ValidateWebhookCredentialBackend rejects PostgreSQL and every backend for
// which this repository has no real webhook credential schema.
func ValidateWebhookCredentialBackend(databaseName string) error {
	switch databaseName {
	case "sqlite3", "mysql":
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedWebhookCredentialDB, databaseName)
	}
}

type storedWebhookCredential struct {
	ID               int64  `gorm:"column:id"`
	Secret           string `gorm:"column:secret"`
	SecretCiphertext string `gorm:"column:secret_ciphertext"`
}

func readWebhookCredentialStorage(transaction *gorm.DB, id int64) (storedWebhookCredential, error) {
	var stored storedWebhookCredential
	if err := transaction.Raw(`
		SELECT
			id,
			COALESCE(secret, '') AS secret,
			COALESCE(secret_ciphertext, '') AS secret_ciphertext
		FROM webhooks
		WHERE id = ?
	`, id).Scan(&stored).Error; err != nil {
		return storedWebhookCredential{}, err
	}
	if stored.ID != id {
		return storedWebhookCredential{}, gorm.ErrRecordNotFound
	}
	return stored, nil
}

func verifyWebhookCredentialStorage(
	transaction *gorm.DB,
	id int64,
	secret, ciphertext string,
) error {
	stored, err := readWebhookCredentialStorage(transaction, id)
	if err != nil {
		return err
	}
	if stored.Secret != secret || stored.SecretCiphertext != ciphertext {
		return ErrWebhookCredentialStorageMismatch
	}
	return nil
}

// storedWebhookRow captures every persisted webhooks column that PostWebhook
// and PutWebhook write: the tri-state secret pair plus every mutable
// metadata field (name, URL, active state). Secret deliberately remains a
// sql.NullString here rather than being normalized with COALESCE: runtime
// writes always set a concrete empty string, and their final verification must
// prove that exact state was persisted. Offline migration/rollback reads use
// storedWebhookCredential instead, where NULL and empty are intentionally
// normalized because both are valid legacy-empty source states.
type storedWebhookRow struct {
	ID               int64          `gorm:"column:id"`
	Name             string         `gorm:"column:name"`
	URL              string         `gorm:"column:url"`
	IsActive         bool           `gorm:"column:is_active"`
	Secret           sql.NullString `gorm:"column:secret"`
	SecretCiphertext string         `gorm:"column:secret_ciphertext"`
}

func readWebhookRowStorage(transaction *gorm.DB, id int64) (storedWebhookRow, error) {
	var stored storedWebhookRow
	if err := transaction.Raw(`
		SELECT
			id,
			name,
			url,
			is_active,
			secret,
			COALESCE(secret_ciphertext, '') AS secret_ciphertext
		FROM webhooks
		WHERE id = ?
	`, id).Scan(&stored).Error; err != nil {
		return storedWebhookRow{}, err
	}
	if stored.ID != id {
		return storedWebhookRow{}, gorm.ErrRecordNotFound
	}
	return stored, nil
}

// verifyWebhookRowStorage re-reads want.ID inside transaction and fails
// closed with ErrWebhookCredentialStorageMismatch unless every column --
// name, URL, active state, legacy secret (including NULL-ness), and ciphertext
// -- exactly matches want. Checking only the secret columns is not enough: a
// guarded UPDATE whose WHERE clause silently matched zero rows (for example
// because a legacy NULL secret escaped a predicate that literally compared
// secret to an empty string) still leaves the old secret state in place, which
// trivially still equals what a preserve/no-op intended, so a secret-only check
// cannot tell that an intended metadata change -- such as deactivating a
// webhook -- was never actually written. Comparing every column this package
// can write is what distinguishes that silent failure from a genuine matched
// no-op, including MySQL's default "changed rows" RowsAffected semantics,
// where zero rows reported changed can still mean the WHERE clause matched and
// every column already held the intended value.
func verifyWebhookRowStorage(transaction *gorm.DB, want storedWebhookRow) error {
	stored, err := readWebhookRowStorage(transaction, want.ID)
	if err != nil {
		return err
	}
	if stored != want {
		return ErrWebhookCredentialStorageMismatch
	}
	return nil
}

// MigrateWebhookSecrets performs the explicit offline plaintext-to-ciphertext
// data migration. Schema migration remains a separate Goose operation.
func MigrateWebhookSecrets(credentialCipher *credentials.Cipher) (WebhookCredentialMigrationResult, error) {
	return transformWebhookSecrets(credentialCipher, false)
}

// RollbackWebhookSecrets performs the explicit offline ciphertext-to-plaintext
// data rollback. It does not invoke Goose Down.
func RollbackWebhookSecrets(credentialCipher *credentials.Cipher) (WebhookCredentialMigrationResult, error) {
	return transformWebhookSecrets(credentialCipher, true)
}

func transformWebhookSecrets(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (WebhookCredentialMigrationResult, error) {
	result := WebhookCredentialMigrationResult{}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateWebhookCredentialBackend(conf.DBName); err != nil {
		return result, err
	}
	if credentialCipher == nil {
		return result, ErrWebhookCredentialKeyringRequired
	}

	transaction := db.Begin()
	if transaction.Error != nil {
		return result, transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	rows := []storedWebhookCredential{}
	if err := transaction.Raw(`
		SELECT
			id,
			COALESCE(secret, '') AS secret,
			COALESCE(secret_ciphertext, '') AS secret_ciphertext
		FROM webhooks
		ORDER BY id
	`).Scan(&rows).Error; err != nil {
		return result, fmt.Errorf("validate webhook credential schema: %w", err)
	}
	if err := preflightWebhookCredentials(credentialCipher, rows); err != nil {
		return result, err
	}

	for _, row := range rows {
		if rollback {
			if err := rollbackWebhookCredentialRow(transaction, credentialCipher, row, &result); err != nil {
				return WebhookCredentialMigrationResult{}, err
			}
			continue
		}
		if err := migrateWebhookCredentialRow(transaction, credentialCipher, row, &result); err != nil {
			return WebhookCredentialMigrationResult{}, err
		}
	}

	if err := transaction.Commit().Error; err != nil {
		return WebhookCredentialMigrationResult{}, err
	}
	committed = true
	return result, nil
}

func preflightWebhookCredentials(credentialCipher *credentials.Cipher, rows []storedWebhookCredential) error {
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if row.ID <= 0 {
			return fmt.Errorf("%w for webhook %d", ErrInvalidWebhookCredentialIdentity, row.ID)
		}
		if _, duplicate := seen[row.ID]; duplicate {
			return fmt.Errorf("%w for webhook %d", ErrInvalidWebhookCredentialIdentity, row.ID)
		}
		seen[row.ID] = struct{}{}
		if row.Secret != "" && row.SecretCiphertext != "" {
			return fmt.Errorf("%w for webhook %d", ErrWebhookCredentialInvalidState, row.ID)
		}
		if row.Secret != "" {
			if err := ValidateWebhookSecret(row.Secret); err != nil {
				return fmt.Errorf("%w for webhook %d", err, row.ID)
			}
		}
		if row.SecretCiphertext != "" {
			if err := validateWebhookCiphertext(row.SecretCiphertext); err != nil {
				return fmt.Errorf("%w for webhook %d", err, row.ID)
			}
			if _, err := DecryptWebhookSecret(Webhook{
				Id:               row.ID,
				SecretCiphertext: row.SecretCiphertext,
			}, credentialCipher); err != nil {
				return fmt.Errorf("%w for webhook %d", err, row.ID)
			}
		}
	}
	return nil
}

func migrateWebhookCredentialRow(
	transaction *gorm.DB,
	credentialCipher *credentials.Cipher,
	row storedWebhookCredential,
	result *WebhookCredentialMigrationResult,
) error {
	if row.Secret == "" {
		result.Unchanged++
		return nil
	}
	envelope, err := encryptWebhookSecret(credentialCipher, row.ID, row.Secret)
	if err != nil {
		return fmt.Errorf("%w for webhook %d", err, row.ID)
	}
	update := transaction.Exec(`
		UPDATE webhooks
		SET secret_ciphertext = ?
		WHERE id = ? AND secret = ? AND secret_ciphertext = ''
	`, envelope, row.ID, row.Secret)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("webhook credential row changed during migration for webhook %d", row.ID)
	}
	if err := verifyWebhookCredentialStorage(
		transaction,
		row.ID,
		row.Secret,
		envelope,
	); err != nil {
		return fmt.Errorf("%w for webhook %d", err, row.ID)
	}
	update = transaction.Exec(`
		UPDATE webhooks
		SET secret = ''
		WHERE id = ? AND secret = ? AND secret_ciphertext = ?
	`, row.ID, row.Secret, envelope)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("webhook credential row changed during migration for webhook %d", row.ID)
	}
	if err := verifyWebhookCredentialStorage(
		transaction,
		row.ID,
		"",
		envelope,
	); err != nil {
		return fmt.Errorf("%w for webhook %d", err, row.ID)
	}
	result.Updated++
	return nil
}

func rollbackWebhookCredentialRow(
	transaction *gorm.DB,
	credentialCipher *credentials.Cipher,
	row storedWebhookCredential,
	result *WebhookCredentialMigrationResult,
) error {
	if row.SecretCiphertext == "" {
		result.Unchanged++
		return nil
	}
	plaintext, err := DecryptWebhookSecret(Webhook{
		Id:               row.ID,
		SecretCiphertext: row.SecretCiphertext,
	}, credentialCipher)
	if err != nil {
		return fmt.Errorf("%w for webhook %d", err, row.ID)
	}
	update := transaction.Exec(`
		UPDATE webhooks
		SET secret = ?
		WHERE id = ? AND (secret IS NULL OR secret = '') AND secret_ciphertext = ?
	`, plaintext, row.ID, row.SecretCiphertext)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("webhook credential row changed during rollback for webhook %d", row.ID)
	}
	if err := verifyWebhookCredentialStorage(
		transaction,
		row.ID,
		plaintext,
		row.SecretCiphertext,
	); err != nil {
		return fmt.Errorf("%w for webhook %d", err, row.ID)
	}
	update = transaction.Exec(`
		UPDATE webhooks
		SET secret_ciphertext = ''
		WHERE id = ? AND secret = ? AND secret_ciphertext = ?
	`, row.ID, plaintext, row.SecretCiphertext)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("webhook credential row changed during rollback for webhook %d", row.ID)
	}
	if err := verifyWebhookCredentialStorage(
		transaction,
		row.ID,
		plaintext,
		"",
	); err != nil {
		return fmt.Errorf("%w for webhook %d", err, row.ID)
	}
	result.Updated++
	return nil
}
