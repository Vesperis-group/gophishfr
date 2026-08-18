package models

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jinzhu/gorm"
	"github.com/pressly/goose/v3"
)

type databaseDriverRecord struct {
	ID    int64
	Value string
}

func TestMigrationDialect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		database string
		want     string
		wantErr  bool
	}{
		{name: "SQLite", database: "sqlite3", want: "sqlite3"},
		{name: "MySQL", database: "mysql", want: "mysql"},
		{name: "PostgreSQL canonical name", database: "postgres", want: "postgres"},
		{name: "PostgreSQL alias", database: "postgresql", want: "postgres"},
		{name: "unsupported", database: "oracle", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := migrationDialect(test.database)
			if test.wantErr {
				if err == nil {
					t.Fatal("migrationDialect() returned no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("migrationDialect() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("migrationDialect() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPostgresConnectionTLS(t *testing.T) {
	t.Setenv("PGSSLMODE", "")
	t.Setenv("PGHOST", "")
	t.Setenv("PGSERVICE", "")

	tests := []struct {
		name             string
		connection       string
		wantPlaintext    bool
		wantServerName   string
		wantParseFailure bool
	}{
		{
			name:           "URL defaults to required TLS",
			connection:     "postgres://user:password@database.example/test",
			wantServerName: "database.example",
		},
		{
			name:           "keyword DSN defaults to required TLS",
			connection:     "host=database.example user=test password=test",
			wantServerName: "database.example",
		},
		{
			name:           "URL empty mode defaults to required TLS",
			connection:     "postgres://user:password@database.example/test?sslmode=",
			wantServerName: "database.example",
		},
		{
			name:           "keyword DSN empty mode defaults to required TLS",
			connection:     "host=database.example user=test password=test sslmode=",
			wantServerName: "database.example",
		},
		{
			name:           "at-prefixed host remains TLS protected",
			connection:     "host=@database.example user=test password=test sslmode=",
			wantServerName: "@database.example",
		},
		{
			name:          "URL preserves explicit disable",
			connection:    "postgres://user:password@database.example/test?sslmode=disable",
			wantPlaintext: true,
		},
		{
			name:          "keyword DSN preserves explicit disable",
			connection:    "host=database.example user=test password=test sslmode=disable",
			wantPlaintext: true,
		},
		{
			name:           "explicit allow is promoted to TLS",
			connection:     "host=database.example user=test password=test sslmode=allow",
			wantServerName: "database.example",
		},
		{
			name:             "URL rejects duplicate modes",
			connection:       "postgres://database.example/test?sslmode=require&sslmode=disable",
			wantParseFailure: true,
		},
		{
			name:             "URL rejects malformed escaping",
			connection:       "postgres://database.example/%zz",
			wantParseFailure: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			configuration, err := postgresConnectionConfig(test.connection)
			if test.wantParseFailure {
				if err == nil {
					t.Fatal("postgresConnectionConfig() returned no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("postgresConnectionConfig() error = %v", err)
			}

			if got := configuration.TLSConfig == nil; got != test.wantPlaintext {
				t.Fatalf("plaintext configuration = %t, want %t", got, test.wantPlaintext)
			}
			if test.wantServerName != "" && configuration.TLSConfig.ServerName != test.wantServerName {
				t.Fatalf(
					"TLS server name = %q, want %q",
					configuration.TLSConfig.ServerName,
					test.wantServerName,
				)
			}
			for _, fallback := range configuration.Fallbacks {
				if fallback.TLSConfig == nil && !test.wantPlaintext {
					t.Fatal("secure PostgreSQL configuration includes a plaintext fallback")
				}
			}
		})
	}

	t.Run("environment preserves explicit disable", func(t *testing.T) {
		t.Setenv("PGSSLMODE", "disable")

		configuration, err := postgresConnectionConfig(
			"host=database.example user=test password=test",
		)
		if err != nil {
			t.Fatalf("postgresConnectionConfig() error = %v", err)
		}
		if configuration.TLSConfig != nil {
			t.Fatal("explicit PGSSLMODE=disable was not preserved")
		}
		for _, fallback := range configuration.Fallbacks {
			if fallback.TLSConfig != nil {
				t.Fatal("explicit PGSSLMODE=disable includes a TLS fallback")
			}
		}
	})
}

func TestPostgresConnectionTLSWithUnixSocketFallback(t *testing.T) {
	t.Setenv("PGSSLMODE", "")
	t.Setenv("PGHOST", "")
	t.Setenv("PGSERVICE", "")

	configuration, err := postgresConnectionConfig(
		"host=/var/run/postgresql,database.example user=test password=test sslmode=",
	)
	if err != nil {
		t.Fatalf("postgresConnectionConfig() error = %v", err)
	}
	if configuration.TLSConfig != nil {
		t.Fatal("Unix socket primary unexpectedly uses TLS")
	}

	foundSecureRemoteFallback := false
	for _, fallback := range configuration.Fallbacks {
		if fallback.Host != "database.example" {
			continue
		}
		if fallback.TLSConfig == nil {
			t.Fatal("remote PostgreSQL host includes a plaintext fallback")
		}
		foundSecureRemoteFallback = true
	}
	if !foundSecureRemoteFallback {
		t.Fatal("remote PostgreSQL host has no TLS fallback")
	}
}

func TestPostgresConnectionHostCompatibility(t *testing.T) {
	t.Setenv("PGHOST", "")
	t.Setenv("PGSERVICE", "")

	tests := []struct {
		name       string
		connection string
		wantHost   string
		wantPort   uint16
	}{
		{
			name:       "keyword DSN defaults to localhost",
			connection: "user=test dbname=test sslmode=disable",
			wantHost:   "localhost",
		},
		{
			name:       "URL defaults to localhost",
			connection: "postgres:///test?sslmode=disable",
			wantHost:   "localhost",
		},
		{
			name:       "URL default preserves explicit port",
			connection: "postgres://:6543/test?sslmode=disable",
			wantHost:   "localhost",
			wantPort:   6543,
		},
		{
			name:       "explicit keyword socket is preserved",
			connection: "host=/var/run/postgresql user=test sslmode=disable",
			wantHost:   "/var/run/postgresql",
		},
		{
			name:       "unrelated service text still defaults to localhost",
			connection: "dbname=test application_name=gophish-service sslmode=disable",
			wantHost:   "localhost",
		},
		{
			name:       "quoted service text still defaults to localhost",
			connection: "dbname=test password='contains service=value' sslmode=disable",
			wantHost:   "localhost",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration, err := postgresConnectionConfig(test.connection)
			if err != nil {
				t.Fatalf("postgresConnectionConfig() error = %v", err)
			}
			if configuration.Host != test.wantHost {
				t.Fatalf("PostgreSQL host = %q, want %q", configuration.Host, test.wantHost)
			}
			if test.wantPort != 0 && configuration.Port != test.wantPort {
				t.Fatalf("PostgreSQL port = %d, want %d", configuration.Port, test.wantPort)
			}
		})
	}

	t.Run("PGHOST is preserved", func(t *testing.T) {
		t.Setenv("PGHOST", "/tmp/postgresql")

		configuration, err := postgresConnectionConfig(
			"user=test dbname=test sslmode=disable",
		)
		if err != nil {
			t.Fatalf("postgresConnectionConfig() error = %v", err)
		}
		if configuration.Host != "/tmp/postgresql" {
			t.Fatalf("PostgreSQL host = %q, want /tmp/postgresql", configuration.Host)
		}
	})
}

func TestPostgresKeywordHasKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		connection string
		target     string
		want       bool
	}{
		{connection: "host = database.example user=test", target: "host", want: true},
		{connection: "service = production user=test", target: "service", want: true},
		{connection: "application_name=gophish-service", target: "service"},
		{connection: "password='contains service=value' user=test", target: "service"},
		{connection: `password=contains\ service=value user=test`, target: "service"},
	}

	for _, test := range tests {
		got := postgresKeywordHasKey(test.connection, test.target)
		if got != test.want {
			t.Fatalf(
				"postgresKeywordHasKey(%q, %q) = %t, want %t",
				test.connection,
				test.target,
				got,
				test.want,
			)
		}
	}
}

func TestResolveMigrationsPath(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	nestedPath := filepath.Join(rootPath, "migrations")
	if err := os.Mkdir(nestedPath, 0o700); err != nil {
		t.Fatalf("create nested migrations directory: %v", err)
	}

	got, err := resolveMigrationsPath(rootPath)
	if err != nil {
		t.Fatalf("resolveMigrationsPath() error = %v", err)
	}
	if got != nestedPath {
		t.Fatalf("resolveMigrationsPath() = %q, want %q", got, nestedPath)
	}

	directPath := t.TempDir()
	got, err = resolveMigrationsPath(directPath)
	if err != nil {
		t.Fatalf("resolveMigrationsPath() direct error = %v", err)
	}
	if got != directPath {
		t.Fatalf("resolveMigrationsPath() direct = %q, want %q", got, directPath)
	}
}

func TestMigrateLegacyGooseDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close legacy sqlite database: %v", err)
		}
	}()

	_, err = database.Exec(`
		CREATE TABLE goose_db_version (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL,
			tstamp TIMESTAMP DEFAULT (datetime('now'))
		);
		INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1);
		INSERT INTO goose_db_version (version_id, is_applied) VALUES (20160118194630, 1);
		INSERT INTO goose_db_version (version_id, is_applied) VALUES (20160118194630, 0);
	`)
	if err != nil {
		t.Fatalf("create legacy Goose metadata: %v", err)
	}

	if err := migrateDatabase(database, "sqlite3", "../db/db_sqlite3"); err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}

	var usersTable string
	if err := database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'users'",
	).Scan(&usersTable); err != nil {
		t.Fatalf("query migrated users table: %v", err)
	}
	if usersTable != "users" {
		t.Fatalf("migrated table = %q, want users", usersTable)
	}
}

func TestPostgresDriverIntegration(t *testing.T) {
	connectionString := os.Getenv("GOPHISHFR_POSTGRES_TEST_DSN")
	if connectionString == "" {
		t.Skip("GOPHISHFR_POSTGRES_TEST_DSN is not set")
	}

	runSyntheticMigrationDriverIntegration(t, "postgres", connectionString, `-- +goose Up
CREATE TABLE gophishfr_driver_test (
	id BIGSERIAL PRIMARY KEY,
	value TEXT NOT NULL
);

-- +goose Down
DROP TABLE gophishfr_driver_test;
`)
}

func TestMySQLDriverIntegration(t *testing.T) {
	connectionString := os.Getenv("GOPHISHFR_MYSQL_TEST_DSN")
	if connectionString == "" {
		t.Skip("GOPHISHFR_MYSQL_TEST_DSN is not set")
	}

	database, err := openDatabase("mysql", connectionString)
	if err != nil {
		t.Fatalf("open mysql database: %v", err)
	}
	sqlDB := database.DB()
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close mysql database: %v", err)
		}
	}()

	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping mysql database: %v", err)
	}
	if _, err := sqlDB.Exec(`
		CREATE TABLE goose_db_version (
			id SERIAL NOT NULL,
			version_id BIGINT NOT NULL,
			is_applied BOOLEAN NOT NULL,
			tstamp TIMESTAMP NULL DEFAULT NOW(),
			PRIMARY KEY (id)
		)
	`); err != nil {
		t.Fatalf("create legacy mysql Goose metadata: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, true)",
	); err != nil {
		t.Fatalf("seed legacy mysql Goose metadata: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (20160118194630, true)",
	); err != nil {
		t.Fatalf("seed applied legacy mysql migration metadata: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (20160118194630, false)",
	); err != nil {
		t.Fatalf("seed rolled-back legacy mysql migration metadata: %v", err)
	}
	if err := migrateDatabase(sqlDB, "mysql", "../db/db_mysql"); err != nil {
		t.Fatalf("run application migrations on mysql: %v", err)
	}
	if err := database.Exec(`
		CREATE TABLE gophishfr_driver_test (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create mysql driver test table: %v", err)
	}
	defer func() {
		if err := database.Exec("DROP TABLE gophishfr_driver_test").Error; err != nil {
			t.Errorf("drop mysql driver test table: %v", err)
		}
	}()

	exerciseDatabaseCRUD(t, database, "mysql")
}

func runSyntheticMigrationDriverIntegration(
	t *testing.T,
	databaseName string,
	connectionString string,
	migration string,
) {
	t.Helper()

	migrationsPath := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(migrationsPath, "00001_create_driver_test.sql"),
		[]byte(migration),
		0o600,
	); err != nil {
		t.Fatalf("write %s test migration: %v", databaseName, err)
	}

	database, err := openDatabase(databaseName, connectionString)
	if err != nil {
		t.Fatalf("open %s database: %v", databaseName, err)
	}
	sqlDB := database.DB()
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close %s database: %v", databaseName, err)
		}
	}()

	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping %s database: %v", databaseName, err)
	}
	if err := migrateDatabase(sqlDB, databaseName, migrationsPath); err != nil {
		t.Fatalf("migrate %s database: %v", databaseName, err)
	}
	defer func() {
		if err := goose.Down(sqlDB, migrationsPath); err != nil {
			t.Errorf("roll back %s test migration: %v", databaseName, err)
		}
	}()

	exerciseDatabaseCRUD(t, database, databaseName)
}

func exerciseDatabaseCRUD(t *testing.T, database *gorm.DB, databaseName string) {
	t.Helper()

	record := databaseDriverRecord{Value: "created"}
	if err := database.Table("gophishfr_driver_test").Create(&record).Error; err != nil {
		t.Fatalf("create %s record: %v", databaseName, err)
	}
	var stored databaseDriverRecord
	if err := database.Table("gophishfr_driver_test").First(&stored, record.ID).Error; err != nil {
		t.Fatalf("read %s record: %v", databaseName, err)
	}
	if stored.Value != "created" {
		t.Fatalf("%s record value = %q, want created", databaseName, stored.Value)
	}
	if err := database.Table("gophishfr_driver_test").
		Model(&stored).
		Update("value", "updated").Error; err != nil {
		t.Fatalf("update %s record: %v", databaseName, err)
	}
	if err := database.Table("gophishfr_driver_test").First(&stored, record.ID).Error; err != nil {
		t.Fatalf("read updated %s record: %v", databaseName, err)
	}
	if stored.Value != "updated" {
		t.Fatalf("updated %s record value = %q, want updated", databaseName, stored.Value)
	}
	if err := database.Table("gophishfr_driver_test").Delete(&stored).Error; err != nil {
		t.Fatalf("delete %s record: %v", databaseName, err)
	}

	transaction := database.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin %s transaction: %v", databaseName, transaction.Error)
	}
	if err := transaction.Exec(
		"INSERT INTO gophishfr_driver_test (value) VALUES (?)",
		"temporary",
	).Error; err != nil {
		_ = transaction.Rollback()
		t.Fatalf("insert %s record: %v", databaseName, err)
	}
	if err := transaction.Rollback().Error; err != nil {
		t.Fatalf("rollback %s transaction: %v", databaseName, err)
	}

	var count int
	if err := database.Table("gophishfr_driver_test").Count(&count).Error; err != nil {
		t.Fatalf("count %s records: %v", databaseName, err)
	}
	if count != 0 {
		t.Fatalf("%s records after rollback = %d, want 0", databaseName, count)
	}
}
