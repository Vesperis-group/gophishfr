package models

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/pressly/goose/v3"
)

func TestMySQLZZAPIKeyVerifierLifecycle(t *testing.T) {
	connectionString := os.Getenv("GOPHISHFR_MYSQL_TEST_DSN")
	if connectionString == "" {
		t.Skip("GOPHISHFR_MYSQL_TEST_DSN is not set")
	}
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
