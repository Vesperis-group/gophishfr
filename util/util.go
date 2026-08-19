package util

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/csv"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/jordan-wright/email"

	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/models"
)

var (
	firstNameRegex = regexp.MustCompile(`(?i)first[\s_-]*name`)
	lastNameRegex  = regexp.MustCompile(`(?i)last[\s_-]*name`)
	emailRegex     = regexp.MustCompile(`(?i)email`)
	positionRegex  = regexp.MustCompile(`(?i)position`)
)

// Group import limits. They bound the memory, CPU, and response size of a
// single /api/import/group request. These are technical guard rails rather than
// business rules, so they are explicit constants instead of configuration.
const (
	// MaxImportRequestBytes bounds the whole request body before it is parsed.
	// A realistic 50,000-recipient export stays near 6 MiB, so this leaves a
	// wide margin while keeping the upload bounded.
	MaxImportRequestBytes int64 = 10 << 20

	// MaxImportParts bounds every multipart part, whether it carries a file or
	// not, so a part-flooding body cannot force unbounded iterations.
	MaxImportParts = 32

	// MaxImportFiles bounds the uploaded files handled by one request.
	MaxImportFiles = 10

	// MaxImportRecords bounds the targets one request may return.
	MaxImportRecords = 50000

	// MaxImportColumns bounds the columns read from a single CSV record. The
	// importer needs four, so this tolerates wide directory exports.
	MaxImportColumns = 256

	// MaxImportFieldCharacters matches the varchar(255) target columns, so an
	// import cannot produce a value the database would reject or truncate.
	MaxImportFieldCharacters = 255
)

// Errors returned when an import exceeds a limit or cannot be parsed. Their
// messages are safe to return to the client: they describe the limit without
// echoing any uploaded content.
var (
	ErrCSVTooManyParts   = fmt.Errorf("Too many form parts (maximum %d)", MaxImportParts)
	ErrCSVTooManyFiles   = fmt.Errorf("Too many files uploaded at once (maximum %d)", MaxImportFiles)
	ErrCSVTooManyRecords = fmt.Errorf("Too many records to import at once (maximum %d)", MaxImportRecords)
	ErrCSVTooManyColumns = fmt.Errorf("Too many columns in a record (maximum %d)", MaxImportColumns)
	ErrCSVFieldTooLong   = fmt.Errorf("A field is longer than %d characters", MaxImportFieldCharacters)
	ErrCSVMalformed      = errors.New("The uploaded data is not valid CSV")
)

// errCSVEmptyPart reports a file part that ends before a header row. The
// importer has always stopped reading further parts in that case, so it is
// kept as an internal signal rather than surfaced as a client error.
var errCSVEmptyPart = errors.New("empty csv part")

// ParseMail takes in an HTTP Request and returns an Email object
// TODO: This function will likely be changed to take in a []byte
func ParseMail(r *http.Request) (email.Email, error) {
	e := email.Email{}
	m, err := mail.ReadMessage(r.Body)
	if err != nil {
		fmt.Println(err)
	}
	body, err := io.ReadAll(m.Body)
	e.HTML = body
	return e, err
}

// ParseCSV contains the logic to parse the user provided csv file containing Target entries
func ParseCSV(r *http.Request) ([]models.Target, error) {
	mr, err := r.MultipartReader()
	ts := []models.Target{}
	if err != nil {
		return ts, err
	}
	parts := 0
	files := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		// A malformed body yields a nil part with a non-EOF error. Returning
		// here is what stops a truncated upload from being retried forever and
		// a corrupt boundary from dereferencing that nil part.
		if err != nil {
			return ts, err
		}
		parts++
		if parts > MaxImportParts {
			_ = part.Close()
			return ts, ErrCSVTooManyParts
		}
		// Skip the "submit" part
		if part.FileName() == "" {
			_ = part.Close()
			continue
		}
		files++
		if files > MaxImportFiles {
			_ = part.Close()
			return ts, ErrCSVTooManyFiles
		}
		records, err := parseCSVTargets(part, MaxImportRecords-len(ts))
		_ = part.Close()
		if errors.Is(err, errCSVEmptyPart) {
			break
		}
		if err != nil {
			return ts, err
		}
		ts = append(ts, records...)
	}
	return ts, nil
}

// parseCSVTargets reads targets from a single uploaded CSV file. It accepts at
// most maxRecords targets and never buffers the whole input, so the caller
// bounds the work performed for one request.
func parseCSVTargets(r io.Reader, maxRecords int) ([]models.Target, error) {
	ts := []models.Target{}
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	// Records with a varying number of fields have always been accepted by this
	// importer; the column and field limits bound them instead.
	reader.FieldsPerRecord = -1

	record, err := reader.Read()
	if err == io.EOF {
		return ts, errCSVEmptyPart
	}
	if err != nil {
		return ts, csvReadError(err)
	}
	if len(record) > MaxImportColumns {
		return ts, ErrCSVTooManyColumns
	}
	fi := -1
	li := -1
	ei := -1
	pi := -1
	fn := ""
	ln := ""
	ea := ""
	ps := ""
	for i, v := range record {
		switch {
		case firstNameRegex.MatchString(v):
			fi = i
		case lastNameRegex.MatchString(v):
			li = i
		case emailRegex.MatchString(v):
			ei = i
		case positionRegex.MatchString(v):
			pi = i
		}
	}
	if fi == -1 && li == -1 && ei == -1 && pi == -1 {
		return ts, nil
	}
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		// Any other error means the remaining input cannot be trusted. The
		// importer used to ignore it, append a target built from the previous
		// row, and read again, which never terminated on a truncated upload.
		if err != nil {
			return ts, csvReadError(err)
		}
		if len(record) > MaxImportColumns {
			return ts, ErrCSVTooManyColumns
		}
		if fi != -1 && len(record) > fi {
			fn = record[fi]
		}
		if li != -1 && len(record) > li {
			ln = record[li]
		}
		if ei != -1 && len(record) > ei {
			csvEmail, err := mail.ParseAddress(record[ei])
			if err != nil {
				continue
			}
			ea = csvEmail.Address
		}
		if pi != -1 && len(record) > pi {
			ps = record[pi]
		}
		// Only a record that would become a target can fail the import, so a
		// record skipped above never aborts the upload because of its length.
		if err := checkImportFields(fn, ln, ea, ps); err != nil {
			return ts, err
		}
		if len(ts) >= maxRecords {
			return ts, ErrCSVTooManyRecords
		}
		t := models.Target{
			BaseRecipient: models.BaseRecipient{
				FirstName: fn,
				LastName:  ln,
				Email:     ea,
				Position:  ps,
			},
		}
		ts = append(ts, t)
	}
	return ts, nil
}

// checkImportFields rejects a target the columns could not store. Values are
// never truncated silently.
func checkImportFields(values ...string) error {
	for _, value := range values {
		if utf8.RuneCountInString(value) > MaxImportFieldCharacters {
			return ErrCSVFieldTooLong
		}
	}
	return nil
}

// csvReadError separates invalid CSV syntax, which the client can correct, from
// a transport failure such as a truncated or oversized body. Reporting the
// second one unchanged lets the caller answer with the matching status.
func csvReadError(err error) error {
	var parseError *csv.ParseError
	if errors.As(err, &parseError) {
		return ErrCSVMalformed
	}
	return err
}

// CheckAndCreateSSL is a helper to setup self-signed certificates for the administrative interface.
func CheckAndCreateSSL(cp string, kp string) error {
	// Check whether there is an existing SSL certificate and/or key, and if so, abort execution of this function
	if _, err := os.Stat(cp); !os.IsNotExist(err) {
		return nil
	}
	if _, err := os.Stat(kp); !os.IsNotExist(err) {
		return nil
	}

	log.Infof("Creating new self-signed certificates for administration interface")

	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return fmt.Errorf("error generating tls private key: %v", err)
	}

	notBefore := time.Now()
	// Generate a certificate that lasts for 10 years
	notAfter := notBefore.Add(10 * 365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)

	if err != nil {
		return fmt.Errorf("tls certificate generation: failed to generate a random serial number: %s", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"GophishFR"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.Public(), priv)
	if err != nil {
		return fmt.Errorf("tls certificate generation: failed to create certificate: %s", err)
	}

	certOut, err := os.Create(cp)
	if err != nil {
		return fmt.Errorf("tls certificate generation: failed to open %s for writing: %s", cp, err)
	}
	// A partially written certificate or key is worse than no file at all: the
	// server would start against a truncated credential, so these writes and
	// closes have to be checked.
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		_ = certOut.Close()
		return fmt.Errorf("tls certificate generation: failed to write %s: %s", cp, err)
	}
	if err := certOut.Close(); err != nil {
		return fmt.Errorf("tls certificate generation: failed to close %s: %s", cp, err)
	}

	keyOut, err := os.OpenFile(kp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("tls certificate generation: failed to open %s for writing", kp)
	}

	b, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		_ = keyOut.Close()
		return fmt.Errorf("tls certificate generation: unable to marshal ECDSA private key: %v", err)
	}

	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}); err != nil {
		_ = keyOut.Close()
		return fmt.Errorf("tls certificate generation: failed to write %s: %s", kp, err)
	}
	if err := keyOut.Close(); err != nil {
		return fmt.Errorf("tls certificate generation: failed to close %s: %s", kp, err)
	}

	log.Info("TLS Certificate Generation complete")
	return nil
}
