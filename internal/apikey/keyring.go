package apikey

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

const (
	// KeyringEnvironment is intentionally distinct from the credential
	// encryption keyring environment variable.
	KeyringEnvironment = "GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE"

	keyringFormatVersion = 1
	keySize              = 32
	maxKeyIDBytes        = 64
	maxKeyringKeys       = 16
	maxKeyringFileBytes  = 64 * 1024
	insecureModeMask     = 0o022
)

type keyEntryDocument struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type keyringDocument struct {
	Version     int                `json:"version"`
	ActiveKeyID string             `json:"active_key_id"`
	Keys        []keyEntryDocument `json:"keys"`
}

// Keyring is immutable after construction and safe for concurrent reads.
type Keyring struct {
	activeKeyID string
	ids         []string
	keys        map[string][keySize]byte
}

// String exposes only non-sensitive keyring shape.
func (k Keyring) String() string {
	return fmt.Sprintf("apikey.Keyring{version:%d keys:%d}", keyringFormatVersion, len(k.ids))
}

// GoString prevents %#v from dumping raw key material.
func (k Keyring) GoString() string { return k.String() }

// NewKeyring validates and copies decoded key material.
func NewKeyring(activeKeyID string, keys map[string][]byte) (*Keyring, error) {
	if len(keys) == 0 || len(keys) > maxKeyringKeys || !validKeyID(activeKeyID) {
		return nil, ErrMalformedKeyring
	}
	immutable := make(map[string][keySize]byte, len(keys))
	ids := make([]string, 0, len(keys))
	for id, key := range keys {
		if !validKeyID(id) || len(key) != keySize {
			return nil, ErrMalformedKeyring
		}
		var copied [keySize]byte
		copy(copied[:], key)
		immutable[id] = copied
		ids = append(ids, id)
	}
	if _, ok := immutable[activeKeyID]; !ok {
		return nil, ErrMalformedKeyring
	}
	sort.Strings(ids)
	return &Keyring{activeKeyID: activeKeyID, ids: ids, keys: immutable}, nil
}

// ParseKeyringJSON strictly parses the versioned API verifier keyring format.
func ParseKeyringJSON(data []byte) (*Keyring, error) {
	if len(data) == 0 || len(data) > maxKeyringFileBytes {
		return nil, ErrMalformedKeyring
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document keyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, ErrMalformedKeyring
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrMalformedKeyring
	}
	if document.Version != keyringFormatVersion ||
		len(document.Keys) == 0 || len(document.Keys) > maxKeyringKeys ||
		!validKeyID(document.ActiveKeyID) {
		return nil, ErrMalformedKeyring
	}
	keys := make(map[string][]byte, len(document.Keys))
	for _, entry := range document.Keys {
		if !validKeyID(entry.ID) {
			return nil, ErrMalformedKeyring
		}
		if _, duplicate := keys[entry.ID]; duplicate {
			return nil, ErrMalformedKeyring
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(entry.Key)
		if err != nil || len(decoded) != keySize ||
			base64.StdEncoding.EncodeToString(decoded) != entry.Key {
			return nil, ErrMalformedKeyring
		}
		keys[entry.ID] = decoded
	}
	return NewKeyring(document.ActiveKeyID, keys)
}

// LoadKeyringFile follows mounted-secret symlinks, validates the resolved
// target's permissions, and reads a bounded document. It never chmods files.
func LoadKeyringFile(path string) (*Keyring, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-configured secret mount.
	if err != nil {
		return nil, ErrMalformedKeyring
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&insecureModeMask != 0 {
		return nil, ErrMalformedKeyring
	}
	data, err := io.ReadAll(io.LimitReader(file, maxKeyringFileBytes+1))
	if err != nil || len(data) > maxKeyringFileBytes {
		return nil, ErrMalformedKeyring
	}
	return ParseKeyringJSON(data)
}

func validKeyID(id string) bool {
	if len(id) == 0 || len(id) > maxKeyIDBytes {
		return false
	}

	for _, character := range []byte(id) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

// ValidKeyID reports whether id is safe for the version-1 storage protocol.
func ValidKeyID(id string) bool { return validKeyID(id) }
