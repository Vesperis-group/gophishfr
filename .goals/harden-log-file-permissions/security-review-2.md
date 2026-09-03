# Final Review Feedback — Iteration 2

## Verdict: FAIL

After the Inspector correctly retracted its false `os.OpenRoot` portability
finding, independent security and code reviews found two real platform issues.

## Finding 1 — Unix ownership is not validated

**Severity:** High
**Affected path:** `logger/logger.go`

An existing regular file can be writable by the service while owned by another
local user. Descriptor `Chmod(0600)` removes group/other access but preserves
ownership, leaving that other owner able to read, alter, truncate, or replace
application logs. This violates the requirement to accept only a file owned by
the service identity.

**Required correction:**

- On Unix, inspect the opened descriptor's ownership and require its UID to
  equal the effective service UID before accepting or chmodding an existing
  file.
- Fail closed for foreign-owned files without changing their mode/content.
- Do not chown or escalate privileges.
- Add deterministic Unix tests for service-owned success and foreign-owned
  rejection where privileges permit; use an isolated subprocess/container when
  root is required.
- Preserve descriptor-based chmod and identity checks.

## Finding 2 — Exact POSIX mode verification breaks Windows runtime

**Severity:** Medium
**Affected path:** `logger/logger.go`

Go on Windows reports writable regular files with mode `0666`. `File.Chmod(0600)`
controls the read-only attribute but does not produce POSIX `0600`; the exact
post-chmod comparison therefore rejects every configured writable log and
prevents startup. Cross-compilation alone did not execute this path.

**Required correction:**

- Isolate permission/ownership hardening and verification behind
  platform-specific helpers.
- Unix helper enforces service ownership, removes excess bits, and verifies the
  resulting mode.
- Windows helper retains regular-file/path-identity validation and writable
  behavior without asserting Unix permission bits or claiming ACL hardening.
- Preserve cross-platform `Setup` behavior and compile both platform paths.
- Add Windows-targeted helper tests or execute a Windows-compatible runtime test
  when available; at minimum compile all affected tests and structurally verify
  no exact POSIX-mode assertion is reachable on Windows.
- Update documentation to distinguish Unix `0600` guarantees from Windows.

Both corrections must preserve append, stderr, format/content, symlink/
non-regular rejection, #59/#60/keyring regressions, dependencies, and scope.
