package models

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Vesperis-group/gophishfr/dialer"
	"github.com/Vesperis-group/gophishfr/internal/credentials"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/mailer"
	"github.com/gophish/gomail"
	"github.com/jinzhu/gorm"
)

// Dialer is a wrapper around a standard gomail.Dialer in order
// to implement the mailer.Dialer interface. This allows us to better
// separate the mailer package as opposed to forcing a connection
// between mailer and gomail.
type Dialer struct {
	*gomail.Dialer
}

// Dial wraps the gomail dialer's Dial command
func (d *Dialer) Dial() (mailer.Sender, error) {
	return d.Dialer.Dial()
}

// SMTP contains the attributes needed to handle the sending of campaign emails
type SMTP struct {
	Id                 int64     `json:"id" gorm:"column:id; primary_key:yes"`
	UserId             int64     `json:"-" gorm:"column:user_id"`
	Interface          string    `json:"interface_type" gorm:"column:interface_type"`
	Name               string    `json:"name"`
	Host               string    `json:"host"`
	Username           string    `json:"username,omitempty"`
	Password           string    `json:"-" gorm:"column:password"`
	PasswordCiphertext string    `json:"-" gorm:"column:password_ciphertext"`
	FromAddress        string    `json:"from_address"`
	IgnoreCertErrors   bool      `json:"ignore_cert_errors"`
	Headers            []Header  `json:"headers"`
	ModifiedDate       time.Time `json:"modified_date"`
}

// Header contains the fields and methods for a sending profile to have
// custom headers
type Header struct {
	Id     int64  `json:"-"`
	SMTPId int64  `json:"-"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

// ErrFromAddressNotSpecified is thrown when there is no "From" address
// specified in the SMTP configuration
var ErrFromAddressNotSpecified = errors.New("No From Address specified")

// ErrInvalidFromAddress is thrown when the SMTP From field in the sending
// profiles containes a value that is not an email address
var ErrInvalidFromAddress = errors.New("Invalid SMTP From address because it is not an email address")

// ErrHostNotSpecified is thrown when there is no Host specified
// in the SMTP configuration
var ErrHostNotSpecified = errors.New("No SMTP Host specified")

// ErrInvalidHost indicates that the SMTP server string is invalid
var ErrInvalidHost = errors.New("Invalid SMTP server address")

// ErrSMTPCredentialContextChange prevents an existing stored password from
// being silently redirected to a different authentication endpoint.
var ErrSMTPCredentialContextChange = errors.New("a new SMTP password is required when changing credential routing")

// TableName specifies the database tablename for Gorm to use
func (s SMTP) TableName() string {
	return "smtp"
}

// Validate ensures that SMTP configs/connections are valid
func (s *SMTP) Validate() error {
	switch {
	case s.FromAddress == "":
		return ErrFromAddressNotSpecified
	case s.Host == "":
		return ErrHostNotSpecified
	case !validateFromAddress(s.FromAddress):
		return ErrInvalidFromAddress
	}
	_, err := mail.ParseAddress(s.FromAddress)
	if err != nil {
		return err
	}
	// Make sure addr is in host:port format
	hp := strings.Split(s.Host, ":")
	if len(hp) > 2 {
		return ErrInvalidHost
	} else if len(hp) < 2 {
		hp = append(hp, "25")
	}
	_, err = strconv.Atoi(hp[1])
	if err != nil {
		return ErrInvalidHost
	}
	return err
}

// fromAddressRegex matches the addr-spec accepted in a sending profile's From
// header. Compiled once at package level: the pattern is a constant, so a
// per-call regexp.Compile could only ever produce the same result, and its
// ignored error hid the fact that a failure would have left a nil matcher.
var fromAddressRegex = regexp.MustCompile(`^([a-zA-Z0-9_\-\.]+)@([a-zA-Z0-9_\-\.]+)\.([a-zA-Z]{2,18})$`)

// validateFromAddress validates
func validateFromAddress(email string) bool {
	return fromAddressRegex.MatchString(email)
}

// GetDialer returns a dialer for the given SMTP profile
func (s *SMTP) GetDialer(credentialCipher *credentials.Cipher) (mailer.Dialer, error) {
	password, err := DecryptSMTPPassword(*s, credentialCipher)
	if err != nil {
		return nil, err
	}
	return s.getDialer(password)
}

// getDialer constructs a dialer with a password that exists only in this call
// stack. It is also used for non-persisted test-email credentials.
func (s *SMTP) getDialer(password string) (mailer.Dialer, error) {
	// Setup the message and dial
	hp := strings.Split(s.Host, ":")
	if len(hp) < 2 {
		hp = append(hp, "25")
	}
	host := hp[0]
	// Any issues should have been caught in validation, but we'll
	// double check here.
	port, err := strconv.Atoi(hp[1])
	if err != nil {
		log.Error(err)
		return nil, err
	}
	dialer := dialer.Dialer()
	d := gomail.NewWithDialer(dialer, host, port, s.Username, password)
	d.TLSConfig = &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: s.IgnoreCertErrors,
	}
	hostname, err := os.Hostname()
	if err != nil {
		log.Error(err)
		hostname = "localhost"
	}
	d.LocalName = hostname
	return &Dialer{d}, err
}

// GetSMTPs returns the SMTPs owned by the given user.
func GetSMTPs(uid int64) ([]SMTP, error) {
	ss := []SMTP{}
	err := db.Where("user_id=?", uid).Find(&ss).Error
	if err != nil {
		log.Error(err)
		return ss, err
	}
	for i := range ss {
		err = db.Where("smtp_id=?", ss[i].Id).Find(&ss[i].Headers).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			log.Error(err)
			return ss, err
		}
	}
	return ss, nil
}

// GetSMTP returns the SMTP, if it exists, specified by the given id and user_id.
func GetSMTP(id int64, uid int64) (SMTP, error) {
	s := SMTP{}
	err := db.Where("user_id=? and id=?", uid, id).Find(&s).Error
	if err != nil {
		log.Error(err)
		return s, err
	}
	err = db.Where("smtp_id=?", s.Id).Find(&s.Headers).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Error(err)
		return s, err
	}
	return s, err
}

// GetSMTPByName returns the SMTP, if it exists, specified by the given name and user_id.
func GetSMTPByName(n string, uid int64) (SMTP, error) {
	s := SMTP{}
	err := db.Where("user_id=? and name=?", uid, n).Find(&s).Error
	if err != nil {
		log.Error(err)
		return s, err
	}
	err = db.Where("smtp_id=?", s.Id).Find(&s.Headers).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Error(err)
	}
	return s, err
}

// PostSMTP creates a new SMTP in the database.
func PostSMTP(s *SMTP, credentialCipher ...*credentials.Cipher) error {
	if err := s.Validate(); err != nil {
		log.Error(err)
		return err
	}
	if err := ValidateSMTPPassword(s.Password); err != nil {
		return err
	}
	if s.UserId <= 0 {
		return ErrInvalidSMTPCredentialIdentity
	}
	var cipher *credentials.Cipher
	if len(credentialCipher) != 0 {
		cipher = credentialCipher[0]
	}
	password := s.Password
	s.Password = ""
	s.PasswordCiphertext = ""

	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()
	if err := transaction.Omit("Headers").Create(s).Error; err != nil {
		return err
	}
	if password != "" {
		ciphertext, err := encryptSMTPPassword(cipher, s.UserId, s.Id, password)
		if err != nil {
			return err
		}
		update := transaction.Model(&SMTP{}).
			Where("id = ? AND user_id = ? AND password = '' AND password_ciphertext = ''", s.Id, s.UserId).
			Update("password_ciphertext", ciphertext)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return fmt.Errorf("SMTP profile changed during credential creation")
		}
		s.PasswordCiphertext = ciphertext
	}
	if err := verifySMTPCredentialStorage(
		transaction,
		s.Id,
		s.UserId,
		"",
		s.PasswordCiphertext,
	); err != nil {
		return err
	}
	if err := saveSMTPHeaders(transaction, s); err != nil {
		return err
	}
	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}

// PutSMTP edits an existing SMTP in the database.
// Per the PUT Method RFC, it presumes all data for a SMTP is provided.
func PutSMTP(s *SMTP, credentialCipher ...*credentials.Cipher) error {
	if err := s.Validate(); err != nil {
		log.Error(err)
		return err
	}
	if err := ValidateSMTPPassword(s.Password); err != nil {
		return err
	}
	if s.Id <= 0 || s.UserId <= 0 {
		return ErrInvalidSMTPCredentialIdentity
	}
	var cipher *credentials.Cipher
	if len(credentialCipher) != 0 {
		cipher = credentialCipher[0]
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()

	existing := SMTP{}
	if err := transaction.Where("id = ? AND user_id = ?", s.Id, s.UserId).First(&existing).Error; err != nil {
		return err
	}
	if err := validateSMTPSecretColumns(existing); err != nil {
		return err
	}
	preserveCredential := s.Password == ""
	if preserveCredential {
		if existing.PasswordCiphertext != "" && !sameSMTPCredentialRouting(existing, *s) {
			return ErrSMTPCredentialContextChange
		}
		s.PasswordCiphertext = existing.PasswordCiphertext
	} else {
		ciphertext, err := encryptSMTPPassword(cipher, s.UserId, s.Id, s.Password)
		if err != nil {
			return err
		}
		s.PasswordCiphertext = ciphertext
	}
	s.Password = ""

	updateQuery := transaction.Model(&SMTP{}).
		Where("id = ? AND user_id = ?", s.Id, s.UserId)
	if preserveCredential && existing.PasswordCiphertext != "" {
		updateQuery = updateQuery.Where(`
			password = '' AND
			password_ciphertext = ? AND
			interface_type = ? AND
			host = ? AND
			username = ? AND
			ignore_cert_errors = ?
		`,
			existing.PasswordCiphertext,
			existing.Interface,
			existing.Host,
			existing.Username,
			existing.IgnoreCertErrors,
		)
	}
	update := updateQuery.Updates(map[string]interface{}{
		"interface_type":      s.Interface,
		"name":                s.Name,
		"host":                s.Host,
		"username":            s.Username,
		"password":            "",
		"password_ciphertext": s.PasswordCiphertext,
		"from_address":        s.FromAddress,
		"modified_date":       s.ModifiedDate,
		"ignore_cert_errors":  s.IgnoreCertErrors,
	})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	if err := verifySMTPCredentialStorage(
		transaction,
		s.Id,
		s.UserId,
		"",
		s.PasswordCiphertext,
	); err != nil {
		return err
	}
	deleteHeaders := transaction.Exec(`
		DELETE FROM headers
		WHERE smtp_id = ?
		  AND EXISTS (SELECT 1 FROM smtp WHERE id = ? AND user_id = ?)
	`, s.Id, s.Id, s.UserId)
	if deleteHeaders.Error != nil {
		return deleteHeaders.Error
	}
	if err := saveSMTPHeaders(transaction, s); err != nil {
		return err
	}
	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}

func sameSMTPCredentialRouting(existing, update SMTP) bool {
	return existing.Interface == update.Interface &&
		existing.Host == update.Host &&
		existing.Username == update.Username &&
		existing.IgnoreCertErrors == update.IgnoreCertErrors
}

func saveSMTPHeaders(transaction *gorm.DB, s *SMTP) error {
	for i := range s.Headers {
		s.Headers[i].SMTPId = s.Id
		if err := transaction.Create(&s.Headers[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

// DeleteSMTP deletes an existing SMTP in the database.
// An error is returned if a SMTP with the given user id and SMTP id is not found.
func DeleteSMTP(id int64, uid int64) error {
	if id <= 0 || uid <= 0 {
		return ErrInvalidSMTPCredentialIdentity
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()
	var count int
	if err := transaction.Model(&SMTP{}).
		Where("id = ? AND user_id = ?", id, uid).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return gorm.ErrRecordNotFound
	}
	if err := transaction.Exec(`
		DELETE FROM headers
		WHERE smtp_id = ?
		  AND EXISTS (SELECT 1 FROM smtp WHERE id = ? AND user_id = ?)
	`, id, id, uid).Error; err != nil {
		return err
	}
	deleted := transaction.Where("id = ? AND user_id = ?", id, uid).Delete(&SMTP{})
	if deleted.Error != nil {
		return deleted.Error
	}
	if deleted.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}
