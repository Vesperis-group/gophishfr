# Application log-file security

When `logging.filename` is configured on Unix, GophishFR creates the application
log file with mode `0600`. Before an existing file is accepted or changed, its
descriptor ownership must match the service's effective user ID. Service-owned,
writable regular files are hardened on open by removing execute and group/other
permissions without adding owner permissions. Foreign-owned files fail without
being chmodded or modified; GophishFR never changes ownership or escalates
privileges. Unsafe symlinks, non-regular objects, path replacements, and files
that cannot be opened or secured also cause configured file logging to fail.

Hardening uses the verified open file descriptor rather than chmodding the path.
Portable standard-library checks before and after opening reject ordinary unsafe
objects and verify that the path still identifies the descriptor. As with any
portable pathname operation, a hostile local user who can replace path
components can still race individual checks; descriptor-based hardening ensures
such a race cannot redirect the permission change to a different file.

This protection applies only to the persistent application log file. It does
not protect against root or host compromise and does not change log content,
stderr output, journald, Docker logging backends, paths, formatting, or
rotation. The default container configuration leaves `logging.filename` empty
and therefore continues to use stderr without a persistent application log
file.

On Windows, setup still requires a writable regular file and performs the same
symlink and descriptor/path identity validation. It does not apply or verify
Unix `0600` semantics: Go's mode bits control only the read-only attribute there,
while access is governed by Windows ACLs. GophishFR does not modify those ACLs.
