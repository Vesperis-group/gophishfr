# Webhook secret encryption operations

GophishFR stores webhook authentication secrets as versioned AES-256-GCM
envelopes produced by [`internal/credentials`](../internal/credentials). Each
envelope is authenticated for the webhook's own immutable, database-generated
ID, so copying it to another webhook's row makes it unusable. Webhooks are a
global administrator resource (there is no owner/tenant field), so the
associated-data identity is `Kind=webhook-secret`, `Table=webhooks`,
`Column=secret`, an empty `OwnerID`, and `RecordID=<webhooks.id>`. Name, URL,
active state, event selection, timestamps, HTTP status/headers, and the
application/build version are all mutable and are therefore never part of the
associated data: changing any of them cannot make an existing secret
undecryptable, and copying ciphertext between two webhook IDs always fails
authentication.

This protects a database, dump, snapshot, or backup obtained without the
separately stored keyring. It does not protect a compromised running process
or a host that can read both the database and keyring. Webhook URLs
(including any userinfo or query-string tokens they may contain) and webhook
headers are not changed by this feature -- see "Out of scope" below.

## Compatibility and API behavior

A webhook without a secret remains fully valid and delivers exactly as
before: `Validate()` still only requires a name and a URL, and delivery signs
with `HMAC-SHA256("", payload)` and still sets the
`X-Gophish-Signature: sha256=<hex>` header. Nothing about the no-secret path
changed.

This is an intentional, security-breaking API change:

- Webhook secrets are write-only and are never returned by list, detail,
  create, update, or validate ("ping") responses.
- Ciphertext, key IDs, envelope markers, masks, and `has_secret` indicators
  are never returned.
- Editing a webhook always opens a blank secret field; the browser never
  fetches the plaintext or ciphertext.

Because the legacy API could not literally keep "empty/absent secret always
clears" once the field can no longer be prefilled (an ordinary edit could
otherwise never resend the secret and would silently revoke it on every
save), create and update now use an explicit tri-state, confirmed by the
project owner as the correct, minimal resolution:

| Incoming `secret` | Meaning | Effect |
| --- | --- | --- |
| absent (key omitted) | preserve | leaves any existing ciphertext byte-for-byte unchanged; no decrypt/re-encrypt |
| JSON `null` | preserve | identical to absent -- **not** rejected, unlike SMTP/IMAP |
| present empty string `""` | explicit clear | atomically empties both secret columns |
| present non-empty string | replace | encrypts the value under the active key and the webhook's unchanged ID |

`CREATE` collapses preserve and clear to the same outcome: there is nothing to
preserve or clear yet, so both simply create a no-secret webhook -- matching
the legacy create contract exactly. Only a non-empty value creates a secret.

Webhook secrets accept up to 255 valid-Unicode **characters** (not bytes),
matching the legacy MySQL `VARCHAR(255)` contract, including multibyte
characters up to the maximum 4-byte UTF-8 encoding.

The `POST /webhooks/{id}/validate` ("ping") action accepts the same
tri-state `secret` field, entirely optionally: an absent/null field (what the
stock UI's ping button sends) authenticates with the webhook's own stored
secret; an explicit empty string validates the no-secret behavior; and a
non-empty value is used only in memory for that one request -- it is never
persisted or echoed back.

## Safety requirements

Before migrating or rolling back:

1. Stop every GophishFR process and every other database writer.
2. Take and test a restorable database backup.
3. Back up the keyring separately from the database.
4. Confirm the database backend is SQLite or MySQL.

The actions are offline-only and transform the complete `webhooks` table in
one transaction. A row failure aborts the whole action.

> **A database backup alone is insufficient after encryption.** Losing every
> copy of a key makes ciphertext using it unrecoverable. Rolling back to an
> old binary requires explicit data decryption before the schema is
> downgraded.

PostgreSQL is explicitly unsupported for this credential migration because
the repository has no PostgreSQL application schema or migration tree. The
CLI rejects it before credential data is mutated.

## Keyring

Webhooks reuse the exact same application keyring as IMAP and SMTP, loaded
once at startup from `GOPHISHFR_CREDENTIAL_KEYRING_FILE`:

```json
{
  "version": 1,
  "active_key_id": "credentials-2026-09",
  "keys": [
    {
      "id": "credentials-2026-09",
      "key": "<base64-encoded 32 random bytes>"
    }
  ]
}
```

Generate key bytes on an operator-controlled machine with
`openssl rand -base64 32`. Never place the resulting keyring in the
repository, database, container image, or logs. The resolved file must not be
group- or world-writable:

```sh
chmod 0400 /etc/gophishfr/credential-keyring.json
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
```

No key is generated automatically, no second environment variable exists, and
no second keyring parser was added. Installations containing only no-secret
webhooks can run without a keyring; creating, replacing, migrating, or
delivering with an encrypted secret fails closed until a valid keyring is
supplied.

New secrets use the active key. Keep retired keys in the keyring while any
envelope still references them. Bulk re-encryption and KMS/Vault integration
remain future work.

## Schema upgrade and offline migration

The paired SQLite/MySQL Goose migration
`20260903020000_encrypt_webhook_secrets.sql` retains the legacy `secret`
column temporarily and adds `secret_ciphertext VARCHAR(2048)`. It adds no
uniqueness, ownership, or event-relation constraint. The application derives
a conservative maximum accepted envelope from the 255-*character* plaintext
limit (sized for the worst case of every character needing the maximum 4
UTF-8 bytes -- 1020 bytes), fixed v1 fields, a 64-byte maximum key ID, a
12-byte nonce, and a 16-byte AES-GCM tag after base64 encoding. The
2048-character column retains a comfortable safety margin.

With writers stopped and both backups complete, run:

```sh
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
./gophishfr --config ./config.json --migrate-webhook-secrets
```

The action validates the backend, schema, unique positive webhook IDs, every
existing row state, and every pre-existing envelope before writing. For each
legacy plaintext row it:

1. encrypts with AAD `webhook-secret / webhooks / secret / (no owner) / webhooks.id`;
2. immediately decrypts and compares the value in memory;
3. writes the envelope while retaining plaintext and reads both columns back
   byte-for-byte inside the transaction;
4. clears plaintext only after that persisted-value check and verifies the
   final column state; and
5. commits only after every row succeeds.

Already-encrypted rows are authenticated and left byte-identical. Rows with
both secret columns empty (no-secret webhooks) remain unchanged. Rows with
both columns populated, invalid IDs, malformed envelopes, missing keys, or
authentication failures block the entire migration. Re-running a successful
migration is a count-only no-op.

Verify state and counts without printing secrets or full envelopes:

```sql
SELECT
  SUM(CASE WHEN COALESCE(secret, '') = '' AND secret_ciphertext <> '' THEN 1 ELSE 0 END)
    AS encrypted_rows,
  SUM(CASE WHEN COALESCE(secret, '') <> '' AND secret_ciphertext = '' THEN 1 ELSE 0 END)
    AS legacy_rows,
  SUM(CASE WHEN COALESCE(secret, '') <> '' AND secret_ciphertext <> '' THEN 1 ELSE 0 END)
    AS invalid_rows,
  SUM(CASE WHEN COALESCE(secret, '') = '' AND secret_ciphertext = '' THEN 1 ELSE 0 END)
    AS no_secret_rows
FROM webhooks;
```

`legacy_rows` and `invalid_rows` must be zero before normal startup: a
not-yet-migrated legacy secret is a distinct, fail-closed runtime state (see
below), not a fallback.

## Normal startup and delivery boundary

Keep the same environment variable set for every process that creates,
updates, validates, or delivers with encrypted webhooks:

```sh
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
./gophishfr --config ./config.json
```

The legacy column is never a runtime fallback. A webhook's secret is
decrypted only immediately before it is handed to the HMAC-signing boundary
(`models.ActiveWebhookEndpoints`, consumed by both campaign event delivery
and the validate/ping endpoint) and is never attached back to a persisted,
loggable, or serializable webhook value.

A webhook whose stored credential cannot be authenticated -- malformed,
oversized, tampered, moved to another webhook's row, encrypted under an
unknown/missing key, or a legacy secret that has not yet been migrated --
fails before any HTTP request for that webhook. During campaign delivery this
means the one bad webhook is excluded and logged once by ID only; every other
active webhook is still delivered normally, and there is no retry loop (there
is no webhook retry engine at all, and this change does not add one). During
`POST /webhooks/{id}/validate` it means the request fails with a
non-sensitive error before `webhook.Send` is ever called.

## Docker

Mount the operator-managed keyring read-only. It is not present in the image:

```sh
docker run --rm \
  --mount type=bind,src=/secure/credential-keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly \
  -e GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
  ghcr.io/vesperis-group/gophishfr:<pinned-version>
```

The image runs as the non-root `app` user (UID/GID 1000). Ensure that user
can read the mounted keyring while retaining owner-only permissions. The same
read-only keyring and database mounts are required for offline actions:

```sh
docker run --rm \
  --mount type=bind,src=/secure/credential-keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly \
  --mount type=bind,src=/srv/gophishfr-data,dst=/data \
  -e GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
  -e DB_FILE_PATH=/data/gophish.db \
  ghcr.io/vesperis-group/gophishfr:<pinned-version> \
  ./docker/run.sh --migrate-webhook-secrets
```

## Explicit rollback

Do not downgrade the schema first.

1. Stop writers and take fresh database and keyring backups.
2. Restore every key still referenced by an envelope.
3. Run:

   ```sh
   ./gophishfr --config ./config.json --rollback-webhook-secrets
   ```

4. Verify that every secret row has `secret <> ''` and
   `secret_ciphertext = ''`, while no-secret rows keep both columns empty.
5. Run exactly one Goose Down for migration `20260903020000`; check the
   current version first and never loop over multiple downs.

The data rollback is transactional, idempotent, and never invokes schema
Down. It validates the 255-character plaintext limit, writes plaintext while
retaining ciphertext, reads both columns back byte-for-byte, and only then
clears and rechecks ciphertext. This detects silent non-strict MySQL
truncation before the only encrypted copy can be committed as cleared. The
Down migration refuses to remove the ciphertext column while any envelope
remains. After data rollback and that single Down, old binaries can read the
restored secret column exactly as before this change. If any step fails, keep
writers stopped and restore the matching database and keyring backups rather
than deleting ciphertext or forcing the schema change.

On SQLite, schema Down also preserves the `webhooks` autoincrement high-water
mark while rebuilding the table. Deleted webhook IDs therefore remain
retired and cannot be reused to retarget any historical reference by ID.

## Out of scope

- **Webhook URLs may contain userinfo or query-string tokens.** This goal
  does not audit, redact, or encrypt `webhooks.url`. Tracking, auditing, and
  deciding a redaction/encryption policy for URL-embedded secrets is a
  separate backlog item.
- Webhook headers do not exist in this schema (unlike SMTP custom headers)
  and are not introduced by this change.
- Broader webhook delivery/retry engine changes: this goal only changes how
  the HMAC secret is obtained, not the existing timeout, redirect, HTTP
  status, goroutine-per-endpoint delivery, or single-error-log behavior.
- IMAP, SMTP, `events.details`, user API key hashing, DSN/config logging, and
  temporary admin password logging are unrelated secret families and are not
  touched here.