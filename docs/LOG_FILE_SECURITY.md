# Application log-file security

When `logging.filename` is configured, GophishFR creates the application log
file with mode `0600` on Unix. Existing writable regular files are hardened on
open by removing execute and group/other permissions without adding owner
permissions. Unsafe symlinks, non-regular objects, path replacements, and files
that cannot be opened or secured cause configured file logging to fail.

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
file. Exact Unix mode guarantees do not represent Windows ACL guarantees.
