package models

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func assertAPIKeyRaceRejectsBeforeHandler(t *testing.T, token string, mutate func() error) {
	t.Helper()
	handlerCalls := 0
	var mutationErr error
	_, err := getUserByAPIKey(token, func() {
		mutationErr = mutate()
	})
	if mutationErr != nil {
		t.Fatalf("race mutation failed: %v", mutationErr)
	}
	if err == nil {
		handlerCalls++
	}
	if handlerCalls != 0 {
		t.Fatal("stale-token request executed its protected handler")
	}
	if !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("stale credential race returned %v", err)
	}
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

func TestPutUserIsolatesAPIKeyStateSQLite(t *testing.T) {
	service := testAPIKeyService(t, "active", "active")
	setupAPIKeyDatabase(t, service)
	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	adminRole, err := GetRoleBySlug(RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}

	const legacyToken = "synthetic-put-user-legacy-token"
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id) VALUES (?, ?, ?, ?)",
		"put-user-legacy", "legacy-hash", legacyToken, role.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	legacy, err := GetUserByUsername("put-user-legacy")
	if err != nil {
		t.Fatal(err)
	}
	loginTime := time.Date(2026, time.September, 3, 20, 15, 30, 0, time.UTC)
	legacy.Username = "put-user-legacy-updated"
	legacy.Hash = "updated-legacy-hash"
	legacy.RoleID = adminRole.ID
	legacy.PasswordChangeRequired = true
	legacy.AccountLocked = true
	legacy.LastLogin = loginTime
	legacy.APIKeyVerifier = bytes.Repeat([]byte{0xa5}, 32)
	legacy.APIKeyVerifierKeyID = "malicious-overwrite"
	if err := PutUser(&legacy); err != nil {
		t.Fatal(err)
	}

	var plaintext, keyID sql.NullString
	var verifier []byte
	if err := db.DB().QueryRow(
		"SELECT api_key, api_key_verifier, api_key_verifier_key_id FROM users WHERE id = ?", legacy.Id,
	).Scan(&plaintext, &verifier, &keyID); err != nil {
		t.Fatal(err)
	}
	if !plaintext.Valid || plaintext.String != legacyToken || verifier != nil || keyID.Valid {
		t.Fatal("ordinary SQLite user update changed byte/NULL-identical LEGACY state")
	}
	updated, err := GetUser(legacy.Id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Username != legacy.Username || updated.Hash != legacy.Hash ||
		updated.RoleID != adminRole.ID || !updated.PasswordChangeRequired ||
		!updated.AccountLocked || !updated.LastLogin.Equal(loginTime) {
		t.Fatal("ordinary SQLite user fields did not persist")
	}
	legacy.AccountLocked = false
	if err := PutUser(&legacy); err != nil {
		t.Fatal(err)
	}
	migration, err := MigrateAPIKeys(service)
	if err != nil || migration.Updated != 1 {
		t.Fatalf("SQLite LEGACY row failed migration after PutUser: result=%+v err=%v", migration, err)
	}
	if authenticated, err := GetUserByAPIKey(legacyToken); err != nil || authenticated.Id != legacy.Id {
		t.Fatalf("SQLite migrated token did not authenticate: %v", err)
	}

	admin, err := GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	originalVerifier := append([]byte(nil), admin.APIKeyVerifier...)
	originalKeyID := admin.APIKeyVerifierKeyID
	admin.Username = "admin-profile-updated"
	admin.Hash = "updated-admin-hash"
	admin.RoleID = role.ID
	admin.PasswordChangeRequired = true
	admin.AccountLocked = true
	admin.LastLogin = loginTime.Add(time.Minute)
	admin.APIKeyVerifier = bytes.Repeat([]byte{0x5a}, 32)
	admin.APIKeyVerifierKeyID = "malicious-overwrite"
	if err := PutUser(&admin); err != nil {
		t.Fatal(err)
	}
	admin, err = GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(admin.APIKeyVerifier, originalVerifier) || admin.APIKeyVerifierKeyID != originalKeyID {
		t.Fatal("ordinary SQLite user update overwrote MIGRATED verifier state")
	}
	if admin.Username != "admin-profile-updated" || admin.Hash != "updated-admin-hash" ||
		admin.RoleID != role.ID || !admin.PasswordChangeRequired || !admin.AccountLocked ||
		!admin.LastLogin.Equal(loginTime.Add(time.Minute)) {
		t.Fatal("ordinary SQLite MIGRATED user fields did not persist")
	}
	if err := PutUser(&admin); err != nil {
		t.Fatalf("no-op SQLite user update failed: %v", err)
	}
	admin.AccountLocked = false
	if err := PutUser(&admin); err != nil {
		t.Fatal(err)
	}

	resetToken, err := ResetUserAPIKey(admin.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey("synthetic-lifecycle-admin-token"); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("dedicated SQLite reset did not atomically invalidate the old token")
	}
	if authenticated, err := GetUserByAPIKey(resetToken); err != nil || authenticated.Id != admin.Id {
		t.Fatalf("dedicated SQLite reset token did not authenticate: %v", err)
	}
}

func TestRuntimeRequiresVerifierOnlyState(t *testing.T) {
	service := testAPIKeyService(t, "active", "active", "old")
	setupAPIKeyDatabase(t, service)

	const activeToken = "synthetic-lifecycle-admin-token"
	if err := db.Exec("UPDATE users SET api_key = ? WHERE id = 1", activeToken).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(activeToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("matching controlled BOTH state authenticated before migration: %v", err)
	}
	result, err := MigrateAPIKeys(service)
	if err != nil || result.Updated != 1 {
		t.Fatalf("clear matching controlled BOTH state: result=%+v err=%v", result, err)
	}
	if user, err := GetUserByAPIKey(activeToken); err != nil || user.Id != 1 {
		t.Fatalf("verified-and-cleared state did not authenticate: %v", err)
	}

	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id) VALUES (?, ?, ?, ?)",
		"legacy-runtime-user", "synthetic-hash", "synthetic-runtime-legacy-token", role.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey("synthetic-runtime-legacy-token"); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("LEGACY state authenticated at runtime: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id) VALUES (?, ?, NULL, ?)",
		"absent-runtime-user", "synthetic-hash", role.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey("synthetic-runtime-absent-token"); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("all-absent state authenticated at runtime: %v", err)
	}

	const oldBothToken = "synthetic-old-both-token"
	oldBothVerifier, _ := service.Compute("old", []byte(oldBothToken))
	insert := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id, api_key_verifier, api_key_verifier_key_id) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		"old-both-user", "synthetic-hash", oldBothToken, role.ID, oldBothVerifier[:], "old",
	)
	if insert.Error != nil {
		t.Fatal(insert.Error)
	}
	if _, err := GetUserByAPIKey(oldBothToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("old-key controlled BOTH state authenticated: %v", err)
	}
	var oldBothKeyID string
	if err := db.Raw(
		"SELECT api_key_verifier_key_id FROM users WHERE username = ?", "old-both-user",
	).Row().Scan(&oldBothKeyID); err != nil {
		t.Fatal(err)
	}
	if oldBothKeyID != "old" {
		t.Fatal("rejected old-key controlled BOTH state was lazily rekeyed")
	}

	const transitionToken = "synthetic-reread-both-token"
	transitionVerifier, _ := service.Compute("old", []byte(transitionToken))
	insert = db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id, api_key_verifier, api_key_verifier_key_id) "+
			"VALUES (?, ?, NULL, ?, ?, ?)",
		"reread-both-user", "synthetic-hash", role.ID, transitionVerifier[:], "old",
	)
	if insert.Error != nil {
		t.Fatal(insert.Error)
	}
	snapshot, err := GetUserByUsername("reread-both-user")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET api_key = ? WHERE id = ?", transitionToken, snapshot.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := lazyUpgradeAPIKeyVerifier(&snapshot, []byte(transitionToken), service); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("lazy CAS reread accepted transition to controlled BOTH: %v", err)
	}
	var transitionKeyID string
	if err := db.Raw(
		"SELECT api_key_verifier_key_id FROM users WHERE id = ?", snapshot.Id,
	).Row().Scan(&transitionKeyID); err != nil {
		t.Fatal(err)
	}
	if transitionKeyID != "old" {
		t.Fatal("transition to controlled BOTH was lazily rekeyed")
	}
}

func TestLockedAccountRejectsAPIKeyAndLazyRekeySQLite(t *testing.T) {
	service := testAPIKeyService(t, "active", "active", "old")
	setupAPIKeyDatabase(t, service)
	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}

	const lockedToken = "synthetic-sqlite-locked-migrated-token"
	lockedVerifier, _ := service.Compute("active", []byte(lockedToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id, account_locked, "+
			"api_key_verifier, api_key_verifier_key_id) VALUES (?, ?, NULL, ?, ?, ?, ?)",
		"sqlite-locked-migrated", "synthetic-hash", role.ID, true,
		lockedVerifier[:], "active",
	).Error; err != nil {
		t.Fatal(err)
	}
	locked, err := GetUserByUsername("sqlite-locked-migrated")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(lockedToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("locked SQLite verifier authenticated: %v", err)
	}
	locked.AccountLocked = false
	if err := PutUser(&locked); err != nil {
		t.Fatal(err)
	}
	if authenticated, err := GetUserByAPIKey(lockedToken); err != nil || authenticated.Id != locked.Id {
		t.Fatalf("unlock did not restore the same SQLite token: %v", err)
	}

	createdLocked := User{
		Username: "sqlite-created-locked", Hash: "synthetic-hash",
		RoleID: role.ID, AccountLocked: true,
	}
	createdToken, err := CreateUserWithAPIKey(&createdLocked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(createdToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("SQLite create made a locked user's token usable")
	}
	resetToken, err := ResetUserAPIKey(createdLocked.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetUserByAPIKey(resetToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatal("SQLite reset made a locked user's token usable")
	}
	createdLocked.AccountLocked = false
	if err := PutUser(&createdLocked); err != nil {
		t.Fatal(err)
	}
	if authenticated, err := GetUserByAPIKey(resetToken); err != nil || authenticated.Id != createdLocked.Id {
		t.Fatalf("unlock did not restore reset SQLite token: %v", err)
	}

	const lazyToken = "synthetic-sqlite-lock-during-lazy-token"
	oldVerifier, _ := service.Compute("old", []byte(lazyToken))
	if err := db.Exec(
		"INSERT INTO users (username, hash, api_key, role_id, account_locked, "+
			"api_key_verifier, api_key_verifier_key_id) VALUES (?, ?, NULL, ?, ?, ?, ?)",
		"sqlite-lock-during-lazy", "synthetic-hash", role.ID, false,
		oldVerifier[:], "old",
	).Error; err != nil {
		t.Fatal(err)
	}
	staleUnlocked, err := GetUserByUsername("sqlite-lock-during-lazy")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET account_locked = ? WHERE id = ?", true, staleUnlocked.Id,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := lazyUpgradeAPIKeyVerifier(&staleUnlocked, []byte(lazyToken), service); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("SQLite lazy rekey accepted a concurrently locked account: %v", err)
	}
	var keyID string
	if err := db.Raw(
		"SELECT api_key_verifier_key_id FROM users WHERE id = ?", staleUnlocked.Id,
	).Row().Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != "old" {
		t.Fatal("SQLite concurrent lock allowed lazy verifier update")
	}
	if _, err := GetUserByAPIKey(lazyToken); !errors.Is(err, ErrInvalidAPICredential) {
		t.Fatalf("concurrently locked SQLite account authenticated: %v", err)
	}
}

func TestAPIKeyFinalStateLinearizesResetAndLockSQLite(t *testing.T) {
	service := testAPIKeyService(t, "active", "active", "old")
	setupAPIKeyDatabase(t, service)
	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	seed := func(username, token, keyID string) User {
		t.Helper()
		digest, err := service.Compute(keyID, []byte(token))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(
			"INSERT INTO users (username, hash, api_key, role_id, account_locked, "+
				"api_key_verifier, api_key_verifier_key_id) VALUES (?, ?, NULL, ?, ?, ?, ?)",
			username, "synthetic-hash", role.ID, false, digest[:], keyID,
		).Error; err != nil {
			t.Fatal(err)
		}
		user, err := GetUserByUsername(username)
		if err != nil {
			t.Fatal(err)
		}
		return user
	}

	active := seed("sqlite-active-reset-race", "synthetic-sqlite-active-reset-race", "active")
	var replacement string
	assertAPIKeyRaceRejectsBeforeHandler(t, "synthetic-sqlite-active-reset-race", func() error {
		var err error
		replacement, err = ResetUserAPIKey(active.Id)
		return err
	})
	if authenticated, err := GetUserByAPIKey(replacement); err != nil || authenticated.Id != active.Id {
		t.Fatalf("active-race replacement did not authenticate: %v", err)
	}

	lazyReset := seed("sqlite-lazy-reset-race", "synthetic-sqlite-lazy-reset-race", "old")
	assertAPIKeyRaceRejectsBeforeHandler(t, "synthetic-sqlite-lazy-reset-race", func() error {
		_, err := ResetUserAPIKey(lazyReset.Id)
		return err
	})

	lazyLock := seed("sqlite-lazy-lock-race", "synthetic-sqlite-lazy-lock-race", "old")
	assertAPIKeyRaceRejectsBeforeHandler(t, "synthetic-sqlite-lazy-lock-race", func() error {
		return db.Model(&User{}).Where("id = ?", lazyLock.Id).
			Update("account_locked", true).Error
	})
	var keyID string
	if err := db.Raw(
		"SELECT api_key_verifier_key_id FROM users WHERE id = ?", lazyLock.Id,
	).Row().Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != "active" {
		t.Fatal("lazy race did not reach active-key resolution before final lock check")
	}
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
	// Land exactly on the API-key verifier migration's own version first,
	// regardless of any later migration (such as event details encryption)
	// now sitting on top of it, so the guarded Down attempted next actually
	// targets this migration instead of whichever is newest.
	if err := goose.DownTo(db.DB(), conf.MigrationsPath, 20260903030000); err != nil {
		t.Fatalf("remove later migrations: %v", err)
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

func TestOfflineAPIKeyMigrationPreservesExactLongTokensSQLite(t *testing.T) {
	service := testAPIKeyService(t, "active", "active")
	setupAPIKeyDatabase(t, service)
	role, err := GetRoleBySlug(RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	tokens := []struct {
		username string
		value    string
	}{
		{username: "sqlite-long-byte-token", value: strings.Repeat("synthetic-long-token-", 20)},
		{username: "sqlite-multibyte-token", value: strings.Repeat("界", 255)},
	}
	for _, token := range tokens {
		if len([]byte(token.value)) <= 255 {
			t.Fatal("test token does not exercise the legacy byte-length boundary")
		}
		if _, err := db.DB().Exec(
			"INSERT INTO users (username, hash, api_key, role_id) VALUES (?, ?, ?, ?)",
			token.username, "synthetic-hash", token.value, role.ID,
		); err != nil {
			t.Fatal(err)
		}
	}

	result, err := MigrateAPIKeys(service)
	if err != nil || result.Updated != len(tokens) || result.Unchanged != 1 {
		t.Fatalf("SQLite exact long-token migration failed: result=%+v err=%v", result, err)
	}
	for _, token := range tokens {
		authenticated, err := GetUserByAPIKey(token.value)
		if err != nil || authenticated.Username != token.username {
			t.Fatalf("SQLite changed exact stored token bytes for %s: %v", token.username, err)
		}
		if _, err := GetUserByAPIKey(string([]byte(token.value)[:255])); !errors.Is(err, ErrInvalidAPICredential) {
			t.Fatalf("SQLite authenticated a byte-truncated token for %s", token.username)
		}
	}
	result, err = MigrateAPIKeys(service)
	if err != nil || result.Updated != 0 || result.Unchanged != len(tokens)+1 {
		t.Fatalf("SQLite long-token migration rerun was not idempotent: result=%+v err=%v", result, err)
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
