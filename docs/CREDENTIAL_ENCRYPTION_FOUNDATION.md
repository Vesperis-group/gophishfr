# Credential encryption foundation

Package: [`internal/credentials`](../internal/credentials)

This document is the human-readable companion to the package's own Go doc
comment (`internal/credentials/doc.go`), which is the authoritative reference
for exact behaviour. This page summarizes it for readers who are not
browsing `go doc`.

The first production integration is IMAP-password-only and is documented in
[`IMAP_CREDENTIAL_ENCRYPTION.md`](IMAP_CREDENTIAL_ENCRYPTION.md). SMTP,
webhook, and other credentials remain outside this foundation and are not
encrypted by that integration.

## Threat model

Encrypting a credential column at rest is meant to raise the cost of:

- a stolen database file, SQL dump, snapshot, or backup — **as long as it does
  not also include the keyring**.

It is **not** meant to, and does not, protect against:

- a compromised GophishFR process (key material is necessarily in memory
  while it is being used);
- a compromised container or host that also has access to the keyring;
- malicious code running as part of the server;
- disclosure of the running process's memory.

If the attacker has the keyring, or has arbitrary code execution in the
process, this primitive provides no protection. It only helps when the data
and the key are compromised separately (e.g. a leaked backup without the
keyring file that lives outside the database).

## Algorithm

AES-256-GCM, an AEAD (authenticated encryption with associated data)
construction, built entirely from the Go standard library
(`crypto/aes`, `crypto/cipher`, `crypto/rand`). No third-party crypto library
and no custom cryptographic construction is used anywhere in this package.

GCM was chosen because it authenticates the ciphertext (tamper detection is
part of the primitive, not bolted on afterwards) and it authenticates
associated data (AAD) without encrypting it, which is exactly what's needed
to bind an envelope to a specific credential's stable identity (see
[AAD](#aad--context) below) without needing to encrypt that identity itself.

- **Key size**: exactly 32 raw bytes (AES-256). A key of any other decoded
  length is rejected outright. Keys are never truncated, padded, or derived
  from a passphrase — the operator supplies 32 already-random bytes.
- **Nonce**: exactly `cipher.AEAD.NonceSize()` bytes (12, for the standard
  library's GCM construction), drawn from `crypto/rand` for every single
  encryption. Nonces are never reused, counter-based, timestamp-based, or
  derived from the record being encrypted.

## Envelope

`Encrypt` returns an `Envelope` (a `string`), safe to store as a database
`TEXT`/`VARCHAR` value, log, or print:

```
gophishfr-cred:v1:<key-id>:<nonce-base64>:<ciphertext-and-tag-base64>
```

| Field | Meaning |
| --- | --- |
| `gophishfr-cred` | Fixed magic. A future offline legacy-plaintext migration must treat any *existing* plaintext value that happens to already start with this prefix as a collision to raise, never as something to blindly decrypt or overwrite. |
| `v1` | Envelope format version. A future `v2` can be added as another parse branch; an unrecognized version is rejected, never guessed at. |
| `<key-id>` | Which keyring entry encrypted this value. Not secret (see [Keyring](#keyring)). Restricted to `[A-Za-z0-9._-]`, 1–64 bytes, so it can never be confused with the `:` field delimiter. |
| `<nonce-base64>` | The per-encryption random nonce, standard base64. |
| `<ciphertext-and-tag-base64>` | `AEAD.Seal`'s output verbatim (ciphertext with the authentication tag already appended), standard base64. |

Parsing is strict: `strings.SplitN` caps the split at exactly 5 fields, so a
tampered/adversarial ciphertext field can never be re-interpreted as
additional structure. A malformed field count, unrecognized magic,
unsupported version, invalid key ID, or invalid base64 is rejected before any
cryptographic operation runs.

## Keyring

A keyring is a small, versioned JSON document:

```json
{
  "version": 1,
  "active_key_id": "key-2026-01",
  "keys": [
    {"id": "key-2026-01", "key": "<base64 32-byte key>"},
    {"id": "key-2025-06", "key": "<base64 32-byte key>"}
  ]
}
```

`keys` is an array, not a JSON object keyed by ID: `encoding/json` silently
collapses duplicate object keys to the last one seen, which would make a
duplicate key ID undetectable. The array form lets the loader reject a
duplicate ID explicitly instead.

Validation (`ParseKeyringJSON`, used by both `LoadKeyringFile` and any
in-process document) rejects, as a single non-partial pass/fail:

- malformed JSON, unknown fields, or trailing data after the document;
- a `version` other than the one this package implements;
- zero keys;
- any key ID that's empty, over 64 bytes, uses a character outside
  `[A-Za-z0-9._-]`, or repeats another entry's ID;
- any key value that isn't standard-encoding base64, or that doesn't decode
  to exactly 32 bytes;
- a missing, empty, or dangling `active_key_id` (it must name a key that is
  actually present in `keys`).

There is no default keyring and no fallback: a document that fails any of the
above is rejected wholesale, with no partially-usable result.

**Rotation**: `Encrypt` always uses the active key. `Decrypt` always uses
whichever key ID the envelope names, active or retired. Rotating means
publishing a new keyring with a new `active_key_id` while keeping the
previous entry present so old envelopes keep decrypting — no scheduler, KMS
integration, or automatic re-encryption exists yet, or is implied by this
change.

**Immutability**: key bytes are copied on load and again every time one is
read internally, so a caller can never corrupt a `Keyring`'s internal state
through a slice it was handed. A loaded `Keyring` is safe for concurrent use
by multiple goroutines — nothing mutates it after construction. `Keyring`
does not implement `String`/`GoString` by exposing key material; printing one
(accidentally, in a log line) only shows its version, active key ID, and key
count.

Go cannot guarantee key bytes are ever actually scrubbed from process memory
(slice growth, GC moves, and swapped pages are all outside this package's
control), so any zeroing this package performs is a best-effort reduction of
exposure window, not a guarantee.

### File loader and permissions

`LoadKeyringFile(path)` opens `path`, checks the **resolved, opened** file
(never the symlink itself, if `path` is one), and rejects it if the resolved
file's mode allows group or world write (`mode & 0o022 != 0`). It does not
require any particular *read* mode: Docker secrets and Kubernetes secret
volumes commonly present a file as `0444` or similar, and this loader accepts
that.

Symlinks are followed on purpose, not rejected: both mechanisms commonly
present a keyring as a symlink into a separately-mounted, read-only location,
and rejecting that shape would break a legitimate secret mount without adding
protection — the properties that matter (permissions, content) belong to the
resolved target, which is exactly what gets checked.

This loader never changes a file's permissions and never creates a keyring
file. An operator whose mount doesn't meet the permission bar must fix the
mount.

**Recommended file mode**: read access restricted to the account/service
running GophishFR (for example `0400`/`0440`, or whatever the secret-mount
mechanism in use provides, as long as it isn't group/world-writable). This is
not a promise that Unix permissions alone protect against a compromised root
account or host — see [Threat model](#threat-model).

### Generating a keyring for local/manual testing

This package ships no default or example keyring file. To generate a
synthetic 32-byte key for local experimentation:

```sh
openssl rand -base64 32
```

The IMAP integration reads `GOPHISHFR_CREDENTIAL_KEYRING_FILE` once at process
bootstrap. Its absence is compatible with installations that do not use
encrypted IMAP credentials; IMAP secret reads and writes fail closed without
it. See the operational guide linked above.

## AAD / Context

`Encrypt`/`Decrypt` take a `Context` (`Kind`, `Table`, `Column`, `OwnerID`,
`RecordID`, all plain strings), which becomes the AEAD's associated data.
The primitive never accepts an IMAP struct, an SMTP struct, or a GORM entity
directly, and this package does not hardcode any concrete GophishFR table,
column, or business identifier — which values a future integration passes is
not decided here.

Encoding is length-prefixed (an 8-byte big-endian length ahead of each
field's raw bytes, `contextProtocolVersion` included), not delimiter-joined.
This means two different `(OwnerID, RecordID)` pairs can never canonicalize
to the same AAD bytes regardless of content — naive `a + ":" + b`
concatenation could, if either value could itself contain `:`. The encoding
is a pure byte operation: independent of map iteration (`Context` is a
struct, not a map), of JSON field ordering (no JSON is involved), and of
locale.

**Why the GophishFR application version is excluded**: only a fixed
crypto-protocol constant (`contextProtocolVersion`, currently
`"gophishfr-credential-context-v1"`) is mixed in — never a release, build, or
git version. If the product version were part of the AAD, every application
upgrade would silently make every already-encrypted credential
undecryptable. The package does not import, and must never import, this
repository's `config`, `models`, or `controllers` packages; a test
(`TestPackageDoesNotImportApplicationLayers`) parses the package's own source
and fails if it ever does.

## Fail-closed behaviour

There is no plaintext fallback anywhere in this package: an invalid envelope,
an unsupported version, an unknown key ID, a bad base64 field, or a failed
authentication check are all errors, never a decision to treat the input as
already-plaintext. The package exposes no `MaybeDecrypt`, `DecryptOrPlaintext`,
or similarly named escape hatch. A future, separate legacy-plaintext migrator
owns that concern entirely; it does not exist yet.

Wrong key, wrong AAD, and any single-bit tamper of the nonce, ciphertext, or
authentication tag are collapsed into the same `ErrAuthenticationFailed`:
AES-GCM cannot distinguish those cases, and this package does not attempt to
— a decryption oracle that revealed *which* one occurred would itself be a
vulnerability.

## Error taxonomy

| Sentinel | Meaning |
| --- | --- |
| `ErrInvalidKeyring` | Malformed keyring document, bad key entry, missing/dangling active key, or an unsafe keyring file. |
| `ErrInvalidEnvelope` | Malformed/truncated envelope: wrong shape, bad magic, invalid key ID, bad base64. |
| `ErrUnsupportedVersion` | A structurally valid envelope or keyring declaring a version this package doesn't implement. |
| `ErrUnknownKey` | The envelope's key ID isn't present in the keyring used to decrypt it. |
| `ErrAuthenticationFailed` | AEAD authentication failed: wrong key, wrong AAD, or tamper. |
| `ErrInvalidContext` | The caller-supplied `Context` failed validation (e.g. empty `Kind`, oversized field). |
| `ErrEntropyUnavailable` | Reading a fresh nonce from the configured random source failed. |

None of these, nor anything wrapped around them, ever includes key bytes,
plaintext, or a full ciphertext.

## Testing

`go test ./internal/credentials/...` covers, among others: round-trip for
empty/ASCII/Unicode/binary/large-but-reasonable plaintext; non-determinism
(two encryptions of the same input always differ, both still decrypt);
tampered nonce/ciphertext/tag/key ID; wrong key; wrong/modified AAD; unknown
key; unsupported/malformed/truncated envelopes; malformed/unsupported
keyrings (including duplicate key IDs); keyring immutability; a
regression check that a distinctive plaintext marker never appears anywhere
in a serialized envelope; and an entropy-failure path exercised through an
unexported, package-private test seam (no such knob exists in the public
API). `go test -race` passes, since a loaded `Keyring` and `Cipher` are never
mutated after construction. `FuzzParseEnvelope` and `FuzzDecrypt` fuzz the
envelope parser and end-to-end `Decrypt` respectively; both run their seed
corpus under a plain `go test` and have been fuzzed locally for a bounded
smoke run with no crash.
