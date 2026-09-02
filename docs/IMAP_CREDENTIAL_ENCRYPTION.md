# IMAP credential encryption operations

GophishFR stores IMAP passwords as versioned AES-256-GCM envelopes produced by
[`internal/credentials`](../internal/credentials). The envelope is authenticated
for the owning user, so moving it to another user's row makes it unusable.

This feature is intentionally limited to IMAP passwords. SMTP credentials,
webhook data, captured event details, API keys, and other secrets remain
separate security backlog items and are not changed by this migration.

## Safety requirements

Before migrating or rolling back:

1. Stop every GophishFR process and every other writer to the database.
2. Take and test a restorable database backup.
3. Back up the keyring separately from the database.
4. Confirm the database backend is SQLite or MySQL.

The actions are offline-only and use one transaction for the small `imap`
table. A row failure aborts the whole action.

> **A database backup alone is insufficient after encryption.** Losing every
> copy of a key makes ciphertext using it unrecoverable. Rolling back to an old
> binary requires explicit data decryption before the schema is downgraded.

PostgreSQL is explicitly unsupported for this credential migration because the
repository has no PostgreSQL schema or migration tree. The CLI rejects it before
credential data is mutated.

## Keyring

Set `GOPHISHFR_CREDENTIAL_KEYRING_FILE` to an external JSON file:

```json
{
  "version": 1,
  "active_key_id": "imap-2026-09",
  "keys": [
    {
      "id": "imap-2026-09",
      "key": "<base64-encoded 32 random bytes>"
    }
  ]
}
```

Generate the key value on an operator-controlled machine with
`openssl rand -base64 32`. Never place the resulting document in this
repository, the database, a container image, or logs.

The resolved file must not be group- or world-writable. Restrict native files
to the service account where practical, for example:

```sh
chmod 0400 /etc/gophishfr/credential-keyring.json
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
```

The keyring is loaded once at process bootstrap. No key is generated
automatically. A configured but missing, malformed, or unsafe file stops the
process with a non-secret error. Normal startup without this environment
variable remains supported, but encrypted IMAP credentials cannot be created,
updated, or used until a valid keyring is supplied.

## Upgrade and offline migration

The versioned Goose migration
`20260903000000_encrypt_imap_credentials.sql`:

- blocks duplicate `imap.user_id` rows without selecting or deleting one;
- adds `password_ciphertext VARCHAR(2048)`;
- enforces one IMAP row per user; and
- retains the legacy `password` column only for offline migration and rollback.

With the application stopped and both backups complete, run:

```sh
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
./gophishfr --config ./config.json --migrate-imap-credentials
```

Startup applies the pending schema migration, then the explicit action:

1. validates the backend and schema;
2. rejects duplicate users and invalid column states;
3. encrypts only legacy rows, even if plaintext starts with
   `gophishfr-cred:`;
4. decrypts each new envelope in memory and compares it with the input;
5. clears the legacy column; and
6. commits only after every row verifies.

A mixed database containing valid encrypted rows and legacy rows is supported.
Re-running a successful migration reports zero updated rows and leaves every
existing envelope byte-identical.

Verify only state and counts; do not print passwords or full envelopes:

```sql
SELECT
  SUM(CASE WHEN password = '' AND password_ciphertext <> '' THEN 1 ELSE 0 END)
    AS encrypted_rows,
  SUM(CASE WHEN password <> '' AND password_ciphertext = '' THEN 1 ELSE 0 END)
    AS legacy_rows,
  SUM(CASE WHEN (password = '') = (password_ciphertext = '') THEN 1 ELSE 0 END)
    AS invalid_rows
FROM imap;
```

`legacy_rows` and `invalid_rows` must both be zero before normal startup.

## Normal startup and rotation

Keep the environment variable set for every process that creates, updates, or
uses IMAP settings:

```sh
export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
./gophishfr --config ./config.json
```

New passwords use the active key. To rotate, add a new random key entry and
change `active_key_id`, while retaining old entries until no envelope refers to
them. New or replaced passwords then use the new active key; preserved
passwords remain byte-identical. Bulk re-encryption and KMS/Vault integration
are intentionally out of scope.

## Docker

Mount the operator-managed keyring read-only. It is not present in the image:

```sh
docker run --rm \
  --mount type=bind,src=/secure/credential-keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly \
  -e GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
  ghcr.io/vesperis-group/gophishfr:<pinned-version>
```

The same mount, environment variable, and database mount are required for the
offline actions. Because an argument replaces the image's default Docker
command, invoke the entrypoint script explicitly:

```sh
docker run --rm \
  --mount type=bind,src=/secure/credential-keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly \
  --mount type=bind,src=/srv/gophishfr-data,dst=/data \
  -e GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
  -e DB_FILE_PATH=/data/gophish.db \
  ghcr.io/vesperis-group/gophishfr:<pinned-version> \
  ./docker/run.sh --migrate-imap-credentials
```

Use `--rollback-imap-credentials` in place of
`--migrate-imap-credentials` for explicit data rollback. A fresh container
with no encrypted IMAP rows may run without the keyring mount; any IMAP secret
write or enabled IMAP login fails closed.

## Explicit rollback

Do not downgrade the schema first.

1. Stop all writers and take fresh database and keyring backups.
2. Use the same keyring, including every retired key still referenced.
3. Run the explicit data rollback:

   ```sh
   export GOPHISHFR_CREDENTIAL_KEYRING_FILE=/etc/gophishfr/credential-keyring.json
   ./gophishfr --config ./config.json --rollback-imap-credentials
   ```

4. Verify `password <> '' AND password_ciphertext = ''` for every configured
   row. Repeating the data rollback is a safe no-op.
5. Run **one** Goose Down for version `20260903000000` only. Check the current
   version first; never loop over multiple downs:

   ```sh
   goose -dir db/db_sqlite3/migrations sqlite3 /path/to/gophish.db status
   goose -dir db/db_sqlite3/migrations sqlite3 /path/to/gophish.db down
   ```

   For MySQL, use the same pinned Goose v3 release and the MySQL migration
   directory/DSN:

   ```sh
   goose -dir db/db_mysql/migrations mysql '<operator-supplied-dsn>' status
   goose -dir db/db_mysql/migrations mysql '<operator-supplied-dsn>' down
   ```

The Down migration refuses to remove the column while any ciphertext remains.
After that single Down succeeds, an old binary can read the restored legacy
column without credential loss.

If migration or rollback cannot complete, keep the application stopped and
investigate the reported user ID and non-secret error. If a key is unavailable,
restore the matching database and separately stored keyring backups. Never
delete ciphertext, choose between duplicate rows, or force the schema Down.
