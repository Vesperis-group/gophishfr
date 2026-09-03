package models

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/auth"
	"github.com/Vesperis-group/gophishfr/config"
	log "github.com/Vesperis-group/gophishfr/logger"
)

const syntheticBootstrapPassword = "synthetic-bootstrap-value-42"

func clearBootstrapEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{InitialAdminPasswordFile, InitialAdminPassword, InitialAdminApiToken} {
		old, present := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(name, old)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func writeBootstrapFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(path, data, 0o400); err != nil {
		t.Fatal(err)
	}
	return path
}

func bootstrapConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		DBName:         "sqlite3",
		DBPath:         filepath.Join(t.TempDir(), "bootstrap.db"),
		MigrationsPath: "../db/db_sqlite3/migrations",
	}
}

func closeBootstrapDB(t *testing.T) {
	t.Helper()
	if db != nil {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		db = nil
	}
}

func TestInitialAdminPasswordFileParsing(t *testing.T) {
	valid := []struct {
		name string
		data string
		want string
	}{
		{"plain", syntheticBootstrapPassword, syntheticBootstrapPassword},
		{"LF", syntheticBootstrapPassword + "\n", syntheticBootstrapPassword},
		{"CRLF", syntheticBootstrapPassword + "\r\n", syntheticBootstrapPassword},
		{"only one ending removed", syntheticBootstrapPassword + "\n\n", syntheticBootstrapPassword + "\n"},
		{"space before newline", syntheticBootstrapPassword + " \n", syntheticBootstrapPassword + " "},
		{"leading space", " " + syntheticBootstrapPassword, " " + syntheticBootstrapPassword},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readInitialAdminPasswordFile(writeBootstrapFile(t, []byte(tc.data)))
			if err != nil {
				t.Fatalf("expected valid input: %v", err)
			}
			if got != tc.want {
				t.Fatal("password file parsing changed bytes unexpectedly")
			}
		})
	}

	invalid := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"LF only", []byte("\n")},
		{"CRLF only", []byte("\r\n")},
		{"too short", []byte("short")},
		{"NUL", append([]byte("synthetic"), 0, 'v', 'a', 'l', 'u', 'e')},
		{"invalid UTF-8", append([]byte("synthetic"), 0xff)},
		{"over bcrypt limit", bytes.Repeat([]byte{'x'}, maxBcryptPasswordBytes+1)},
		{"oversized with CRLF", append(bytes.Repeat([]byte{'x'}, maxBcryptPasswordBytes+1), '\r', '\n')},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readInitialAdminPasswordFile(writeBootstrapFile(t, tc.data)); err == nil {
				t.Fatal("expected password file rejection")
			}
		})
	}

	t.Run("directory", func(t *testing.T) {
		if _, err := readInitialAdminPasswordFile(t.TempDir()); err == nil {
			t.Fatal("expected directory rejection")
		}
	})

	t.Run("missing", func(t *testing.T) {
		if _, err := readInitialAdminPasswordFile(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected missing file rejection")
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		path := writeBootstrapFile(t, []byte(syntheticBootstrapPassword))
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := readInitialAdminPasswordFile(path); err == nil {
			t.Skip("current test identity can read mode-000 files")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		target := writeBootstrapFile(t, []byte(syntheticBootstrapPassword))
		link := filepath.Join(t.TempDir(), "mounted-secret")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readInitialAdminPasswordFile(link); err != nil {
			t.Fatalf("expected symlink-backed secret to work: %v", err)
		}
	})
}

func TestInitialAdminPasswordSourcePriorityAndValidation(t *testing.T) {
	clearBootstrapEnvironment(t)

	t.Run("file wins", func(t *testing.T) {
		clearBootstrapEnvironment(t)
		t.Setenv(InitialAdminPasswordFile, writeBootstrapFile(t, []byte(syntheticBootstrapPassword)))
		t.Setenv(InitialAdminPassword, "synthetic-environment-value")
		got, err := resolveInitialAdminPassword()
		if err != nil {
			t.Fatal(err)
		}
		if got != syntheticBootstrapPassword {
			t.Fatal("file source did not take precedence")
		}
	})

	t.Run("invalid file never falls back", func(t *testing.T) {
		clearBootstrapEnvironment(t)
		t.Setenv(InitialAdminPasswordFile, filepath.Join(t.TempDir(), "missing"))
		t.Setenv(InitialAdminPassword, syntheticBootstrapPassword)
		if _, err := resolveInitialAdminPassword(); err == nil {
			t.Fatal("expected authoritative file error")
		}
	})

	t.Run("empty file path is authoritative", func(t *testing.T) {
		clearBootstrapEnvironment(t)
		t.Setenv(InitialAdminPasswordFile, "")
		t.Setenv(InitialAdminPassword, syntheticBootstrapPassword)
		if _, err := resolveInitialAdminPassword(); err == nil {
			t.Fatal("expected empty file path error")
		}
	})

	t.Run("environment fallback", func(t *testing.T) {
		clearBootstrapEnvironment(t)
		t.Setenv(InitialAdminPassword, syntheticBootstrapPassword)
		if _, err := resolveInitialAdminPassword(); err != nil {
			t.Fatalf("expected environment fallback: %v", err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		clearBootstrapEnvironment(t)
		if _, err := resolveInitialAdminPassword(); err == nil ||
			!strings.Contains(err.Error(), InitialAdminPasswordFile) ||
			!strings.Contains(err.Error(), InitialAdminPassword) {
			t.Fatal("expected actionable missing-source error")
		}
	})

	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{"too short", "short", false},
		{"72 bytes", strings.Repeat("x", 72), true},
		{"73 bytes", strings.Repeat("x", 73), false},
		{"invalid UTF-8", "synthetic\xffvalue", false},
	} {
		t.Run("environment "+tc.name, func(t *testing.T) {
			clearBootstrapEnvironment(t)
			t.Setenv(InitialAdminPassword, tc.value)
			_, err := resolveInitialAdminPassword()
			if (err == nil) != tc.valid {
				t.Fatal("unexpected environment validation result")
			}
			if err != nil && strings.Contains(err.Error(), tc.value) {
				t.Fatal("validation error disclosed environment input")
			}
		})
	}
}

func TestFreshBootstrapIsAtomicAndDoesNotLogSecret(t *testing.T) {
	clearBootstrapEnvironment(t)
	conf := bootstrapConfig(t)
	t.Cleanup(func() { closeBootstrapDB(t) })

	if err := Setup(conf); err == nil {
		t.Fatal("expected fresh bootstrap without a source to fail")
	}
	var count int
	if err := db.Model(&User{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed bootstrap created a user row")
	}
	closeBootstrapDB(t)

	t.Setenv(InitialAdminPasswordFile, filepath.Join(t.TempDir(), "missing"))
	t.Setenv(InitialAdminPassword, syntheticBootstrapPassword)
	if err := Setup(conf); err == nil {
		t.Fatal("expected invalid authoritative file to fail fresh bootstrap")
	}
	if err := db.Model(&User{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid file bootstrap created a user row")
	}
	closeBootstrapDB(t)

	passwordFile := writeBootstrapFile(t, []byte(syntheticBootstrapPassword+"\n"))
	t.Setenv(InitialAdminPasswordFile, passwordFile)
	t.Setenv(InitialAdminApiToken, "synthetic-api-token-value")
	var output bytes.Buffer
	logPath := filepath.Join(t.TempDir(), "application.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logFile.Close() })
	originalOutput := log.Logger.Out
	log.Logger.SetOutput(io.MultiWriter(&output, logFile))
	t.Cleanup(func() { log.Logger.SetOutput(originalOutput) })

	if err := Setup(conf); err != nil {
		t.Fatalf("fresh bootstrap failed: %v", err)
	}
	admin, err := GetUserByUsername(DefaultAdminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if admin.Hash == "" || admin.Hash == syntheticBootstrapPassword {
		t.Fatal("bootstrap did not persist a non-plaintext hash")
	}
	if err := auth.ValidatePassword(syntheticBootstrapPassword, admin.Hash); err != nil {
		t.Fatal("stored hash does not validate")
	}
	if !admin.PasswordChangeRequired {
		t.Fatal("forced first password change was not preserved")
	}
	if admin.ApiKey != "synthetic-api-token-value" {
		t.Fatal("initial API token behavior changed")
	}
	if strings.Contains(output.String(), syntheticBootstrapPassword) {
		t.Fatal("bootstrap secret reached logger output")
	}
	if err := logFile.Sync(); err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(logData, []byte(syntheticBootstrapPassword)) {
		t.Fatal("bootstrap secret reached application log file")
	}

	originalHash := admin.Hash
	closeBootstrapDB(t)
	t.Setenv(InitialAdminPasswordFile, filepath.Join(t.TempDir(), "must-not-be-read"))
	if err := Setup(conf); err != nil {
		t.Fatalf("initialized restart read bootstrap source: %v", err)
	}
	restarted, err := GetUserByUsername(DefaultAdminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Hash != originalHash {
		t.Fatal("restart replaced initialized hash")
	}
}

func TestHistoricalEmptyHashRecovery(t *testing.T) {
	clearBootstrapEnvironment(t)
	conf := bootstrapConfig(t)
	t.Cleanup(func() { closeBootstrapDB(t) })

	if err := Setup(conf); err == nil {
		t.Fatal("expected initial no-source failure")
	}
	role, err := GetRoleBySlug(RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	partial := User{
		Username:               DefaultAdminUsername,
		ApiKey:                 "synthetic-partial-api-token",
		Role:                   role,
		RoleID:                 role.ID,
		PasswordChangeRequired: true,
	}
	if err := db.Create(&partial).Error; err != nil {
		t.Fatal(err)
	}
	closeBootstrapDB(t)

	if err := Setup(conf); err == nil {
		t.Fatal("expected missing-source recovery failure")
	}
	stillPartial, err := GetUserByUsername(DefaultAdminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if stillPartial.Hash != "" {
		t.Fatal("failed recovery modified partial administrator")
	}
	closeBootstrapDB(t)

	t.Setenv(InitialAdminPassword, syntheticBootstrapPassword)
	if err := Setup(conf); err != nil {
		t.Fatalf("historical recovery failed: %v", err)
	}
	recovered, err := GetUserByUsername(DefaultAdminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Hash == "" || !recovered.PasswordChangeRequired {
		t.Fatal("historical recovery did not initialize required fields")
	}
	firstHash := recovered.Hash
	closeBootstrapDB(t)

	clearBootstrapEnvironment(t)
	if err := Setup(conf); err != nil {
		t.Fatalf("recovered installation required a source: %v", err)
	}
	restarted, err := GetUserByUsername(DefaultAdminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Hash != firstHash {
		t.Fatal("historical recovery ran more than once")
	}
}
