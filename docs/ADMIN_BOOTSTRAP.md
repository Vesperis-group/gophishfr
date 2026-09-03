# Secure administrator bootstrap

GophishFR treats the initial administrator password as an operator input, never
as application output. It does not generate, print, log, or write a bootstrap
password.

## Preferred password file

Create a UTF-8 file readable by the service account and restrict it to that
account (`0400` or `0440`, or the equivalent secret-mount permissions). Point
the process at it:

```sh
GOPHISH_INITIAL_ADMIN_PASSWORD_FILE=/run/secrets/gophishfr_admin_password ./gophishfr
```

For containers, mount the file read-only at that path. The application follows
normal symlinks, including symlinks used by mounted Kubernetes-style secrets.
It does not change or copy the file.

The file contains an 8–72 byte UTF-8 password with no NUL byte. At most one
final LF or CRLF is removed; all other bytes, including spaces, are preserved.
The 72-byte limit is the explicit maximum accepted by bcrypt.

`GOPHISH_INITIAL_ADMIN_PASSWORD` remains available as a less-preferred
compatibility fallback and receives the same logical validation. When
`GOPHISH_INITIAL_ADMIN_PASSWORD_FILE` is configured, it is authoritative over
the environment password. An empty path, missing or unreadable file, directory,
invalid content, or oversized file is fatal and never falls back to the
environment value.

## Installation and upgrades

**Breaking security change:** a fresh installation without either explicit
source exits non-zero. No administrator row or generated password is left
behind. Prepare the source before starting the application.

The password is bcrypt-hashed before the initial `admin` row is inserted.
The plaintext is not persisted. The existing forced first-login password change
remains in effect.

Normal upgrades with an existing non-empty administrator hash need no bootstrap
source. The application does not read or re-hash FILE or ENV, even while the
first password change is still pending, so restarts leave the original bootstrap
password stable. A historical partial installation with an empty administrator
hash must provide FILE or ENV once; a failed recovery leaves that row unchanged.

The initial administrator API token setting remains independent of the password
source.

Application log-file permissions and their threat boundary are documented in
[Application log-file security](LOG_FILE_SECURITY.md). This does not change the
bootstrap password handling described above.
