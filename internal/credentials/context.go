package credentials

import (
	"encoding/binary"
	"fmt"
)

// contextProtocolVersion is mixed into every AAD as a fixed, stable constant.
//
// It is a crypto-protocol version, not a GophishFR release/build/git version.
// A product version changes on every release and must never gate whether
// previously encrypted data can still be decrypted; only a bump of this
// constant (which would also require a new envelope/AAD format version) may
// change what is authenticated here.
const contextProtocolVersion = "gophishfr-credential-context-v1"

// Field length bounds exist to keep the AAD (and the work Encrypt/Decrypt do)
// bounded, not to encode any business rule. They are generous enough for any
// stable identifier a caller is likely to hold (a table/column name, a
// decimal or UUID-shaped ID, ...).
const (
	maxContextKindLength  = 128
	maxContextFieldLength = 256
)

// Context is the caller-supplied, structured associated data (AAD) bound to
// an envelope. It never carries an IMAP struct, an SMTP struct, a GORM
// entity, or any other application type — only plain strings the caller
// already extracted.
//
// Kind is required and should identify what is being encrypted (e.g. a
// credential type) so that unrelated data sharing the same owner/record
// identity cannot be swapped for one another undetected. Table, Column,
// OwnerID, and RecordID are optional and let a caller bind an envelope to a
// specific stable location; this package assigns them no meaning beyond
// "distinct values produce distinct AAD" and does not hardcode any concrete
// GophishFR table, column, or business identifier.
//
// Decrypt requires the exact same Context that was passed to Encrypt: any
// difference in any field changes the AAD and fails authentication.
type Context struct {
	Kind     string
	Table    string
	Column   string
	OwnerID  string
	RecordID string
}

// validate rejects a Context that cannot safely be used as AAD input. It does
// not, and cannot, detect a Context whose fields are individually valid but
// semantically wrong for the ciphertext at hand — that is what
// authentication (a mismatched AAD failing to decrypt) is for.
func (c Context) validate() error {
	if c.Kind == "" {
		return fmt.Errorf("%w: kind is required", ErrInvalidContext)
	}
	if len(c.Kind) > maxContextKindLength {
		return fmt.Errorf("%w: kind exceeds maximum length", ErrInvalidContext)
	}
	for _, field := range [...]string{c.Table, c.Column, c.OwnerID, c.RecordID} {
		if len(field) > maxContextFieldLength {
			return fmt.Errorf("%w: field exceeds maximum length", ErrInvalidContext)
		}
	}
	return nil
}

// canonicalAAD deterministically encodes the Context into the bytes used as
// AEAD associated data.
//
// Every field, including the protocol version, is framed as an 8-byte
// big-endian length prefix followed by its raw bytes, in a fixed field order.
// This is deliberate: naive delimiter concatenation (a + ":" + b) can make two
// different (a, b) pairs produce the same joined string if either value may
// contain the delimiter. Length-prefixing has no such ambiguity regardless of
// field content, requires no escaping, and does not depend on map iteration
// order (Context is a plain struct, not a map), JSON field ordering (no JSON
// is involved), or locale (this is a pure byte encoding).
func (c Context) canonicalAAD() []byte {
	// contextProtocolVersion, Kind, Table, Column, OwnerID, RecordID: 6
	// fields, each prefixed by an 8-byte length.
	size := 6*8 + len(contextProtocolVersion) + len(c.Kind) + len(c.Table) + len(c.Column) + len(c.OwnerID) + len(c.RecordID)
	out := make([]byte, 0, size)
	out = appendLengthPrefixed(out, contextProtocolVersion)
	out = appendLengthPrefixed(out, c.Kind)
	out = appendLengthPrefixed(out, c.Table)
	out = appendLengthPrefixed(out, c.Column)
	out = appendLengthPrefixed(out, c.OwnerID)
	out = appendLengthPrefixed(out, c.RecordID)
	return out
}

// appendLengthPrefixed appends an 8-byte big-endian length prefix followed by
// v's bytes to dst, returning the extended slice.
//
// encoding/binary.BigEndian.PutUint64 is used instead of manual shifting so
// the truncating uint64->byte conversions it performs internally are not
// flagged as this package's own (gosec's G115 fires on any direct `byte(n >>
// shift)` truncation, even when — as here — it is the deliberate, correct way
// to extract one byte of a fixed-width length prefix).
func appendLengthPrefixed(dst []byte, v string) []byte {
	var lenPrefix [8]byte
	binary.BigEndian.PutUint64(lenPrefix[:], uint64(len(v)))
	dst = append(dst, lenPrefix[:]...)
	return append(dst, v...)
}
