package models

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/internal/apikey"
	"github.com/jinzhu/gorm"
	"github.com/pressly/goose/v3"
)

func testAPIKeyService(t *testing.T, active string, ids ...string) *apikey.Service {
	t.Helper()
	keys := make(map[string][]byte, len(ids))
	for _, id := range ids {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			t.Fatal(err)
		}
		keys[id] = key
	}
	return testAPIKeyServiceFromKeys(t, active, keys)
}

func testAPIKeyServiceFromKeys(t *testing.T, active string, keys map[string][]byte) *apikey.Service {
	t.Helper()
	keyring, err := apikey.NewKeyring(active, keys)
	if err != nil {
		t.Fatal(err)
	}
	service, err := apikey.New(keyring)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func setupAPIKeyDatabase(t *testing.T, service *apikey.Service) {
	t.Helper()
	closeBootstrapDB(t)
	SetAPIKeyVerifier(service)
	t.Setenv(InitialAdminPassword, syntheticBootstrapPassword)
	t.Setenv(InitialAdminApiToken, "synthetic-lifecycle-admin-token")
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         filepath.Join(t.TempDir(), "api-key-verifier.db"),
		MigrationsPath: "../db/db_sqlite3/migrations",
	}
	if err := Setup(conf); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeBootstrapDB(t) })
}

func TestAPIKeyVerifierCreateResetAndMissingKeyring(t *testing.T) {
	service := testAPIKeyService(t, "active", "active")
	setupAPIKeyDatabase(t, service)

	admin, err := GetUser(1)
	if err != nil {
		t.Fatal(err)
	}

	if len(admin.APIKeyVerifier) != apikey.VerifierSize || admin.APIKeyVerifierKeyID != "active" {
		t.Fatal("fresh bootstrap did not store the verifier state")
	}
	var plaintext sql.NullString
	if err := db.DB().QueryRow("SELECT api_key FROM users WHERE id = 1").Scan(&plaintext); err != nil {
		t.Fatal(err)
	}
	if plaintext.Valid {
		t.Fatal("fresh bootstrap persisted plaintext")
	}
	if _, err := GetUserByAPIKey("synthetic-lifecycle-admin-token"); err != nil {
		t.Fatalf("provided bootstrap token changed: %v", err)
	}

	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	user := User{Username: "verifier-create", Hash: "synthetic-hash", RoleID: role.ID}
	createdToken, err := CreateUserWithAPIKey(&user)
	if err != nil {
		t.Fatal(err)
	}
	if len(createdToken) != 64 {
		t.Fatal("generated token format changed")
	}
	if _, err := hex.DecodeString(createdToken); err != nil {
		t.Fatal("generated token is not lowercase hexadecimal")
	}
	if authenticated, err := GetUserByAPIKey(createdToken); err != nil || authenticated.Id != user.Id {
		t.Fatalf("created token did not authenticate exact user: %v", err)
	}

	resetToken, err := ResetUserAPIKey(user.Id)
	if err != nil {
		t.Fatal(err)
	}
	if resetToken == createdToken {
		t.Fatal("reset reused old token")
	}
	if _, err := GetUserByAPIKey(createdToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("old token remained valid after reset commit")
	}
	if authenticated, err := GetUserByAPIKey(resetToken); err != nil || authenticated.Id != user.Id {
		t.Fatalf("reset token did not authenticate exact user: %v", err)
	}

	SetAPIKeyVerifier(nil)
	if _, err := GetUser(user.Id); err != nil {
		t.Fatal("session user lookup failed without verifier keyring")
	}
	if _, err := GetUserByAPIKey(resetToken); !errors.Is(err, ErrAPIKeyVerifierUnavailable) {
		t.Fatal("API authentication did not fail closed without keyring")
	}
	if _, err := ResetUserAPIKey(user.Id); !errors.Is(err, ErrAPIKeyVerifierUnavailable) {
		t.Fatal("reset did not fail closed without keyring")
	}
	SetAPIKeyVerifier(service)
}

func TestFreshBootstrapMissingVerifierIsAtomic(t *testing.T) {
	closeBootstrapDB(t)
	SetAPIKeyVerifier(nil)
	t.Setenv(InitialAdminPassword, syntheticBootstrapPassword)
	conf := &config.Config{
		DBName: "sqlite3", DBPath: filepath.Join(t.TempDir(), "missing-keyring.db"),
		MigrationsPath: "../db/db_sqlite3/migrations",
	}
	if err := Setup(conf); !errors.Is(err, ErrAPIKeyVerifierUnavailable) {
		t.Fatalf("fresh bootstrap did not fail for missing verifier: %v", err)
	}
	var count int
	if err := db.Model(&User{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("missing verifier keyring left a partial user")
	}
	closeBootstrapDB(t)
	SetAPIKeyVerifier(testAPIKeyService(t, "restored", "restored"))
}

func TestOfflineAPIKeyMigrationAtomicIdempotentAndIrreversible(t *testing.T) {
	service := testAPIKeyService(t, "active", "active", "old")
	setupAPIKeyDatabase(t, service)
	if _, err := db.DB().Exec(
		"UPDATE users SET api_key = ?, api_key_verifier = NULL, api_key_verifier_key_id = NULL WHERE id = 1",
		"synthetic-legacy-admin-token",
	); err != nil {
		t.Fatal(err)
	}
	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(
		"INSERT INTO users (username, hash, api_key, role_id) VALUES (?, ?, ?, ?)",
		"legacy-user", "synthetic-hash", "weak-synthetic-legacy-token", role.ID,
	); err != nil {
		t.Fatal(err)
	}

	result, err := MigrateAPIKeys(service)
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 2 || result.Unchanged != 0 {
		t.Fatalf("unexpected migration counts: %+v", result)
	}
	for _, token := range []string{"synthetic-legacy-admin-token", "weak-synthetic-legacy-token"} {
		if _, err := GetUserByAPIKey(token); err != nil {
			t.Fatalf("legacy token changed during migration: %v", err)
		}
	}
	var plaintextCount, wrongLength int
	if err := db.DB().QueryRow(
		"SELECT SUM(CASE WHEN api_key IS NOT NULL THEN 1 ELSE 0 END), "+
			"SUM(CASE WHEN length(api_key_verifier) <> 32 THEN 1 ELSE 0 END) FROM users",
	).Scan(&plaintextCount, &wrongLength); err != nil {
		t.Fatal(err)
	}
	if plaintextCount != 0 || wrongLength != 0 {
		t.Fatal("migration left plaintext or malformed verifiers")
	}
	before := append([]byte(nil), mustGetUser(t, 1).APIKeyVerifier...)
	result, err = MigrateAPIKeys(service)
	if err != nil || result.Updated != 0 || result.Unchanged != 2 {
		t.Fatalf("migration rerun was not an idempotent no-op: %+v %v", result, err)
	}
	if !hmacEqual(before, mustGetUser(t, 1).APIKeyVerifier) {
		t.Fatal("idempotent migration changed a verifier")
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("schema Down discarded irreversible verifier-only state")
	}

	// A malformed later row blocks the entire preflight before any legacy row
	// is changed.
	if _, err := db.DB().Exec(
		"UPDATE users SET api_key = ?, api_key_verifier = NULL, api_key_verifier_key_id = NULL WHERE id = 1",
		"synthetic-rollback-token",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(
		"UPDATE users SET api_key = ?, api_key_verifier = ?, api_key_verifier_key_id = ? WHERE id = 2",
		"synthetic-mismatch-token", make([]byte, 32), "active",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateAPIKeys(service); err == nil {
		t.Fatal("mismatched controlled BOTH state was accepted")
	}
	var plaintext sql.NullString
	if err := db.DB().QueryRow("SELECT api_key FROM users WHERE id = 1").Scan(&plaintext); err != nil {
		t.Fatal(err)
	}
	if !plaintext.Valid || plaintext.String != "synthetic-rollback-token" {
		t.Fatal("failed migration lost an earlier plaintext token")
	}
}

func TestOldPepperLazyUpgradeAndResetRace(t *testing.T) {
	keys := map[string][]byte{"active": make([]byte, 32), "old": make([]byte, 32)}
	if _, err := rand.Read(keys["active"]); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(keys["old"]); err != nil {
		t.Fatal(err)
	}
	service := testAPIKeyServiceFromKeys(t, "active", keys)
	setupAPIKeyDatabase(t, service)
	role, _ := GetRoleBySlug(RoleUser)
	token := "synthetic-old-pepper-token"
	oldVerifier, _ := service.Compute("old", []byte(token))
	result := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id, api_key_verifier, api_key_verifier_key_id) "+
			"VALUES (?, ?, NULL, ?, ?, ?)",
		"old-pepper-user", "synthetic-hash", role.ID, oldVerifier[:], "old",
	)
	if result.Error != nil {
		t.Fatal(result.Error)
	}

	user, err := GetUserByAPIKey(token)
	if err != nil {
		t.Fatal(err)
	}

	upgraded := mustGetUser(t, user.Id)
	if upgraded.APIKeyVerifierKeyID != "active" {
		t.Fatal("old pepper verifier was not lazily upgraded")
	}
	activeOnly := testAPIKeyServiceFromKeys(t, "active", map[string][]byte{"active": keys["active"]})
	SetAPIKeyVerifier(activeOnly)
	if authenticated, err := GetUserByAPIKey(token); err != nil || authenticated.Id != user.Id {
		t.Fatal("token failed after safely retiring lazily upgraded old pepper")
	}
	SetAPIKeyVerifier(service)

	// Concurrent upgrades may race, but all successful identities are exact.
	oldVerifier, _ = service.Compute("old", []byte(token))
	if err := db.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"api_key_verifier": oldVerifier[:], "api_key_verifier_key_id": "old",
	}).Error; err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if authenticated, err := GetUserByAPIKey(token); err != nil || authenticated.Id != user.Id {
				t.Errorf("concurrent lazy upgrade confused identity: %v", err)
			}
		}()
	}
	wait.Wait()

	oldToken := token
	var resetToken string
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = GetUserByAPIKey(oldToken)
	}()
	go func() {
		defer wait.Done()
		var err error
		resetToken, err = ResetUserAPIKey(user.Id)
		if err != nil {
			t.Errorf("concurrent reset failed: %v", err)
		}
	}()
	wait.Wait()
	if _, err := GetUserByAPIKey(oldToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("old token succeeded after concurrent reset committed")
	}
	if authenticated, err := GetUserByAPIKey(resetToken); err != nil || authenticated.Id != user.Id {
		t.Fatalf("new token failed after concurrent reset: %v", err)
	}

	activeOnly = testAPIKeyService(t, "active", "active")
	// This distinct active-only service has different bytes and therefore
	// correctly demonstrates wrong-pepper failure.
	SetAPIKeyVerifier(activeOnly)
	if _, err := GetUserByAPIKey(resetToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("wrong pepper authenticated a token")
	}
	SetAPIKeyVerifier(service)
}

func TestSQLiteAPIKeySchemaRebuildPreservesLegacyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	sqlDB, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	migrations := "../db/db_sqlite3/migrations"
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(sqlDB, migrations, 20260903020000); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (id, username, hash, api_key, role_id, password_change_required, last_login, account_locked) " +
			"VALUES (7, 'legacy-schema-user', 'synthetic-hash', 'synthetic-schema-token', 2, 1, '2026-01-02 03:04:05', 0)",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec("UPDATE sqlite_sequence SET seq = 41 WHERE name = 'users'"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		t.Fatal(err)
	}
	var sequence int64
	if err := sqlDB.QueryRow("SELECT seq FROM sqlite_sequence WHERE name = 'users'").Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if sequence != 41 {
		t.Fatalf("users sequence high-water mark changed: %d", sequence)
	}
	var token sql.NullString
	var verifier []byte
	var keyID sql.NullString
	if err := sqlDB.QueryRow(
		"SELECT api_key, api_key_verifier, api_key_verifier_key_id FROM users WHERE id = 7",
	).Scan(&token, &verifier, &keyID); err != nil {
		t.Fatal(err)
	}
	if !token.Valid || token.String != "synthetic-schema-token" || verifier != nil || keyID.Valid {
		t.Fatal("schema Up did not preserve exact legacy row")
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (username, api_key) VALUES ('legacy-schema-user', 'other')",
	); err == nil {
		t.Fatal("username uniqueness was lost")
	}
	if err := goose.Down(sqlDB, migrations); err != nil {
		t.Fatalf("safe legacy schema Down failed: %v", err)
	}
	if err := sqlDB.QueryRow(
		"SELECT api_key FROM users WHERE id = 7",
	).Scan(&token); err != nil || !token.Valid || token.String != "synthetic-schema-token" {
		t.Fatalf("schema Down lost legacy data: %v", err)
	}
}

func mustGetUser(t *testing.T, id int64) User {
	t.Helper()
	user, err := GetUser(id)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal(err)
	}
	return user
}

func TestAPIKeyVerifierPostgreSQLUnsupported(t *testing.T) {
	if !errors.Is(ValidateAPIKeyVerifierBackend("postgres"), ErrUnsupportedAPIKeyVerifierDB) {
		t.Fatal("PostgreSQL verifier migration was not explicitly unsupported")
	}
}

func TestAPIKeyMigrationErrorRedaction(t *testing.T) {
	for _, err := range []error{
		ErrAPIKeyVerifierUnavailable,
		ErrInvalidAPIKeyState,
		ErrInvalidAPICredential,
		ErrAmbiguousAPICredential,
	} {
		if text := fmt.Sprint(err); text == "" {
			t.Fatal("typed migration error has no safe message")
		}
	}
}

func TestAPIKeyAuthenticationFailsClosedForStoredState(t *testing.T) {
	service := testAPIKeyService(t, "active", "active", "old")
	setupAPIKeyDatabase(t, service)
	role, _ := GetRoleBySlug(RoleUser)
	first := User{Username: "state-first", Hash: "synthetic-hash", RoleID: role.ID}
	firstToken, err := CreateUserWithAPIKey(&first)
	if err != nil {
		t.Fatal(err)
	}
	second := User{Username: "state-second", Hash: "synthetic-hash", RoleID: role.ID}
	if _, err := CreateUserWithAPIKey(&second); err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(
		"UPDATE users SET api_key_verifier_key_id = ? WHERE id = ?", "missing-key", first.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(firstToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("unknown stored key ID authenticated")
	}
	tampered := make([]byte, 32)
	if _, err := rand.Read(tampered); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET api_key_verifier = ?, api_key_verifier_key_id = ? WHERE id = ?",
		tampered, "active", first.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(firstToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("tampered verifier authenticated")
	}

	activeDigest, _ := service.Compute("active", []byte(firstToken))
	oldDigest, _ := service.Compute("old", []byte(firstToken))
	if err := db.Exec(
		"UPDATE users SET api_key_verifier = ?, api_key_verifier_key_id = ? WHERE id = ?",
		activeDigest[:], "active", first.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET api_key_verifier = ?, api_key_verifier_key_id = ? WHERE id = ?",
		oldDigest[:], "old", second.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(firstToken); !errors.Is(err, ErrAmbiguousAPICredential) {
		t.Fatal("ambiguous verifier matches did not fail closed")
	}

	SetAPIKeyVerifier(nil)
	if _, err := GetUser(first.Id); err != nil {
		t.Fatal("key loss broke session administration lookup")
	}
	replacement := testAPIKeyService(t, "replacement", "replacement")
	SetAPIKeyVerifier(replacement)
	newToken, err := ResetUserAPIKey(first.Id)
	if err != nil {
		t.Fatal("session recovery could not reissue after key loss")
	}
	if _, err := GetUserByAPIKey(newToken); err != nil {
		t.Fatal("reissued token failed after key loss recovery")
	}
}
