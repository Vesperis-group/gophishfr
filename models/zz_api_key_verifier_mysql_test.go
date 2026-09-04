package models

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/pressly/goose/v3"
)

func mysqlAPIKeyTestDSN(t *testing.T) string {
	t.Helper()
	connectionString := os.Getenv("GOPHISHFR_MYSQL_TEST_DSN")
	if connectionString == "" {
		t.Skip("GOPHISHFR_MYSQL_TEST_DSN is not set")
	}
	return connectionString
}

func TestMySQLZZPutUserIsolatesAPIKeyState(t *testing.T) {
	connectionString := mysqlAPIKeyTestDSN(t)
	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatal(err)
	}
	db = database
	db.LogMode(false)
	conf = &config.Config{
		DBName: "mysql", DBPath: connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM users").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM users").Error
		_ = database.Close()
	})

	service := testAPIKeyService(t, "mysql-active", "mysql-active")
	SetAPIKeyVerifier(service)
	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	adminRole, err := GetRoleBySlug(RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	const legacyToken = "synthetic-mysql-put-user-legacy-token"
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id) VALUES (?, ?, ?, ?)",
		"mysql-put-user-legacy", "legacy-hash", legacyToken, role.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	user, err := GetUserByUsername("mysql-put-user-legacy")
	if err != nil {
		t.Fatal(err)
	}
	loginTime := time.Date(2026, time.September, 3, 20, 15, 30, 0, time.UTC)
	user.Username = "mysql-put-user-updated"
	user.Hash = "updated-mysql-hash"
	user.RoleID = adminRole.ID
	user.PasswordChangeRequired = true
	user.AccountLocked = true
	user.LastLogin = loginTime
	user.APIKeyVerifier = bytes.Repeat([]byte{0xa5}, 32)
	user.APIKeyVerifierKeyID = "malicious-overwrite"
	if err := PutUser(&user); err != nil {
		t.Fatal(err)
	}

	var plaintext, keyID sql.NullString
	var verifier []byte
	if err := db.DB().QueryRow(
		"SELECT api_key, api_key_verifier, api_key_verifier_key_id FROM users WHERE id = ?", user.Id,
	).Scan(&plaintext, &verifier, &keyID); err != nil {
		t.Fatal(err)
	}
	if !plaintext.Valid || plaintext.String != legacyToken || verifier != nil || keyID.Valid {
		t.Fatal("ordinary MySQL user update changed byte/NULL-identical LEGACY state")
	}
	updated, err := GetUser(user.Id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Username != user.Username || updated.Hash != user.Hash ||
		updated.RoleID != adminRole.ID || !updated.PasswordChangeRequired ||
		!updated.AccountLocked || !updated.LastLogin.Equal(loginTime) {
		t.Fatal("ordinary MySQL user fields did not persist")
	}
	user.AccountLocked = false
	if err := PutUser(&user); err != nil {
		t.Fatal(err)
	}
	migration, err := MigrateAPIKeys(service)
	if err != nil || migration.Updated != 1 {
		t.Fatalf("MySQL LEGACY row failed migration after PutUser: result=%+v err=%v", migration, err)
	}
	if authenticated, err := GetUserByAPIKey(legacyToken); err != nil || authenticated.Id != user.Id {
		t.Fatalf("MySQL migrated token did not authenticate: %v", err)
	}

	migrated, err := GetUser(user.Id)
	if err != nil {
		t.Fatal(err)
	}
	originalVerifier := append([]byte(nil), migrated.APIKeyVerifier...)
	originalKeyID := migrated.APIKeyVerifierKeyID
	migrated.Username = "mysql-migrated-profile-updated"
	migrated.Hash = "updated-migrated-hash"
	migrated.RoleID = role.ID
	migrated.PasswordChangeRequired = false
	migrated.AccountLocked = false
	migrated.LastLogin = loginTime.Add(time.Minute)
	migrated.APIKeyVerifier = bytes.Repeat([]byte{0x5a}, 32)
	migrated.APIKeyVerifierKeyID = "malicious-overwrite"
	if err := PutUser(&migrated); err != nil {
		t.Fatal(err)
	}
	migrated, err = GetUser(user.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(migrated.APIKeyVerifier, originalVerifier) ||
		migrated.APIKeyVerifierKeyID != originalKeyID {
		t.Fatal("ordinary MySQL user update overwrote MIGRATED verifier state")
	}
	if migrated.Username != "mysql-migrated-profile-updated" ||
		migrated.Hash != "updated-migrated-hash" || migrated.RoleID != role.ID ||
		migrated.PasswordChangeRequired || migrated.AccountLocked ||
		!migrated.LastLogin.Equal(loginTime.Add(time.Minute)) {
		t.Fatal("ordinary MySQL MIGRATED user fields did not persist")
	}
	if err := PutUser(&migrated); err != nil {
		t.Fatalf("no-op MySQL user update failed: %v", err)
	}

	resetToken, err := ResetUserAPIKey(migrated.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(legacyToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("dedicated MySQL reset did not atomically invalidate the old token")
	}
	if authenticated, err := GetUserByAPIKey(resetToken); err != nil || authenticated.Id != migrated.Id {
		t.Fatalf("dedicated MySQL reset token did not authenticate: %v", err)
	}
}

func TestMySQLZZLockedAccountRejectsAPIKeyAndLazyRekey(t *testing.T) {
	connectionString := mysqlAPIKeyTestDSN(t)
	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatal(err)
	}
	db = database
	db.LogMode(false)
	conf = &config.Config{
		DBName: "mysql", DBPath: connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM users").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM users").Error
		_ = database.Close()
	})

	service := testAPIKeyService(t, "mysql-active", "mysql-active", "mysql-old")
	SetAPIKeyVerifier(service)
	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	const lockedToken = "synthetic-mysql-locked-migrated-token"
	lockedVerifier, _ := service.Compute("mysql-active", []byte(lockedToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id, account_locked, "+
			"api_key_verifier, api_key_verifier_key_id) VALUES (?, ?, NULL, ?, ?, ?, ?)",
		"mysql-locked-migrated", "synthetic-hash", role.ID, true,
		lockedVerifier[:], "mysql-active",
	).Error; err != nil {
		t.Fatal(err)
	}
	locked, err := GetUserByUsername("mysql-locked-migrated")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(lockedToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("locked MySQL verifier authenticated: %v", err)
	}
	locked.AccountLocked = false
	locked.LastLogin = time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC)
	if err := PutUser(&locked); err != nil {
		t.Fatal(err)
	}
	if authenticated, err := GetUserByAPIKey(lockedToken); err != nil || authenticated.Id != locked.Id {
		t.Fatalf("unlock did not restore the same MySQL token: %v", err)
	}

	createdLocked := User{
		Username: "mysql-created-locked", Hash: "synthetic-hash",
		RoleID: role.ID, AccountLocked: true,
		LastLogin: time.Date(2026, time.September, 4, 3, 1, 0, 0, time.UTC),
	}
	createdToken, err := CreateUserWithAPIKey(&createdLocked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(createdToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("MySQL create made a locked user's token usable")
	}
	resetToken, err := ResetUserAPIKey(createdLocked.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(resetToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("MySQL reset made a locked user's token usable")
	}
	createdLocked.AccountLocked = false
	if err := PutUser(&createdLocked); err != nil {
		t.Fatal(err)
	}
	if authenticated, err := GetUserByAPIKey(resetToken); err != nil || authenticated.Id != createdLocked.Id {
		t.Fatalf("unlock did not restore reset MySQL token: %v", err)
	}

	const lazyToken = "synthetic-mysql-lock-during-lazy-token"
	oldVerifier, _ := service.Compute("mysql-old", []byte(lazyToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id, account_locked, "+
			"api_key_verifier, api_key_verifier_key_id) VALUES (?, ?, NULL, ?, ?, ?, ?)",
		"mysql-lock-during-lazy", "synthetic-hash", role.ID, false,
		oldVerifier[:], "mysql-old",
	).Error; err != nil {
		t.Fatal(err)
	}
	staleUnlocked, err := GetUserByUsername("mysql-lock-during-lazy")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET account_locked = ? WHERE id = ?", true, staleUnlocked.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := lazyUpgradeAPIKeyVerifier(&staleUnlocked, []byte(lazyToken), service); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("MySQL lazy rekey accepted a concurrently locked account: %v", err)
	}
	var keyID string
	if err := db.Raw(
		"SELECT api_key_verifier_key_id FROM users WHERE id = ?", staleUnlocked.Id,
	).Row().Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != "mysql-old" {
		t.Fatal("MySQL concurrent lock allowed lazy verifier update")
	}
	if _, err := GetUserByAPIKey(lazyToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("concurrently locked MySQL account authenticated: %v", err)
	}
}

func TestMySQLZZRuntimeRequiresVerifierOnlyState(t *testing.T) {
	connectionString := mysqlAPIKeyTestDSN(t)
	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatal(err)
	}
	db = database
	db.LogMode(false)
	conf = &config.Config{
		DBName: "mysql", DBPath: connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM users").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM users").Error
		_ = database.Close()
	})

	service := testAPIKeyService(t, "mysql-active", "mysql-active", "mysql-old")
	SetAPIKeyVerifier(service)
	const activeToken = "synthetic-mysql-matching-both-token"
	activeVerifier, _ := service.Compute("mysql-active", []byte(activeToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, api_key_verifier, api_key_verifier_key_id) "+
			"VALUES (?, ?, ?, ?, ?)",
		"mysql-matching-both", "synthetic-hash", activeToken, activeVerifier[:], "mysql-active",
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(activeToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("MySQL matching controlled BOTH state authenticated: %v", err)
	}
	migration, err := MigrateAPIKeys(service)
	if err != nil || migration.Updated != 1 {
		t.Fatalf("clear MySQL controlled BOTH state: result=%+v err=%v", migration, err)
	}
	activeUser, err := GetUserByAPIKey(activeToken)
	if err != nil {
		t.Fatalf("cleared MySQL verifier-only state did not authenticate: %v", err)
	}
	resetToken, err := ResetUserAPIKey(activeUser.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(activeToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("MySQL reset did not invalidate the old token")
	}
	if _, err := GetUserByAPIKey(resetToken); err != nil {
		t.Fatalf("MySQL reset token did not authenticate: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key) VALUES (?, ?, ?)",
		"mysql-legacy-runtime", "synthetic-hash", "synthetic-mysql-runtime-legacy-token",
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey("synthetic-mysql-runtime-legacy-token"); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("MySQL LEGACY state authenticated at runtime: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key) VALUES (?, ?, NULL)",
		"mysql-absent-runtime", "synthetic-hash",
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey("synthetic-mysql-runtime-absent-token"); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("MySQL all-absent state authenticated at runtime: %v", err)
	}

	const oldBothToken = "synthetic-mysql-old-both-token"
	oldBothVerifier, _ := service.Compute("mysql-old", []byte(oldBothToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, api_key_verifier, api_key_verifier_key_id) "+
			"VALUES (?, ?, ?, ?, ?)",
		"mysql-old-both", "synthetic-hash", oldBothToken, oldBothVerifier[:], "mysql-old",
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(oldBothToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("MySQL old-key controlled BOTH state authenticated: %v", err)
	}
	var keyID string
	if err := db.Raw(
		"SELECT api_key_verifier_key_id FROM users WHERE username = ?", "mysql-old-both",
	).Row().Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != "mysql-old" {
		t.Fatal("MySQL rejected old-key BOTH state was lazily rekeyed")
	}

	const lazyToken = "synthetic-mysql-normal-lazy-token"
	lazyVerifier, _ := service.Compute("mysql-old", []byte(lazyToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, api_key_verifier, api_key_verifier_key_id) "+
			"VALUES (?, ?, NULL, ?, ?)",
		"mysql-normal-lazy", "synthetic-hash", lazyVerifier[:], "mysql-old",
	).Error; err != nil {
		t.Fatal(err)
	}
	lazyUser, err := GetUserByAPIKey(lazyToken)
	if err != nil {
		t.Fatalf("MySQL verifier-only old-key state did not authenticate: %v", err)
	}
	if current, err := GetUser(lazyUser.Id); err != nil || current.APIKeyVerifierKeyID != "mysql-active" {
		t.Fatalf("MySQL normal lazy rekey failed: %v", err)
	}

	const transitionToken = "synthetic-mysql-reread-both-token"
	transitionVerifier, _ := service.Compute("mysql-old", []byte(transitionToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, api_key_verifier, api_key_verifier_key_id) "+
			"VALUES (?, ?, NULL, ?, ?)",
		"mysql-reread-both", "synthetic-hash", transitionVerifier[:], "mysql-old",
	).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err := GetUserByUsername("mysql-reread-both")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET api_key = ? WHERE id = ?", transitionToken, snapshot.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := lazyUpgradeAPIKeyVerifier(&snapshot, []byte(transitionToken), service); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("MySQL lazy CAS reread accepted transition to BOTH: %v", err)
	}
	if err := db.Raw(
		"SELECT api_key_verifier_key_id FROM users WHERE id = ?", snapshot.Id,
	).Row().Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != "mysql-old" {
		t.Fatal("MySQL transition to BOTH was lazily rekeyed")
	}
}

func TestMySQLZZAPIKeyVerifierLifecycle(t *testing.T) {
	connectionString := mysqlAPIKeyTestDSN(t)
	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatal(err)
	}
	db = database
	db.LogMode(false)
	conf = &config.Config{
		DBName: "mysql", DBPath: connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM users").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = goose.SetDialect("mysql")
		_ = db.Exec("DELETE FROM users").Error
		_ = goose.Up(db.DB(), conf.MigrationsPath)
		_ = database.Close()
	})
	service := testAPIKeyService(t, "mysql-active", "mysql-active")
	SetAPIKeyVerifier(service)
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key) VALUES (?, ?, ?)",
		"mysql-legacy-user", "synthetic-hash", "synthetic-mysql-legacy-token",
	).Error; err != nil {
		t.Fatal(err)
	}
	migration, err := MigrateAPIKeys(service)
	if err != nil || migration.Updated != 1 {
		t.Fatalf("real MySQL migration failed: %+v %v", migration, err)
	}
	if _, err := GetUserByAPIKey("synthetic-mysql-legacy-token"); err != nil {
		t.Fatal("unchanged MySQL legacy token did not authenticate")
	}
	var plaintext sql.NullString
	var verifier []byte
	var keyID string
	if err := db.DB().QueryRow(
		"SELECT api_key, api_key_verifier, api_key_verifier_key_id FROM users LIMIT 1",
	).Scan(&plaintext, &verifier, &keyID); err != nil {
		t.Fatal(err)
	}
	if plaintext.Valid || len(verifier) != 32 || keyID != "mysql-active" {
		t.Fatal("MySQL verifier storage/readback contract failed")
	}
	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("MySQL guarded Down accepted verifier-only data")
	}

	// Non-strict MySQL may truncate an oversized key ID instead of returning a
	// write error. Exact state validation must still block migration.
	if err := db.Exec("SET SESSION sql_mode = ''").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET api_key = ?, api_key_verifier = ?, api_key_verifier_key_id = ?",
		"synthetic-mysql-both", make([]byte, 32), strings.Repeat("x", 65),
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateAPIKeys(service); err == nil {
		t.Fatal("non-strict MySQL truncation/malformed readback was accepted")
	}
}
