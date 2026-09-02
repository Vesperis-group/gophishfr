package credentials

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Envelope is the versioned, self-describing serialization of an encrypted
// credential. It is safe to store as text (a database column, a config
// value, ...) and safe to log or print: it carries no plaintext and no key
// material, only a key ID (not secret, see doc.go) and the AEAD ciphertext
// (which, together with its authentication tag, is exactly the data this
// primitive is designed to expose at rest).
type Envelope string

const (
	// envelopeMagic and envelopeVersionV1 are literal constants, never
	// derived from caller input. Together they let a future format change
	// add a "v2" branch without ambiguity, and let a future legacy-plaintext
	// migration recognize (and refuse to blindly overwrite) anything already
	// carrying this prefix.
	envelopeMagic     = "gophishfr-cred"
	envelopeVersionV1 = "v1"

	// envelopeFieldCount is magic, version, key ID, nonce, ciphertext.
	envelopeFieldCount = 5

	// maxEnvelopeLength bounds the total size of a serialized envelope this
	// package will attempt to parse. It exists only to keep malformed/fuzzed
	// input cheap to reject; a real credential envelope is a few hundred
	// bytes.
	maxEnvelopeLength = 10 << 20 // 10 MiB

	// gcmTagSize is the fixed AES-GCM authentication tag size in bytes, used
	// only as a lower bound sanity check on the ciphertext field before it is
	// ever handed to AEAD.Open.
	gcmTagSize = 16
)

// rawEnvelope is the parsed, structural form of an Envelope. It is not
// exported: callers only ever see the opaque Envelope string.
type rawEnvelope struct {
	keyID      string
	nonce      []byte
	ciphertext []byte
}

// marshal serializes a rawEnvelope into its canonical text form.
func (e *rawEnvelope) marshal() Envelope {
	fields := []string{
		envelopeMagic,
		envelopeVersionV1,
		e.keyID,
		base64.StdEncoding.EncodeToString(e.nonce),
		base64.StdEncoding.EncodeToString(e.ciphertext),
	}
	return Envelope(strings.Join(fields, ":"))
}

// parseEnvelope strictly parses and validates an Envelope's structure. It
// does not know about, and cannot check, the key ID's existence in any
// keyring, the nonce's expected length for a specific AEAD instance, or
// whether the ciphertext actually authenticates — those checks happen in
// Decrypt, once the corresponding key is known.
//
// parseEnvelope never panics on any input, including empty, truncated, or
// adversarial byte sequences: every rejection path returns an error.
func parseEnvelope(env Envelope) (*rawEnvelope, error) {
	raw := string(env)
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrInvalidEnvelope)
	}
	if len(raw) > maxEnvelopeLength {
		return nil, fmt.Errorf("%w: exceeds maximum length", ErrInvalidEnvelope)
	}

	// SplitN caps the number of colon-delimited fields at envelopeFieldCount:
	// the ciphertext field (the last one) is never itself re-split even if a
	// tampered/adversarial payload happens to contain further ':' bytes. The
	// magic and version fields are fixed constants and the key ID alphabet
	// excludes ':' (see validKeyID), and standard base64 cannot produce ':'
	// either, so a genuine envelope is never affected by this cap.
	parts := strings.SplitN(raw, ":", envelopeFieldCount)
	if len(parts) != envelopeFieldCount {
		return nil, fmt.Errorf("%w: wrong field count", ErrInvalidEnvelope)
	}

	magic, version, keyID, nonceField, ciphertextField := parts[0], parts[1], parts[2], parts[3], parts[4]

	if magic != envelopeMagic {
		return nil, fmt.Errorf("%w: unrecognized magic", ErrInvalidEnvelope)
	}
	if version != envelopeVersionV1 {
		return nil, fmt.Errorf("%w: envelope version", ErrUnsupportedVersion)
	}
	if !validKeyID(keyID) {
		return nil, fmt.Errorf("%w: invalid key id", ErrInvalidEnvelope)
	}

	nonce, err := decodeCanonicalBase64(nonceField)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid nonce encoding", ErrInvalidEnvelope)
	}
	if len(nonce) == 0 {
		return nil, fmt.Errorf("%w: empty nonce", ErrInvalidEnvelope)
	}

	ciphertext, err := decodeCanonicalBase64(ciphertextField)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ciphertext encoding", ErrInvalidEnvelope)
	}
	if len(ciphertext) < gcmTagSize {
		return nil, fmt.Errorf("%w: ciphertext shorter than authentication tag", ErrInvalidEnvelope)
	}

	return &rawEnvelope{keyID: keyID, nonce: nonce, ciphertext: ciphertext}, nil
}

// maxKeyIDLength bounds the key ID accepted in both an envelope and a
// keyring document.
const maxKeyIDLength = 64

// validKeyID reports whether id is a non-empty, bounded string built only
// from ASCII letters, digits, '.', '-', and '_'. This excludes ':' (the
// envelope field delimiter) and every byte that is not a plain, printable
// ASCII character, so a key ID can never be mistaken for another envelope
// field or carry anything that would need escaping.
func validKeyID(id string) bool {
	if len(id) == 0 || len(id) > maxKeyIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}
