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
	update.Name = "Updated SMTP"
	update.FromAddress = "updated@example.test"
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
	for name, mutate := range map[string]func(*SMTP){
		"interface": func(profile *SMTP) { profile.Interface = "SMTPS" },
		"host":      func(profile *SMTP) { profile.Host = "localhost:2526" },
		"username":  func(profile *SMTP) { profile.Username = "redirected-user" },
		"TLS policy": func(profile *SMTP) {
			profile.IgnoreCertErrors = !profile.IgnoreCertErrors
		},
	} {
		redirect := stored
		redirect.Password = ""
		mutate(&redirect)
		if err := PutSMTP(&redirect, oldCipher); !errors.Is(err, ErrSMTPCredentialContextChange) {
			t.Fatalf("%s redirect error = %v, want credential-context rejection", name, err)
		}
		unchanged := loadOnlySMTP(t, profile.Id, profile.UserId)
		if unchanged.PasswordCiphertext != originalCiphertext ||
			!sameSMTPCredentialRouting(unchanged, stored) {
			t.Fatalf("rejected %s redirect changed the stored profile", name)
		}
	}

	rotatedCipher := testCredentialCipher(t, "smtp-key-new", map[string][]byte{
		"smtp-key-old": bytes.Repeat([]byte{0x21}, 32),
		"smtp-key-new": bytes.Repeat([]byte{0x72}, 32),
	})
	update.Host = "localhost:2526"
	update.Username = "renamed-user"
	update.IgnoreCertErrors = true
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
	noAuth.Name = "Updated no-auth SMTP"
	noAuth.Host = "localhost:2526"
	noAuth.IgnoreCertErrors = true
	if err := PutSMTP(&noAuth, nil); err != nil {
		t.Fatalf("update no-auth routing without a password: %v", err)
	}
	stored = loadOnlySMTP(t, noAuth.Id, noAuth.UserId)
	if stored.Password != "" || stored.PasswordCiphertext != "" ||
		stored.Host != noAuth.Host || stored.IgnoreCertErrors != noAuth.IgnoreCertErrors {
		t.Fatal("no-auth routing update invented a credential or lost profile changes")
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

func TestSMTPPasswordByteBoundsAndPersistedVerification(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x34}, 32),
	})
	maxPassword := strings.Repeat("é", 127) + "a"
	overPassword := strings.Repeat("é", 128)
	if len(maxPassword) != smtpPasswordMaxBytes || len(overPassword) != smtpPasswordMaxBytes+1 {
		t.Fatal("test password fixtures do not exercise UTF-8 byte boundaries")
	}
	maxKeyID := strings.Repeat("k", 64)
	maxKeyCipher := testCredentialCipher(t, maxKeyID, map[string][]byte{
		maxKeyID: bytes.Repeat([]byte{0x36}, 32),
	})
	maxEnvelope, err := encryptSMTPPassword(maxKeyCipher, 1, 1, maxPassword)
	if err != nil {
		t.Fatalf("encrypt maximum envelope fixture: %v", err)
	}
	if len(maxEnvelope) != smtpPasswordEnvelopeMaxBytes ||
		len(maxEnvelope) > smtpPasswordCiphertextColumnBytes {
		t.Fatalf(
			"maximum envelope length = %d, derived=%d column=%d",
			len(maxEnvelope),
			smtpPasswordEnvelopeMaxBytes,
			smtpPasswordCiphertextColumnBytes,
		)
	}

	profile := validModelSMTP(maxPassword)
	profile.Name = "Maximum password"
	if err := PostSMTP(&profile, cipher); err != nil {
		t.Fatalf("create maximum-byte password: %v", err)
	}
	stored := loadOnlySMTP(t, profile.Id, profile.UserId)
	if len(stored.PasswordCiphertext) > smtpPasswordCiphertextColumnBytes {
		t.Fatal("generated envelope exceeds the declared ciphertext capacity")
	}
	if plaintext, err := DecryptSMTPPassword(stored, cipher); err != nil || plaintext != maxPassword {
		t.Fatalf("round-trip maximum-byte password: matched=%t err=%v", plaintext == maxPassword, err)
	}

	rejected := stored
	rejected.Password = overPassword
	rejected.Host = "localhost:2526"
	if err := PutSMTP(&rejected, cipher); !errors.Is(err, ErrSMTPCredentialTooLong) {
		t.Fatalf("over-limit update error = %v, want too long", err)
	}
	unchanged := loadOnlySMTP(t, profile.Id, profile.UserId)
	if unchanged.PasswordCiphertext != stored.PasswordCiphertext || unchanged.Host != stored.Host {
		t.Fatal("rejected over-limit update changed the prior row")
	}

	for name, password := range map[string]string{
		"over-limit":   overPassword,
		"invalid-UTF8": string([]byte{0xff}),
	} {
		candidate := validModelSMTP(password)
		candidate.Name = name
		err := PostSMTP(&candidate, cipher)
		if name == "over-limit" && !errors.Is(err, ErrSMTPCredentialTooLong) {
			t.Fatalf("over-limit create error = %v, want too long", err)
		}
		if name == "invalid-UTF8" && !errors.Is(err, ErrSMTPCredentialInvalidEncoding) {
			t.Fatalf("invalid UTF-8 create error = %v, want invalid encoding", err)
		}
		var count int
		if queryErr := db.Model(&SMTP{}).Where("name = ?", name).Count(&count).Error; queryErr != nil || count != 0 {
			t.Fatalf("rejected %s create left rows: count=%d err=%v", name, count, queryErr)
		}
	}

	if err := db.Exec(`
		CREATE TRIGGER truncate_smtp_ciphertext
		AFTER UPDATE OF password_ciphertext ON smtp
		WHEN NEW.name LIKE 'Truncated%'
		BEGIN
			UPDATE smtp
			SET password_ciphertext = substr(NEW.password_ciphertext, 1, 64)
			WHERE id = NEW.id;
		END
	`).Error; err != nil {
		t.Fatalf("create ciphertext truncation trigger: %v", err)
	}
	truncatedCreate := validModelSMTP(testSMTPSecret)
	truncatedCreate.Name = "Truncated create"
	if err := PostSMTP(&truncatedCreate, cipher); !errors.Is(err, ErrSMTPCredentialStorageMismatch) {
		t.Fatalf("silently truncated create error = %v, want storage mismatch", err)
	}
	var count int
	if err := db.Model(&SMTP{}).Where("name = ?", truncatedCreate.Name).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("truncated create was not rolled back: count=%d err=%v", count, err)
	}

	rotation := stored
	rotation.Name = "Truncated rotation"
	rotation.Password = testSMTPReplacement
	if err := PutSMTP(&rotation, cipher); !errors.Is(err, ErrSMTPCredentialStorageMismatch) {
		t.Fatalf("silently truncated rotation error = %v, want storage mismatch", err)
	}
	unchanged = loadOnlySMTP(t, profile.Id, profile.UserId)
	if unchanged.PasswordCiphertext != stored.PasswordCiphertext || unchanged.Name != stored.Name {
		t.Fatal("truncated rotation did not roll back the prior profile and ciphertext")
	}
}

func TestSMTPMigrationRollbackBoundsAndPersistedVerification(t *testing.T) {
	setupIMAPCredentialDatabase(t)
	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x35}, 32),
	})
	maxPassword := strings.Repeat("é", 127) + "a"
	overPassword := strings.Repeat("é", 128)

	if err := db.Exec(`
		INSERT INTO smtp (user_id, name, host, username, password, password_ciphertext, from_address)
		VALUES (1, 'Maximum legacy', 'localhost:25', 'legacy-user', ?, '', 'sender@example.test')
	`, maxPassword).Error; err != nil {
		t.Fatalf("seed maximum legacy password: %v", err)
	}
	if result, err := MigrateSMTPCredentials(cipher); err != nil || result.Updated != 1 {
		t.Fatalf("migrate maximum legacy password: result=%+v err=%v", result, err)
	}
	migrated, err := GetSMTPByName("Maximum legacy", 1)
	if err != nil {
		t.Fatalf("load maximum migrated profile: %v", err)
	}
	if plaintext, err := DecryptSMTPPassword(migrated, cipher); err != nil || plaintext != maxPassword {
		t.Fatalf("decrypt maximum migrated password: matched=%t err=%v", plaintext == maxPassword, err)
	}
	if result, err := RollbackSMTPCredentials(cipher); err != nil || result.Updated != 1 {
		t.Fatalf("rollback maximum password: result=%+v err=%v", result, err)
	}
	var restored string
	if err := db.Raw("SELECT password FROM smtp WHERE id = ?", migrated.Id).Row().Scan(&restored); err != nil || restored != maxPassword {
		t.Fatalf("maximum rollback did not restore exact bytes: matched=%t err=%v", restored == maxPassword, err)
	}

	if err := db.Exec(`
		INSERT INTO smtp (user_id, name, host, username, password, password_ciphertext, from_address)
		VALUES (1, 'Oversized legacy', 'localhost:25', 'legacy-user', ?, '', 'sender@example.test')
	`, overPassword).Error; err != nil {
		t.Fatalf("seed oversized legacy password: %v", err)
	}
	if _, err := MigrateSMTPCredentials(cipher); !errors.Is(err, ErrSMTPCredentialTooLong) {
		t.Fatalf("oversized migration error = %v, want too long", err)
	}
	var oversized storedSMTPCredential
	if err := db.Raw(`
		SELECT id, user_id, password, password_ciphertext
		FROM smtp WHERE name = 'Oversized legacy'
	`).Scan(&oversized).Error; err != nil {
		t.Fatalf("read rejected oversized migration row: %v", err)
	}
	if oversized.Password != overPassword || oversized.PasswordCiphertext != "" {
		t.Fatal("rejected oversized migration changed the legacy row")
	}

	if err := db.Exec("DELETE FROM smtp").Error; err != nil {
		t.Fatalf("reset SMTP rows: %v", err)
	}
	oversizedEncrypted := validModelSMTP("")
	oversizedEncrypted.Name = "Oversized encrypted"
	if err := db.Omit("Headers").Create(&oversizedEncrypted).Error; err != nil {
		t.Fatalf("seed oversized encrypted profile: %v", err)
	}
	envelope, err := cipher.Encrypt(
		smtpCredentialContext(oversizedEncrypted.UserId, oversizedEncrypted.Id),
		[]byte(overPassword),
	)
	if err != nil {
		t.Fatalf("create oversized test envelope: %v", err)
	}
	if err := db.Model(&SMTP{}).
		Where("id = ?", oversizedEncrypted.Id).
		Update("password_ciphertext", string(envelope)).Error; err != nil {
		t.Fatalf("store oversized test envelope: %v", err)
	}
	if _, err := RollbackSMTPCredentials(cipher); !errors.Is(err, ErrSMTPCredentialTooLong) {
		t.Fatalf("oversized rollback error = %v, want too long", err)
	}
	oversized = storedSMTPCredential{}
	if err := db.Raw(`
		SELECT id, user_id, password, password_ciphertext
		FROM smtp WHERE id = ?
	`, oversizedEncrypted.Id).Scan(&oversized).Error; err != nil {
		t.Fatalf("read rejected oversized rollback row: %v", err)
	}
	if oversized.Password != "" || oversized.PasswordCiphertext != string(envelope) {
		t.Fatal("rejected oversized rollback cleared or changed ciphertext")
	}

	if err := db.Exec("DELETE FROM smtp").Error; err != nil {
		t.Fatalf("reset SMTP rows for write verification: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO smtp (user_id, name, host, username, password, password_ciphertext, from_address)
		VALUES (1, 'Truncated migration', 'localhost:25', 'legacy-user', ?, '', 'sender@example.test')
	`, testSMTPSecret).Error; err != nil {
		t.Fatalf("seed truncation migration row: %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER truncate_smtp_migration
		AFTER UPDATE OF password_ciphertext ON smtp
		WHEN NEW.name = 'Truncated migration' AND NEW.password <> ''
		BEGIN
			UPDATE smtp SET password_ciphertext = substr(NEW.password_ciphertext, 1, 64)
			WHERE id = NEW.id;
		END
	`).Error; err != nil {
		t.Fatalf("create migration truncation trigger: %v", err)
	}
	if _, err := MigrateSMTPCredentials(cipher); !errors.Is(err, ErrSMTPCredentialStorageMismatch) {
		t.Fatalf("truncated migration error = %v, want storage mismatch", err)
	}
	var legacyPassword, legacyCiphertext string
	if err := db.Raw(`
		SELECT password, password_ciphertext FROM smtp WHERE name = 'Truncated migration'
	`).Row().Scan(&legacyPassword, &legacyCiphertext); err != nil {
		t.Fatalf("read rolled-back migration row: %v", err)
	}
	if legacyPassword != testSMTPSecret || legacyCiphertext != "" {
		t.Fatal("migration verification failure did not retain the only plaintext copy")
	}
	if err := db.Exec("DROP TRIGGER truncate_smtp_migration").Error; err != nil {
		t.Fatalf("drop migration truncation trigger: %v", err)
	}

	if err := db.Exec("DELETE FROM smtp").Error; err != nil {
		t.Fatalf("reset SMTP rows for rollback verification: %v", err)
	}
	rollbackProfile := validModelSMTP(testSMTPSecret)
	rollbackProfile.Name = "Truncated rollback"
	if err := PostSMTP(&rollbackProfile, cipher); err != nil {
		t.Fatalf("seed rollback verification profile: %v", err)
	}
	rollbackCiphertext := rollbackProfile.PasswordCiphertext
	if err := db.Exec(`
		CREATE TRIGGER truncate_smtp_rollback
		AFTER UPDATE OF password ON smtp
		WHEN NEW.name = 'Truncated rollback' AND NEW.password <> ''
		BEGIN
			UPDATE smtp SET password = substr(NEW.password, 1, 5) WHERE id = NEW.id;
		END
	`).Error; err != nil {
		t.Fatalf("create rollback truncation trigger: %v", err)
	}
	if _, err := RollbackSMTPCredentials(cipher); !errors.Is(err, ErrSMTPCredentialStorageMismatch) {
		t.Fatalf("truncated rollback error = %v, want storage mismatch", err)
	}
	rollbackStored := loadOnlySMTP(t, rollbackProfile.Id, rollbackProfile.UserId)
	if rollbackStored.Password != "" || rollbackStored.PasswordCiphertext != rollbackCiphertext {
		t.Fatal("rollback verification failure cleared the only ciphertext copy")
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
	oversizedCiphertext := SMTP{
		Id:                 1,
		UserId:             1,
		PasswordCiphertext: strings.Repeat("A", smtpPasswordEnvelopeMaxBytes+1),
	}
	if _, err := oversizedCiphertext.GetDialer(cipher); !errors.Is(err, ErrSMTPCredentialInvalidState) {
		t.Fatalf("oversized runtime ciphertext error = %v, want invalid state", err)
	}
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

func TestMySQLSMTPCredentialStorageBoundsNonStrict(t *testing.T) {
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
	for _, table := range []string{"imap", "headers", "smtp"} {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("clear MySQL %s rows: %v", table, err)
		}
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
		_ = db.Exec("ALTER TABLE smtp MODIFY password VARCHAR(255)").Error
		_ = db.Exec("ALTER TABLE smtp MODIFY password_ciphertext VARCHAR(2048) NOT NULL DEFAULT ''").Error
		_ = db.Exec("SET SESSION sql_mode = ?", originalSQLMode).Error
		_ = db.Exec("DELETE FROM headers").Error
		_ = db.Exec("DELETE FROM smtp").Error
		_ = database.Close()
	})

	cipher := testCredentialCipher(t, "smtp-key", map[string][]byte{
		"smtp-key": bytes.Repeat([]byte{0x79}, 32),
	})
	maxPassword := strings.Repeat("é", 127) + "a"
	overPassword := strings.Repeat("é", 128)
	profile := validModelSMTP(maxPassword)
	profile.Name = "MySQL maximum"
	if err := PostSMTP(&profile, cipher); err != nil {
		t.Fatalf("create maximum-byte MySQL password: %v", err)
	}
	stored := loadOnlySMTP(t, profile.Id, profile.UserId)
	if plaintext, err := DecryptSMTPPassword(stored, cipher); err != nil || plaintext != maxPassword {
		t.Fatalf("round-trip maximum MySQL password: matched=%t err=%v", plaintext == maxPassword, err)
	}

	rejectedUpdate := stored
	rejectedUpdate.Password = overPassword
	rejectedUpdate.Host = "localhost:2526"
	if err := PutSMTP(&rejectedUpdate, cipher); !errors.Is(err, ErrSMTPCredentialTooLong) {
		t.Fatalf("over-limit MySQL update error = %v, want too long", err)
	}
	unchanged := loadOnlySMTP(t, profile.Id, profile.UserId)
	if unchanged.PasswordCiphertext != stored.PasswordCiphertext || unchanged.Host != stored.Host {
		t.Fatal("rejected MySQL update changed the prior row")
	}
	rejectedCreate := validModelSMTP(overPassword)
	rejectedCreate.Name = "MySQL oversized"
	if err := PostSMTP(&rejectedCreate, cipher); !errors.Is(err, ErrSMTPCredentialTooLong) {
		t.Fatalf("over-limit MySQL create error = %v, want too long", err)
	}
	var count int
	if err := db.Model(&SMTP{}).Where("name = ?", rejectedCreate.Name).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected MySQL create left rows: count=%d err=%v", count, err)
	}

	if result, err := RollbackSMTPCredentials(cipher); err != nil || result.Updated != 1 {
		t.Fatalf("rollback maximum MySQL password: result=%+v err=%v", result, err)
	}
	var restored string
	var restoredBytes int
	if err := db.Raw(`
		SELECT password, OCTET_LENGTH(password)
		FROM smtp WHERE id = ?
	`, profile.Id).Row().Scan(&restored, &restoredBytes); err != nil {
		t.Fatalf("read maximum MySQL rollback: %v", err)
	}
	if restored != maxPassword || restoredBytes != smtpPasswordMaxBytes {
		t.Fatalf("maximum MySQL rollback mismatch: bytes=%d matched=%t", restoredBytes, restored == maxPassword)
	}
	if result, err := MigrateSMTPCredentials(cipher); err != nil || result.Updated != 1 {
		t.Fatalf("re-migrate maximum MySQL password: result=%+v err=%v", result, err)
	}
	remigrated := loadOnlySMTP(t, profile.Id, profile.UserId)
	if plaintext, err := DecryptSMTPPassword(remigrated, cipher); err != nil || plaintext != maxPassword {
		t.Fatalf("re-migrated maximum MySQL password mismatch: matched=%t err=%v", plaintext == maxPassword, err)
	}

	if err := db.Exec("DELETE FROM smtp").Error; err != nil {
		t.Fatalf("reset MySQL rows: %v", err)
	}
	oversizedEncrypted := validModelSMTP("")
	oversizedEncrypted.Name = "MySQL oversized encrypted"
	if err := PostSMTP(&oversizedEncrypted, nil); err != nil {
		t.Fatalf("seed MySQL oversized encrypted profile: %v", err)
	}
	envelope, err := cipher.Encrypt(
		smtpCredentialContext(oversizedEncrypted.UserId, oversizedEncrypted.Id),
		[]byte(overPassword),
	)
	if err != nil {
		t.Fatalf("create oversized MySQL envelope: %v", err)
	}
	if err := db.Model(&SMTP{}).
		Where("id = ?", oversizedEncrypted.Id).
		Update("password_ciphertext", string(envelope)).Error; err != nil {
		t.Fatalf("store oversized MySQL envelope: %v", err)
	}
	if _, err := RollbackSMTPCredentials(cipher); !errors.Is(err, ErrSMTPCredentialTooLong) {
		t.Fatalf("oversized MySQL rollback error = %v, want too long", err)
	}
	var rejectedPassword, retainedCiphertext string
	if err := db.Raw(`
		SELECT password, password_ciphertext FROM smtp WHERE id = ?
	`, oversizedEncrypted.Id).Row().Scan(&rejectedPassword, &retainedCiphertext); err != nil {
		t.Fatalf("read rejected MySQL rollback: %v", err)
	}
	if rejectedPassword != "" || retainedCiphertext != string(envelope) {
		t.Fatal("rejected MySQL rollback cleared or changed ciphertext")
	}

	if err := db.Exec("DELETE FROM smtp").Error; err != nil {
		t.Fatalf("reset MySQL rows for ciphertext truncation: %v", err)
	}
	if err := db.Exec(`
		ALTER TABLE smtp
		MODIFY password_ciphertext VARCHAR(100) NOT NULL DEFAULT ''
	`).Error; err != nil {
		t.Fatalf("reduce MySQL ciphertext capacity: %v", err)
	}
	truncatedCreate := validModelSMTP(testSMTPSecret)
	truncatedCreate.Name = "MySQL truncated ciphertext"
	if err := PostSMTP(&truncatedCreate, cipher); !errors.Is(err, ErrSMTPCredentialStorageMismatch) {
		t.Fatalf("non-strict ciphertext truncation error = %v, want storage mismatch", err)
	}
	if err := db.Model(&SMTP{}).Where("name = ?", truncatedCreate.Name).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("truncated MySQL create was not rolled back: count=%d err=%v", count, err)
	}
	if err := db.Exec(`
		ALTER TABLE smtp
		MODIFY password_ciphertext VARCHAR(2048) NOT NULL DEFAULT ''
	`).Error; err != nil {
		t.Fatalf("restore MySQL ciphertext capacity: %v", err)
	}

	rollbackProfile := validModelSMTP("twenty-byte-password")
	rollbackProfile.Name = "MySQL truncated rollback"
	if err := PostSMTP(&rollbackProfile, cipher); err != nil {
		t.Fatalf("seed MySQL rollback truncation profile: %v", err)
	}
	rollbackCiphertext := rollbackProfile.PasswordCiphertext
	if err := db.Exec("ALTER TABLE smtp MODIFY password VARCHAR(10)").Error; err != nil {
		t.Fatalf("reduce MySQL plaintext capacity: %v", err)
	}
	if _, err := RollbackSMTPCredentials(cipher); !errors.Is(err, ErrSMTPCredentialStorageMismatch) {
		t.Fatalf("non-strict plaintext truncation error = %v, want storage mismatch", err)
	}
	rollbackStored := loadOnlySMTP(t, rollbackProfile.Id, rollbackProfile.UserId)
	if rollbackStored.Password != "" || rollbackStored.PasswordCiphertext != rollbackCiphertext {
		t.Fatal("MySQL rollback verification failure cleared the only ciphertext copy")
	}
}
