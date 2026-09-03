# Application Log-File Permission Hardening Summary

## Outcome

The goal passed independent inspection, security review, and final code review
after the initial implementation and one platform-security correction. New Unix
application logs are `0600`; existing service-owned permissive regular files are
privately hardened through their verified open descriptor; symlink, non-regular,
foreign-owned, and unsecurable paths fail closed. Windows preserves native ACL
semantics without false POSIX mode assertions.

## Acceptance Criteria Achieved

- New files use append/create mode `0600` and remain `0600` under umask `000`.
- Existing service-owned `0644`, `0666`, `0777`, `0640`, and `0604` files end at
  `0600` without truncation.
- More restrictive files are never widened.
- Existing content remains and new records append.
- Pre-open type checks and post-open descriptor/path identity checks reject
  symlinks, replacement, directories, FIFOs, devices, sockets, and other unsafe
  objects.
- Unix hardening verifies ownership against the effective UID before and after
  descriptor chmod; foreign-owned files remain unchanged.
- Windows uses a platform helper that validates normal writable logs without
  claiming POSIX mode or ACL hardening.
- Stderr, Logrus formatting, levels, messages, paths, and rotation behavior are
  unchanged.
- Unix umask/ownership/mode tests, native Windows runtime tests, Windows
  cross-build, logger race, and container/security regressions pass.
- PR #59 bootstrap, PR #60 config-no-log, and credential/keyring anti-leak
  contracts remain green.
- Dependencies, frontend, Docker entrypoint, Ansible, bootstrap, API, crypto,
  and config are unchanged.

## Iteration History

1. Builder implemented portable descriptor/path validation, Unix `0600`
   hardening, focused tests, and documentation in signed commit
   `bf68359e23061143a8eaa9f622ffb55d0142cea4`.
2. Inspector commit `467e6cbc01349c2b55bd95a9a19688711521bf58`
   incorrectly reported `os.OpenRoot` unavailable on Windows.
3. After a real pinned-Go Windows PE build proved portability, Inspector commit
   `273b284e3cd34b896be97cae3b4ebb915910753b` retracted that finding.
4. Independent reviews then found two real issues: foreign-owned Unix files
   remained controlled by their owner, and exact POSIX mode verification blocked
   configured Windows logs at runtime.
5. Builder commit `1937231334fc373b814aaddcb80980aeaf1b469b`
   added Unix effective-UID enforcement and platform-specific Windows behavior.
6. Inspector commit `035e6c03fd90322924a19a68c5dd298f7bafb821`
   returned PASS; final security and code reviews also returned PASS.

## Review Resolution

The final design keeps common path/type/inode safety cross-platform while
separating access-control enforcement: Unix requires service ownership and
removes excess mode bits; Windows relies on native ACLs and validates writable
regular-file behavior without impossible `0600` equality.

## Recommendations

- Proceed next with API-key session decoupling as a separate change.
- Keep API-key verifier and `events.details` work independently scoped.
