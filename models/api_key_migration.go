package models

import (
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Vesperis-group/gophishfr/internal/apikey"
)

const maxLegacyAPIKeyBytes = 255

var ErrUnsupportedAPIKeyVerifierDB = errors.New("API verifier migration supports only sqlite3 and mysql")

// APIKeyMigrationResult contains counts only; it intentionally cannot carry
// token, verifier, or keyring material.
type APIKeyMigrationResult struct {
	Updated   int
	Unchanged int
}

type legacyAPIKeyRow struct {
	id       int64
	token    sql.NullString
	verifier []byte
	keyID    sql.NullString
	state    apiKeyRowState
}

type apiKeyRowState uint8

const (
	apiKeyStateLegacy apiKeyRowState = iota + 1
	apiKeyStateMigrated
	apiKeyStateControlledBoth
)

// ValidateAPIKeyVerifierBackend rejects PostgreSQL explicitly: the repository
// has no PostgreSQL application migration tree for this schema.
func ValidateAPIKeyVerifierBackend(name string) error {
	if name != "sqlite3" && name != "mysql" {
		return ErrUnsupportedAPIKeyVerifierDB
	}
	return nil
}

// MigrateAPIKeys performs the explicit irreversible offline transformation in
// one global transaction. Callers must stop all application writers first.
func MigrateAPIKeys(verifier *apikey.Service) (APIKeyMigrationResult, error) {
	var result APIKeyMigrationResult
	if verifier == nil {
		return result, ErrAPIKeyVerifierUnavailable
	}
	if conf == nil {
		return result, errors.New("database is not configured")
	}
	if err := ValidateAPIKeyVerifierBackend(conf.DBName); err != nil {
		return result, err
	}
	transaction, err := db.DB().Begin()
	if err != nil {
		return result, fmt.Errorf("begin API verifier migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	rows, err := transaction.Query(
		"SELECT id, api_key, api_key_verifier, api_key_verifier_key_id FROM users ORDER BY id",
	)
	if err != nil {
		return result, errors.New("API verifier schema is unavailable")
	}
	allRows := make([]legacyAPIKeyRow, 0)
	seenIDs := make(map[int64]struct{})
	for rows.Next() {
		var row legacyAPIKeyRow
		if err := rows.Scan(&row.id, &row.token, &row.verifier, &row.keyID); err != nil {
			_ = rows.Close()
			return result, errors.New("invalid API verifier migration row")
		}
		if row.id <= 0 {
			_ = rows.Close()
			return result, errors.New("invalid API verifier migration row ID")
		}
		if _, duplicate := seenIDs[row.id]; duplicate {
			_ = rows.Close()
			return result, errors.New("duplicate API verifier migration row ID")
		}
		seenIDs[row.id] = struct{}{}
		state, err := classifyAPIKeyMigrationRow(row, verifier)
		if err != nil {
			_ = rows.Close()
			return result, err
		}
		row.state = state
		allRows = append(allRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, errors.New("read API verifier migration rows")
	}
	if err := rows.Close(); err != nil {
		return result, errors.New("close API verifier migration rows")
	}
	if err := preflightAPIKeyCollisions(allRows, verifier); err != nil {
		return result, err
	}

	for _, row := range allRows {
		switch row.state {
		case apiKeyStateMigrated:
			result.Unchanged++
		case apiKeyStateLegacy:
			keyID, digest, err := verifier.ComputeActive([]byte(row.token.String))
			if err != nil {
				return result, ErrAPIKeyVerifierUnavailable
			}
			update, err := transaction.Exec(
				"UPDATE users SET api_key_verifier = ?, api_key_verifier_key_id = ? "+
					"WHERE id = ? AND api_key = ? AND api_key_verifier IS NULL "+
					"AND api_key_verifier_key_id IS NULL",
				digest[:], keyID, row.id, row.token.String,
			)
			if err != nil || exactRowsAffected(update) != nil {
				return result, errors.New("guarded API verifier migration write failed")
			}
			if err := readBackControlledBoth(transaction, row.id, row.token.String, keyID, digest[:]); err != nil {
				return result, err
			}
			if err := clearLegacyAPIKey(transaction, row.id, row.token.String, keyID, digest[:]); err != nil {
				return result, err
			}
			result.Updated++
		case apiKeyStateControlledBoth:
			if err := clearLegacyAPIKey(transaction, row.id, row.token.String, row.keyID.String, row.verifier); err != nil {
				return result, err
			}
			result.Updated++
		}
	}
	if err := transaction.Commit(); err != nil {
		return result, errors.New("commit API verifier migration")
	}
	committed = true
	return result, nil
}

func classifyAPIKeyMigrationRow(row legacyAPIKeyRow, verifier *apikey.Service) (apiKeyRowState, error) {
	hasToken := row.token.Valid
	hasVerifier := row.verifier != nil
	hasKeyID := row.keyID.Valid
	if hasToken && (row.token.String == "" || len([]byte(row.token.String)) > maxLegacyAPIKeyBytes) {
		return 0, errors.New("invalid legacy API credential state")
	}
	switch {
	case hasToken && !hasVerifier && !hasKeyID:
		return apiKeyStateLegacy, nil
	case !hasToken && hasVerifier && hasKeyID:
		if validateMigratedAPIKeyState(row.keyID.String, row.verifier) != nil {
			return 0, ErrInvalidAPIKeyState
		}
		if _, err := verifier.Compute(row.keyID.String, nil); err != nil {
			return 0, ErrAPIKeyVerifierUnavailable
		}
		return apiKeyStateMigrated, nil
	case hasToken && hasVerifier && hasKeyID:
		if validateMigratedAPIKeyState(row.keyID.String, row.verifier) != nil ||
			verifier.Verify(row.keyID.String, []byte(row.token.String), row.verifier) != nil {
			return 0, errors.New("mismatched controlled API verifier state")
		}
		return apiKeyStateControlledBoth, nil
	default:
		return 0, ErrInvalidAPIKeyState
	}
}

func preflightAPIKeyCollisions(rows []legacyAPIKeyRow, verifier *apikey.Service) error {
	type owner struct {
		id int64
	}
	stored := make(map[string]owner, len(rows))
	for _, row := range rows {
		if row.state == apiKeyStateMigrated || row.state == apiKeyStateControlledBoth {
			key := row.keyID.String + "\x00" + string(row.verifier)
			if previous, exists := stored[key]; exists && previous.id != row.id {
				return ErrAmbiguousAPICredential
			}
			stored[key] = owner{id: row.id}
		}
	}
	for _, row := range rows {
		if row.state != apiKeyStateLegacy && row.state != apiKeyStateControlledBoth {
			continue
		}
		candidates, err := verifier.Candidates([]byte(row.token.String))
		if err != nil {
			return ErrAPIKeyVerifierUnavailable
		}
		for _, candidate := range candidates {
			key := candidate.KeyID + "\x00" + string(candidate.Verifier[:])
			if previous, exists := stored[key]; exists && previous.id != row.id {
				return ErrAmbiguousAPICredential
			}
		}
		active, digest, _ := verifier.ComputeActive([]byte(row.token.String))
		key := active + "\x00" + string(digest[:])
		if previous, exists := stored[key]; exists && previous.id != row.id {
			return ErrAmbiguousAPICredential
		}
		stored[key] = owner{id: row.id}
	}
	return nil
}

func exactRowsAffected(result sql.Result) error {
	if result == nil {
		return errors.New("missing guarded API verifier result")
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.New("guarded API verifier row count mismatch")
	}
	return nil
}

func readBackControlledBoth(
	transaction *sql.Tx,
	id int64,
	token, keyID string,
	verifier []byte,
) error {
	var storedToken sql.NullString
	var storedVerifier []byte
	var storedKeyID sql.NullString
	if err := transaction.QueryRow(
		"SELECT api_key, api_key_verifier, api_key_verifier_key_id FROM users WHERE id = ?", id,
	).Scan(&storedToken, &storedVerifier, &storedKeyID); err != nil {
		return errors.New("read back controlled API verifier state")
	}
	if !storedToken.Valid || storedToken.String != token || !storedKeyID.Valid ||
		storedKeyID.String != keyID || !hmac.Equal(storedVerifier, verifier) {
		return ErrInvalidAPIKeyState
	}
	return nil
}

func clearLegacyAPIKey(
	transaction *sql.Tx,
	id int64,
	token, keyID string,
	verifier []byte,
) error {
	update, err := transaction.Exec(
		"UPDATE users SET api_key = NULL WHERE id = ? AND api_key = ? "+
			"AND api_key_verifier_key_id = ? AND api_key_verifier = ?",
		id, token, keyID, verifier,
	)
	if err != nil || exactRowsAffected(update) != nil {
		return errors.New("guarded legacy API credential clear failed")
	}
	var storedToken sql.NullString
	var storedVerifier []byte
	var storedKeyID sql.NullString
	if err := transaction.QueryRow(
		"SELECT api_key, api_key_verifier, api_key_verifier_key_id FROM users WHERE id = ?", id,
	).Scan(&storedToken, &storedVerifier, &storedKeyID); err != nil {
		return errors.New("read back migrated API verifier state")
	}
	if storedToken.Valid || !storedKeyID.Valid || storedKeyID.String != keyID ||
		!hmac.Equal(storedVerifier, verifier) {
		return ErrInvalidAPIKeyState
	}
	return nil
}
