package models

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/jinzhu/gorm"
)

const (
	smtpCredentialKind   = "smtp-password"
	smtpCredentialTable  = "smtp"
	smtpCredentialColumn = "password"

	// Byte limits are deliberately independent of database character-count
	// semantics. Keep this conservative derivation aligned with the immutable
	// v1 format: magic, separators, version, max key ID, nonce, ciphertext, and
	// GCM tag. The resulting 464-byte maximum leaves ample room in VARCHAR(2048).
	smtpPasswordMaxBytes         = 255
	smtpPasswordEnvelopeMaxBytes = len("gophishfr-cred") + 4 + len("v1") + 64 +
		((12 + 2) / 3 * 4) +
		((smtpPasswordMaxBytes + 16 + 2) / 3 * 4)
	smtpPasswordCiphertextColumnBytes = 2048
	_                                 = uint(smtpPasswordCiphertextColumnBytes - smtpPasswordEnvelopeMaxBytes)
)

var (
	ErrSMTPCredentialKeyringRequired = errors.New("SMTP credential keyring is required")
	ErrSMTPCredentialUnavailable     = errors.New("SMTP credential is unavailable")
	ErrSMTPCredentialInvalidState    = errors.New("SMTP credential columns are in an invalid state")
	ErrSMTPCredentialNotMigrated     = errors.New("SMTP credential plaintext migration is required")
	ErrInvalidSMTPCredentialIdentity = errors.New("SMTP credential has an invalid record or owner ID")
	ErrUnsupportedSMTPCredentialDB   = errors.New("SMTP credential migration is unsupported for this database")
	ErrSMTPCredentialTooLong         = errors.New("SMTP password exceeds the storage-safe limit")
	ErrSMTPCredentialInvalidEncoding = errors.New("SMTP password is not valid UTF-8")
	ErrSMTPCredentialStorageMismatch = errors.New("SMTP credential storage verification failed")
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

// ValidateSMTPPassword applies the same UTF-8 byte limit to every SMTP
// password entry point, including request-only test-email credentials.
func ValidateSMTPPassword(password string) error {
	if !utf8.ValidString(password) {
		return ErrSMTPCredentialInvalidEncoding
	}
	if len(password) > smtpPasswordMaxBytes {
		return ErrSMTPCredentialTooLong
	}
	return nil
}

func validateSMTPCiphertext(ciphertext string) error {
	if !utf8.ValidString(ciphertext) ||
		len(ciphertext) > smtpPasswordEnvelopeMaxBytes ||
		len(ciphertext) > smtpPasswordCiphertextColumnBytes {
		return ErrSMTPCredentialInvalidState
	}
	return nil
}

func encryptSMTPPassword(credentialCipher *credentials.Cipher, userID, recordID int64, password string) (string, error) {
	if err := ValidateSMTPPassword(password); err != nil {
		return "", err
	}
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
	ciphertext := string(envelope)
	if err := validateSMTPCiphertext(ciphertext); err != nil {
		return "", err
	}
	return ciphertext, nil
}

func validateSMTPSecretColumns(s SMTP) error {
	switch {
	case s.Password != "" && s.PasswordCiphertext != "":
		return ErrSMTPCredentialInvalidState
	case s.Password != "":
		if err := ValidateSMTPPassword(s.Password); err != nil {
			return err
		}
		return ErrSMTPCredentialNotMigrated
	case s.PasswordCiphertext != "":
		return validateSMTPCiphertext(s.PasswordCiphertext)
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
	if err := validateSMTPCiphertext(s.PasswordCiphertext); err != nil {
		return "", err
	}
	plaintext, err := credentialCipher.Decrypt(
		smtpCredentialContext(s.UserId, s.Id),
		credentials.Envelope(s.PasswordCiphertext),
	)
	if err != nil || len(plaintext) == 0 {
		return "", ErrSMTPCredentialUnavailable
	}
	password := string(plaintext)
	if err := ValidateSMTPPassword(password); err != nil {
		return "", err
	}
	return password, nil
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

func readSMTPCredentialStorage(transaction *gorm.DB, id, userID int64) (storedSMTPCredential, error) {
	var stored storedSMTPCredential
	if err := transaction.Raw(`
		SELECT
			id,
			user_id,
			COALESCE(password, '') AS password,
			COALESCE(password_ciphertext, '') AS password_ciphertext
		FROM smtp
		WHERE id = ? AND user_id = ?
	`, id, userID).Scan(&stored).Error; err != nil {
		return storedSMTPCredential{}, err
	}
	if stored.ID != id || stored.UserID != userID {
		return storedSMTPCredential{}, gorm.ErrRecordNotFound
	}
	return stored, nil
}

func verifySMTPCredentialStorage(
	transaction *gorm.DB,
	id, userID int64,
	password, ciphertext string,
) error {
	stored, err := readSMTPCredentialStorage(transaction, id, userID)
	if err != nil {
		return err
	}
	if stored.Password != password || stored.PasswordCiphertext != ciphertext {
		return ErrSMTPCredentialStorageMismatch
	}
	return nil
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
		if row.Password != "" {
			if err := ValidateSMTPPassword(row.Password); err != nil {
				return fmt.Errorf("%w for profile %d", err, row.ID)
			}
		}
		if row.PasswordCiphertext != "" {
			if err := validateSMTPCiphertext(row.PasswordCiphertext); err != nil {
				return fmt.Errorf("%w for profile %d", err, row.ID)
			}
			if _, err := DecryptSMTPPassword(SMTP{
				Id:                 row.ID,
				UserId:             row.UserID,
				PasswordCiphertext: row.PasswordCiphertext,
			}, credentialCipher); err != nil {
				return fmt.Errorf("%w for profile %d", err, row.ID)
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
		SET password_ciphertext = ?
		WHERE id = ? AND user_id = ? AND password = ? AND password_ciphertext = ''
	`, envelope, row.ID, row.UserID, row.Password)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("SMTP credential row changed during migration for profile %d", row.ID)
	}
	if err := verifySMTPCredentialStorage(
		transaction,
		row.ID,
		row.UserID,
		row.Password,
		envelope,
	); err != nil {
		return fmt.Errorf("%w for profile %d", err, row.ID)
	}
	update = transaction.Exec(`
		UPDATE smtp
		SET password = ''
		WHERE id = ? AND user_id = ? AND password = ? AND password_ciphertext = ?
	`, row.ID, row.UserID, row.Password, envelope)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("SMTP credential row changed during migration for profile %d", row.ID)
	}
	if err := verifySMTPCredentialStorage(
		transaction,
		row.ID,
		row.UserID,
		"",
		envelope,
	); err != nil {
		return fmt.Errorf("%w for profile %d", err, row.ID)
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
	plaintext, err := DecryptSMTPPassword(SMTP{
		Id:                 row.ID,
		UserId:             row.UserID,
		PasswordCiphertext: row.PasswordCiphertext,
	}, credentialCipher)
	if err != nil {
		return fmt.Errorf("%w for profile %d", err, row.ID)
	}
	update := transaction.Exec(`
		UPDATE smtp
		SET password = ?
		WHERE id = ? AND user_id = ? AND password = '' AND password_ciphertext = ?
	`, plaintext, row.ID, row.UserID, row.PasswordCiphertext)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("SMTP credential row changed during rollback for profile %d", row.ID)
	}
	if err := verifySMTPCredentialStorage(
		transaction,
		row.ID,
		row.UserID,
		plaintext,
		row.PasswordCiphertext,
	); err != nil {
		return fmt.Errorf("%w for profile %d", err, row.ID)
	}
	update = transaction.Exec(`
		UPDATE smtp
		SET password_ciphertext = ''
		WHERE id = ? AND user_id = ? AND password = ? AND password_ciphertext = ?
	`, row.ID, row.UserID, plaintext, row.PasswordCiphertext)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("SMTP credential row changed during rollback for profile %d", row.ID)
	}
	if err := verifySMTPCredentialStorage(
		transaction,
		row.ID,
		row.UserID,
		plaintext,
		"",
	); err != nil {
		return fmt.Errorf("%w for profile %d", err, row.ID)
	}
	result.Updated++
	return nil
}
