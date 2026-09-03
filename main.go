package main

/*
GophishFR - Security Awareness Simulation Framework

Derived from Gophish - Open-Source Phishing Framework

The MIT License (MIT)

Copyright (c) 2013 Jordan Wright

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
import (
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/Vesperis-group/gophishfr/config"
	"github.com/Vesperis-group/gophishfr/controllers"
	"github.com/Vesperis-group/gophishfr/dialer"
	"github.com/Vesperis-group/gophishfr/imap"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/middleware"
	"github.com/Vesperis-group/gophishfr/models"
	"github.com/Vesperis-group/gophishfr/webhook"
	"github.com/Vesperis-group/gophishfr/worker"
)

const (
	modeAll   string = "all"
	modeAdmin string = "admin"
	modePhish string = "phish"
)

var (
	configPath             = kingpin.Flag("config", "Location of config.json.").Default("./config.json").String()
	disableMailer          = kingpin.Flag("disable-mailer", "Disable the mailer (for use with multi-system deployments)").Bool()
	migrateIMAPCredentials = kingpin.Flag(
		"migrate-imap-credentials",
		"Offline: encrypt legacy IMAP passwords after stopping all application writers.",
	).Bool()
	rollbackIMAPCredentials = kingpin.Flag(
		"rollback-imap-credentials",
		"Offline: decrypt IMAP passwords before downgrading the schema or binary.",
	).Bool()
	migrateSMTPCredentials = kingpin.Flag(
		"migrate-smtp-credentials",
		"Offline: encrypt legacy SMTP passwords after stopping all application writers.",
	).Bool()
	rollbackSMTPCredentials = kingpin.Flag(
		"rollback-smtp-credentials",
		"Offline: decrypt SMTP passwords before downgrading the schema or binary.",
	).Bool()
	mode = kingpin.Flag("mode", fmt.Sprintf("Run the binary in one of the modes (%s, %s or %s)", modeAll, modeAdmin, modePhish)).
		Default("all").Enum(modeAll, modeAdmin, modePhish)
)

func main() {
	// Load the version

	version, err := os.ReadFile("./VERSION")
	if err != nil {
		log.Fatal(err)
	}
	kingpin.Version(string(version))

	// Parse the CLI flags and load the config
	kingpin.CommandLine.HelpFlag.Short('h')
	kingpin.Parse()

	// Load the config
	conf, err := config.LoadConfig(*configPath)
	// Just warn if a contact address hasn't been configured
	if err != nil {
		log.Fatal(err)
	}
	if conf.ContactAddress == "" {
		log.Warnf("No contact address has been configured.")
		log.Warnf("Please consider adding a contact_address entry in your config.json")
	}
	config.Version = string(version)
	credentialActions := 0
	for _, selected := range []bool{
		*migrateIMAPCredentials,
		*rollbackIMAPCredentials,
		*migrateSMTPCredentials,
		*rollbackSMTPCredentials,
	} {
		if selected {
			credentialActions++
		}
	}
	if credentialActions > 1 {
		log.Fatal("credential migration and rollback actions are mutually exclusive")
	}
	if *migrateIMAPCredentials || *rollbackIMAPCredentials {
		if err := models.ValidateIMAPCredentialBackend(conf.DBName); err != nil {
			log.Fatal(err)
		}
	}
	if *migrateSMTPCredentials || *rollbackSMTPCredentials {
		if err := models.ValidateSMTPCredentialBackend(conf.DBName); err != nil {
			log.Fatal(err)
		}
	}

	// Configure our various upstream clients to make sure that we restrict
	// outbound connections as needed.
	//
	// A malformed entry here means the operator intended a restriction that we
	// cannot honour, so refuse to start rather than run with an allowlist that
	// does not match the configuration file.
	if err := dialer.SetAllowedHosts(conf.AdminConf.AllowedInternalHosts); err != nil {
		log.Fatal(err)
	}
	webhook.SetTransport(&http.Transport{
		DialContext: dialer.Dialer().DialContext,
	})

	err = log.Setup(conf.Logging)
	if err != nil {
		log.Fatal(err)
	}

	credentialCipher, err := loadCredentialCipher()
	if err != nil {
		log.Fatal(err)
	}
	if credentialActions != 0 && credentialCipher == nil {
		if *migrateSMTPCredentials || *rollbackSMTPCredentials {
			log.Fatal(models.ErrSMTPCredentialKeyringRequired)
		}
		log.Fatal(models.ErrIMAPCredentialKeyringRequired)
	}

	// Provide the option to disable the built-in mailer
	// Setup the global variables and settings
	err = models.Setup(conf)
	if err != nil {
		log.Fatal(err)
	}
	if *migrateIMAPCredentials || *rollbackIMAPCredentials {
		result, err := runIMAPCredentialAction(credentialCipher, *rollbackIMAPCredentials)
		if err != nil {
			log.Fatal(err)
		}
		action := "migration"
		if *rollbackIMAPCredentials {
			action = "rollback"
		}
		log.Infof("IMAP credential %s complete: %d rows updated, %d rows unchanged", action, result.Updated, result.Unchanged)
		return
	}
	if *migrateSMTPCredentials || *rollbackSMTPCredentials {
		result, err := runSMTPCredentialAction(credentialCipher, *rollbackSMTPCredentials)
		if err != nil {
			log.Fatal(err)
		}
		action := "migration"
		if *rollbackSMTPCredentials {
			action = "rollback"
		}
		log.Infof("SMTP credential %s complete: %d rows updated, %d rows unchanged", action, result.Updated, result.Unchanged)
		return
	}

	// Unlock any maillogs that may have been locked for processing
	// when GophishFR was last shut down.
	err = models.UnlockAllMailLogs()
	if err != nil {
		log.Fatal(err)
	}

	// Create our servers
	adminOptions := []controllers.AdminServerOption{}
	adminOptions = append(adminOptions, controllers.WithCredentialCipher(credentialCipher))
	if *disableMailer {
		adminOptions = append(adminOptions, controllers.WithWorker(nil))
	} else {
		mailWorker, err := worker.New(worker.WithCredentialCipher(credentialCipher))
		if err != nil {
			log.Fatal(err)
		}
		adminOptions = append(adminOptions, controllers.WithWorker(mailWorker))
	}
	adminConfig := conf.AdminConf
	adminServer := controllers.NewAdminServer(adminConfig, adminOptions...)
	middleware.Store.Options.Secure = adminConfig.UseTLS

	phishConfig := conf.PhishConf
	phishServer := controllers.NewPhishingServer(phishConfig)

	imapMonitor := imap.NewMonitor(credentialCipher)
	if *mode == "admin" || *mode == "all" {
		go adminServer.Start()
		if err := imapMonitor.Start(); err != nil {
			log.Fatal(err)
		}
	}
	if *mode == "phish" || *mode == "all" {
		go phishServer.Start()
	}

	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	log.Info("CTRL+C Received... Gracefully shutting down servers")
	if *mode == modeAdmin || *mode == modeAll {
		if err := adminServer.Shutdown(); err != nil {
			log.Error(err)
		}
		if err := imapMonitor.Shutdown(); err != nil {
			log.Error(err)
		}
	}
	if *mode == modePhish || *mode == modeAll {
		if err := phishServer.Shutdown(); err != nil {
			log.Error(err)
		}
	}

}

func loadCredentialCipher() (*credentials.Cipher, error) {
	path := os.Getenv(models.IMAPCredentialKeyringEnvironment)
	if path == "" {
		return nil, nil
	}
	keyring, err := credentials.LoadKeyringFile(path)
	if err != nil {
		return nil, fmt.Errorf("load IMAP credential keyring: %w", err)
	}
	credentialCipher, err := credentials.New(keyring)
	if err != nil {
		return nil, fmt.Errorf("initialize IMAP credential cipher: %w", err)
	}
	return credentialCipher, nil
}

func runSMTPCredentialAction(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (models.SMTPCredentialMigrationResult, error) {
	if rollback {
		return models.RollbackSMTPCredentials(credentialCipher)
	}
	return models.MigrateSMTPCredentials(credentialCipher)
}

func runIMAPCredentialAction(
	credentialCipher *credentials.Cipher,
	rollback bool,
) (models.IMAPCredentialMigrationResult, error) {
	if rollback {
		return models.RollbackIMAPCredentials(credentialCipher)
	}
	return models.MigrateIMAPCredentials(credentialCipher)
}
