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
	smtpCredentialKind   = "smtp-password"
	smtpCredentialTable  = "smtp"
	smtpCredentialColumn = "password"
)

var (
	ErrSMTPCredentialKeyringRequired = errors.New("SMTP credential keyring is required")
	ErrSMTPCredentialUnavailable     = errors.New("SMTP credential is unavailable")
	ErrSMTPCredentialInvalidState    = errors.New("SMTP credential columns are in an invalid state")
	ErrSMTPCredentialNotMigrated     = errors.New("SMTP credential plaintext migration is required")
	ErrInvalidSMTPCredentialIdentity = errors.New("SMTP credential has an invalid record or owner ID")
	ErrUnsupportedSMTPCredentialDB   = errors.New("SMTP credential migration is unsupported for this database")
)

// SMTPCredentialMigrationResult reports non-secret row counts for an offline
// migration or rollback.
type SMTPCredentialMigrationResult struct {
	Updated   int
	Unchanged int
}

func smtpCredentialContext(userID, recordID int64) credentials.Context {
	return credentials.Context{
		Kind:     smtpCredentialKind,
		Table:    smtpCredentialTable,
		Column:   smtpCredentialColumn,
		OwnerID:  strconv.FormatInt(userID, 10),
		RecordID: strconv.FormatInt(recordID, 10),
	}
}

func encryptSMTPPassword(credentialCipher *credentials.Cipher, userID, recordID int64, password string) (string, error) {
	if credentialCipher == nil {
		return "", ErrSMTPCredentialKeyringRequired
	}
	if userID <= 0 || recordID <= 0 {
		return "", ErrInvalidSMTPCredentialIdentity
	}
	if password == "" {
		return "", ErrSMTPCredentialInvalidState
	}
	envelope, err := credentialCipher.Encrypt(
		smtpCredentialContext(userID, recordID),
		[]byte(password),
	)
	if err != nil {
		return "", ErrSMTPCredentialUnavailable
	}
	verified, err := credentialCipher.Decrypt(
		smtpCredentialContext(userID, recordID),
		envelope,
	)
	if err != nil || !bytes.Equal(verified, []byte(password)) {
		return "", ErrSMTPCredentialUnavailable
	}
	return string(envelope), nil
}

func validateSMTPSecretColumns(s SMTP) error {
	switch {
	case s.Password != "" && s.PasswordCiphertext != "":
		return ErrSMTPCredentialInvalidState
	case s.Password != "":
		return ErrSMTPCredentialNotMigrated
	default:
		return nil
	}
}

// DecryptSMTPPassword returns an empty password for a profile with no stored
// secret. It never treats the legacy password column as a runtime fallback.
func DecryptSMTPPassword(s SMTP, credentialCipher *credentials.Cipher) (string, error) {
	if err := validateSMTPSecretColumns(s); err != nil {
		return "", err
	}
	if s.PasswordCiphertext == "" {
		return "", nil
	}
	if s.UserId <= 0 || s.Id <= 0 {
		return "", ErrInvalidSMTPCredentialIdentity
	}
	if credentialCipher == nil {
		return "", ErrSMTPCredentialKeyringRequired
	}
	plaintext, err := credentialCipher.Decrypt(
		smtpCredentialContext(s.UserId, s.Id),
		credentials.Envelope(s.PasswordCiphertext),
	)
	if err != nil || len(plaintext) == 0 {
		return "", ErrSMTPCredentialUnavailable
	}
	return string(plaintext), nil
}

// ValidateSMTPCredentialBackend rejects PostgreSQL and every backend for which
// this repository has no real SMTP credential schema.
func ValidateSMTPCredentialBackend(databaseName string) error {
	switch databaseName {
	case "sqlite3", "mysql":
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedSMTPCredentialDB, databaseName)
	}
}

type storedSMTPCredential struct {
	ID                 int64  `gorm:"column:id"`
	UserID             int64  `gorm:"column:user_id"`
	Password           string `gorm:"column:password"`
	PasswordCiphertext string `gorm:"column:password_ciphertext"`
}

// MigrateSMTPCredentials performs the explicit offline plaintext-to-ciphertext
// data migration. Schema migration remains a separate Goose operation.
func MigrateSMTPCredentials(credentialCipher *credentials.Cipher) (SMTPCredentialMigrationResult, error) {
	return transformSMTPCredentials(credentialCipher, false)
}

// RollbackSMTPCredentials performs the explicit offline ciphertext-to-plaintext
// data rollback. It does not invoke Goose Down.
func RollbackSMTPCredentials(credentialCipher *credentials.Cipher) (SMTPCredentialMigrationResult, error) {
	return transformSMTPCredentials(credentialCipher, true)
}

func transformSMTPCredentials(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (SMTPCredentialMigrationResult, error) {
	result := SMTPCredentialMigrationResult{}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateSMTPCredentialBackend(conf.DBName); err != nil {
		return result, err
	}
	if credentialCipher == nil {
		return result, ErrSMTPCredentialKeyringRequired
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

	rows := []storedSMTPCredential{}
	if err := transaction.Raw(`
		SELECT
			id,
			user_id,
			COALESCE(password, '') AS password,
			COALESCE(password_ciphertext, '') AS password_ciphertext
		FROM smtp
		ORDER BY id
	`).Scan(&rows).Error; err != nil {
		return result, fmt.Errorf("validate SMTP credential schema: %w", err)
	}
	if err := preflightSMTPCredentials(credentialCipher, rows); err != nil {
		return result, err
	}

	for _, row := range rows {
		if rollback {
			if err := rollbackSMTPCredentialRow(transaction, credentialCipher, row, &result); err != nil {
				return SMTPCredentialMigrationResult{}, err
			}
			continue
		}
		if err := migrateSMTPCredentialRow(transaction, credentialCipher, row, &result); err != nil {
			return SMTPCredentialMigrationResult{}, err
		}
	}

	if err := transaction.Commit().Error; err != nil {
		return SMTPCredentialMigrationResult{}, err
	}
	committed = true
	return result, nil
}

func preflightSMTPCredentials(credentialCipher *credentials.Cipher, rows []storedSMTPCredential) error {
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if row.ID <= 0 || row.UserID <= 0 {
			return fmt.Errorf("%w for profile %d", ErrInvalidSMTPCredentialIdentity, row.ID)
		}
		if _, duplicate := seen[row.ID]; duplicate {
			return fmt.Errorf("%w for profile %d", ErrInvalidSMTPCredentialIdentity, row.ID)
		}
		seen[row.ID] = struct{}{}
		if row.Password != "" && row.PasswordCiphertext != "" {
			return fmt.Errorf("%w for profile %d", ErrSMTPCredentialInvalidState, row.ID)
		}
		if row.PasswordCiphertext != "" {
			plaintext, err := credentialCipher.Decrypt(
				smtpCredentialContext(row.UserID, row.ID),
				credentials.Envelope(row.PasswordCiphertext),
			)
			if err != nil || len(plaintext) == 0 {
				return fmt.Errorf("%w for profile %d", ErrSMTPCredentialUnavailable, row.ID)
			}
		}
	}
	return nil
}

func migrateSMTPCredentialRow(
	transaction *gorm.DB,
	credentialCipher *credentials.Cipher,
	row storedSMTPCredential,
	result *SMTPCredentialMigrationResult,
) error {
	if row.Password == "" {
		result.Unchanged++
		return nil
	}
	envelope, err := encryptSMTPPassword(credentialCipher, row.UserID, row.ID, row.Password)
	if err != nil {
		return fmt.Errorf("%w for profile %d", err, row.ID)
	}
	update := transaction.Exec(`
		UPDATE smtp
		SET password = '', password_ciphertext = ?
		WHERE id = ? AND user_id = ? AND password = ? AND password_ciphertext = ''
	`, envelope, row.ID, row.UserID, row.Password)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("SMTP credential row changed during migration for profile %d", row.ID)
	}
	result.Updated++
	return nil
}

func rollbackSMTPCredentialRow(
	transaction *gorm.DB,
	credentialCipher *credentials.Cipher,
	row storedSMTPCredential,
	result *SMTPCredentialMigrationResult,
) error {
	if row.PasswordCiphertext == "" {
		result.Unchanged++
		return nil
	}
	plaintext, err := credentialCipher.Decrypt(
		smtpCredentialContext(row.UserID, row.ID),
		credentials.Envelope(row.PasswordCiphertext),
	)
	if err != nil || len(plaintext) == 0 {
		return fmt.Errorf("%w for profile %d", ErrSMTPCredentialUnavailable, row.ID)
	}
	update := transaction.Exec(`
		UPDATE smtp
		SET password = ?, password_ciphertext = ''
		WHERE id = ? AND user_id = ? AND password = '' AND password_ciphertext = ?
	`, string(plaintext), row.ID, row.UserID, row.PasswordCiphertext)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("SMTP credential row changed during rollback for profile %d", row.ID)
	}
	result.Updated++
	return nil
}
