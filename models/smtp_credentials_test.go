package models

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/jinzhu/gorm"
	"github.com/pressly/goose/v3"
)

const (
	testSMTPSecret      = "synthetic-smtp-secret-alpha"
	testSMTPReplacement = "synthetic-smtp-secret-beta"
)

func validModelSMTP(password string) SMTP {
	return SMTP{
		UserId:      1,
		Interface:   "SMTP",
		Name:        "Synthetic SMTP",
		Host:        "127.0.0.1:2525",
		Username:    "smtp-user",
		Password:    password,
		FromAddress: "sender@example.test",
		Headers: []Header{{
			Key:   "X-Synthetic",
			Value: "created",
		}},
	}
}

func loadOnlySMTP(t *testing.T, id, userID int64) SMTP {
	t.Helper()
	profile, err := GetSMTP(id, userID)
	if err != nil {
		t.Fatalf("load SMTP profile: %v", err)
	}
	return profile
}

func TestSMTPCredentialRuntimeLifecycle(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	oldCipher := testCredentialCipher(t, "smtp-key-old", map[string][]byte{
		"smtp-key-old": bytes.Repeat([]byte{0x21}, 32),
		"smtp-key-new": bytes.Repeat([]byte{0x72}, 32),
	})

	profile := validModelSMTP(testSMTPSecret)
	if err := PostSMTP(&profile, oldCipher); err != nil {
		t.Fatalf("create encrypted SMTP profile: %v", err)
	}
	if profile.Id <= 0 {
		t.Fatal("create did not assign an immutable profile ID")
	}
	stored := loadOnlySMTP(t, profile.Id, profile.UserId)
	if stored.Password != "" {
		t.Fatal("create retained legacy plaintext")
	}
	if stored.PasswordCiphertext == "" || strings.Contains(stored.PasswordCiphertext, testSMTPSecret) {
		t.Fatal("create did not store opaque ciphertext")
	}
	if plaintext, err := DecryptSMTPPassword(stored, oldCipher); err != nil || plaintext != testSMTPSecret {
		t.Fatalf("decrypt created password: matched=%t err=%v", plaintext == testSMTPSecret, err)
	}
	dialer, err := stored.GetDialer(oldCipher)
	if err != nil {
		t.Fatalf("construct encrypted SMTP dialer: %v", err)
	}
	if got := dialer.(*Dialer).Password; got != testSMTPSecret {
		t.Fatal("SMTP boundary did not use the decrypted credential")
	}
	campaignMail := MailLog{cachedCampaign: &Campaign{SMTP: stored}}
	campaignDialer, err := campaignMail.GetDialer(oldCipher)
	if err != nil {
		t.Fatalf("construct campaign SMTP dialer: %v", err)
	}
	if got := campaignDialer.(*Dialer).Password; got != testSMTPSecret {
		t.Fatal("campaign send boundary did not use the decrypted credential")
	}

	for _, moved := range []SMTP{
		{Id: stored.Id + 1, UserId: stored.UserId, PasswordCiphertext: stored.PasswordCiphertext},
		{Id: stored.Id, UserId: stored.UserId + 1, PasswordCiphertext: stored.PasswordCiphertext},
	} {
		if _, err := DecryptSMTPPassword(moved, oldCipher); !errors.Is(err, ErrSMTPCredentialUnavailable) {
			t.Fatalf("moved ciphertext error = %v, want unavailable", err)
		}
	}

	originalCiphertext := stored.PasswordCiphertext
	update := stored
	update.Password = ""
	update.Host = "localhost:2526"
	update.Username = "renamed-user"
	update.Name = "Updated SMTP"
	update.Headers = []Header{{Key: "X-Synthetic", Value: "updated"}}
	if err := PutSMTP(&update, oldCipher); err != nil {
		t.Fatalf("preserve encrypted SMTP password: %v", err)
	}
	stored = loadOnlySMTP(t, profile.Id, profile.UserId)
	if stored.PasswordCiphertext != originalCiphertext {
		t.Fatal("empty update did not preserve ciphertext byte-for-byte")
	}
	if plaintext, err := DecryptSMTPPassword(stored, oldCipher); err != nil || plaintext != testSMTPSecret {
		t.Fatalf("decrypt preserved password: matched=%t err=%v", plaintext == testSMTPSecret, err)
	}

	rotatedCipher := testCredentialCipher(t, "smtp-key-new", map[string][]byte{
		"smtp-key-old": bytes.Repeat([]byte{0x21}, 32),
		"smtp-key-new": bytes.Repeat([]byte{0x72}, 32),
	})
	update.Password = testSMTPReplacement
	if err := PutSMTP(&update, rotatedCipher); err != nil {
		t.Fatalf("rotate SMTP password: %v", err)
	}
	rotated := loadOnlySMTP(t, profile.Id, profile.UserId)
	if rotated.Password != "" || rotated.PasswordCiphertext == originalCiphertext {
		t.Fatal("rotation did not replace ciphertext and clear legacy plaintext")
	}
	if plaintext, err := DecryptSMTPPassword(rotated, rotatedCipher); err != nil || plaintext != testSMTPReplacement {
		t.Fatalf("decrypt rotated password: matched=%t err=%v", plaintext == testSMTPReplacement, err)
	}
	if plaintext, err := DecryptSMTPPassword(stored, rotatedCipher); err != nil || plaintext != testSMTPSecret {
		t.Fatalf("decrypt retained old-key password: matched=%t err=%v", plaintext == testSMTPSecret, err)
	}

	wrongCipher := testCredentialCipher(t, "smtp-key-new", map[string][]byte{
		"smtp-key-new": bytes.Repeat([]byte{0x7f}, 32),
	})
	if _, err := rotated.GetDialer(wrongCipher); !errors.Is(err, ErrSMTPCredentialUnavailable) {
		t.Fatalf("wrong-key dialer error = %v, want unavailable", err)
	}
	if _, err := rotated.GetDialer(nil); !errors.Is(err, ErrSMTPCredentialKeyringRequired) {
		t.Fatalf("missing-keyring dialer error = %v, want keyring required", err)
	}
	unknownKey := rotated
	unknownKey.PasswordCiphertext = strings.Replace(
		unknownKey.PasswordCiphertext,
		"smtp-key-new",
		"missing-key1",
		1,
	)
	if _, err := unknownKey.GetDialer(rotatedCipher); !errors.Is(err, ErrSMTPCredentialUnavailable) {
		t.Fatalf("unknown-key dialer error = %v, want unavailable", err)
	}
	tampered := rotated
	replacement := "A"
	if strings.HasSuffix(tampered.PasswordCiphertext, replacement) {
		replacement = "B"
	}
	tampered.PasswordCiphertext = tampered.PasswordCiphertext[:len(tampered.PasswordCiphertext)-1] + replacement
	if _, err := tampered.GetDialer(rotatedCipher); !errors.Is(err, ErrSMTPCredentialUnavailable) {
		t.Fatalf("tampered dialer error = %v, want unavailable", err)
	}
	tamperedCampaignMail := MailLog{cachedCampaign: &Campaign{SMTP: tampered}}
	if _, err := tamperedCampaignMail.GetDialer(rotatedCipher); !errors.Is(err, ErrSMTPCredentialUnavailable) {
		t.Fatalf("tampered campaign dialer error = %v, want unavailable", err)
	}
}

func TestSMTPNoSecretAndTransactionalWrites(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x33}, 32),
	})

	noAuth := validModelSMTP("")
	noAuth.Username = ""
	if err := PostSMTP(&noAuth, nil); err != nil {
		t.Fatalf("create no-auth SMTP profile: %v", err)
	}
	stored := loadOnlySMTP(t, noAuth.Id, noAuth.UserId)
	if stored.Password != "" || stored.PasswordCiphertext != "" {
		t.Fatal("no-auth profile persisted credential data")
	}
	if _, err := stored.GetDialer(nil); err != nil {
		t.Fatalf("construct no-auth dialer without keyring: %v", err)
	}

	emptyAuth := validModelSMTP("")
	emptyAuth.Name = "Empty password AUTH"
	if err := PostSMTP(&emptyAuth, nil); err != nil {
		t.Fatalf("create legacy-compatible empty-password AUTH profile: %v", err)
	}
	if got := loadOnlySMTP(t, emptyAuth.Id, emptyAuth.UserId); got.PasswordCiphertext != "" {
		t.Fatal("empty password was encrypted")
	}

	missingKeyring := validModelSMTP(testSMTPSecret)
	missingKeyring.Name = "Missing keyring"
	if err := PostSMTP(&missingKeyring, nil); !errors.Is(err, ErrSMTPCredentialKeyringRequired) {
		t.Fatalf("missing-keyring create error = %v", err)
	}
	var count int
	if err := db.Model(&SMTP{}).Where("name = ?", missingKeyring.Name).Count(&count).Error; err != nil {
		t.Fatalf("count rolled-back profile: %v", err)
	}
	if count != 0 {
		t.Fatal("failed encrypted create left an incomplete row")
	}

	if err := db.Exec(`
		CREATE TRIGGER reject_smtp_header
		BEFORE INSERT ON headers
		WHEN NEW.value = 'reject'
		BEGIN
			SELECT RAISE(ABORT, 'synthetic header interruption');
		END
	`).Error; err != nil {
		t.Fatalf("create header interruption trigger: %v", err)
	}
	interrupted := validModelSMTP(testSMTPSecret)
	interrupted.Name = "Interrupted"
	interrupted.Headers[0].Value = "reject"
	if err := PostSMTP(&interrupted, cipher); err == nil {
		t.Fatal("header-interrupted create unexpectedly succeeded")
	}
	if err := db.Model(&SMTP{}).Where("name = ?", interrupted.Name).Count(&count).Error; err != nil {
		t.Fatalf("count interrupted profile: %v", err)
	}
	if count != 0 {
		t.Fatal("header failure left a partially-created SMTP row")
	}
}

func TestSMTPMutationsEnforceOwner(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x44}, 32),
	})
	profile := validModelSMTP(testSMTPSecret)
	if err := PostSMTP(&profile, cipher); err != nil {
		t.Fatalf("seed SMTP profile: %v", err)
	}

	original := loadOnlySMTP(t, profile.Id, profile.UserId)

	foreignUpdate := original
	foreignUpdate.UserId = 2
	foreignUpdate.Password = testSMTPReplacement
	if err := PutSMTP(&foreignUpdate, cipher); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-user update error = %v, want not found", err)
	}
	if err := DeleteSMTP(profile.Id, 2); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-user delete error = %v, want not found", err)
	}
	after := loadOnlySMTP(t, profile.Id, profile.UserId)
	if after.PasswordCiphertext != original.PasswordCiphertext || len(after.Headers) != 1 {
		t.Fatal("cross-user mutation changed the profile or headers")
	}
}

func TestSMTPUpdateRollsBackProfileAndHeaders(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x45}, 32),
	})
	profile := validModelSMTP(testSMTPSecret)
	if err := PostSMTP(&profile, cipher); err != nil {
		t.Fatalf("seed SMTP profile: %v", err)
	}
	before := loadOnlySMTP(t, profile.Id, profile.UserId)
	if err := db.Exec(`
		CREATE TRIGGER reject_smtp_update_header
		BEFORE INSERT ON headers
		WHEN NEW.value = 'reject-update'
		BEGIN
			SELECT RAISE(ABORT, 'synthetic update interruption');
		END
	`).Error; err != nil {
		t.Fatalf("create update interruption trigger: %v", err)
	}
	update := before
	update.Name = "Must roll back"
	update.Password = testSMTPReplacement
	update.Headers = []Header{{Key: "X-Synthetic", Value: "reject-update"}}
	if err := PutSMTP(&update, cipher); err == nil {
		t.Fatal("header-interrupted update unexpectedly succeeded")
	}
	after := loadOnlySMTP(t, profile.Id, profile.UserId)
	if after.Name != before.Name ||
		after.PasswordCiphertext != before.PasswordCiphertext ||
		len(after.Headers) != 1 ||
		after.Headers[0].Value != before.Headers[0].Value {
		t.Fatal("failed header update changed the profile, credential, or headers")
	}
}

func TestOfflineSMTPCredentialMigrationAndRollback(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x55}, 32),
	})
	if err := db.Exec(`
		INSERT INTO smtp (user_id, name, host, username, password, password_ciphertext, from_address)
		VALUES
			(1, 'Legacy SMTP', 'localhost:25', 'legacy-user', ?, '', 'sender@example.test'),
			(1, 'No Auth SMTP', 'localhost:25', '', '', '', 'sender@example.test'),
			(1, 'Legacy NULL No Auth SMTP', 'localhost:25', '', NULL, '', 'sender@example.test')
	`, testSMTPSecret).Error; err != nil {
		t.Fatalf("seed SMTP migration rows: %v", err)
	}
	encrypted := validModelSMTP(testSMTPReplacement)
	encrypted.Name = "Already encrypted"
	if err := PostSMTP(&encrypted, cipher); err != nil {
		t.Fatalf("seed encrypted SMTP profile: %v", err)
	}
	originalCiphertext := encrypted.PasswordCiphertext

	result, err := MigrateSMTPCredentials(cipher)
	if err != nil || result.Updated != 1 || result.Unchanged != 3 {
		t.Fatalf("SMTP migration result = %+v err=%v", result, err)
	}
	result, err = MigrateSMTPCredentials(cipher)
	if err != nil || result.Updated != 0 || result.Unchanged != 4 {
		t.Fatalf("idempotent SMTP migration result = %+v err=%v", result, err)
	}
	storedEncrypted := loadOnlySMTP(t, encrypted.Id, encrypted.UserId)
	if storedEncrypted.PasswordCiphertext != originalCiphertext {
		t.Fatal("migration changed an already-valid ciphertext")
	}

	var rows []storedSMTPCredential
	if err := db.Raw(`
		SELECT id, user_id, COALESCE(password, '') AS password, password_ciphertext
		FROM smtp
		ORDER BY id
	`).Scan(&rows).Error; err != nil {
		t.Fatalf("read migrated SMTP rows: %v", err)
	}
	for _, row := range rows {
		if row.Password != "" {
			t.Fatalf("profile %d retained legacy plaintext", row.ID)
		}
		if row.PasswordCiphertext == "" {
			continue
		}
		if plaintext, decryptErr := DecryptSMTPPassword(SMTP{
			Id: row.ID, UserId: row.UserID, PasswordCiphertext: row.PasswordCiphertext,
		}, cipher); decryptErr != nil || plaintext == "" {
			t.Fatalf("decrypt migrated profile %d: err=%v", row.ID, decryptErr)
		}
	}

	wrongCipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x56}, 32),
	})
	if _, err := RollbackSMTPCredentials(wrongCipher); !errors.Is(err, ErrSMTPCredentialUnavailable) {
		t.Fatalf("wrong-key rollback error = %v, want unavailable", err)
	}
	result, err = RollbackSMTPCredentials(cipher)
	if err != nil || result.Updated != 2 || result.Unchanged != 2 {
		t.Fatalf("SMTP rollback result = %+v err=%v", result, err)
	}
	result, err = RollbackSMTPCredentials(cipher)
	if err != nil || result.Updated != 0 || result.Unchanged != 4 {
		t.Fatalf("idempotent SMTP rollback result = %+v err=%v", result, err)
	}
}

func TestOfflineSMTPCredentialMigrationRollsBackOnFailure(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x66}, 32),
	})
	for _, name := range []string{"First legacy", "Second legacy"} {
		if err := db.Exec(`
			INSERT INTO smtp (user_id, name, host, username, password, password_ciphertext, from_address)
			VALUES (1, ?, 'localhost:25', 'legacy-user', ?, '', 'sender@example.test')
		`, name, testSMTPSecret).Error; err != nil {
			t.Fatalf("seed legacy SMTP row: %v", err)
		}
	}
	if err := db.Exec(`
		CREATE TRIGGER interrupt_smtp_migration
		BEFORE UPDATE ON smtp
		WHEN NEW.name = 'Second legacy'
		BEGIN
			SELECT RAISE(ABORT, 'synthetic SMTP migration interruption');
		END
	`).Error; err != nil {
		t.Fatalf("create migration interruption trigger: %v", err)
	}
	if _, err := MigrateSMTPCredentials(cipher); err == nil {
		t.Fatal("interrupted SMTP migration unexpectedly succeeded")
	}
	var legacy, encrypted int
	if err := db.Raw(`
		SELECT
			SUM(CASE WHEN password <> '' THEN 1 ELSE 0 END),
			SUM(CASE WHEN password_ciphertext <> '' THEN 1 ELSE 0 END)
		FROM smtp
	`).Row().Scan(&legacy, &encrypted); err != nil {
		t.Fatalf("inspect rolled-back migration: %v", err)
	}
	if legacy != 2 || encrypted != 0 {
		t.Fatal("failed SMTP migration exposed a partial update")
	}
}

func TestSMTPCredentialStateAndBackendValidation(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x77}, 32),
	})
	if err := db.Exec(`
		INSERT INTO smtp (user_id, name, host, password, password_ciphertext, from_address)
		VALUES (1, 'Ambiguous SMTP', 'localhost:25', ?, 'malformed', 'sender@example.test')
	`, testSMTPSecret).Error; err != nil {
		t.Fatalf("seed ambiguous SMTP row: %v", err)
	}
	if _, err := MigrateSMTPCredentials(cipher); !errors.Is(err, ErrSMTPCredentialInvalidState) {
		t.Fatalf("ambiguous-state error = %v, want invalid state", err)
	}
	if _, err := MigrateSMTPCredentials(nil); !errors.Is(err, ErrSMTPCredentialKeyringRequired) {
		t.Fatalf("missing-keyring migration error = %v", err)
	}
	for _, backend := range []string{"postgres", "postgresql", "oracle"} {
		if err := ValidateSMTPCredentialBackend(backend); !errors.Is(err, ErrUnsupportedSMTPCredentialDB) {
			t.Fatalf("backend %q error = %v", backend, err)
		}
	}
}

func TestSQLiteSMTPCredentialSchemaLifecycle(t *testing.T) {
	sqlDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "smtp-schema.db"))
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close SQLite database: %v", err)
		}
	})
	if _, err := sqlDB.Exec(`
		CREATE TABLE smtp (
			id integer primary key autoincrement,
			user_id bigint,
			interface_type varchar(255),
			name varchar(255),
			host varchar(255),
			username varchar(255),
			password varchar(255),
			from_address varchar(255),
			modified_date datetime default CURRENT_TIMESTAMP,
			ignore_cert_errors BOOLEAN
		);
		CREATE TABLE headers (
			id integer primary key autoincrement,
			key varchar(255),
			value varchar(255),
			smtp_id bigint
		);
		INSERT INTO smtp (user_id, name, password) VALUES (1, 'One', ''), (1, 'Two', '');
		INSERT INTO headers (key, value, smtp_id) VALUES ('X-Test', 'value', 1);
	`); err != nil {
		t.Fatalf("create legacy SMTP schema: %v", err)
	}
	migrations := t.TempDir()
	migrationBody, err := os.ReadFile("../db/db_sqlite3/migrations/20260903010000_encrypt_smtp_credentials.sql")
	if err != nil {
		t.Fatalf("read SMTP migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrations, "20260903010000_encrypt_smtp_credentials.sql"), migrationBody, 0o600); err != nil {
		t.Fatalf("write isolated SMTP migration: %v", err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set SQLite dialect: %v", err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		t.Fatalf("apply SMTP credential schema: %v", err)
	}
	if _, err := sqlDB.Exec("UPDATE smtp SET password_ciphertext = 'synthetic-ciphertext' WHERE id = 1"); err != nil {
		t.Fatalf("seed Down guard: %v", err)
	}
	if err := goose.Down(sqlDB, migrations); err == nil {
		t.Fatal("SMTP schema Down accepted ciphertext")
	}
	if _, err := sqlDB.Exec("UPDATE smtp SET password = ?, password_ciphertext = '' WHERE id = 1", testSMTPSecret); err != nil {
		t.Fatalf("prepare SMTP rollback state: %v", err)
	}
	if err := goose.Down(sqlDB, migrations); err != nil {
		t.Fatalf("roll back SMTP credential schema: %v", err)
	}
	var profiles, headers int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM smtp WHERE user_id = 1").Scan(&profiles); err != nil {
		t.Fatalf("count preserved SMTP profiles: %v", err)
	}
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM headers WHERE smtp_id = 1").Scan(&headers); err != nil {
		t.Fatalf("count preserved SMTP headers: %v", err)
	}
	if profiles != 2 || headers != 1 {
		t.Fatalf("schema Down lost profiles or headers: profiles=%d headers=%d", profiles, headers)
	}
}

func TestMySQLSMTPCredentialLifecycle(t *testing.T) {
	connectionString := testingMySQLDSN(t)
	if connectionString == "" {
		return
	}
	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatalf("open MySQL database: %v", err)
	}
	db = database
	db.LogMode(false)
	conf = &config.Config{
		DBName:         "mysql",
		DBPath:         connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatalf("migrate MySQL database: %v", err)
	}
	for _, table := range []string{"imap", "headers", "smtp"} {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("clear MySQL %s rows: %v", table, err)
		}
	}
	t.Cleanup(func() {
		_ = goose.SetDialect("mysql")
		_ = goose.Up(db.DB(), conf.MigrationsPath)
		_ = db.Exec("DELETE FROM headers").Error
		_ = db.Exec("DELETE FROM smtp").Error
		_ = database.Close()
	})

	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x78}, 32),
	})
	if err := db.Exec(`
		INSERT INTO smtp (user_id, name, host, username, password, password_ciphertext, from_address)
		VALUES
			(1, 'MySQL legacy', 'localhost:25', 'mysql-user', ?, '', 'sender@example.test'),
			(1, 'MySQL no auth', 'localhost:25', '', '', '', 'sender@example.test')
	`, testSMTPSecret).Error; err != nil {
		t.Fatalf("seed MySQL SMTP rows: %v", err)
	}
	result, err := MigrateSMTPCredentials(cipher)
	if err != nil || result.Updated != 1 || result.Unchanged != 1 {
		t.Fatalf("migrate MySQL SMTP credentials: result=%+v err=%v", result, err)
	}
	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("set MySQL dialect: %v", err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("MySQL SMTP schema Down accepted ciphertext")
	}
	if _, err := RollbackSMTPCredentials(cipher); err != nil {
		t.Fatalf("roll back MySQL SMTP credentials: %v", err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("roll back MySQL SMTP schema: %v", err)
	}
	var profiles int
	if err := db.Raw("SELECT COUNT(*) FROM smtp WHERE user_id = 1").Row().Scan(&profiles); err != nil {
		t.Fatalf("count old-binary MySQL SMTP rows: %v", err)
	}
	if profiles != 2 {
		t.Fatalf("MySQL schema Down preserved %d profiles, want 2", profiles)
	}
}
