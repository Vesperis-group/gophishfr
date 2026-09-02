package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// randReader is the entropy source used for every nonce. It is deliberately
// unexported: production code always uses crypto/rand.Reader, and there is no
// exported way to change that. A package-internal test file may still
// substitute it, in-process, to exercise the entropy-failure path (see
// ErrEntropyUnavailable) without adding a production-facing knob that could
// be pointed at a weak or predictable source.
var randReader io.Reader = rand.Reader

// Cipher binds a Keyring to the Encrypt/Decrypt operations. It holds no
// mutable state of its own and is safe for concurrent use by multiple
// goroutines, since the Keyring it wraps is immutable after construction.
type Cipher struct {
	keyring *Keyring
}

// New returns a Cipher that encrypts with keyring's active key and decrypts
// with whichever of keyring's keys an envelope names.
func New(keyring *Keyring) (*Cipher, error) {
	if keyring == nil {
		return nil, fmt.Errorf("%w: nil keyring", ErrInvalidKeyring)
	}
	return &Cipher{keyring: keyring}, nil
}

// Encrypt authenticates plaintext under ctx and returns a new Envelope
// sealed with the Cipher's active key.
//
// A fresh nonce is read from crypto/rand for this call alone (never reused,
// derived, or counter-based), so two calls with identical ctx and plaintext
// always produce different Envelopes, even though both decrypt back to the
// same plaintext.
func (c *Cipher) Encrypt(ctx Context, plaintext []byte) (Envelope, error) {
	if c == nil || c.keyring == nil {
		return "", fmt.Errorf("%w: cipher has no keyring", ErrInvalidKeyring)
	}
	if err := ctx.validate(); err != nil {
		return "", err
	}

	keyID, key, err := c.keyring.activeKey()
	if err != nil {
		return "", err
	}
	defer zero(key)

	gcm, err := newAEAD(key)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return "", fmt.Errorf("%w", ErrEntropyUnavailable)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, ctx.canonicalAAD())

	env := &rawEnvelope{keyID: keyID, nonce: nonce, ciphertext: ciphertext}
	return env.marshal(), nil
}

// Decrypt strictly parses env, looks up the key it names in the Cipher's
// keyring, and returns the plaintext only if authentication under ctx
// succeeds.
//
// Decrypt never falls back to returning env's raw bytes as plaintext: every
// failure — a malformed or truncated envelope, an unsupported version, an
// unknown key ID, or a failed authentication check (wrong key, wrong ctx, or
// any tamper) — is an error.
func (c *Cipher) Decrypt(ctx Context, env Envelope) ([]byte, error) {
	if c == nil || c.keyring == nil {
		return nil, fmt.Errorf("%w: cipher has no keyring", ErrInvalidKeyring)
	}
	if err := ctx.validate(); err != nil {
		return nil, err
	}

	parsed, err := parseEnvelope(env)
	if err != nil {
		return nil, err
	}

	key, err := c.keyring.lookup(parsed.keyID)
	if err != nil {
		return nil, err
	}
	defer zero(key)

	gcm, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	// The nonce length must match this exact AEAD instance before it is ever
	// passed to Open: cipher.AEAD.Open panics (rather than returning an
	// error) when given a nonce of the wrong length. Checking here is what
	// keeps a malformed or fuzzed envelope from ever reaching that panic.
	if len(parsed.nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("%w: invalid nonce length", ErrInvalidEnvelope)
	}

	plaintext, err := gcm.Open(nil, parsed.nonce, parsed.ciphertext, ctx.canonicalAAD())
	if err != nil {
		return nil, fmt.Errorf("%w", ErrAuthenticationFailed)
	}
	return plaintext, nil
}

// newAEAD builds the AES-256-GCM AEAD for key, which must already be exactly
// aeadKeySize bytes (every Keyring construction path guarantees this; the
// length is re-checked here only as a defensive invariant, not because a
// caller is expected to trip it).
func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != aeadKeySize {
		return nil, fmt.Errorf("%w: key has invalid size", ErrInvalidKeyring)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: unable to initialize cipher", ErrInvalidKeyring)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: unable to initialize AEAD", ErrInvalidKeyring)
	}
	return gcm, nil
}

// zero best-effort overwrites b with zero bytes. This reduces, but — as
// documented in doc.go — cannot guarantee, how long key material observed by
// this package remains resident in process memory: the Go runtime may have
// already copied it (e.g. during a slice grow or a garbage-collector move)
// before zero ever runs.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
