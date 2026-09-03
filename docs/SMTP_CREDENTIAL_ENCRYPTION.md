# SMTP credential encryption operations

GophishFR stores sending-profile passwords as versioned AES-256-GCM envelopes
produced by [`internal/credentials`](../internal/credentials). Each envelope is
authenticated for both the owning user and immutable sending-profile ID, so
copying it to another user or profile makes it unusable.

This protects a database, dump, snapshot, or backup obtained without the
separately stored keyring. It does not protect a compromised running process or
a host that can read both the database and keyring. SMTP custom headers and
other secret types are not changed by this feature.

## Compatibility and API behavior

SMTP authentication remains optional. A profile with an empty username and
password continues to send without SMTP AUTH and stores neither plaintext nor
ciphertext. The pre-existing allowance for a non-empty username with an empty
password also remains: the SMTP library may attempt AUTH with an empty password.

This is an intentional security-breaking API change:

- SMTP passwords are write-only and are never returned by list, detail, create,
  update, campaign, or test-email responses.
- Ciphertext, key IDs, envelope markers, masks, and `has_password` indicators
  are never returned.
- An empty or omitted password on update preserves an existing encrypted
  password byte-for-byte. A non-empty value replaces it.
- `password: null` is rejected. There is no password-clearing action in this
  release.
- Editing and copying a profile always opens an empty password field.

## Safety requirements

Before migrating or rolling back:

1. Stop every GophishFR process and every other database writer.
2. Take and test a restorable database backup.
3. Back up the keyring separately from the database.
4. Confirm the database backend is SQLite or MySQL.

The actions are offline-only and transform the complete `smtp` table in one
transaction. A row failure aborts the whole action.

> **A database backup alone is insufficient after encryption.** Losing every
> copy of a key makes ciphertext using it unrecoverable. Rolling back to an old
> binary requires explicit data decryption before the schema is downgraded.

PostgreSQL is explicitly unsupported for this credential migration because the
repository has no PostgreSQL application schema or migration tree. The CLI
rejects it before credential data is mutated.

## Keyring

SMTP and IMAP use the same application keyring, loaded once at startup from
`GOPHISHFR_CREDENTIAL_KEYRING_FILE`:

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
`openssl rand -base64 32`. Never place the resulting keyring in the repository,
database, container image, or logs. The resolved file must not be group- or
world-writable:

```sh
chmod 0400 /etc/gophishfr/credential-keyring.json
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
```

No key is generated automatically. Installations containing only no-secret
SMTP profiles can run without a keyring; creating, replacing, migrating, or
using an encrypted password fails closed until a valid keyring is supplied.

New passwords use the active key. Keep retired keys in the keyring while any
envelope still references them. Bulk re-encryption and KMS/Vault integration
remain future work.

## Schema upgrade and offline migration

The paired SQLite/MySQL Goose migration
`20260903010000_encrypt_smtp_credentials.sql` retains the legacy `password`
column temporarily and adds `password_ciphertext VARCHAR(2048)`. It does not
add a uniqueness constraint: multiple sending profiles per user remain valid.

With writers stopped and both backups complete, run:

```sh
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
./gophishfr --config ./config.json --migrate-smtp-credentials
```

The action validates the backend, schema, non-zero profile and owner IDs,
unique profile IDs, every existing row state, and every pre-existing envelope
before writing. For each legacy plaintext row it:

1. encrypts with AAD `smtp-password / smtp / password / user_id / smtp.id`;
2. immediately decrypts and compares the value in memory;
3. writes the envelope and clears the legacy column; and
4. commits only after every row succeeds.

Already-encrypted rows are authenticated and left byte-identical. Rows with
both secret columns empty remain unchanged. Rows with both columns populated,
invalid identities, malformed envelopes, missing keys, or authentication
failures block the entire migration. Re-running a successful migration is a
count-only no-op.

Verify state and counts without printing passwords or full envelopes:

```sql
SELECT
  SUM(CASE WHEN COALESCE(password, '') = '' AND password_ciphertext <> '' THEN 1 ELSE 0 END)
    AS encrypted_rows,
  SUM(CASE WHEN COALESCE(password, '') <> '' AND password_ciphertext = '' THEN 1 ELSE 0 END)
    AS legacy_rows,
  SUM(CASE WHEN COALESCE(password, '') <> '' AND password_ciphertext <> '' THEN 1 ELSE 0 END)
    AS invalid_rows,
  SUM(CASE WHEN COALESCE(password, '') = '' AND password_ciphertext = '' THEN 1 ELSE 0 END)
    AS no_secret_rows
FROM smtp;
```

`legacy_rows` and `invalid_rows` must be zero before normal startup.

## Normal startup and send boundary

Keep the same environment variable set for every process that creates,
updates, tests, or sends with encrypted profiles:

```sh
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
./gophishfr --config ./config.json
```

The legacy column is never a runtime fallback. GophishFR decrypts only while
constructing the SMTP dialer for a campaign or test email. A malformed,
tampered, moved, wrong-key, or unknown-key envelope fails before any SMTP
connection is attempted.

For an existing authorized profile, test email uses its stored envelope when
the browser's password field is empty. A newly entered test password is used
only in memory and is neither persisted nor echoed.

## Docker

Mount the operator-managed keyring read-only. It is not present in the image:

```sh
docker run --rm \
  --mount type=bind,src=/secure/credential-keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly \
  -e GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
  ghcr.io/vesperis-group/gophishfr:<pinned-version>
```

The image runs as the non-root `app` user (UID/GID 1000). Ensure that user can
read the mounted keyring while retaining owner-only permissions. The same
read-only keyring and database mounts are required for offline actions:

```sh
docker run --rm \
  --mount type=bind,src=/secure/credential-keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly \
  --mount type=bind,src=/srv/gophishfr-data,dst=/data \
  -e GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
  -e DB_FILE_PATH=/data/gophish.db \
  ghcr.io/vesperis-group/gophishfr:<pinned-version> \
  ./docker/run.sh --migrate-smtp-credentials
```

## Explicit rollback

Do not downgrade the schema first.

1. Stop writers and take fresh database and keyring backups.
2. Restore every key still referenced by an envelope.
3. Run:

   ```sh
   ./gophishfr --config ./config.json --rollback-smtp-credentials
   ```

4. Verify that every secret row has `password <> ''` and
   `password_ciphertext = ''`, while no-secret rows keep both columns empty.
5. Run exactly one Goose Down for migration `20260903010000`; check the current
   version first and never loop over multiple downs.

The data rollback is transactional, idempotent, and never invokes schema Down.
The Down migration refuses to remove the ciphertext column while any envelope
remains. After data rollback and that single Down, old binaries can read the
restored password column. If any step fails, keep writers stopped and restore
the matching database and keyring backups rather than deleting ciphertext or
forcing the schema change.
