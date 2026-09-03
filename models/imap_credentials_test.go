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
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	"github.com/pressly/goose/v3"
)

const (
	testIMAPSecret      = "synthetic-imap-secret-alpha"
	testIMAPReplacement = "synthetic-imap-secret-beta"
)

func testCredentialCipher(t *testing.T, active string, keys map[string][]byte) *credentials.Cipher {
	t.Helper()
	keyring, err := credentials.NewKeyring(active, keys)
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	credentialCipher, err := credentials.New(keyring)
	if err != nil {
		t.Fatalf("create credential cipher: %v", err)
	}
	return credentialCipher
}

func testCredentialKeys() map[string][]byte {
	return map[string][]byte{
		"imap-key-old": bytes.Repeat([]byte{0x31}, 32),
		"imap-key-new": bytes.Repeat([]byte{0x72}, 32),
	}
}

func setupIMAPCredentialDatabase(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv(InitialAdminPassword, "synthetic-test-admin-password")
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         filepath.Join(t.TempDir(), "imap-credentials.db"),
		MigrationsPath: "../db/db_sqlite3/migrations",
	}
	if err := Setup(conf); err != nil {
		t.Fatalf("setup credential database: %v", err)
	}
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
	})
	return conf
}

func validModelIMAP(password string) IMAP {
	return IMAP{
		Enabled:          false,
		Host:             "localhost",
		Port:             993,
		Username:         "imap-test-user",
		Password:         password,
		TLS:              true,
		Folder:           "INBOX",
		IMAPFreq:         60,
		RestrictDomain:   "example.test",
		IgnoreCertErrors: true,
	}
}

func loadOnlyIMAP(t *testing.T, userID int64) IMAP {
	t.Helper()
	settings, err := GetIMAP(userID)
	if err != nil {
		t.Fatalf("load IMAP settings: %v", err)
	}
	if len(settings) != 1 {
		t.Fatalf("got %d IMAP rows for user %d, want 1", len(settings), userID)
	}
	return settings[0]
}

func TestIMAPCredentialRuntimeLifecycle(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	oldCipher := testCredentialCipher(t, "imap-key-old", testCredentialKeys())

	settings := validModelIMAP(testIMAPSecret)
	if err := PostIMAP(&settings, 41, oldCipher); err != nil {
		t.Fatalf("create encrypted IMAP settings: %v", err)
	}
	stored := loadOnlyIMAP(t, 41)
	if stored.Password != "" {
		t.Fatal("create retained legacy plaintext")
	}
	if stored.PasswordCiphertext == "" || strings.Contains(stored.PasswordCiphertext, testIMAPSecret) {
		t.Fatal("create did not store opaque ciphertext")
	}
	if plaintext, err := DecryptIMAPPassword(stored, oldCipher); err != nil || plaintext != testIMAPSecret {
		t.Fatalf("decrypt created password: plaintext matched=%t err=%v", plaintext == testIMAPSecret, err)
	}
	if _, err := DecryptIMAPPassword(IMAP{
		UserId:             42,
		PasswordCiphertext: stored.PasswordCiphertext,
	}, oldCipher); !errors.Is(err, ErrIMAPCredentialUnavailable) {
		t.Fatalf("cross-user ciphertext error = %v, want unavailable", err)
	}

	originalCiphertext := stored.PasswordCiphertext
	preserve := validModelIMAP("")
	preserve.Host = "127.0.0.1"
	if err := PostIMAP(&preserve, 41, oldCipher); err != nil {
		t.Fatalf("preserve encrypted password: %v", err)
	}
	stored = loadOnlyIMAP(t, 41)
	if stored.PasswordCiphertext != originalCiphertext {
		t.Fatal("empty password did not preserve ciphertext byte-for-byte")
	}

	rotatedCipher := testCredentialCipher(t, "imap-key-new", testCredentialKeys())
	replacement := validModelIMAP(testIMAPReplacement)
	if err := PostIMAP(&replacement, 41, rotatedCipher); err != nil {
		t.Fatalf("replace encrypted password: %v", err)
	}
	replaced := loadOnlyIMAP(t, 41)
	if replaced.Password != "" || replaced.PasswordCiphertext == originalCiphertext {
		t.Fatal("replacement did not atomically rotate ciphertext")
	}
	if plaintext, err := DecryptIMAPPassword(replaced, rotatedCipher); err != nil || plaintext != testIMAPReplacement {
		t.Fatalf("decrypt replacement: plaintext matched=%t err=%v", plaintext == testIMAPReplacement, err)
	}
	oldRecord := IMAP{UserId: 41, PasswordCiphertext: originalCiphertext}
	if plaintext, err := DecryptIMAPPassword(oldRecord, rotatedCipher); err != nil || plaintext != testIMAPSecret {
		t.Fatalf("decrypt retained old-key ciphertext: plaintext matched=%t err=%v", plaintext == testIMAPSecret, err)
	}

	wrongCipher := testCredentialCipher(t, "imap-key-new", map[string][]byte{
		"imap-key-new": bytes.Repeat([]byte{0x19}, 32),
	})
	if _, err := DecryptIMAPPassword(replaced, wrongCipher); !errors.Is(err, ErrIMAPCredentialUnavailable) {
		t.Fatalf("wrong key error = %v, want unavailable", err)
	}
	tampered := replaced
	last := "A"
	if strings.HasSuffix(tampered.PasswordCiphertext, last) {
		last = "B"
	}
	tampered.PasswordCiphertext = tampered.PasswordCiphertext[:len(tampered.PasswordCiphertext)-1] + last
	if _, err := DecryptIMAPPassword(tampered, rotatedCipher); !errors.Is(err, ErrIMAPCredentialUnavailable) {
		t.Fatalf("tamper error = %v, want unavailable", err)
	}
	unknownKey := replaced
	unknownKey.PasswordCiphertext = strings.Replace(
		unknownKey.PasswordCiphertext,
		"imap-key-new",
		"missing-key1",
		1,
	)
	if _, err := DecryptIMAPPassword(unknownKey, rotatedCipher); !errors.Is(err, ErrIMAPCredentialUnavailable) {
		t.Fatalf("unknown key error = %v, want unavailable", err)
	}
	if _, err := DecryptIMAPPassword(replaced, nil); !errors.Is(err, ErrIMAPCredentialKeyringRequired) {
		t.Fatalf("missing keyring error = %v", err)
	}
}

func TestPostIMAPRejectsPlaintextFallbackAndRollsBackReplacement(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	credentialCipher := testCredentialCipher(t, "imap-key-old", testCredentialKeys())

	legacy := validModelIMAP("")
	legacy.UserId = 51
	legacy.Password = testIMAPSecret
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if _, err := DecryptIMAPPassword(legacy, credentialCipher); !errors.Is(err, ErrIMAPCredentialNotMigrated) {
		t.Fatalf("legacy read error = %v, want migration required", err)
	}
	preserveLegacy := validModelIMAP("")
	if err := PostIMAP(&preserveLegacy, 51, credentialCipher); !errors.Is(err, ErrIMAPCredentialNotMigrated) {
		t.Fatalf("legacy preserve error = %v, want migration required", err)
	}
	missingKeyring := validModelIMAP(testIMAPSecret)
	if err := PostIMAP(&missingKeyring, 52, nil); !errors.Is(err, ErrIMAPCredentialKeyringRequired) {
		t.Fatalf("missing keyring create error = %v", err)
	}

	original := validModelIMAP(testIMAPSecret)
	if err := PostIMAP(&original, 53, credentialCipher); err != nil {
		t.Fatalf("seed encrypted row: %v", err)
	}
	before := loadOnlyIMAP(t, 53)
	if err := db.Exec(`
		CREATE TRIGGER reject_imap_replacement
		BEFORE INSERT ON imap
		WHEN NEW.user_id = 53
		BEGIN
			SELECT RAISE(ABORT, 'synthetic replacement interruption');
		END
	`).Error; err != nil {
		t.Fatalf("create interruption trigger: %v", err)
	}
	replacement := validModelIMAP(testIMAPReplacement)
	if err := PostIMAP(&replacement, 53, credentialCipher); err == nil {
		t.Fatal("replacement unexpectedly succeeded")
	}
	after := loadOnlyIMAP(t, 53)
	if after.PasswordCiphertext != before.PasswordCiphertext {
		t.Fatal("failed replacement lost or changed the original credential")
	}
}

func TestOfflineIMAPCredentialMigrationAndRollback(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	credentialCipher := testCredentialCipher(t, "imap-key-old", testCredentialKeys())
	prefixPlaintext := "gophishfr-cred:v1:this-is-still-legacy-plaintext"

	for userID, plaintext := range map[int64]string{61: testIMAPSecret, 62: prefixPlaintext} {
		if err := db.Exec(`
			INSERT INTO imap (
				user_id, host, port, username, password, password_ciphertext,
				tls, enabled, folder, imap_freq
			) VALUES (?, 'localhost', 993, 'legacy-user', ?, '', 1, 0, 'INBOX', 60)
		`, userID, plaintext).Error; err != nil {
			t.Fatalf("seed legacy row %d: %v", userID, err)
		}
	}
	migratedEnvelope, err := credentialCipher.Encrypt(imapCredentialContext(63), []byte(testIMAPReplacement))
	if err != nil {
		t.Fatalf("encrypt mixed-state row: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO imap (
			user_id, host, port, username, password, password_ciphertext,
			tls, enabled, folder, imap_freq
		) VALUES (63, 'localhost', 993, 'migrated-user', '', ?, 1, 0, 'INBOX', 60)
	`, string(migratedEnvelope)).Error; err != nil {
		t.Fatalf("seed migrated row: %v", err)
	}

	result, err := MigrateIMAPCredentials(credentialCipher)
	if err != nil {
		t.Fatalf("migrate credentials: %v", err)
	}
	if result.Updated != 2 || result.Unchanged != 1 {
		t.Fatalf("migration result = %+v, want 2 updated/1 unchanged", result)
	}
	for userID, plaintext := range map[int64]string{
		61: testIMAPSecret, 62: prefixPlaintext, 63: testIMAPReplacement,
	} {
		stored := loadOnlyIMAP(t, userID)
		if stored.Password != "" || strings.Contains(stored.PasswordCiphertext, plaintext) {
			t.Fatalf("user %d retained readable plaintext", userID)
		}
		if got, decryptErr := DecryptIMAPPassword(stored, credentialCipher); decryptErr != nil || got != plaintext {
			t.Fatalf("decrypt migrated user %d: matched=%t err=%v", userID, got == plaintext, decryptErr)
		}
	}
	result, err = MigrateIMAPCredentials(credentialCipher)
	if err != nil || result.Updated != 0 || result.Unchanged != 3 {
		t.Fatalf("idempotent migration result = %+v err=%v", result, err)
	}

	wrongCipher := testCredentialCipher(t, "imap-key-old", map[string][]byte{
		"imap-key-old": bytes.Repeat([]byte{0x7f}, 32),
	})
	if _, err := RollbackIMAPCredentials(wrongCipher); !errors.Is(err, ErrIMAPCredentialUnavailable) {
		t.Fatalf("wrong-key rollback error = %v, want unavailable", err)
	}
	for _, userID := range []int64{61, 62, 63} {
		stored := loadOnlyIMAP(t, userID)
		if stored.Password != "" || stored.PasswordCiphertext == "" {
			t.Fatalf("failed rollback exposed partial state for user %d", userID)
		}
	}

	result, err = RollbackIMAPCredentials(credentialCipher)
	if err != nil {
		t.Fatalf("rollback credentials: %v", err)
	}

	if result.Updated != 3 || result.Unchanged != 0 {
		t.Fatalf("rollback result = %+v, want 3 updated", result)
	}
	for userID, plaintext := range map[int64]string{
		61: testIMAPSecret, 62: prefixPlaintext, 63: testIMAPReplacement,
	} {
		stored := loadOnlyIMAP(t, userID)
		if stored.Password != plaintext || stored.PasswordCiphertext != "" {
			t.Fatalf("rollback state for user %d is not old-binary compatible", userID)
		}
	}
	result, err = RollbackIMAPCredentials(credentialCipher)
	if err != nil || result.Updated != 0 || result.Unchanged != 3 {
		t.Fatalf("idempotent rollback result = %+v err=%v", result, err)
	}
}

func TestOfflineMigrationRejectsDuplicateUsersWithoutMutation(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	credentialCipher := testCredentialCipher(t, "imap-key-old", testCredentialKeys())
	if err := db.Exec("DROP INDEX imap_user_id_unique").Error; err != nil {
		t.Fatalf("remove unique index for legacy duplicate fixture: %v", err)
	}
	for _, plaintext := range []string{testIMAPSecret, testIMAPReplacement} {
		if err := db.Exec(`
			INSERT INTO imap (
				user_id, host, port, username, password, password_ciphertext,
				tls, enabled, folder, imap_freq
			) VALUES (66, 'localhost', 993, 'duplicate-user', ?, '', 1, 0, 'INBOX', 60)
		`, plaintext).Error; err != nil {
			t.Fatalf("seed duplicate legacy row: %v", err)
		}
	}
	if _, err := MigrateIMAPCredentials(credentialCipher); !errors.Is(err, ErrDuplicateIMAPUser) {
		t.Fatalf("duplicate migration error = %v, want duplicate user", err)
	}
	var legacyRows, encryptedRows int
	if err := db.Raw(`
		SELECT
			SUM(CASE WHEN password <> '' THEN 1 ELSE 0 END),
			SUM(CASE WHEN password_ciphertext <> '' THEN 1 ELSE 0 END)
		FROM imap WHERE user_id = 66
	`).Row().Scan(&legacyRows, &encryptedRows); err != nil {
		t.Fatalf("inspect duplicate rows: %v", err)
	}
	if legacyRows != 2 || encryptedRows != 0 {
		t.Fatal("duplicate preflight mutated credential rows")
	}
}

func TestOfflineMigrationRollsBackEveryRowOnInterruption(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	credentialCipher := testCredentialCipher(t, "imap-key-old", testCredentialKeys())
	for _, userID := range []int64{71, 72} {
		if err := db.Exec(`
			INSERT INTO imap (
				user_id, host, port, username, password, password_ciphertext,
				tls, enabled, folder, imap_freq
			) VALUES (?, 'localhost', 993, 'legacy-user', ?, '', 1, 0, 'INBOX', 60)
		`, userID, testIMAPSecret).Error; err != nil {
			t.Fatalf("seed legacy row %d: %v", userID, err)
		}
	}
	if err := db.Exec(`
		CREATE TRIGGER interrupt_imap_migration
		BEFORE UPDATE ON imap
		WHEN NEW.user_id = 72
		BEGIN
			SELECT RAISE(ABORT, 'synthetic migration interruption');
		END
	`).Error; err != nil {
		t.Fatalf("create migration interruption trigger: %v", err)
	}
	if _, err := MigrateIMAPCredentials(credentialCipher); err == nil {
		t.Fatal("interrupted migration unexpectedly succeeded")
	}
	for _, userID := range []int64{71, 72} {
		stored := loadOnlyIMAP(t, userID)
		if stored.Password != testIMAPSecret || stored.PasswordCiphertext != "" {
			t.Fatalf("partial migration became visible for user %d", userID)
		}
	}
}

func TestIMAPCredentialStateValidation(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	credentialCipher := testCredentialCipher(t, "imap-key-old", testCredentialKeys())
	for _, test := range []struct {
		name       string
		password   string
		ciphertext string
	}{
		{name: "both empty"},
		{name: "both populated", password: testIMAPSecret, ciphertext: "not-an-envelope"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Exec(`
				INSERT INTO imap (
					user_id, host, port, username, password, password_ciphertext,
					tls, enabled, folder, imap_freq
				) VALUES (?, 'localhost', 993, 'state-user', ?, ?, 1, 0, 'INBOX', 60)
			`, 80+len(test.name), test.password, test.ciphertext).Error; err != nil {
				t.Fatalf("seed invalid state: %v", err)
			}
			if _, err := MigrateIMAPCredentials(credentialCipher); !errors.Is(err, ErrIMAPCredentialInvalidState) {
				t.Fatalf("migration error = %v, want invalid state", err)
			}
			db.Exec("DELETE FROM imap")
		})
	}
	if _, err := MigrateIMAPCredentials(nil); !errors.Is(err, ErrIMAPCredentialKeyringRequired) {
		t.Fatalf("missing keyring migration error = %v", err)
	}
	for _, backend := range []string{"postgres", "postgresql", "oracle"} {
		if err := ValidateIMAPCredentialBackend(backend); !errors.Is(err, ErrUnsupportedIMAPCredentialDB) {
			t.Fatalf("backend %q error = %v", backend, err)
		}
	}
}

func TestSQLiteIMAPSchemaMigrationGuardsDuplicatesAndCiphertext(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema.db")
	sqlDB, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close sqlite database: %v", err)
		}
	})
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set sqlite dialect: %v", err)
	}
	migrationsPath := "../db/db_sqlite3/migrations"
	if err := goose.UpTo(sqlDB, migrationsPath, 20220321133237); err != nil {
		t.Fatalf("migrate to legacy schema: %v", err)
	}
	if _, err := sqlDB.Exec(`
		INSERT INTO imap (user_id, password) VALUES (91, ?), (91, ?)
	`, testIMAPSecret, testIMAPReplacement); err != nil {
		t.Fatalf("seed duplicate rows: %v", err)
	}
	if err := goose.Up(sqlDB, migrationsPath); err == nil {
		t.Fatal("schema migration accepted duplicate user_id rows")
	}
	var duplicateRows int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM imap WHERE user_id = 91").Scan(&duplicateRows); err != nil {
		t.Fatalf("count duplicate rows: %v", err)
	}
	if duplicateRows != 2 {
		t.Fatal("duplicate preflight selected or deleted a row")
	}
	if _, err := sqlDB.Exec("SELECT password_ciphertext FROM imap LIMIT 0"); err == nil {
		t.Fatal("failed duplicate preflight left a partial SQLite schema")
	}
	if _, err := sqlDB.Exec("DELETE FROM imap WHERE rowid = (SELECT MIN(rowid) FROM imap WHERE user_id = 91)"); err != nil {
		t.Fatalf("resolve synthetic duplicate: %v", err)
	}
	if err := goose.Up(sqlDB, migrationsPath); err != nil {
		t.Fatalf("apply schema after duplicate resolution: %v", err)
	}
	if err := goose.Down(sqlDB, migrationsPath); err != nil {
		t.Fatalf("remove later SMTP credential migration: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO imap (user_id, password) VALUES (91, ?)", testIMAPSecret); err == nil {
		t.Fatal("unique IMAP user constraint was not enforced")
	}
	if _, err := sqlDB.Exec(
		"UPDATE imap SET password = '', password_ciphertext = 'synthetic-ciphertext' WHERE user_id = 91",
	); err != nil {
		t.Fatalf("seed ciphertext for Down guard: %v", err)
	}
	if err := goose.Down(sqlDB, migrationsPath); err == nil {
		t.Fatal("schema Down accepted ciphertext before explicit data rollback")
	}
	if _, err := sqlDB.Exec("SELECT password_ciphertext FROM imap LIMIT 0"); err != nil {
		t.Fatalf("failed Down removed ciphertext column: %v", err)
	}
	if _, err := sqlDB.Exec(
		"UPDATE imap SET password = ?, password_ciphertext = '' WHERE user_id = 91",
		testIMAPSecret,
	); err != nil {
		t.Fatalf("prepare old-binary rollback state: %v", err)
	}
	if err := goose.Down(sqlDB, migrationsPath); err != nil {
		t.Fatalf("roll back only credential schema migration: %v", err)
	}
	var plaintext string
	if err := sqlDB.QueryRow("SELECT password FROM imap WHERE user_id = 91").Scan(&plaintext); err != nil {
		t.Fatalf("read old-binary-compatible plaintext: %v", err)
	}
	if plaintext != testIMAPSecret {
		t.Fatal("schema Down lost the restored credential")
	}
}

func TestMySQLIMAPCredentialLifecycle(t *testing.T) {
	connectionString := testingMySQLDSN(t)
	if connectionString == "" {
		return
	}
	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatalf("open mysql database: %v", err)
	}
	db = database
	db.LogMode(false)
	conf = &config.Config{
		DBName:         "mysql",
		DBPath:         connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatalf("migrate mysql database: %v", err)
	}
	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("set mysql dialect: %v", err)
	}
	if err := db.Exec("DELETE FROM imap").Error; err != nil {
		t.Fatalf("clear mysql IMAP rows: %v", err)
	}
	if err := db.Exec("DELETE FROM headers").Error; err != nil {
		t.Fatalf("clear mysql SMTP headers: %v", err)
	}
	if err := db.Exec("DELETE FROM smtp").Error; err != nil {
		t.Fatalf("clear mysql SMTP rows: %v", err)
	}
	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("set mysql dialect: %v", err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("remove later SMTP credential migration: %v", err)
	}
	t.Cleanup(func() {
		_ = goose.SetDialect("mysql")
		_ = goose.Up(db.DB(), conf.MigrationsPath)
		_ = db.Exec("DELETE FROM imap").Error
		_ = database.Close()
	})
	if err := goose.Down(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("prepare legacy mysql schema: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO imap (user_id, password) VALUES (101, ?), (101, ?)",
		testIMAPSecret,
		testIMAPReplacement,
	).Error; err != nil {
		t.Fatalf("seed mysql duplicate rows: %v", err)
	}
	if err := goose.Up(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("mysql schema migration accepted duplicate user_id rows")
	}
	var duplicateRows int
	if err := db.Raw("SELECT COUNT(*) FROM imap WHERE user_id = 101").Row().Scan(&duplicateRows); err != nil {
		t.Fatalf("count mysql duplicate rows: %v", err)
	}
	if duplicateRows != 2 {
		t.Fatal("mysql duplicate preflight selected or deleted a row")
	}
	if err := db.Exec("DELETE FROM imap WHERE password = ?", testIMAPReplacement).Error; err != nil {
		t.Fatalf("resolve mysql duplicate: %v", err)
	}
	if err := goose.Up(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("apply mysql credential schema: %v", err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("remove later SMTP credential migration: %v", err)
	}
	if err := db.Exec("INSERT INTO imap (user_id, password) VALUES (101, ?)", testIMAPSecret).Error; err == nil {
		t.Fatal("mysql unique IMAP user constraint was not enforced")
	}
	if err := db.Exec("DELETE FROM imap").Error; err != nil {
		t.Fatalf("clear mysql duplicate fixture: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO imap (
			user_id, host, port, username, password, password_ciphertext,
			tls, enabled, folder, imap_freq
		) VALUES (101, 'localhost', 993, 'mysql-user', ?, '', true, false, 'INBOX', 60)
	`, testIMAPSecret).Error; err != nil {
		t.Fatalf("seed mysql legacy row: %v", err)
	}
	credentialCipher := testCredentialCipher(t, "imap-key-old", testCredentialKeys())
	result, err := MigrateIMAPCredentials(credentialCipher)
	if err != nil || result.Updated != 1 {
		t.Fatalf("migrate mysql credential: result=%+v err=%v", result, err)
	}
	stored := loadOnlyIMAP(t, 101)
	if plaintext, err := DecryptIMAPPassword(stored, credentialCipher); err != nil || plaintext != testIMAPSecret {
		t.Fatalf("decrypt mysql credential: matched=%t err=%v", plaintext == testIMAPSecret, err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("mysql schema Down accepted ciphertext before data rollback")
	}
	result, err = RollbackIMAPCredentials(credentialCipher)
	if err != nil || result.Updated != 1 {
		t.Fatalf("rollback mysql credential: result=%+v err=%v", result, err)
	}
	stored = loadOnlyIMAP(t, 101)
	if stored.Password != testIMAPSecret || stored.PasswordCiphertext != "" {
		t.Fatal("mysql rollback did not restore old-binary state")
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("down mysql credential schema after data rollback: %v", err)
	}
	var plaintext string
	if err := db.Raw("SELECT password FROM imap WHERE user_id = 101").Row().Scan(&plaintext); err != nil {
		t.Fatalf("read mysql old-binary credential: %v", err)
	}
	if plaintext != testIMAPSecret {
		t.Fatal("mysql schema Down lost restored credential")
	}
	if err := goose.Up(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("restore mysql credential schema: %v", err)
	}
}

func testingMySQLDSN(t *testing.T) string {
	t.Helper()
	connectionString := os.Getenv("GOPHISHFR_MYSQL_TEST_DSN")
	if connectionString == "" {
		t.Skip("GOPHISHFR_MYSQL_TEST_DSN is not set")
	}
	return connectionString
}
