package credentials

import "errors"

// The error taxonomy is deliberately small: every caller-visible failure maps
// to exactly one of these sentinels, checkable with errors.Is. None of them,
// and nothing wrapped around them elsewhere in this package, ever includes
// key bytes, plaintext, or a full ciphertext.
//
// Note in particular that ErrAuthenticationFailed is returned for a wrong
// key, a wrong/modified AAD, and any tamper of the nonce, ciphertext, or tag
// alike. AES-GCM cannot distinguish those cases from each other, and this
// package does not attempt to: a decryption oracle that reveals which one
// occurred would itself be a vulnerability.
var (
	// ErrInvalidKeyring covers a malformed keyring document, an unusable key
	// entry (bad base64, wrong decoded length, invalid or duplicate ID), a
	// missing or dangling active key, and an unsafe keyring file (see
	// keyring_file.go for the file-permission policy).
	ErrInvalidKeyring = errors.New("credentials: invalid keyring")

	// ErrInvalidEnvelope covers a malformed, truncated, or otherwise
	// structurally invalid envelope: wrong field count, unrecognized magic,
	// invalid key ID characters, or a field that fails to base64-decode.
	ErrInvalidEnvelope = errors.New("credentials: invalid envelope")

	// ErrUnsupportedVersion covers an envelope or keyring document that
	// parses structurally but declares a format version this package does
	// not implement.
	ErrUnsupportedVersion = errors.New("credentials: unsupported version")

	// ErrUnknownKey is returned when an envelope's key ID does not exist in
	// the keyring used to decrypt it.
	ErrUnknownKey = errors.New("credentials: unknown key")

	// ErrAuthenticationFailed is returned when AEAD authentication fails:
	// wrong key, wrong AAD, or any tamper of the nonce, ciphertext, or tag.
	ErrAuthenticationFailed = errors.New("credentials: authentication failed")

	// ErrInvalidContext is returned when the caller-supplied Context fails
	// validation (for example, an empty Kind or an oversized field).
	ErrInvalidContext = errors.New("credentials: invalid context")

	// ErrEntropyUnavailable is returned when reading a fresh nonce from the
	// configured random source fails.
	ErrEntropyUnavailable = errors.New("credentials: entropy unavailable")
)
