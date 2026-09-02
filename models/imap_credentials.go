package models

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/jinzhu/gorm"
)

const (
	// #nosec G101 -- This constant is an environment variable name, not a credential.
	IMAPCredentialKeyringEnvironment = "GOPHISHFR_CREDENTIAL_KEYRING_FILE"

	imapCredentialKind   = "imap-password"
	imapCredentialTable  = "imap"
	imapCredentialColumn = "password"
)

var (
	ErrIMAPCredentialKeyringRequired = errors.New("IMAP credential keyring is required")
	ErrIMAPCredentialUnavailable     = errors.New("IMAP credential is unavailable")
	ErrIMAPCredentialInvalidState    = errors.New("IMAP credential columns are in an invalid state")
	ErrIMAPCredentialNotMigrated     = errors.New("IMAP credential plaintext migration is required")
	ErrDuplicateIMAPUser             = errors.New("duplicate IMAP rows exist for a user")
	ErrUnsupportedIMAPCredentialDB   = errors.New("IMAP credential migration is unsupported for this database")
)

// IMAPCredentialMigrationResult reports non-secret row counts for an offline
// migration or rollback.
type IMAPCredentialMigrationResult struct {
	Updated   int
	Unchanged int
}

func imapCredentialContext(userID int64) credentials.Context {
	return credentials.Context{
		Kind:    imapCredentialKind,
		Table:   imapCredentialTable,
		Column:  imapCredentialColumn,
		OwnerID: strconv.FormatInt(userID, 10),
	}
}

func encryptIMAPPassword(credentialCipher *credentials.Cipher, userID int64, password string) (string, error) {
	if credentialCipher == nil {
		return "", ErrIMAPCredentialKeyringRequired
	}
	if password == "" {
		return "", ErrIMAPPasswordNotSpecified
	}
	envelope, err := credentialCipher.Encrypt(imapCredentialContext(userID), []byte(password))
	if err != nil {
		return "", ErrIMAPCredentialUnavailable
	}
	return string(envelope), nil
}

func validateEncryptedIMAPCredential(im IMAP) error {
	switch {
	case im.Password != "" && im.PasswordCiphertext != "":
		return ErrIMAPCredentialInvalidState
	case im.Password != "":
		return ErrIMAPCredentialNotMigrated
	case im.PasswordCiphertext == "":
		return ErrIMAPCredentialInvalidState
	default:
		return nil
	}
}

// DecryptIMAPPassword decrypts a migrated password using the stable owner AAD.
// It never treats the legacy password column as a runtime fallback.
func DecryptIMAPPassword(im IMAP, credentialCipher *credentials.Cipher) (string, error) {
	if err := validateEncryptedIMAPCredential(im); err != nil {
		return "", err
	}
	if credentialCipher == nil {
		return "", ErrIMAPCredentialKeyringRequired
	}
	plaintext, err := credentialCipher.Decrypt(
		imapCredentialContext(im.UserId),
		credentials.Envelope(im.PasswordCiphertext),
	)
	if err != nil || len(plaintext) == 0 {
		return "", ErrIMAPCredentialUnavailable
	}
	return string(plaintext), nil
}

// ValidateIMAPCredentialBackend rejects PostgreSQL and every backend for which
// this repository has no real IMAP credential schema.
func ValidateIMAPCredentialBackend(databaseName string) error {
	switch databaseName {
	case "sqlite3", "mysql":
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedIMAPCredentialDB, databaseName)
	}
}

type storedIMAPCredential struct {
	UserID             int64  `gorm:"column:user_id"`
	Password           string `gorm:"column:password"`
	PasswordCiphertext string `gorm:"column:password_ciphertext"`
}

// MigrateIMAPCredentials performs the explicit offline plaintext-to-ciphertext
// data migration. Schema migration remains a separate Goose operation.
func MigrateIMAPCredentials(credentialCipher *credentials.Cipher) (IMAPCredentialMigrationResult, error) {
	return transformIMAPCredentials(credentialCipher, false)
}

// RollbackIMAPCredentials performs the explicit offline ciphertext-to-plaintext
// data rollback. It does not invoke Goose Down.
func RollbackIMAPCredentials(credentialCipher *credentials.Cipher) (IMAPCredentialMigrationResult, error) {
	return transformIMAPCredentials(credentialCipher, true)
}

func transformIMAPCredentials(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (IMAPCredentialMigrationResult, error) {
	result := IMAPCredentialMigrationResult{}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateIMAPCredentialBackend(conf.DBName); err != nil {
		return result, err
	}
	if credentialCipher == nil {
		return result, ErrIMAPCredentialKeyringRequired
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

	var duplicateCount int
	if err := transaction.Raw(`
		SELECT COUNT(*) FROM (
			SELECT user_id FROM imap GROUP BY user_id HAVING COUNT(*) > 1
		) AS duplicate_imap_users
	`).Row().Scan(&duplicateCount); err != nil {
		return result, fmt.Errorf("validate IMAP credential schema: %w", err)
	}
	if duplicateCount != 0 {
		return result, ErrDuplicateIMAPUser
	}

	rows := []storedIMAPCredential{}
	if err := transaction.Raw(`
		SELECT user_id, password, password_ciphertext
		FROM imap
		ORDER BY user_id
	`).Scan(&rows).Error; err != nil {
		return result, fmt.Errorf("validate IMAP credential schema: %w", err)
	}

	for _, row := range rows {
		if rollback {
			if err := rollbackIMAPCredentialRow(transaction, credentialCipher, row, &result); err != nil {
				return IMAPCredentialMigrationResult{}, err
			}
			continue
		}
		if err := migrateIMAPCredentialRow(transaction, credentialCipher, row, &result); err != nil {
			return IMAPCredentialMigrationResult{}, err
		}
	}

	if err := transaction.Commit().Error; err != nil {
		return IMAPCredentialMigrationResult{}, err
	}
	committed = true
	return result, nil
}

func migrateIMAPCredentialRow(
	transaction *gorm.DB,
	credentialCipher *credentials.Cipher,
	row storedIMAPCredential,
	result *IMAPCredentialMigrationResult,
) error {
	switch {
	case row.Password != "" && row.PasswordCiphertext != "":
		return fmt.Errorf("%w for user %d", ErrIMAPCredentialInvalidState, row.UserID)
	case row.Password == "" && row.PasswordCiphertext == "":
		return fmt.Errorf("%w for user %d", ErrIMAPCredentialInvalidState, row.UserID)
	case row.Password == "":
		plaintext, err := credentialCipher.Decrypt(
			imapCredentialContext(row.UserID),
			credentials.Envelope(row.PasswordCiphertext),
		)
		if err != nil || len(plaintext) == 0 {
			return fmt.Errorf("%w for user %d", ErrIMAPCredentialUnavailable, row.UserID)
		}
		result.Unchanged++
		return nil
	}

	envelope, err := credentialCipher.Encrypt(
		imapCredentialContext(row.UserID),
		[]byte(row.Password),
	)
	if err != nil {
		return fmt.Errorf("%w for user %d", ErrIMAPCredentialUnavailable, row.UserID)
	}
	verified, err := credentialCipher.Decrypt(imapCredentialContext(row.UserID), envelope)
	if err != nil || !bytes.Equal(verified, []byte(row.Password)) {
		return fmt.Errorf("%w for user %d", ErrIMAPCredentialUnavailable, row.UserID)
	}
	update := transaction.Exec(`
		UPDATE imap
		SET password = '', password_ciphertext = ?
		WHERE user_id = ? AND password = ? AND password_ciphertext = ''
	`, string(envelope), row.UserID, row.Password)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("IMAP credential row changed during migration for user %d", row.UserID)
	}
	result.Updated++
	return nil
}

func rollbackIMAPCredentialRow(
	transaction *gorm.DB,
	credentialCipher *credentials.Cipher,
	row storedIMAPCredential,
	result *IMAPCredentialMigrationResult,
) error {
	switch {
	case row.Password != "" && row.PasswordCiphertext != "":
		return fmt.Errorf("%w for user %d", ErrIMAPCredentialInvalidState, row.UserID)
	case row.Password == "" && row.PasswordCiphertext == "":
		return fmt.Errorf("%w for user %d", ErrIMAPCredentialInvalidState, row.UserID)
	case row.PasswordCiphertext == "":
		result.Unchanged++
		return nil
	}

	plaintext, err := credentialCipher.Decrypt(
		imapCredentialContext(row.UserID),
		credentials.Envelope(row.PasswordCiphertext),
	)
	if err != nil || len(plaintext) == 0 {
		return fmt.Errorf("%w for user %d", ErrIMAPCredentialUnavailable, row.UserID)
	}
	update := transaction.Exec(`
		UPDATE imap
		SET password = ?, password_ciphertext = ''
		WHERE user_id = ? AND password = '' AND password_ciphertext = ?
	`, string(plaintext), row.UserID, row.PasswordCiphertext)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("IMAP credential row changed during rollback for user %d", row.UserID)
	}
	result.Updated++
	return nil
}
