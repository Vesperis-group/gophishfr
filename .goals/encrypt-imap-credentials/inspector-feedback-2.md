# Inspector Feedback — Iteration 2

## Verdict: PASS

The Builder's commit 34e3316 correctly addresses the root cause of the Docker CI
Container build failure while preserving all security properties, operational
controls, and fail-closed runtime behavior established in iteration 1. The fix
is minimal, targeted to only the temporary test fixture, and introduces no
weakening of keyring validation or hiding of permission failures.

## Root Cause Analysis

The GitHub CI failure was caused by a Linux file ownership mismatch during
bind-mount:

1. **Symptom**: Docker container build failed when the IMAP credential test
   mounted the temporary test keyring (keyring.json).
2. **Root cause**: GitHub-hosted Linux runners own temporary files as UID 1001,
   while the image runs as the non-root app user (UID/GID 1000). Linux bind
   mounts preserve the source file's numeric ownership, so the 0400 keyring owned
   by UID 1001 was unreadable by the container process running as UID 1000.
3. **Not an error in the keyring logic**: The keyring validation in
   models/imap_credentials.go correctly rejected the unreadable file, failing
   closed as required. The test fixture preparation was the problem, not the
   production code.

## Commit Verification

- [x] Signature: git show --show-signature 34e3316 reports Good signature
  from vg-kvr-grp. Signature is cryptographically valid.
- [x] Files changed: Only two files modified:
  - docs/IMAP_CREDENTIAL_ENCRYPTION.md (documentation only)
  - scripts/test-container-imap-keyring.sh (test fixture setup only)
- [x] No production code modified: Zero changes to models/, controllers/,
  imap/, docker/run.sh, or main.go. All Go code remains unchanged.
- [x] Commit metadata: Builder model is Claude:Sonnet-4.6, trailers present,
  Conventional Commits format used (fix(imap): [B] make keyring mount portable).

## The Fix — Detailed Verification

### Test Script Changes

The fix aligns the temporary test fixture's UID/GID to the image's app user (1000:1000)
before the read-only mount and verifies both ownership and mode.

1. Executes a privileged chown inside the container (lines 43-51):
   - Runs as root (--user 0:0) to perform the chown.
   - Scope: Only the temporary test fixture, not the operator's production keyring.
   - Effect: Changes the bind-mounted file's numeric owner from runner UID 1001
     to image app UID 1000 on the host (before any application runs).

2. Verifies ownership and mode before the test (lines 53-61):
   - Extracts the image's app UID/GID from the image itself.
   - Verifies the host fixture matches both ownership and exact octal mode 400.
   - Fails immediately and explicitly if either check fails; does not hide failures.

3. Preserves the read-only mount for the actual test (original code, unchanged):
   - Bind mount still uses readonly flag.

### Documentation Changes

Added operational guidance for operators managing production keyrings:

- [x] Explains bind-mount ownership preservation: Instructed that bind mounts
  preserve the source file's numeric owner and permission bits.
- [x] Prescribes UID/GID alignment for operators: Instructs to sudo chown
  1000:1000 the source keyring before mounting.
- [x] Reinforces mode 0400: sudo chmod 0400 to retain owner-only access.
- [x] Emphasizes security properties: Do not make keyring writable by group or
  other. Keep the container mount readonly.

## Acceptance Criteria Preservation Check

All criteria from the goal remain met. The fix does not modify any behavior or
weaken any controls:

- [x] Foundation and AAD — Unchanged
- [x] Schema — Unchanged
- [x] External keyring and bootstrap — Unchanged
- [x] Explicit offline migration — Unchanged
- [x] Explicit data rollback — Unchanged
- [x] Runtime read and write behavior — Unchanged
- [x] API and browser behavior — Unchanged
- [x] Tests and security verification — Unchanged

## Security Properties Verified

### Mode 0400 Preserved
- [x] Test fixture created with chmod 0400
- [x] Verification checks that mode is exactly 400
- [x] Effect: Owner-readable only; fail-closed if mode is broader

### Read-Only Mount Semantics Preserved
- [x] Container mount still uses readonly flag
- [x] Application runs as non-root app user
- [x] Effect: Keyring is immutable in running container

### External Keyring Storage Preserved
- [x] Keyring is still a separate file, not embedded in image
- [x] Operator manages source file on host
- [x] Effect: Keyring lifecycle remains under operator control

### Fail-Closed Runtime Behavior Preserved
- [x] Production code in models/imap_credentials.go unchanged
- [x] Validation and permission checks unchanged
- [x] Test verifies fixture is readable before passing to application
- [x] Effect: Missing/unsafe keyrings fail closed

### No Production Keyring Validation Weakened
- [x] Zero changes to credential validation logic
- [x] Zero changes to decryption or error handling
- [x] Fix only addresses pre-test fixture ownership issue
- [x] Effect: All validation gates remain unchanged

### Permission Failures Not Hidden
- [x] Verification step fails immediately if mode/owner mismatch
- [x] Explicit error message to stderr
- [x] Non-zero exit code on failure
- [x] Effect: Misconfiguration is visible and fails loudly

## Test Execution Results

- [x] Build: go build → OK
- [x] IMAP credential tests: All PASS
- [x] Shell syntax: bash -n → OK
- [x] Signature: git show --show-signature → Good signature

## Quality Gate: PASS

- All unit and integration tests pass
- Build succeeds without errors
- Shell scripts syntactically correct
- No regressions in credential encryption or IMAP behavior
- Documentation is clear and operationally sound

## What This Iteration Fixed

Iteration 1 Result: All acceptance criteria met; credential encryption fully
implemented.

Iteration 2 Failure: GitHub CI Container build failed due to test fixture UID
mismatch (1001 from runner vs 1000 in image).

Iteration 2 Fix: Align temporary test fixture UID/GID to image app user before
read-only mount, verify both ownership and mode, enable container test.

Result: Container test can now proceed. All acceptance criteria remain satisfied.

## Issues Found

None. The fix is correct, minimal, secure, and complete.

## Iteration 2 Verdict

PASS — The Builder's fix correctly resolves the Docker CI failure while
preserving all established security properties. No production code modified. No
acceptance criteria violated or weakened. Ready for merge.

---

Inspector: Claude:Haiku-4.5 (independent verification)
Assisted-by: Copilot App <223556219+Copilot@users.noreply.github.com>