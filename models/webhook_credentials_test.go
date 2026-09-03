package models

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/jinzhu/gorm"
	"github.com/pressly/goose/v3"
)

const (
	testWebhookSecret      = "synthetic-webhook-secret-alpha"
	testWebhookReplacement = "synthetic-webhook-secret-beta"
)

func validModelWebhook() Webhook {
	return Webhook{
		Name:     "Synthetic Webhook",
		URL:      "https://webhook.invalid/synthetic",
		IsActive: true,
	}
}

func loadOnlyWebhook(t *testing.T, id int64) Webhook {
	t.Helper()
	wh, err := GetWebhook(id)
	if err != nil {
		t.Fatalf("load webhook: %v", err)
	}
	return wh
}

func TestWebhookCredentialRuntimeLifecycle(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	oldCipher := testCredentialCipher(t, "webhook-key-old", map[string][]byte{
		"webhook-key-old": bytes.Repeat([]byte{0x21}, 32),
		"webhook-key-new": bytes.Repeat([]byte{0x72}, 32),
	})

	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, oldCipher); err != nil {
		t.Fatalf("create encrypted webhook: %v", err)
	}
	if wh.Id <= 0 {
		t.Fatal("create did not assign an immutable webhook ID")
	}
	stored := loadOnlyWebhook(t, wh.Id)
	if stored.Secret != "" {
		t.Fatal("create retained legacy plaintext")
	}
	if stored.SecretCiphertext == "" || strings.Contains(stored.SecretCiphertext, testWebhookSecret) {
		t.Fatal("create did not store opaque ciphertext")
	}
	if plaintext, err := DecryptWebhookSecret(stored, oldCipher); err != nil || plaintext != testWebhookSecret {
		t.Fatalf("decrypt created secret: matched=%t err=%v", plaintext == testWebhookSecret, err)
	}

	// Record-copy attack: the same ciphertext under a different webhook ID
	// must never authenticate.
	other := validModelWebhook()
	other.Name = "Other webhook"
	if err := PostWebhook(&other, WebhookSecretPreserve, "", oldCipher); err != nil {
		t.Fatalf("create second webhook: %v", err)
	}
	if err := db.Exec(
		"UPDATE webhooks SET secret_ciphertext = ? WHERE id = ?",
		stored.SecretCiphertext, other.Id,
	).Error; err != nil {
		t.Fatalf("copy ciphertext to other webhook: %v", err)
	}
	moved := loadOnlyWebhook(t, other.Id)
	if _, err := DecryptWebhookSecret(moved, oldCipher); !errors.Is(err, ErrWebhookCredentialUnavailable) {
		t.Fatalf("record-copy decrypt error = %v, want unavailable", err)
	}

	// Old-key ciphertext remains decryptable once the keyring also carries a
	// newer active key, and a fresh rotation under that keyring uses the new
	// key rather than the retained old one.
	newActiveCipher := testCredentialCipher(t, "webhook-key-new", map[string][]byte{
		"webhook-key-old": bytes.Repeat([]byte{0x21}, 32),
		"webhook-key-new": bytes.Repeat([]byte{0x72}, 32),
	})
	if plaintext, err := DecryptWebhookSecret(stored, newActiveCipher); err != nil || plaintext != testWebhookSecret {
		t.Fatalf("decrypt old-key ciphertext under rotated keyring: matched=%t err=%v", plaintext == testWebhookSecret, err)
	}
	rotated := stored
	rotated.Secret = ""
	if err := PutWebhook(&rotated, WebhookSecretReplace, testWebhookReplacement, newActiveCipher); err != nil {
		t.Fatalf("rotate webhook secret under new active key: %v", err)
	}
	rotatedStored := loadOnlyWebhook(t, wh.Id)
	if rotatedStored.SecretCiphertext == stored.SecretCiphertext {
		t.Fatal("rotation did not change ciphertext")
	}
	oldKeyOnlyCipher := testCredentialCipher(t, "webhook-key-old", map[string][]byte{
		"webhook-key-old": bytes.Repeat([]byte{0x21}, 32),
	})
	if _, err := DecryptWebhookSecret(rotatedStored, oldKeyOnlyCipher); !errors.Is(err, ErrWebhookCredentialUnavailable) {
		t.Fatalf("rotated ciphertext decrypted under a keyring missing the new key: err=%v", err)
	}
	if plaintext, err := DecryptWebhookSecret(rotatedStored, newActiveCipher); err != nil || plaintext != testWebhookReplacement {
		t.Fatalf("decrypt rotated secret: matched=%t err=%v", plaintext == testWebhookReplacement, err)
	}

	// Preserve is byte-for-byte and never decrypts/re-encrypts, even when
	// unrelated mutable fields (URL, name, active state) change together.
	originalCiphertext := rotatedStored.SecretCiphertext
	preserveUpdate := rotatedStored
	preserveUpdate.Name = "Renamed webhook"
	preserveUpdate.URL = "https://webhook.invalid/renamed"
	preserveUpdate.IsActive = false
	if err := PutWebhook(&preserveUpdate, WebhookSecretPreserve, "", newActiveCipher); err != nil {
		t.Fatalf("preserve webhook secret across metadata change: %v", err)
	}
	afterMetadata := loadOnlyWebhook(t, wh.Id)
	if afterMetadata.SecretCiphertext != originalCiphertext {
		t.Fatal("metadata-only update did not preserve ciphertext byte-for-byte")
	}
	if afterMetadata.Name != "Renamed webhook" || afterMetadata.URL != "https://webhook.invalid/renamed" || afterMetadata.IsActive {
		t.Fatal("metadata update was not applied alongside the preserved secret")
	}
	if plaintext, err := DecryptWebhookSecret(afterMetadata, newActiveCipher); err != nil || plaintext != testWebhookReplacement {
		t.Fatalf("decrypt preserved secret after metadata change: matched=%t err=%v", plaintext == testWebhookReplacement, err)
	}

	// Explicit clear atomically empties both secret columns.
	clearUpdate := afterMetadata
	if err := PutWebhook(&clearUpdate, WebhookSecretClear, "", newActiveCipher); err != nil {
		t.Fatalf("clear webhook secret: %v", err)
	}
	cleared := loadOnlyWebhook(t, wh.Id)
	if cleared.Secret != "" || cleared.SecretCiphertext != "" {
		t.Fatal("explicit clear did not empty both secret columns")
	}
	if plaintext, err := DecryptWebhookSecret(cleared, newActiveCipher); err != nil || plaintext != "" {
		t.Fatalf("decrypt cleared webhook: plaintext=%q err=%v", plaintext, err)
	}

	if err := DeleteWebhook(wh.Id); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if _, err := GetWebhook(wh.Id); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted webhook still loadable: err=%v", err)
	}
}

// TestWebhookCreateNeverInsertsPlaintext proves that a create with a secret
// never persists plaintext, not even transiently within the transaction: the
// guard trigger aborts the whole INSERT if it ever carries a non-empty
// secret, which PostWebhook must never trigger.
func TestWebhookCreateNeverInsertsPlaintext(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x11}, 32),
	})
	if err := db.Exec(`
		CREATE TRIGGER reject_plaintext_webhook_insert
		BEFORE INSERT ON webhooks
		WHEN NEW.secret <> ''
		BEGIN
			SELECT RAISE(ABORT, 'plaintext webhook secret inserted');
		END
	`).Error; err != nil {
		t.Fatalf("create plaintext-insert guard trigger: %v", err)
	}
	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("create with plaintext-insert guard active: %v", err)
	}
	stored := loadOnlyWebhook(t, wh.Id)
	if stored.Secret != "" {
		t.Fatal("plaintext leaked into the legacy column")
	}
	if stored.SecretCiphertext == "" {
		t.Fatal("ciphertext missing after guarded create")
	}
}

func TestWebhookCreateWithoutSecretMatrix(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x22}, 32),
	})
	for _, test := range []struct {
		name   string
		intent WebhookSecretIntent
	}{
		{name: "absent/null maps to preserve", intent: WebhookSecretPreserve},
		{name: "present empty string maps to clear", intent: WebhookSecretClear},
	} {
		wh := validModelWebhook()
		wh.Name = "No secret " + test.name
		if err := PostWebhook(&wh, test.intent, "", cipher); err != nil {
			t.Fatalf("%s: create failed: %v", test.name, err)
		}
		stored := loadOnlyWebhook(t, wh.Id)
		if stored.Secret != "" || stored.SecretCiphertext != "" {
			t.Fatalf("%s: no-secret create left a non-empty secret column", test.name)
		}
	}
}

func TestWebhookValidateRequiresNameAndURL(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x23}, 32),
	})
	noURL := Webhook{Name: "No URL"}
	if err := PostWebhook(&noURL, WebhookSecretPreserve, "", cipher); !errors.Is(err, ErrURLNotSpecified) {
		t.Fatalf("missing URL error = %v, want ErrURLNotSpecified", err)
	}
	noName := Webhook{URL: "https://webhook.invalid/x"}
	if err := PostWebhook(&noName, WebhookSecretPreserve, "", cipher); !errors.Is(err, ErrNameNotSpecified) {
		t.Fatalf("missing name error = %v, want ErrNameNotSpecified", err)
	}
}

// TestWebhookUpdateSilentMutationRollsBack proves that a concurrently
// mutated ciphertext is detected by the post-update read-back verification
// and rolled back, rather than silently committed or masked by MySQL-style
// "rows changed" semantics.
func TestWebhookUpdateSilentMutationRollsBack(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x24}, 32),
	})
	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	before := loadOnlyWebhook(t, wh.Id)
	if err := db.Exec(`
		CREATE TRIGGER silently_mutate_webhook_ciphertext
		AFTER UPDATE ON webhooks
		WHEN NEW.name = 'silent-mutator'
		BEGIN
			UPDATE webhooks SET secret_ciphertext = 'tampered-by-trigger' WHERE id = NEW.id;
		END
	`).Error; err != nil {
		t.Fatalf("create silent-mutation trigger: %v", err)
	}
	update := before
	update.Name = "silent-mutator"
	if err := PutWebhook(&update, WebhookSecretPreserve, "", cipher); !errors.Is(err, ErrWebhookCredentialStorageMismatch) {
		t.Fatalf("silently-mutated update error = %v, want storage mismatch", err)
	}
	after := loadOnlyWebhook(t, wh.Id)
	if after.Name != before.Name || after.SecretCiphertext != before.SecretCiphertext {
		t.Fatal("failed update was not rolled back completely")
	}
}

// TestWebhookUpdateNoOpDoesNotFalselyReportConcurrentChange guards against a
// MySQL-specific hazard: a genuine no-op resubmission (identical name, URL,
// active state, and preserved ciphertext) matches the WHERE clause but
// changes no column value, which MySQL's default RowsAffected reports as 0.
// That must not be misreported as a concurrent-change failure.
func TestWebhookUpdateNoOpDoesNotFalselyReportConcurrentChange(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x25}, 32),
	})
	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	stored := loadOnlyWebhook(t, wh.Id)
	noop := stored
	if err := PutWebhook(&noop, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("identical no-op update: %v", err)
	}
	after := loadOnlyWebhook(t, wh.Id)
	if after.SecretCiphertext != stored.SecretCiphertext || after.Name != stored.Name || after.URL != stored.URL {
		t.Fatal("no-op update unexpectedly changed the webhook")
	}
}

func TestWebhookMutationsRequireValidIdentity(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x26}, 32),
	})
	zero := validModelWebhook()
	zero.Id = 0
	if err := PutWebhook(&zero, WebhookSecretPreserve, "", cipher); !errors.Is(err, ErrInvalidWebhookCredentialIdentity) {
		t.Fatalf("zero-ID update error = %v, want invalid identity", err)
	}
	if err := DeleteWebhook(0); !errors.Is(err, ErrInvalidWebhookCredentialIdentity) {
		t.Fatalf("zero-ID delete error = %v, want invalid identity", err)
	}
	missing := validModelWebhook()
	missing.Id = 999999
	if err := PutWebhook(&missing, WebhookSecretPreserve, "", cipher); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing-row update error = %v, want not found", err)
	}
	if err := DeleteWebhook(999999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing-row delete error = %v, want not found", err)
	}
}

func TestWebhookCredentialLengthBounds(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x27}, 32),
	})

	// The legacy varchar(255) contract is a MySQL *character* limit: 255
	// runes are valid even when every rune needs the maximum 4 UTF-8 bytes
	// (for example an emoji), which is the true worst case the envelope
	// bound must accommodate. 256 runes -- of any width -- must be rejected
	// before any encryption or database mutation occurs.
	maxNarrow := strings.Repeat("\u00e9", webhookSecretMaxRunes)
	maxWide := strings.Repeat("\U0001F600", webhookSecretMaxRunes)
	overNarrow := strings.Repeat("\u00e9", webhookSecretMaxRunes+1)
	overWide := strings.Repeat("\U0001F600", webhookSecretMaxRunes+1)

	for _, secret := range []string{maxNarrow, maxWide} {
		if err := ValidateWebhookSecret(secret); err != nil {
			t.Fatalf("boundary-valid %d-byte secret rejected: %v", len(secret), err)
		}
	}
	for _, secret := range []string{overNarrow, overWide} {
		if err := ValidateWebhookSecret(secret); !errors.Is(err, ErrWebhookCredentialTooLong) {
			t.Fatalf("over-limit %d-byte secret error = %v, want too-long", len(secret), err)
		}
	}

	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, maxWide, cipher); err != nil {
		t.Fatalf("create with worst-case 4-byte-rune secret: %v", err)
	}
	stored := loadOnlyWebhook(t, wh.Id)
	if len(stored.SecretCiphertext) > webhookSecretCiphertextColumnBytes {
		t.Fatalf("worst-case ciphertext length %d exceeds column bytes %d", len(stored.SecretCiphertext), webhookSecretCiphertextColumnBytes)
	}
	if plaintext, err := DecryptWebhookSecret(stored, cipher); err != nil || plaintext != maxWide {
		t.Fatalf("round-trip worst-case secret: matched=%t err=%v", plaintext == maxWide, err)
	}

	rowCountBefore := 0
	if err := db.Model(&Webhook{}).Count(&rowCountBefore).Error; err != nil {
		t.Fatalf("count webhooks: %v", err)
	}
	rejected := validModelWebhook()
	rejected.Name = "Over limit"
	if err := PostWebhook(&rejected, WebhookSecretReplace, overWide, cipher); !errors.Is(err, ErrWebhookCredentialTooLong) {
		t.Fatalf("over-limit create error = %v, want too-long", err)
	}
	rowCountAfter := 0
	if err := db.Model(&Webhook{}).Count(&rowCountAfter).Error; err != nil {
		t.Fatalf("count webhooks: %v", err)
	}
	if rowCountAfter != rowCountBefore {
		t.Fatal("rejected over-limit secret still mutated the database")
	}
}

func TestOfflineWebhookCredentialMigrationAndRollback(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x55}, 32),
	})
	if err := db.Exec(`
		INSERT INTO webhooks (name, url, secret, secret_ciphertext, is_active)
		VALUES
			('Legacy webhook', 'https://webhook.invalid/legacy', ?, '', 1),
			('No secret webhook', 'https://webhook.invalid/none', '', '', 1),
			('Legacy NULL webhook', 'https://webhook.invalid/null', NULL, '', 0)
	`, testWebhookSecret).Error; err != nil {
		t.Fatalf("seed webhook migration rows: %v", err)
	}
	encrypted := validModelWebhook()
	encrypted.Name = "Already encrypted"
	if err := PostWebhook(&encrypted, WebhookSecretReplace, testWebhookReplacement, cipher); err != nil {
		t.Fatalf("seed encrypted webhook: %v", err)
	}
	originalCiphertext := encrypted.SecretCiphertext

	result, err := MigrateWebhookSecrets(cipher)
	if err != nil || result.Updated != 1 || result.Unchanged != 3 {
		t.Fatalf("webhook migration result = %+v err=%v", result, err)
	}
	result, err = MigrateWebhookSecrets(cipher)
	if err != nil || result.Updated != 0 || result.Unchanged != 4 {
		t.Fatalf("idempotent webhook migration result = %+v err=%v", result, err)
	}
	storedEncrypted := loadOnlyWebhook(t, encrypted.Id)
	if storedEncrypted.SecretCiphertext != originalCiphertext {
		t.Fatal("migration changed an already-valid ciphertext")
	}

	var rows []storedWebhookCredential
	if err := db.Raw(`
		SELECT id, COALESCE(secret, '') AS secret, secret_ciphertext
		FROM webhooks
		ORDER BY id
	`).Scan(&rows).Error; err != nil {
		t.Fatalf("read migrated webhook rows: %v", err)
	}
	for _, row := range rows {
		if row.Secret != "" {
			t.Fatalf("webhook %d retained legacy plaintext", row.ID)
		}
		if row.SecretCiphertext == "" {
			continue
		}
		if plaintext, decryptErr := DecryptWebhookSecret(Webhook{
			Id: row.ID, SecretCiphertext: row.SecretCiphertext,
		}, cipher); decryptErr != nil || plaintext == "" {
			t.Fatalf("decrypt migrated webhook %d: err=%v", row.ID, decryptErr)
		}
	}

	wrongCipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x56}, 32),
	})
	if _, err := RollbackWebhookSecrets(wrongCipher); !errors.Is(err, ErrWebhookCredentialUnavailable) {
		t.Fatalf("wrong-key rollback error = %v, want unavailable", err)
	}
	result, err = RollbackWebhookSecrets(cipher)
	if err != nil || result.Updated != 2 || result.Unchanged != 2 {
		t.Fatalf("webhook rollback result = %+v err=%v", result, err)
	}
	result, err = RollbackWebhookSecrets(cipher)
	if err != nil || result.Updated != 0 || result.Unchanged != 4 {
		t.Fatalf("idempotent webhook rollback result = %+v err=%v", result, err)
	}
}

func TestOfflineWebhookCredentialMigrationRollsBackOnFailure(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x66}, 32),
	})
	for _, name := range []string{"First legacy", "Second legacy"} {
		if err := db.Exec(`
			INSERT INTO webhooks (name, url, secret, secret_ciphertext, is_active)
			VALUES (?, 'https://webhook.invalid/legacy', ?, '', 1)
		`, name, testWebhookSecret).Error; err != nil {
			t.Fatalf("seed legacy webhook row: %v", err)
		}
	}
	if err := db.Exec(`
		CREATE TRIGGER interrupt_webhook_migration
		BEFORE UPDATE ON webhooks
		WHEN NEW.name = 'Second legacy'
		BEGIN
			SELECT RAISE(ABORT, 'synthetic webhook migration interruption');
		END
	`).Error; err != nil {
		t.Fatalf("create migration interruption trigger: %v", err)
	}
	if _, err := MigrateWebhookSecrets(cipher); err == nil {
		t.Fatal("interrupted webhook migration unexpectedly succeeded")
	}
	var legacy, encrypted int
	if err := db.Raw(`
		SELECT
			SUM(CASE WHEN secret <> '' THEN 1 ELSE 0 END),
			SUM(CASE WHEN secret_ciphertext <> '' THEN 1 ELSE 0 END)
		FROM webhooks
	`).Row().Scan(&legacy, &encrypted); err != nil {
		t.Fatalf("inspect rolled-back migration: %v", err)
	}
	if legacy != 2 || encrypted != 0 {
		t.Fatal("failed webhook migration exposed a partial update")
	}
}

func TestWebhookCredentialStateAndBackendValidation(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x77}, 32),
	})
	oversizedCiphertext := Webhook{
		Id:               1,
		SecretCiphertext: strings.Repeat("A", webhookSecretEnvelopeMaxBytes+1),
	}
	if _, err := DecryptWebhookSecret(oversizedCiphertext, cipher); !errors.Is(err, ErrWebhookCredentialInvalidState) {
		t.Fatalf("oversized runtime ciphertext error = %v, want invalid state", err)
	}
	if err := db.Exec(`
		INSERT INTO webhooks (name, url, secret, secret_ciphertext, is_active)
		VALUES (?, 'https://webhook.invalid/ambiguous', ?, 'malformed', 1)
	`, "Ambiguous webhook", testWebhookSecret).Error; err != nil {
		t.Fatalf("seed ambiguous webhook row: %v", err)
	}
	if _, err := MigrateWebhookSecrets(cipher); !errors.Is(err, ErrWebhookCredentialInvalidState) {
		t.Fatalf("ambiguous-state error = %v, want invalid state", err)
	}
	if _, err := MigrateWebhookSecrets(nil); !errors.Is(err, ErrWebhookCredentialKeyringRequired) {
		t.Fatalf("missing-keyring migration error = %v", err)
	}
	// A legacy, not-yet-migrated non-empty secret is a distinct, fail-closed
	// state once the schema is active: the runtime never falls back to it.
	if _, err := DecryptWebhookSecret(Webhook{Id: 2, Secret: testWebhookSecret}, cipher); !errors.Is(err, ErrWebhookCredentialNotMigrated) {
		t.Fatalf("not-migrated legacy secret error = %v, want not migrated", err)
	}
	for _, backend := range []string{"postgres", "postgresql", "oracle"} {
		if err := ValidateWebhookCredentialBackend(backend); !errors.Is(err, ErrUnsupportedWebhookCredentialDB) {
			t.Fatalf("backend %q error = %v", backend, err)
		}
	}
}

func TestSQLiteWebhookCredentialSchemaLifecycle(t *testing.T) {
	sqlDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "webhook-schema.db"))
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close SQLite database: %v", err)
		}
	})
	if _, err := sqlDB.Exec(`
		CREATE TABLE webhooks (
			id integer primary key autoincrement,
			name varchar(255),
			url varchar(1000),
			secret varchar(255),
			is_active boolean default 0
		);
		INSERT INTO webhooks (name, url, secret, is_active) VALUES
			('One', 'https://webhook.invalid/1', '', 1),
			('Two', 'https://webhook.invalid/2', '', 1);
	`); err != nil {
		t.Fatalf("create legacy webhook schema: %v", err)
	}
	migrations := t.TempDir()
	migrationBody, err := os.ReadFile("../db/db_sqlite3/migrations/20260903020000_encrypt_webhook_secrets.sql")
	if err != nil {
		t.Fatalf("read webhook migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrations, "20260903020000_encrypt_webhook_secrets.sql"), migrationBody, 0o600); err != nil {
		t.Fatalf("write isolated webhook migration: %v", err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set SQLite dialect: %v", err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		t.Fatalf("apply webhook credential schema: %v", err)
	}
	if _, err := sqlDB.Exec("UPDATE webhooks SET secret_ciphertext = 'synthetic-ciphertext' WHERE id = 1"); err != nil {
		t.Fatalf("seed Down guard: %v", err)
	}
	if err := goose.Down(sqlDB, migrations); err == nil {
		t.Fatal("webhook schema Down accepted ciphertext")
	}
	if _, err := sqlDB.Exec("UPDATE webhooks SET secret = ?, secret_ciphertext = '' WHERE id = 1", testWebhookSecret); err != nil {
		t.Fatalf("prepare webhook rollback state: %v", err)
	}
	if err := goose.Down(sqlDB, migrations); err != nil {
		t.Fatalf("roll back webhook credential schema: %v", err)
	}
	var webhookCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM webhooks").Scan(&webhookCount); err != nil {
		t.Fatalf("count preserved webhooks: %v", err)
	}
	if webhookCount != 2 {
		t.Fatalf("schema Down lost webhooks: count=%d", webhookCount)
	}
	var restoredSecret string
	if err := sqlDB.QueryRow("SELECT secret FROM webhooks WHERE id = 1").Scan(&restoredSecret); err != nil {
		t.Fatalf("read restored legacy secret: %v", err)
	}
	if restoredSecret != testWebhookSecret {
		t.Fatalf("schema Down did not preserve the restored legacy secret: got %q", restoredSecret)
	}
}

func TestSQLiteWebhookCredentialDownPreservesSequence(t *testing.T) {
	testConfig := setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x76}, 32),
	})
	webhooks := make([]Webhook, 3)
	for i := range webhooks {
		webhooks[i] = validModelWebhook()
		webhooks[i].Name = fmt.Sprintf("Sequence webhook %d", i+1)
		if err := PostWebhook(&webhooks[i], WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
			t.Fatalf("create sequence webhook %d: %v", i+1, err)
		}
	}
	deletedID := webhooks[len(webhooks)-1].Id
	if err := DeleteWebhook(deletedID); err != nil {
		t.Fatalf("delete highest-ID webhook: %v", err)
	}
	if result, err := RollbackWebhookSecrets(cipher); err != nil || result.Updated != 2 {
		t.Fatalf("rollback credentials before schema Down: result=%+v err=%v", result, err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set SQLite dialect: %v", err)
	}
	if err := goose.Down(db.DB(), testConfig.MigrationsPath); err != nil {
		t.Fatalf("roll back webhook credential schema: %v", err)
	}
	insert, err := db.DB().Exec(`
		INSERT INTO webhooks (name, url, secret, is_active)
		VALUES ('After schema rollback', 'https://webhook.invalid/after', '', 0)
	`)
	if err != nil {
		t.Fatalf("create webhook after schema Down: %v", err)
	}
	newID, err := insert.LastInsertId()
	if err != nil {
		t.Fatalf("read new webhook ID: %v", err)
	}
	if newID <= deletedID {
		t.Fatalf("webhook ID %d reused deleted high-water ID %d", newID, deletedID)
	}
}

func TestMySQLWebhookCredentialLifecycle(t *testing.T) {
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
	if err := db.Exec("DELETE FROM webhooks").Error; err != nil {
		t.Fatalf("clear MySQL webhooks: %v", err)
	}
	t.Cleanup(func() {
		_ = goose.SetDialect("mysql")
		_ = goose.Up(db.DB(), conf.MigrationsPath)
		_ = db.Exec("DELETE FROM webhooks").Error
		_ = database.Close()
	})

	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x78}, 32),
	})
	if err := db.Exec(`
		INSERT INTO webhooks (name, url, secret, secret_ciphertext, is_active)
		VALUES
			('MySQL legacy', 'https://webhook.invalid/mysql-legacy', ?, '', 1),
			('MySQL no secret', 'https://webhook.invalid/mysql-none', '', '', 1),
			('MySQL NULL no secret', 'https://webhook.invalid/mysql-null', NULL, '', 0)
	`, testWebhookSecret).Error; err != nil {
		t.Fatalf("seed MySQL webhook rows: %v", err)
	}
	result, err := MigrateWebhookSecrets(cipher)
	if err != nil || result.Updated != 1 || result.Unchanged != 2 {
		t.Fatalf("migrate MySQL webhook credentials: result=%+v err=%v", result, err)
	}
	var migratedNull sql.NullString
	if err := db.Raw("SELECT secret FROM webhooks WHERE name = 'MySQL NULL no secret'").
		Row().Scan(&migratedNull); err != nil {
		t.Fatalf("read MySQL NULL no-secret row after migration: %v", err)
	}
	if migratedNull.Valid {
		t.Fatalf("MySQL migration changed valid NULL no-secret state to %q", migratedNull.String)
	}
	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("set MySQL dialect: %v", err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err == nil {
		t.Fatal("MySQL webhook schema Down accepted ciphertext")
	}
	if _, err := RollbackWebhookSecrets(cipher); err != nil {
		t.Fatalf("roll back MySQL webhook credentials: %v", err)
	}
	if err := goose.Down(db.DB(), conf.MigrationsPath); err != nil {
		t.Fatalf("roll back MySQL webhook schema: %v", err)
	}
	var webhookCount int
	if err := db.Raw("SELECT COUNT(*) FROM webhooks").Row().Scan(&webhookCount); err != nil {
		t.Fatalf("count old-binary MySQL webhook rows: %v", err)
	}
	if webhookCount != 3 {
		t.Fatalf("MySQL schema Down preserved %d webhooks, want 3", webhookCount)
	}
}

func TestMySQLWebhookCredentialStorageBoundsNonStrict(t *testing.T) {
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
	db.DB().SetMaxOpenConns(1)
	db.DB().SetMaxIdleConns(1)
	conf = &config.Config{
		DBName:         "mysql",
		DBPath:         connectionString,
		MigrationsPath: "../db/db_mysql/migrations",
	}
	if err := migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		t.Fatalf("migrate MySQL database: %v", err)
	}
	if err := db.Exec("DELETE FROM webhooks").Error; err != nil {
		t.Fatalf("clear MySQL webhooks: %v", err)
	}
	var originalSQLMode string
	if err := db.Raw("SELECT @@SESSION.sql_mode").Row().Scan(&originalSQLMode); err != nil {
		t.Fatalf("read MySQL session SQL mode: %v", err)
	}
	if err := db.Exec("SET SESSION sql_mode = ''").Error; err != nil {
		t.Fatalf("disable MySQL strict mode: %v", err)
	}
	var nonStrictSQLMode string
	if err := db.Raw("SELECT @@SESSION.sql_mode").Row().Scan(&nonStrictSQLMode); err != nil {
		t.Fatalf("verify MySQL session SQL mode: %v", err)
	}
	if nonStrictSQLMode != "" {
		t.Fatalf("MySQL test requires non-strict mode, got %q", nonStrictSQLMode)
	}
	t.Cleanup(func() {
		_ = db.Exec("ALTER TABLE webhooks MODIFY secret VARCHAR(255)").Error
		_ = db.Exec("ALTER TABLE webhooks MODIFY secret_ciphertext VARCHAR(2048) NOT NULL DEFAULT ''").Error
		_ = db.Exec("SET SESSION sql_mode = ?", originalSQLMode).Error
		_ = db.Exec("DELETE FROM webhooks").Error
		_ = database.Close()
	})

	cipher := testCredentialCipher(t, "webhook-key", map[string][]byte{
		"webhook-key": bytes.Repeat([]byte{0x79}, 32),
	})
	maxSecret := strings.Repeat("\U0001F600", webhookSecretMaxRunes)
	wh := validModelWebhook()
	wh.Name = "MySQL maximum"
	if err := PostWebhook(&wh, WebhookSecretReplace, maxSecret, cipher); err != nil {
		t.Fatalf("create maximum-width MySQL secret: %v", err)
	}
	stored := loadOnlyWebhook(t, wh.Id)
	if plaintext, err := DecryptWebhookSecret(stored, cipher); err != nil || plaintext != maxSecret {
		t.Fatalf("round-trip maximum MySQL secret: matched=%t err=%v", plaintext == maxSecret, err)
	}

	noOpProbe := db.Model(&Webhook{}).
		Where("id = ?", stored.Id).
		Update("name", stored.Name)
	if noOpProbe.Error != nil {
		t.Fatalf("probe MySQL changed-rows semantics: %v", noOpProbe.Error)
	}
	if noOpProbe.RowsAffected != 0 {
		t.Fatalf("MySQL test requires changed-rows semantics, got %d affected", noOpProbe.RowsAffected)
	}
	metadataOnly := stored
	metadataOnly.URL = "https://webhook.invalid/mysql-updated"
	if err := PutWebhook(&metadataOnly, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("same-second MySQL metadata-only update: %v", err)
	}
	updated := loadOnlyWebhook(t, wh.Id)
	if updated.URL != "https://webhook.invalid/mysql-updated" || updated.SecretCiphertext != stored.SecretCiphertext {
		t.Fatal("MySQL metadata-only update did not preserve ciphertext and apply the URL change")
	}
}

// ---------------------------------------------------------------------------
// Iteration 2: a legacy `secret IS NULL` column is exactly as valid a
// no-secret state as a legacy empty string. The tests below prove that
// preserve, clear, replace, a bundled metadata update, a genuine no-op, a
// concurrent conflict, and offline rollback all treat NULL and '' as the
// same state, and that a NULL-secret webhook's deactivation actually
// persists and is reflected in active-delivery selection.
// ---------------------------------------------------------------------------

// forceNullWebhookSecret bypasses the model layer to put webhooks.id into a
// genuine SQL NULL secret state. PostWebhook and PutWebhook always write a
// concrete empty string, so this simulates the state a pre-existing,
// imported, or partially restored row can have that this application's own
// write paths never produce on their own.
func forceNullWebhookSecret(t *testing.T, id int64) {
	t.Helper()
	if err := db.Exec("UPDATE webhooks SET secret = NULL WHERE id = ?", id).Error; err != nil {
		t.Fatalf("force NULL legacy secret for webhook %d: %v", id, err)
	}
	var raw sql.NullString
	if err := db.Raw("SELECT secret FROM webhooks WHERE id = ?", id).Row().Scan(&raw); err != nil {
		t.Fatalf("read raw secret column for webhook %d: %v", id, err)
	}
	if raw.Valid {
		t.Fatalf("test setup did not produce a NULL secret column for webhook %d, got %q", id, raw.String)
	}
}

// TestWebhookNullLegacySecretPreserveClearReplace freezes that a legacy
// `secret IS NULL` row -- not just a legacy empty string -- is an equally
// valid starting state for every update intent: preserve leaves an existing
// ciphertext byte-for-byte unchanged, clear empties both columns, and
// replace encrypts a new secret, whether or not a ciphertext already exists
// alongside the NULL legacy column. A successful update also always
// normalizes the legacy column to a concrete empty string, so a NULL never
// reappears once this package has written the row.
func TestWebhookNullLegacySecretPreserveClearReplace(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-null-key", map[string][]byte{
		"webhook-null-key": bytes.Repeat([]byte{0x81}, 32),
	})

	tests := []struct {
		name            string
		seedCiphertext  bool // whether the NULL-secret row already has a stored secret
		intent          WebhookSecretIntent
		incomingSecret  string
		wantSecretAfter string // "" means no secret is expected afterward
	}{
		{
			name:            "NULL secret with ciphertext, preserve",
			seedCiphertext:  true,
			intent:          WebhookSecretPreserve,
			wantSecretAfter: testWebhookSecret,
		},
		{
			name:            "NULL secret with ciphertext, explicit clear",
			seedCiphertext:  true,
			intent:          WebhookSecretClear,
			wantSecretAfter: "",
		},
		{
			name:            "NULL secret with ciphertext, replace",
			seedCiphertext:  true,
			intent:          WebhookSecretReplace,
			incomingSecret:  testWebhookReplacement,
			wantSecretAfter: testWebhookReplacement,
		},
		{
			name:            "NULL secret with no ciphertext, preserve stays empty",
			seedCiphertext:  false,
			intent:          WebhookSecretPreserve,
			wantSecretAfter: "",
		},
		{
			name:            "NULL secret with no ciphertext, explicit clear stays empty",
			seedCiphertext:  false,
			intent:          WebhookSecretClear,
			wantSecretAfter: "",
		},
		{
			name:            "NULL secret with no ciphertext, replace",
			seedCiphertext:  false,
			intent:          WebhookSecretReplace,
			incomingSecret:  testWebhookReplacement,
			wantSecretAfter: testWebhookReplacement,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			seedIntent := WebhookSecretPreserve
			seedSecret := ""
			if test.seedCiphertext {
				seedIntent = WebhookSecretReplace
				seedSecret = testWebhookSecret
			}
			wh := validModelWebhook()
			wh.Name = "NULL matrix: " + test.name
			if err := PostWebhook(&wh, seedIntent, seedSecret, cipher); err != nil {
				t.Fatalf("seed webhook: %v", err)
			}
			forceNullWebhookSecret(t, wh.Id)

			update := wh
			if err := PutWebhook(&update, test.intent, test.incomingSecret, cipher); err != nil {
				t.Fatalf("update NULL-secret row: %v", err)
			}

			var rawSecret sql.NullString
			if err := db.Raw("SELECT secret FROM webhooks WHERE id = ?", wh.Id).Row().Scan(&rawSecret); err != nil {
				t.Fatalf("read raw secret column after update: %v", err)
			}
			if !rawSecret.Valid || rawSecret.String != "" {
				t.Fatalf("legacy secret column after update = valid=%v value=%q, want a concrete empty string, not NULL", rawSecret.Valid, rawSecret.String)
			}

			stored := loadOnlyWebhook(t, wh.Id)
			if test.wantSecretAfter == "" {
				if stored.SecretCiphertext != "" {
					t.Fatal("expected no-secret state after update, found ciphertext")
				}
				return
			}
			plaintext, err := DecryptWebhookSecret(stored, cipher)
			if err != nil || plaintext != test.wantSecretAfter {
				t.Fatalf("decrypt after NULL-secret update: matched=%t err=%v", plaintext == test.wantSecretAfter, err)
			}
		})
	}
}

// TestWebhookNullLegacySecretMetadataUpdatePersistsAndExcludesFromActiveDelivery
// is the central regression test for the iteration-2 security finding: a
// metadata update (deactivating a webhook, alongside a rename) on a row
// whose legacy secret column is a genuine SQL NULL must actually persist,
// not silently no-op because a predicate that literally compares
// secret to an empty string never
// matches NULL. An administrator deactivating a NULL-secret webhook must
// see it excluded from both GetActiveWebhooks and ActiveWebhookEndpoints
// afterward, not receive a false-success response while the webhook keeps
// receiving campaign events.
func TestWebhookNullLegacySecretMetadataUpdatePersistsAndExcludesFromActiveDelivery(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-null-deactivate-key", map[string][]byte{
		"webhook-null-deactivate-key": bytes.Repeat([]byte{0x82}, 32),
	})

	wh := validModelWebhook()
	wh.Name = "NULL secret active webhook"
	wh.IsActive = true
	if err := PostWebhook(&wh, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("create no-secret active webhook: %v", err)
	}
	forceNullWebhookSecret(t, wh.Id)

	activeBefore, err := GetActiveWebhooks()
	if err != nil || len(activeBefore) != 1 || activeBefore[0].Id != wh.Id {
		t.Fatalf("GetActiveWebhooks before deactivation = %+v, err=%v, want exactly the seeded webhook", activeBefore, err)
	}
	endpointsBefore, err := ActiveWebhookEndpoints(cipher)
	if err != nil || len(endpointsBefore) != 1 {
		t.Fatalf("ActiveWebhookEndpoints before deactivation = %+v, err=%v, want exactly one endpoint", endpointsBefore, err)
	}

	deactivate := wh
	deactivate.IsActive = false
	deactivate.Name = "Deactivated NULL secret webhook"
	deactivate.URL = "https://webhook.invalid/deactivated"
	if err := PutWebhook(&deactivate, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("deactivate NULL-secret webhook: %v", err)
	}

	persisted := loadOnlyWebhook(t, wh.Id)
	if persisted.IsActive {
		t.Fatal("deactivation of a NULL-secret webhook did not persist -- it is still active in storage")
	}
	if persisted.Name != "Deactivated NULL secret webhook" || persisted.URL != "https://webhook.invalid/deactivated" {
		t.Fatal("metadata changes bundled with a NULL-secret preserve update did not persist")
	}

	activeAfter, err := GetActiveWebhooks()
	if err != nil || len(activeAfter) != 0 {
		t.Fatalf("GetActiveWebhooks after deactivation = %+v, err=%v, want none", activeAfter, err)
	}
	endpointsAfter, err := ActiveWebhookEndpoints(cipher)
	if err != nil || len(endpointsAfter) != 0 {
		t.Fatalf("ActiveWebhookEndpoints after deactivation = %+v, err=%v, want none (a deactivated webhook must never be eligible for delivery)", endpointsAfter, err)
	}
}

// TestWebhookNullLegacySecretNoOpUpdateSucceeds proves that a genuine no-op
// resubmission -- identical name, URL, and active state, preserving a
// ciphertext on a row whose legacy secret column started as NULL --
// succeeds. The NULL-aware predicate fix must not turn an ordinary
// unmodified resave into a spurious concurrency failure.
func TestWebhookNullLegacySecretNoOpUpdateSucceeds(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-null-noop-key", map[string][]byte{
		"webhook-null-noop-key": bytes.Repeat([]byte{0x83}, 32),
	})
	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	forceNullWebhookSecret(t, wh.Id)

	stored := loadOnlyWebhook(t, wh.Id)
	noop := stored
	if err := PutWebhook(&noop, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("identical no-op update on NULL-secret row: %v", err)
	}
	after := loadOnlyWebhook(t, wh.Id)
	if after.SecretCiphertext != stored.SecretCiphertext || after.Name != stored.Name ||
		after.URL != stored.URL || after.IsActive != stored.IsActive {
		t.Fatal("no-op update unexpectedly changed the webhook")
	}
}

// TestWebhookNullLegacySecretConcurrentChangeFailsClosed proves the
// NULL-aware predicate fix does not weaken concurrency detection: a
// concurrently mutated ciphertext on a row whose legacy secret column was
// NULL is still caught by verifyWebhookRowStorage and rolled back, not
// silently accepted just because the (now NULL-matching) predicate found a
// row to update.
func TestWebhookNullLegacySecretConcurrentChangeFailsClosed(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-null-conflict-key", map[string][]byte{
		"webhook-null-conflict-key": bytes.Repeat([]byte{0x84}, 32),
	})
	wh := validModelWebhook()
	if err := PostWebhook(&wh, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	forceNullWebhookSecret(t, wh.Id)
	before := loadOnlyWebhook(t, wh.Id)

	if err := db.Exec(`
		CREATE TRIGGER silently_mutate_null_secret_webhook_ciphertext
		AFTER UPDATE ON webhooks
		WHEN NEW.name = 'null-secret-silent-mutator'
		BEGIN
			UPDATE webhooks SET secret_ciphertext = 'tampered-by-trigger' WHERE id = NEW.id;
		END
	`).Error; err != nil {
		t.Fatalf("create silent-mutation trigger: %v", err)
	}
	update := before
	update.Name = "null-secret-silent-mutator"
	if err := PutWebhook(&update, WebhookSecretPreserve, "", cipher); !errors.Is(err, ErrWebhookCredentialStorageMismatch) {
		t.Fatalf("silently-mutated NULL-secret update error = %v, want storage mismatch", err)
	}
	after := loadOnlyWebhook(t, wh.Id)
	if after.Name != before.Name || after.SecretCiphertext != before.SecretCiphertext {
		t.Fatal("failed update on a NULL-secret row was not rolled back completely")
	}
	var rawSecret sql.NullString
	if err := db.Raw("SELECT secret FROM webhooks WHERE id = ?", wh.Id).Row().Scan(&rawSecret); err != nil {
		t.Fatalf("read failed-update legacy secret state: %v", err)
	}
	if rawSecret.Valid {
		t.Fatalf("failed update did not restore original NULL legacy secret, got %q", rawSecret.String)
	}
}

// TestRollbackWebhookSecretsAcceptsNullLegacySecretWithCiphertext is the
// regression test for the iteration-2 rollback finding: a row whose legacy
// secret column is a genuine SQL NULL alongside a valid ciphertext -- for
// example one restored from a partial backup -- is a valid state the
// offline rollback must restore, not reject as an unexpected row change.
func TestRollbackWebhookSecretsAcceptsNullLegacySecretWithCiphertext(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "webhook-null-rollback-key", map[string][]byte{
		"webhook-null-rollback-key": bytes.Repeat([]byte{0x85}, 32),
	})
	encrypted := validModelWebhook()
	encrypted.Name = "NULL secret with ciphertext"
	if err := PostWebhook(&encrypted, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("seed encrypted webhook: %v", err)
	}
	// Simulate a row whose legacy column reverted to (or was restored as) a
	// genuine NULL while its ciphertext survived -- PostWebhook itself never
	// produces this combination, but a real database can carry it.
	forceNullWebhookSecret(t, encrypted.Id)

	result, err := RollbackWebhookSecrets(cipher)
	if err != nil || result.Updated != 1 || result.Unchanged != 0 {
		t.Fatalf("rollback NULL-secret+ciphertext row: result=%+v err=%v", result, err)
	}

	var restoredSecret, restoredCiphertext string
	if err := db.Raw("SELECT secret, secret_ciphertext FROM webhooks WHERE id = ?", encrypted.Id).
		Row().Scan(&restoredSecret, &restoredCiphertext); err != nil {
		t.Fatalf("read rolled-back row: %v", err)
	}
	if restoredSecret != testWebhookSecret {
		t.Fatalf("rollback did not restore the legacy secret: got %q", restoredSecret)
	}
	if restoredCiphertext != "" {
		t.Fatal("rollback did not clear the ciphertext column")
	}

	// Idempotent: repeating rollback on the now-plaintext row is a safe
	// no-op, not an error.
	result, err = RollbackWebhookSecrets(cipher)
	if err != nil || result.Updated != 0 || result.Unchanged != 1 {
		t.Fatalf("idempotent rollback result = %+v err=%v", result, err)
	}
}

// TestMySQLWebhookCredentialNullLegacySecretLifecycle is the real-MySQL
// counterpart of the SQLite NULL-legacy-secret regression tests above: it
// proves against a real server that a legacy `secret IS NULL` row's
// metadata update actually persists and excludes it from active delivery,
// that a genuine no-op resubmission succeeds under MySQL's default
// changed-rows RowsAffected semantics, and that rollback accepts a NULL
// secret alongside a valid ciphertext.
func TestMySQLWebhookCredentialNullLegacySecretLifecycle(t *testing.T) {
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
	if err := db.Exec("DELETE FROM webhooks").Error; err != nil {
		t.Fatalf("clear MySQL webhooks: %v", err)
	}
	t.Cleanup(func() {
		_ = goose.SetDialect("mysql")
		_ = goose.Up(db.DB(), conf.MigrationsPath)
		_ = db.Exec("DELETE FROM webhooks").Error
		_ = database.Close()
	})

	cipher := testCredentialCipher(t, "webhook-null-mysql-key", map[string][]byte{
		"webhook-null-mysql-key": bytes.Repeat([]byte{0x86}, 32),
	})

	// Preserve, clear, and replace must all accept both valid NULL source
	// states (with and without ciphertext), persist a concrete empty legacy
	// column, and leave exactly the requested credential state.
	for _, test := range []struct {
		name            string
		seedCiphertext  bool
		intent          WebhookSecretIntent
		incomingSecret  string
		wantSecretAfter string
	}{
		{name: "preserve ciphertext", seedCiphertext: true, intent: WebhookSecretPreserve, wantSecretAfter: testWebhookSecret},
		{name: "clear ciphertext", seedCiphertext: true, intent: WebhookSecretClear},
		{name: "replace ciphertext", seedCiphertext: true, intent: WebhookSecretReplace, incomingSecret: testWebhookReplacement, wantSecretAfter: testWebhookReplacement},
		{name: "preserve no secret", intent: WebhookSecretPreserve},
		{name: "clear no secret", intent: WebhookSecretClear},
		{name: "replace no secret", intent: WebhookSecretReplace, incomingSecret: testWebhookReplacement, wantSecretAfter: testWebhookReplacement},
	} {
		seedIntent := WebhookSecretPreserve
		seedSecret := ""
		if test.seedCiphertext {
			seedIntent = WebhookSecretReplace
			seedSecret = testWebhookSecret
		}
		wh := validModelWebhook()
		wh.Name = "MySQL NULL matrix: " + test.name
		wh.IsActive = false
		if err := PostWebhook(&wh, seedIntent, seedSecret, cipher); err != nil {
			t.Fatalf("%s: seed MySQL webhook: %v", test.name, err)
		}
		if err := db.Exec("UPDATE webhooks SET secret = NULL WHERE id = ?", wh.Id).Error; err != nil {
			t.Fatalf("%s: force MySQL NULL legacy secret: %v", test.name, err)
		}
		update := wh
		if err := PutWebhook(&update, test.intent, test.incomingSecret, cipher); err != nil {
			t.Fatalf("%s: update MySQL NULL-secret row: %v", test.name, err)
		}
		var rawSecret sql.NullString
		if err := db.Raw("SELECT secret FROM webhooks WHERE id = ?", wh.Id).Row().Scan(&rawSecret); err != nil {
			t.Fatalf("%s: read MySQL raw secret after update: %v", test.name, err)
		}
		if !rawSecret.Valid || rawSecret.String != "" {
			t.Fatalf("%s: MySQL runtime update left legacy secret valid=%v value=%q, want concrete empty", test.name, rawSecret.Valid, rawSecret.String)
		}
		stored := loadOnlyWebhook(t, wh.Id)
		if test.wantSecretAfter == "" {
			if stored.SecretCiphertext != "" {
				t.Fatalf("%s: expected no ciphertext after update", test.name)
			}
		} else if plaintext, decryptErr := DecryptWebhookSecret(stored, cipher); decryptErr != nil || plaintext != test.wantSecretAfter {
			t.Fatalf("%s: MySQL decrypt after update matched=%t err=%v", test.name, plaintext == test.wantSecretAfter, decryptErr)
		}
	}

	// Deactivating a NULL-secret webhook must persist and exclude it from
	// active delivery.
	active := validModelWebhook()
	active.Name = "MySQL NULL secret active"
	active.IsActive = true
	if err := PostWebhook(&active, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("create no-secret active webhook: %v", err)
	}
	if err := db.Exec("UPDATE webhooks SET secret = NULL WHERE id = ?", active.Id).Error; err != nil {
		t.Fatalf("force NULL legacy secret: %v", err)
	}
	activeBefore, err := GetActiveWebhooks()
	if err != nil || len(activeBefore) != 1 {
		t.Fatalf("GetActiveWebhooks before deactivation = %+v, err=%v, want exactly one", activeBefore, err)
	}
	deactivate := active
	deactivate.IsActive = false
	if err := PutWebhook(&deactivate, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("deactivate MySQL NULL-secret webhook: %v", err)
	}
	persisted := loadOnlyWebhook(t, active.Id)
	if persisted.IsActive {
		t.Fatal("MySQL: deactivation of a NULL-secret webhook did not persist")
	}
	activeAfter, err := GetActiveWebhooks()
	if err != nil || len(activeAfter) != 0 {
		t.Fatalf("GetActiveWebhooks after deactivation = %+v, err=%v, want none", activeAfter, err)
	}
	endpointsAfter, err := ActiveWebhookEndpoints(cipher)
	if err != nil || len(endpointsAfter) != 0 {
		t.Fatalf("ActiveWebhookEndpoints after deactivation = %+v, err=%v, want none", endpointsAfter, err)
	}

	// A genuine no-op resubmission on a NULL-secret (now normalized to '')
	// row must still succeed under MySQL's default (non-CLIENT_FOUND_ROWS)
	// changed-rows semantics.
	noOpProbe := db.Model(&Webhook{}).
		Where("id = ?", persisted.Id).
		Update("name", persisted.Name)
	if noOpProbe.Error != nil {
		t.Fatalf("probe MySQL changed-rows semantics: %v", noOpProbe.Error)
	}
	if noOpProbe.RowsAffected != 0 {
		t.Fatalf("MySQL test requires changed-rows semantics, got %d affected", noOpProbe.RowsAffected)
	}
	noop := persisted
	if err := PutWebhook(&noop, WebhookSecretPreserve, "", cipher); err != nil {
		t.Fatalf("identical no-op update on MySQL NULL-secret row: %v", err)
	}

	// A missing row must not be mistaken for a MySQL changed-row no-op.
	missing := validModelWebhook()
	missing.Id = 999999999
	if err := PutWebhook(&missing, WebhookSecretPreserve, "", cipher); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing MySQL webhook update error = %v, want record not found", err)
	}

	// Simulate a conflicting writer changing the row after the caller loaded
	// it but before PutWebhook starts its transaction. The resulting ambiguous
	// plaintext+ciphertext state must fail closed rather than being mistaken
	// for a changed-row no-op and overwritten.
	conflict := validModelWebhook()
	conflict.Name = "MySQL NULL conflict source"
	if err := PostWebhook(&conflict, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("seed MySQL conflict webhook: %v", err)
	}
	conflictBefore := loadOnlyWebhook(t, conflict.Id)
	if err := db.Exec("UPDATE webhooks SET secret = ? WHERE id = ?", testWebhookReplacement, conflict.Id).Error; err != nil {
		t.Fatalf("simulate conflicting MySQL credential write: %v", err)
	}
	conflictingUpdate := conflictBefore
	conflictingUpdate.Name = "mysql-null-secret-conflict"
	if err := PutWebhook(&conflictingUpdate, WebhookSecretPreserve, "", cipher); !errors.Is(err, ErrWebhookCredentialInvalidState) {
		t.Fatalf("conflicting MySQL update error = %v, want invalid state", err)
	}
	conflictAfter := loadOnlyWebhook(t, conflict.Id)
	if conflictAfter.Name != conflictBefore.Name || conflictAfter.SecretCiphertext != conflictBefore.SecretCiphertext {
		t.Fatal("conflicting MySQL update changed metadata or ciphertext")
	}
	var conflictRawSecret string
	if err := db.Raw("SELECT secret FROM webhooks WHERE id = ?", conflict.Id).Row().Scan(&conflictRawSecret); err != nil {
		t.Fatalf("read conflicting MySQL secret: %v", err)
	}
	if conflictRawSecret != testWebhookReplacement {
		t.Fatal("conflicting MySQL credential state was overwritten")
	}

	// Rollback must accept a NULL legacy secret alongside a valid
	// ciphertext instead of rejecting it as an unexpected row change.
	encrypted := validModelWebhook()
	encrypted.Name = "MySQL NULL secret with ciphertext"
	if err := PostWebhook(&encrypted, WebhookSecretReplace, testWebhookSecret, cipher); err != nil {
		t.Fatalf("seed MySQL encrypted webhook: %v", err)
	}
	if err := db.Exec("UPDATE webhooks SET secret = NULL WHERE id = ?", encrypted.Id).Error; err != nil {
		t.Fatalf("force NULL legacy secret alongside MySQL ciphertext: %v", err)
	}
	result, err := RollbackWebhookSecrets(cipher)
	if !errors.Is(err, ErrWebhookCredentialInvalidState) {
		t.Fatalf("rollback accepted conflicting MySQL credential state: result=%+v err=%v", result, err)
	}
	if err := db.Exec("UPDATE webhooks SET secret = '' WHERE id = ?", conflict.Id).Error; err != nil {
		t.Fatalf("repair synthetic MySQL conflict before rollback: %v", err)
	}
	result, err = RollbackWebhookSecrets(cipher)
	if err != nil || result.Updated != 5 || result.Unchanged != 4 {
		t.Fatalf("roll back MySQL NULL-secret+ciphertext row: result=%+v err=%v", result, err)
	}
	var restoredSecret, restoredCiphertext string
	if err := db.Raw("SELECT secret, secret_ciphertext FROM webhooks WHERE id = ?", encrypted.Id).
		Row().Scan(&restoredSecret, &restoredCiphertext); err != nil {
		t.Fatalf("read rolled-back MySQL row: %v", err)
	}
	if restoredSecret != testWebhookSecret || restoredCiphertext != "" {
		t.Fatalf("MySQL rollback did not restore plaintext and clear ciphertext: secret=%q ciphertext=%q", restoredSecret, restoredCiphertext)
	}
}
