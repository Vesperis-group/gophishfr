package models

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/Vesperis-group/gophishfr/auth"
	"github.com/Vesperis-group/gophishfr/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3" // Blank import needed to import sqlite3
	"github.com/pressly/goose/v3"

	log "github.com/Vesperis-group/gophishfr/logger"
)

var db *gorm.DB
var conf *config.Config

const MaxDatabaseConnectionAttempts int = 10

// DefaultAdminUsername is the default username for the administrative user
const DefaultAdminUsername = "admin"

// InitialAdminPassword is the environment variable that specifies which
// password to use for the initial root login instead of generating one
// randomly
const InitialAdminPassword = "GOPHISH_INITIAL_ADMIN_PASSWORD"

// InitialAdminApiToken is the environment variable that specifies the
// API token to seed the initial root login instead of generating one
// randomly
const InitialAdminApiToken = "GOPHISH_INITIAL_ADMIN_API_TOKEN"

const (
	CampaignInProgress string = "In progress"
	CampaignQueued     string = "Queued"
	CampaignCreated    string = "Created"
	CampaignEmailsSent string = "Emails Sent"
	CampaignComplete   string = "Completed"
	EventSent          string = "Email Sent"
	EventSendingError  string = "Error Sending Email"
	EventOpened        string = "Email Opened"
	EventClicked       string = "Clicked Link"
	EventDataSubmit    string = "Submitted Data"
	EventReported      string = "Email Reported"
	EventProxyRequest  string = "Proxied request"
	StatusSuccess      string = "Success"
	StatusQueued       string = "Queued"
	StatusSending      string = "Sending"
	StatusUnknown      string = "Unknown"
	StatusScheduled    string = "Scheduled"
	StatusRetry        string = "Retrying"
	Error              string = "Error"
)

// Flash is used to hold flash information for use in templates.
type Flash struct {
	Type    string
	Message string
}

// Response contains the attributes found in an API response
type Response struct {
	Message string      `json:"message"`
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
}

func openDatabase(name, connectionString string) (*gorm.DB, error) {
	switch name {
	case "postgres", "postgresql":
		postgresConfig, err := postgresConnectionConfig(connectionString)
		if err != nil {
			return nil, err
		}
		sqlDB := stdlib.OpenDB(*postgresConfig)
		// Keep GORM's PostgreSQL SQL dialect while pgx owns the connection.
		database, err := gorm.Open("postgres", sqlDB)
		if err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		return database, nil
	default:
		return gorm.Open(name, connectionString)
	}
}

func postgresConnectionConfig(connectionString string) (*pgx.ConnConfig, error) {
	validatedConnectionString, err := validatePostgresConnectionString(connectionString)
	if err != nil {
		return nil, err
	}
	defaultedConnectionString, err := applyLegacyPostgresHostDefault(validatedConnectionString)
	if err != nil {
		return nil, err
	}
	postgresConfig, err := pgx.ParseConfig(defaultedConnectionString)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL connection string: %w", err)
	}
	if postgresConfig.TLSConfig == nil &&
		!isPostgresUnixSocket(postgresConfig.Host, postgresConfig.Port) {
		for index, fallback := range postgresConfig.Fallbacks {
			if fallback.Host == postgresConfig.Host &&
				fallback.Port == postgresConfig.Port &&
				fallback.TLSConfig != nil {
				postgresConfig.TLSConfig = fallback.TLSConfig
				postgresConfig.Fallbacks = append(
					postgresConfig.Fallbacks[:index],
					postgresConfig.Fallbacks[index+1:]...,
				)
				break
			}
		}
	}
	hasTLSConfig := postgresConfig.TLSConfig != nil
	for _, fallback := range postgresConfig.Fallbacks {
		hasTLSConfig = hasTLSConfig || fallback.TLSConfig != nil
	}
	if hasTLSConfig {
		secureFallbacks := postgresConfig.Fallbacks[:0]
		for _, fallback := range postgresConfig.Fallbacks {
			if fallback.TLSConfig != nil || isPostgresUnixSocket(fallback.Host, fallback.Port) {
				secureFallbacks = append(secureFallbacks, fallback)
			}
		}
		postgresConfig.Fallbacks = secureFallbacks
	}
	return postgresConfig, nil
}

func applyLegacyPostgresHostDefault(connectionString string) (string, error) {
	if os.Getenv("PGHOST") != "" || os.Getenv("PGSERVICE") != "" {
		return connectionString, nil
	}

	if strings.HasPrefix(connectionString, "postgres://") ||
		strings.HasPrefix(connectionString, "postgresql://") {
		parsedURL, err := url.Parse(connectionString)
		if err != nil {
			return "", fmt.Errorf("parse PostgreSQL connection URL: %w", err)
		}
		query := parsedURL.Query()
		if parsedURL.Hostname() != "" || query.Has("host") || query.Has("service") {
			return connectionString, nil
		}
		port := parsedURL.Port()
		parsedURL.Host = "localhost"
		if port != "" {
			parsedURL.Host = net.JoinHostPort("localhost", port)
		}
		return parsedURL.String(), nil
	}

	if postgresKeywordHasKey(connectionString, "host") ||
		postgresKeywordHasKey(connectionString, "service") {
		return connectionString, nil
	}
	return strings.TrimSpace("host=localhost " + connectionString), nil
}

func postgresKeywordHasKey(connectionString, target string) bool {
	remaining := strings.TrimSpace(connectionString)
	for remaining != "" {
		equalsIndex := strings.IndexByte(remaining, '=')
		if equalsIndex < 0 {
			return false
		}
		key := strings.TrimSpace(remaining[:equalsIndex])
		remaining = strings.TrimLeft(remaining[equalsIndex+1:], " \t\n\r\v\f")
		if key == target {
			return true
		}

		quoted := strings.HasPrefix(remaining, "'")
		if quoted {
			remaining = remaining[1:]
		}
		escaped := false
		valueEnd := len(remaining)
		for index, character := range remaining {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if (quoted && character == '\'') || (!quoted && strings.ContainsRune(" \t\n\r\v\f", character)) {
				valueEnd = index
				if quoted {
					valueEnd++
				}
				break
			}
		}
		remaining = strings.TrimLeft(remaining[valueEnd:], " \t\n\r\v\f")
	}
	return false
}

func isPostgresUnixSocket(host string, port uint16) bool {
	network, _ := pgconn.NetworkAddress(host, port)
	return network == "unix"
}

func validatePostgresConnectionString(connectionString string) (string, error) {
	if !strings.HasPrefix(connectionString, "postgres://") &&
		!strings.HasPrefix(connectionString, "postgresql://") {
		return connectionString, nil
	}

	parsedURL, err := url.Parse(connectionString)
	if err != nil {
		return "", fmt.Errorf("parse PostgreSQL connection URL: %w", err)
	}
	if len(parsedURL.Query()["sslmode"]) > 1 {
		return "", errors.New("PostgreSQL connection URL contains multiple sslmode values")
	}
	return connectionString, nil
}

func migrationDialect(name string) (string, error) {
	switch name {
	case "sqlite3", "mysql":
		return name, nil
	case "postgres", "postgresql":
		return "postgres", nil
	default:
		return "", fmt.Errorf("unsupported database %q", name)
	}
}

func migrateDatabase(database *sql.DB, name, migrationsPath string) error {
	dialect, err := migrationDialect(name)
	if err != nil {
		return err
	}
	migrationsPath, err = resolveMigrationsPath(migrationsPath)
	if err != nil {
		return err
	}
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("configure database migrations: %w", err)
	}
	if _, err := goose.EnsureDBVersion(database); err != nil {
		return fmt.Errorf("initialize database migration metadata: %w", err)
	}
	if err := normalizeLegacyGooseHistory(database, dialect); err != nil {
		return err
	}
	if err := goose.Up(database, migrationsPath); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	return nil
}

func resolveMigrationsPath(configuredPath string) (string, error) {
	configuredInfo, err := os.Stat(configuredPath)
	if err != nil {
		return "", fmt.Errorf("inspect migrations path: %w", err)
	}
	if !configuredInfo.IsDir() {
		return "", fmt.Errorf("migrations path %q is not a directory", configuredPath)
	}

	nestedPath := filepath.Join(configuredPath, "migrations")
	nestedInfo, err := os.Stat(nestedPath)
	switch {
	case err == nil && nestedInfo.IsDir():
		return nestedPath, nil
	case err == nil:
		return "", fmt.Errorf("nested migrations path %q is not a directory", nestedPath)
	case errors.Is(err, os.ErrNotExist):
		return configuredPath, nil
	default:
		return "", fmt.Errorf("inspect nested migrations path: %w", err)
	}
}

func normalizeLegacyGooseHistory(database *sql.DB, dialect string) error {
	transaction, err := database.Begin()
	if err != nil {
		return fmt.Errorf("begin migration metadata normalization: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	rows, err := transaction.Query(
		"SELECT id, version_id, is_applied FROM goose_db_version ORDER BY id DESC",
	)
	if err != nil {
		return fmt.Errorf("read migration metadata: %w", err)
	}

	type migrationState struct {
		id        int64
		isApplied bool
	}
	latestStates := make(map[int64]migrationState)
	for rows.Next() {
		var id int64
		var version int64
		var isApplied bool
		if err := rows.Scan(&id, &version, &isApplied); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan migration metadata: %w", err)
		}
		if _, exists := latestStates[version]; !exists {
			latestStates[version] = migrationState{id: id, isApplied: isApplied}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate migration metadata: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close migration metadata rows: %w", err)
	}

	deleteVersionQuery := "DELETE FROM goose_db_version WHERE version_id = ?"
	deleteDuplicateQuery := "DELETE FROM goose_db_version WHERE version_id = ? AND id <> ?"
	if dialect == "postgres" {
		deleteVersionQuery = "DELETE FROM goose_db_version WHERE version_id = $1"
		deleteDuplicateQuery = "DELETE FROM goose_db_version WHERE version_id = $1 AND id <> $2"
	}
	for version, state := range latestStates {
		if !state.isApplied {
			if _, err := transaction.Exec(deleteVersionQuery, version); err != nil {
				return fmt.Errorf("remove rolled-back migration metadata: %w", err)
			}
			continue
		}
		if _, err := transaction.Exec(deleteDuplicateQuery, version, state.id); err != nil {
			return fmt.Errorf("deduplicate migration metadata: %w", err)
		}
	}

	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration metadata normalization: %w", err)
	}
	committed = true
	return nil
}

func createTemporaryPassword(u *User) error {
	var temporaryPassword string
	if envPassword := os.Getenv(InitialAdminPassword); envPassword != "" {
		temporaryPassword = envPassword
	} else {
		// This will result in a 16 character password which could be viewed as an
		// inconvenience, but it should be ok for now.
		temporaryPassword = auth.GenerateSecureKey(auth.MinPasswordLength)
	}
	hash, err := auth.GeneratePasswordHash(temporaryPassword)
	if err != nil {
		return err
	}
	u.Hash = hash
	// Anytime a temporary password is created, we will force the user
	// to change their password
	u.PasswordChangeRequired = true
	err = db.Save(u).Error
	if err != nil {
		return err
	}
	log.Infof("Please login with the username admin and the password %s", temporaryPassword)
	return nil
}

// Setup initializes the database and runs any needed migrations.
//
// First, it establishes a connection to the database, then runs any migrations
// newer than the version the database is on.
//
// Once the database is up-to-date, we create an admin user (if needed) that
// has a randomly generated API key and password.
func Setup(c *config.Config) error {
	// Setup the package-scoped config
	conf = c

	// Register certificates for tls encrypted db connections
	if conf.DBSSLCaPath != "" {
		switch conf.DBName {
		case "mysql":
			rootCertPool := x509.NewCertPool()
			pem, err := os.ReadFile(conf.DBSSLCaPath)
			if err != nil {
				log.Error(err)
				return err
			}
			if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
				log.Error("Failed to append PEM.")
				return err
			}
			if err := mysql.RegisterTLSConfig("ssl_ca", &tls.Config{
				RootCAs: rootCertPool,
			}); err != nil {
				log.Error(err)
				return err
			}
			// Default database is sqlite3, which supports no tls, as connection
			// is file based
		default:
		}
	}

	// Open our database connection
	i := 0
	var err error
	for {
		db, err = openDatabase(conf.DBName, conf.DBPath)
		if err == nil {
			break
		}
		if err != nil && i >= MaxDatabaseConnectionAttempts {
			log.Error(err)
			return err
		}
		i += 1
		log.Warn("waiting for database to be up...")
		time.Sleep(5 * time.Second)
	}
	db.LogMode(false)
	db.SetLogger(log.Logger)
	db.DB().SetMaxOpenConns(1)
	if err != nil {
		log.Error(err)
		return err
	}
	if err = migrateDatabase(db.DB(), conf.DBName, conf.MigrationsPath); err != nil {
		log.Error(err)
		return err
	}
	// Create the admin user if it doesn't exist
	var userCount int64
	var adminUser User
	db.Model(&User{}).Count(&userCount)
	adminRole, err := GetRoleBySlug(RoleAdmin)
	if err != nil {
		log.Error(err)
		return err
	}
	if userCount == 0 {
		adminUser := User{
			Username:               DefaultAdminUsername,
			Role:                   adminRole,
			RoleID:                 adminRole.ID,
			PasswordChangeRequired: true,
		}

		if envToken := os.Getenv(InitialAdminApiToken); envToken != "" {
			adminUser.ApiKey = envToken
		} else {
			adminUser.ApiKey = auth.GenerateSecureKey(auth.APIKeyLength)
		}

		err = db.Save(&adminUser).Error
		if err != nil {
			log.Error(err)
			return err
		}
	}
	// If this is the first time the user is installing GophishFR, then we will
	// generate a temporary password for the admin user.
	//
	// We do this here instead of in the block above where the admin is created
	// since there's the chance the user executes GophishFR and has some kind of
	// error, then tries restarting it. If they didn't grab the password out of
	// the logs, then they would have lost it.
	//
	// By doing the temporary password here, we will regenerate that temporary
	// password until the user is able to reset the admin password.
	if adminUser.Username == "" {
		adminUser, err = GetUserByUsername(DefaultAdminUsername)
		if err != nil {
			log.Error(err)
			return err
		}
	}
	if adminUser.PasswordChangeRequired {
		err = createTemporaryPassword(&adminUser)
		if err != nil {
			log.Error(err)
			return err
		}
	}
	return nil
}
