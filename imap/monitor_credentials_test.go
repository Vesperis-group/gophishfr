package imap

import (
	"testing"

	"github.com/Vesperis-group/gophishfr/models"
)

func TestCheckForNewEmailsFailsBeforeNetworkingWithoutUsableCiphertext(t *testing.T) {
	settings := models.IMAP{
		UserId:   1,
		Enabled:  true,
		Host:     "external-imap.invalid",
		Port:     993,
		Username: "synthetic-user",
		Password: "synthetic-legacy-password",
	}
	if err := checkForNewEmails(settings, nil); err == nil {
		t.Fatal("legacy plaintext unexpectedly reached the IMAP networking boundary")
	}

	settings.Password = ""
	settings.PasswordCiphertext = "malformed-ciphertext"
	if err := checkForNewEmails(settings, nil); err == nil {
		t.Fatal("ciphertext without a keyring unexpectedly reached the IMAP networking boundary")
	}
}
