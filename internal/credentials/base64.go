package credentials

import (
	"encoding/base64"
	"errors"
)

// decodeCanonicalBase64 decodes s as standard-encoding (RFC 4648 §4) base64
// and requires that s is *the* canonical text representation of the decoded
// bytes: re-encoding the decoded bytes with base64.StdEncoding must
// reproduce s byte-for-byte.
//
// base64.StdEncoding.Strict() is not sufficient on its own: per its own
// documentation, "the input is still malleable, as new line characters (CR
// and LF) are still ignored" even in strict mode. That means, without this
// extra check, two different envelope/keyring texts — one with an embedded
// or trailing '\r'/'\n' inside a base64 field, one without — would decode to
// the exact same bytes. This package treats an Envelope and a keyring
// document as opaque text that is safe to compare, deduplicate, or log
// verbatim, so it must reject every input that is not already in its unique
// canonical form rather than silently normalizing it.
func decodeCanonicalBase64(s string) ([]byte, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, err
	}
	if base64.StdEncoding.EncodeToString(decoded) != s {
		return nil, errors.New("non-canonical base64 encoding")
	}
	return decoded, nil
}
