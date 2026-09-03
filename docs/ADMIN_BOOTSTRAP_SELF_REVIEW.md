# Secure administrator bootstrap self-review

## Security properties

- Automatic password generation and plaintext logging are removed.
- FILE is authoritative over ENV; every configured FILE failure is fatal.
- One bounded loader accepts normal and symlink-backed files, strips at most one
  LF/CRLF, and rejects empty, invalid UTF-8, NUL, policy-invalid, and over-72-byte
  logical values.
- FILE and ENV share one validation path and bcrypt receives only validated
  input.
- Fresh setup computes the hash and API key before its single administrator
  insert. Failures before that insert leave zero user rows.
- Only an existing empty hash enters historical recovery. A non-empty hash,
  including one still requiring its first change, does not read either source.
- Recovery updates only the hash and forced-change marker after successful
  resolution and hashing.
- The forced first change and independent initial API-token behavior are
  preserved.

## Exposure review

No code logs, prints, fingerprints, encodes, or writes the input secret.
Focused tests inspect logger output and database state. Ansible log scraping was
deleted; the secret copy task uses `no_log` and mode `0400`. Documentation uses
only a read-only mounted input file.

The global logger, log-file mode, Docker configuration dump, API/session
authentication, API-key storage, credential encryption, and global password
change policy were not modified.

## Test mapping

`models/bootstrap_test.go` covers source priority, fatal invalid FILE with valid
ENV, missing source, FILE parsing and bounds, ENV bounds, symlinks, fresh
atomicity, bcrypt-only persistence, logging absence, API-token independence,
restart hash stability, safe historical recovery, and one-time hash marking.
Existing middleware and controller tests retain forced-reset behavior.

No dependency or lockfile changes are required.

## Ansible precedence correction

The empty non-secret value lives in role `defaults/`, Ansible's lowest
precedence tier, rather than role `vars/`. Inventory, play, and Vault input can
therefore reach both the mode `0400`, service-user-owned input-file task and the
systemd environment. An unset default skips both for an already initialized
installation. Both tasks that evaluate the secret retain `no_log: true`.
`scripts/test-ansible-bootstrap.py` enforces this wiring without displaying its
synthetic override values.
