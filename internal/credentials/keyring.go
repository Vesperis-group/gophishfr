package credentials

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// keyringFormatVersion is the only keyring document version this package
// implements. A document declaring any other value is rejected wholesale
// (ErrUnsupportedVersion) rather than partially interpreted.
const keyringFormatVersion = 1

// aeadKeySize is the exact raw key length this package accepts: 32 bytes for
// AES-256. A key that decodes to any other length is rejected; this package
// never truncates, pads, or hashes a value into shape.
const aeadKeySize = 32

// keyEntryDocument and keyringDocument mirror the on-disk/wire JSON shape of
// a keyring. Keys are an array, not a JSON object keyed by ID: encoding/json
// silently collapses duplicate object keys to the last occurrence before
// application code ever sees them, which would make a duplicate key ID
// undetectable. An array lets this package reject duplicates explicitly.
type keyEntryDocument struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type keyringDocument struct {
	Version     int                `json:"version"`
	ActiveKeyID string             `json:"active_key_id"`
	Keys        []keyEntryDocument `json:"keys"`
}

// Keyring is a validated, immutable set of AES-256 keys: one active key used
// for every new encryption, plus any number of retired keys kept only so
// their still-existing envelopes keep decrypting.
//
// A Keyring is always fully valid: there is no way to obtain one for which
// the active key is missing, a key is the wrong size, or an ID is invalid or
// duplicated. It is safe for concurrent reads by multiple goroutines, and
// nothing in this package ever mutates one after construction.
type Keyring struct {
	version     int
	activeKeyID string
	keys        map[string][]byte
}

// String deliberately does not expose key material, only the shape of the
// keyring. It is safe to log or print a *Keyring.
func (k *Keyring) String() string {
	if k == nil {
		return "credentials.Keyring(nil)"
	}
	return fmt.Sprintf("credentials.Keyring{version:%d active_key_id:%q keys:%d}", k.version, k.activeKeyID, len(k.keys))
}

// GoString mirrors String so that %#v is equally safe to print.
func (k *Keyring) GoString() string {
	return k.String()
}

// ActiveKeyID returns the key ID that Encrypt uses. It is not secret.
func (k *Keyring) ActiveKeyID() string {
	return k.activeKeyID
}

// activeKey returns the active key ID and a fresh copy of its raw bytes.
//
// Construction (NewKeyring / ParseKeyringJSON) always guarantees the active
// key ID names a present key, so the error path here is an unreachable
// defensive check, not a case a well-formed Keyring can hit.
func (k *Keyring) activeKey() (string, []byte, error) {
	raw, err := k.lookup(k.activeKeyID)
	if err != nil {
		return "", nil, fmt.Errorf("%w: active key is missing", ErrInvalidKeyring)
	}
	return k.activeKeyID, raw, nil
}

// lookup returns a fresh copy of the raw key bytes for id, or
// ErrUnknownKey if id is not present. The copy ensures a caller holding the
// returned slice cannot observe or corrupt the Keyring's internal state.
func (k *Keyring) lookup(id string) ([]byte, error) {
	stored, ok := k.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKey, id)
	}
	cp := make([]byte, len(stored))
	copy(cp, stored)
	return cp, nil
}

// NewKeyring constructs a Keyring from already-decoded key material,
// applying the same validation ParseKeyringJSON applies to a parsed
// document. It exists for callers (including tests) that already hold raw
// key bytes rather than a JSON document. Every key slice is copied, so the
// caller's slices are never aliased by the returned Keyring.
func NewKeyring(activeKeyID string, keys map[string][]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no keys present", ErrInvalidKeyring)
	}
	copied := make(map[string][]byte, len(keys))
	for id, key := range keys {
		if !validKeyID(id) {
			return nil, fmt.Errorf("%w: invalid key id", ErrInvalidKeyring)
		}
		if len(key) != aeadKeySize {
			return nil, fmt.Errorf("%w: invalid key size", ErrInvalidKeyring)
		}
		cp := make([]byte, aeadKeySize)
		copy(cp, key)
		copied[id] = cp
	}
	if activeKeyID == "" {
		return nil, fmt.Errorf("%w: missing active key id", ErrInvalidKeyring)
	}
	if _, ok := copied[activeKeyID]; !ok {
		return nil, fmt.Errorf("%w: active key id not present in keys", ErrInvalidKeyring)
	}
	return &Keyring{version: keyringFormatVersion, activeKeyID: activeKeyID, keys: copied}, nil
}

// ParseKeyringJSON strictly parses and validates a keyring document.
//
// Rejected outright (ErrInvalidKeyring unless noted):
//   - empty input;
//   - malformed JSON, or any field not part of the documented shape
//     (unknown fields are rejected rather than silently ignored);
//   - trailing bytes after the JSON document;
//   - a declared version other than keyringFormatVersion
//     (ErrUnsupportedVersion);
//   - zero keys;
//   - any key ID that is empty, too long, uses a byte outside
//     validKeyID's alphabet, or is duplicated;
//   - any key value that is not standard-encoding base64, or that decodes to
//     anything other than exactly 32 bytes;
//   - a missing or empty active_key_id, or one that does not name a key
//     present in keys.
//
// A returned *Keyring is always fully valid; there is no partially-valid
// result.
func ParseKeyringJSON(data []byte) (*Keyring, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty document", ErrInvalidKeyring)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var doc keyringDocument
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON", ErrInvalidKeyring)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: trailing data after JSON document", ErrInvalidKeyring)
	}

	if doc.Version != keyringFormatVersion {
		return nil, fmt.Errorf("%w: keyring version %d", ErrUnsupportedVersion, doc.Version)
	}
	if len(doc.Keys) == 0 {
		return nil, fmt.Errorf("%w: no keys present", ErrInvalidKeyring)
	}

	keys := make(map[string][]byte, len(doc.Keys))
	for _, entry := range doc.Keys {
		if !validKeyID(entry.ID) {
			return nil, fmt.Errorf("%w: invalid key id", ErrInvalidKeyring)
		}
		if _, exists := keys[entry.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate key id", ErrInvalidKeyring)
		}

		raw, err := base64.StdEncoding.Strict().DecodeString(entry.Key)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid key encoding", ErrInvalidKeyring)
		}
		if len(raw) != aeadKeySize {
			return nil, fmt.Errorf("%w: invalid key size", ErrInvalidKeyring)
		}

		cp := make([]byte, aeadKeySize)
		copy(cp, raw)
		keys[entry.ID] = cp
	}

	if doc.ActiveKeyID == "" {
		return nil, fmt.Errorf("%w: missing active key id", ErrInvalidKeyring)
	}
	if _, ok := keys[doc.ActiveKeyID]; !ok {
		return nil, fmt.Errorf("%w: active key id not present in keys", ErrInvalidKeyring)
	}

	return &Keyring{version: doc.Version, activeKeyID: doc.ActiveKeyID, keys: keys}, nil
}
