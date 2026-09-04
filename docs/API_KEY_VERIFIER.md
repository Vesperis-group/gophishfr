# API-key HMAC verifiers

GophishFR never needs to recover an API token after issuing it. New and
migrated users therefore store only a keyed verifier and its key ID. Existing
client token **values and formats do not change**.

## Protocol and threat model

Protocol version 1 computes:

```text
HMAC-SHA-256(
  32-byte verifier key,
  uint64_be(len("gophishfr-api-key-verifier:v1")) ||
  "gophishfr-api-key-verifier:v1" ||
  uint64_be(len(exact_token_bytes)) ||
  exact_token_bytes
)
```

The raw 32-byte result is stored in the database. Generated tokens remain 32
bytes from `crypto/rand`, encoded as 64 lowercase hexadecimal characters (256
bits). An unkeyed digest would already resist guessing for those generated
tokens. HMAC is necessary because the legacy
`GOPHISH_INITIAL_ADMIN_API_TOKEN` contract permits any non-empty value,
including weak values; the external pepper prevents offline verification after
a database-only leak.

This protects token confidentiality in a read-only database, dump, or backup
leak without the pepper. It does **not** protect against:

- compromise of the process/host and pepper;
- a write-capable database attacker (moving a verifier/key-ID pair can move
  the authentication identity);
- query/form tokens captured by URL, proxy, or access logs;
- compromise of the browser during the one-time reveal; or
- online guessing of weak operator-provided tokens. API-key rate limiting and
  removal of query/form transports remain separate backlog items.

SQL index behavior can expose timing effects; constant-time Go comparisons do
not claim to remove database timing signals.

## Dedicated keyring

Set `GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE` to a separately backed-up,
read-only mounted secret:

```json
{
  "version": 1,
  "active_key_id": "api-key-2026-01",
  "keys": [
    {"id": "api-key-2026-01", "key": "<canonical base64 of exactly 32 random bytes>"},
    {"id": "api-key-old", "key": "<canonical base64 of a different 32-byte key>"}
  ]
}
```

The array shape allows duplicate IDs to be rejected. IDs are 1–64 ASCII
letters, digits, `.`, `_`, or `-`; at most 16 keys are accepted. Unknown
fields/versions, duplicate IDs, missing active keys, non-canonical base64,
wrong key sizes, oversized files, and group/world-writable targets are
rejected. Mounted-secret symlinks and read-only `0400`/`0440` targets work.
The file is loaded once; there is no hot reload, default, auto-generation,
repository/image copy, or database copy.

This keyring is independent from
`GOPHISHFR_CREDENTIAL_KEYRING_FILE` and `internal/credentials`. Never reuse
credential-encryption material, and mount the two files from separate secret
sources. There is no fallback in either direction.

## Storage and authentication

SQLite stores a nullable legacy `api_key`, a raw BLOB verifier, and a
`VARCHAR(64)` key ID. MySQL uses nullable `VARCHAR(255)`, `BINARY(32)`, and
`VARCHAR(64)`. Both have a unique composite index on `(key ID, verifier)`.
PostgreSQL is **unsupported** because this repository has no PostgreSQL
application migration tree; no PostgreSQL DDL is implied.

Normal runtime accepts only:

```text
api_key IS NULL
verifier is exactly 32 bytes
key ID is structurally valid and present in the loaded keyring
account_locked is not true
```

It computes one candidate per accepted pepper and performs one bounded,
parameterized indexed logical query. Zero matches is invalid, one match is the
identity, and multiple matches fail closed. Runtime never reads, queries, or
falls back to plaintext.

An old-key match is conditionally upgraded to the active key with an exact
user/old-pair CAS. If another writer wins, the presented token is checked
against the current row: a concurrent upgrade may succeed, while a committed
reset makes the old token fail. A non-conflict rekey database error may leave
the already-validated old verifier and allow that request. Lock state is part
of the initial indexed lookup, lazy-rekey CAS/reread, and final authentication
check. A lock committed before that final check rejects the request without
rekeying when it precedes the CAS; a lock committed after completed
authentication follows normal per-request race semantics. Locked and unknown
accounts return the same invalid-key response. Existing session behavior is
unchanged.

An old pepper may be retired only after no row references its ID, or affected
users have authenticated (lazy upgrade) or reset. Offline bulk rekey is
impossible without plaintext. Losing a referenced key makes those tokens
unverifiable; restore the keyring or use session administration with a
restored/new active keyring to reissue tokens. Old values cannot be recovered.

## Explicit irreversible migration

Schema migration only makes the legacy column nullable and adds verifier
storage. It never silently transforms data. Before external API clients resume:

1. stop every application writer;
2. create a restorable pre-migration database backup;
3. back up the verifier keyring separately;
4. test database and keyring restoration;
5. run `./gophishfr --migrate-api-keys` with the dedicated keyring configured;
6. verify existing clients using their unchanged token values; and
7. retain the backup for the rollback window.

The command preflights every row before mutation, uses one global transaction,
writes and exactly reads back the verifier/key ID before clearing plaintext,
and then reads back the final state. Coherent legacy rows migrate under the
active key; coherent verifier-only rows remain byte-for-byte unchanged; a
controlled BOTH row is cleared only if HMAC verification succeeds. Empty,
oversized, malformed, unknown-key, ambiguous, mismatched, truncated, collided,
or concurrently changed rows roll back the whole transaction. Re-running a
successful migration is a no-op.

**This operation is irreversible.** HMAC cannot reconstruct a token. There is
no `--rollback-api-keys` command. Real rollback means restoring the tested
pre-migration database backup with the old runtime, or reissuing keys. The
schema Down guard refuses before mutation if any verifier-only state would be
lost.

## Issuance and browser behavior

Fresh bootstrap requires a valid verifier keyring before inserting the first
administrator. A provided `GOPHISH_INITIAL_ADMIN_API_TOKEN` is used byte for
byte but only its verifier is stored. Without it, a strong token is generated
and not logged; sign in and intentionally reset in Settings to obtain a usable
replacement. Password-file bootstrap remains unchanged and atomic.

Existing installations may start without the verifier keyring for session
administration. API-key authentication, user creation, and reset fail closed.
Legacy plaintext rows never authenticate at runtime; run the offline migration.

User GET/PUT responses and ordinary settings pages contain no token, verifier,
key ID, mask, last-four value, or presence oracle. User creation and reset
return a new token only in that immediate successful response. Closing the
dedicated reveal removes it from the DOM and retained JavaScript state where
reasonable; reload cannot recover it. It is never placed in cookies,
`localStorage`, `sessionStorage`, global page state, logs, or later requests.
JavaScript memory zeroization is not guaranteed. If the response is lost, there
is no grace period: reset again through an authenticated session/admin.

Bearer, raw Authorization, query, and form transports remain compatible.
Session/API-key selection, explicit-invalid no-fallback, RBAC, view-only, CSRF,
forced-password-change, and campaign-completion boundaries are unchanged.
