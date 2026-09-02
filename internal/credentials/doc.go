// Package credentials is the first-party cryptographic foundation that a
// later change will use to store GophishFR's reversible secrets (IMAP/SMTP
// passwords, webhook secrets, and similar values) encrypted at rest.
//
// This package encrypts nothing on its own yet: it only provides the
// primitive. No caller in this repository invokes it, no database column
// depends on it, and no application startup path requires it.
//
// # Threat model
//
// The primitive is designed to protect credentials against:
//
//   - a stolen database file, SQL dump, snapshot, or backup that does not
//     also include the keyring;
//
// It explicitly does NOT protect against:
//
//   - a compromised GophishFR process (the key material is necessarily in
//     process memory while in use);
//   - a compromised container or host that also has access to the keyring;
//   - malicious code running as part of the server;
//   - memory disclosure of the running process.
//
// Encryption at rest raises the cost of a passive disk/backup compromise. It
// is not a substitute for access control, host hardening, or not compromising
// the server in the first place.
//
// # Algorithm
//
// AES-256-GCM (AEAD), built exclusively from the Go standard library
// (crypto/aes, crypto/cipher, crypto/rand). No third-party crypto package and
// no home-grown primitive is used anywhere in this package.
//
//   - Keys are exactly 32 raw bytes (AES-256). A key that decodes to any
//     other length is rejected; keys are never truncated, padded, or derived
//     from a passphrase by this package.
//   - Nonces are exactly cipher.AEAD.NonceSize() bytes, drawn from
//     crypto/rand for every single encryption. Nonces are never reused,
//     derived from a counter, a timestamp, or record data.
//
// # Envelope
//
// Encrypt returns an Envelope: a versioned, self-describing string of the
// form:
//
//	gophishfr-cred:v1:<key-id>:<nonce-base64>:<ciphertext-and-tag-base64>
//
// The magic ("gophishfr-cred") and version ("v1") are literal constants, not
// derived from any field. Fields are never delimiter-joined without framing
// that could make them ambiguous: the key ID alphabet excludes ':', and the
// nonce/ciphertext fields are standard base64, which cannot contain ':'
// either, so the four-way split is unambiguous. The ciphertext field is the
// raw output of AEAD.Seal, which already appends the authentication tag; no
// tag is carried separately.
//
// Any value carrying the "gophishfr-cred:" prefix that a future offline
// legacy-plaintext migration encounters is a collision/blocker to raise, not
// data to decrypt or overwrite silently. That migration is out of scope here.
//
// Parsing is strict. Decrypt never falls back to treating its input as
// plaintext: an invalid envelope, an unsupported version, an unknown key ID,
// a bad base64 field, or a failed authentication tag check all return one of
// the sentinel errors in errors.go. There is no MaybeDecrypt,
// DecryptOrPlaintext, or similar escape hatch — a legacy-plaintext migrator is
// future scope, tracked separately, and will live outside this package.
//
// # Key IDs
//
// A key ID is not secret. It identifies which keyring entry produced an
// envelope, so it must be stable, but it is never derived from the key bytes
// and the key bytes are never stored in the envelope. Key IDs are restricted
// to ASCII letters, digits, '.', '-', and '_', 1 to 64 bytes long.
//
// # Keyring
//
// A Keyring is a small, versioned, statically loaded document: one active key
// used for every new encryption, plus any number of retired keys kept only so
// their still-existing envelopes keep decrypting (rotation-ready, without a
// scheduler, a KMS integration, or automatic re-encryption — none of that
// exists yet). Encrypt always uses the active key. Decrypt always uses the
// key ID carried by the envelope, which may be the active key or an older
// one.
//
// Keys are base64 standard encoding (RFC 4648 §4, with padding) of exactly 32
// raw bytes; this is the only encoding this package accepts. The document
// format, loader, and validation rules are documented in keyring.go and
// keyring_file.go. There is no default keyring: a missing or absent
// GOPHISHFR_CREDENTIAL_KEYRING_FILE is not this package's concern — it is not
// read here, and nothing in this repository requires it yet. Wiring an
// environment variable to LoadKeyringFile, and deciding whether its absence
// should be fail-closed at startup, is deliberately left to the future change
// that actually starts encrypting a credential column.
//
// A loaded Keyring is immutable: key bytes are copied on load and again on
// every read, so a caller cannot corrupt the keyring's internal state through
// a returned slice. It is safe for concurrent use by multiple goroutines;
// nothing mutates it after construction. Go cannot guarantee that key bytes
// are ever actually scrubbed from process memory (the runtime may keep
// copies, move objects, or already have paged something to swap), so callers
// must not treat any "zeroing" performed here as more than a best-effort
// reduction of the window key material stays resident — not a guarantee.
// Keyring, and the internal type holding raw key bytes, deliberately do not
// print their key material through String/GoString/%v/%#v.
//
// # AAD (the "context")
//
// Encrypt and Decrypt take a Context, which becomes the AEAD's authenticated
// (but not encrypted) associated data. The primitive never accepts an IMAP
// struct, an SMTP struct, or a GORM model directly — Context only carries
// caller-supplied, already-scalar fields (see context.go). Two Contexts that
// differ in any field always canonicalize to different AAD bytes: fields are
// length-prefixed rather than delimiter-joined, so, for example,
// {OwnerID:"1", RecordID:"23"} and {OwnerID:"12", RecordID:"3"} cannot
// collide the way naive "1"+":"+"23" concatenation could.
//
// Context intentionally has no notion of "table" or "record identity" beyond
// caller-supplied strings: this package does not hardcode any GophishFR
// table, column, or business ID. Which concrete values IMAP/SMTP/webhook
// integration will pass (a follow-up change) is not decided here.
//
// Critically, the AAD never includes a GophishFR release/build/git version.
// Only a stable crypto-protocol constant is mixed in (see
// contextProtocolVersion in context.go). If the product version were part of
// the AAD, every application upgrade would silently make every previously
// encrypted credential undecryptable. This package does not import, and must
// never import, any application version/config/model/controller package —
// that is enforced structurally by keeping this package's only inputs the
// Context struct and []byte, and is exercised by a compile-time-adjacent test
// asserting the package's import graph stays free of those packages.
//
// # Fail-closed behaviour
//
// Every one of the following is an error, never a fallback and never a panic:
// wrong key, wrong AAD, a modified Context, an unknown key ID, an unsupported
// envelope or keyring version, a malformed or truncated envelope, and any
// single-bit tamper of the nonce, ciphertext, authentication tag, or key ID.
// Two encryptions of the same plaintext under the same Context always produce
// different envelopes (the random nonce sees to that), and both still decrypt
// back to the original plaintext.
package credentials
