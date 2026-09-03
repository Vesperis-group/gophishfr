# SMTP Credential Encryption Goal Summary

## Outcome

The goal passed independent inspection, code review, security-specialist review,
and CI browser-race verification after four Builder iterations.
SMTP sending-profile passwords are now write-only at the HTTP/browser boundary,
encrypted at rest with the existing AES-256-GCM credential foundation, bound to
the authenticated owner and immutable profile ID, and decrypted only at the SMTP
dialer boundary.

## Acceptance Criteria

- Existing `internal/credentials` crypto and the shared external keyring are
  reused without dependency or envelope-format changes.
- SQLite and MySQL retain the legacy password column for explicit rollback and
  add a guarded ciphertext column; PostgreSQL migration is rejected.
- Create obtains the generated profile ID inside one transaction before
  encryption, while profile/header mutations are atomic and owner-scoped.
- Empty or absent updates preserve an existing ciphertext byte-for-byte only
  when interface, host/port, username, and TLS verification policy are
  unchanged. Routing changes require a non-empty replacement, while
  unauthenticated profiles remain freely editable with both secret columns
  empty.
- Stored-password test email binds the owner-authorized stored profile ID,
  interface, host/port, username, TLS policy, and ciphertext while retaining
  safe submitted From/header fields. A stored no-secret profile or explicit
  non-empty password uses submitted connection context only in memory.
- Plaintext is limited to 255 valid UTF-8 bytes and v1 ciphertext to its derived
  464-byte maximum within the 2048-character column. Create, rotate, migration,
  and rollback re-read and compare secret columns byte-for-byte in the same
  transaction.
- Migration stages verified ciphertext before clearing plaintext; rollback
  stages and verifies full plaintext before clearing the only ciphertext copy.
- SMTP, campaign, and test-email API responses expose neither plaintext nor
  ciphertext metadata, and the frontend never prefills stored passwords.
- Campaign and test-email sends decrypt only at the use boundary and fail before
  network activity on missing, malformed, tampered, unknown-key, or wrong-AAD
  ciphertext.
- Explicit offline migration and rollback are transactional, idempotent,
  fail-closed, and documented with backup and stopped-writer requirements.
- SQLite/MySQL lifecycle, API, browser, campaign/test-email, Docker/keyring,
  credential-foundation, IMAP regression, fuzz, and scanner coverage were added
  or executed without dependency changes.

## Iteration History

1. Builder implemented the complete schema, model, API, frontend, runtime,
   migration/rollback, Docker, test, CI, and documentation changes in commit
   `2710482ca8f7a98d645f070d46069f64c838a110`.
2. Inspector independently checked every immutable criterion and returned PASS
   with no product-code findings in commit
   `15a73468cbd213dbecd0ef192ca58b1c5cb3586a`.
3. A security specialist identified credential-context redirection and
   non-strict MySQL truncation risks.
4. Builder iteration 2 bound preserved credentials to their stored context,
   enforced byte-safe limits and staged verification, and added cross-layer
   regression coverage in commit
   `63c00cc31c649821eaae2b9021ba5162c2c76def`.
5. The Goal Inspector returned PASS again in commit
   `fddb6726cba274e47bcb853f3af79d07119f40f6`, and the independent security
   specialist re-review also returned PASS.
6. A final code review found SQLite sequence regression on schema Down, loss of
   safe unsaved test-email fields, and false MySQL failure for matched no-op
   updates. These are assigned to Builder iteration 3.
7. Builder iteration 3 preserved the SQLite SMTP ID high-water mark, composed
   test-email profiles from stored protected and submitted safe fields, and
   verified owner-scoped MySQL no-op updates inside the transaction.
8. The Goal Inspector returned PASS in commit
   `7b8d5b4e3874db9beab1ac5ae0f56a918c2d13b5`; final code and security
   re-reviews both returned PASS with no remaining high-confidence finding.
9. PR #55 passed every CI job except browser smoke, which failed twice because a
   settings retry contract observed a late success flash from its preceding
   settings scenario. This is assigned to Builder iteration 4.
10. Builder iteration 4 identified that the successful-controls settings helper
    returned after request interception but before its asynchronous success
    callback. It now verifies that exact flash before the failure/retry helper
    resets event state. Two consecutive full browser suites passed, and the Goal
    Inspector independently returned PASS in commit
    `a20a66c634f7bf0fa029aa5ef488e81707b6f249`.

## Inspector Findings

All review findings are corrected. PUT rejects preserved-password changes to
host, username, TLS policy, or equivalent routing fields; blank/absent
stored-password test-email requests use stored authentication routing but keep
safe unsaved sender/header fields. No-secret profiles retain submitted context,
and explicit replacement passwords still permit context changes. SQLite Down
retains deleted SMTP IDs in its sequence high-water mark. MySQL matched no-op
updates succeed only after locked, owner-scoped verification, while ownership
and context conflicts remain fail-closed. UTF-8 byte bounds, conservative
envelope sizing, staged migration/rollback, and transactional readback checks
continue to prevent silent storage truncation from committing.

## Iteration 2 validation

- `./scripts/verify.sh`: PASS with Go 1.25.13 and Node 24.19.0.
- Full unit, race, vet, build, format, module verification, and lint gates: PASS.
- Browser suite: PASS.
- Real MySQL 8.4 driver, IMAP, SMTP lifecycle, and SMTP non-strict storage-bound
  tests: PASS.
- Docker build and credential lifecycle, including redirect rejection and
  stored-context test-email delivery: PASS.
- Both credential fuzz targets completed bounded 10-second runs.
- `govulncheck`, Gitleaks, actionlint, zizmor, Yarn audit, and Retire.js: PASS.
- Gosec reports the same 14 documented pre-existing findings and no finding in
  the new credential-bound/storage-verification code.
- Dependency manifests, lockfiles, and generated frontend assets are unchanged.

## Iteration 3 validation

- `./scripts/verify.sh`: PASS with format, lint, module, vet, build, unit, race,
  frontend, action-pin, and pinned-toolchain vulnerability gates.
- Targeted SQLite/model/API tests and the clean real MySQL 8.4 driver, IMAP,
  SMTP lifecycle, and non-strict changed-row/storage-bound suite: PASS.
- Browser suite and Docker build/credential lifecycle: PASS, including safe
  unsaved test-email sender/header delivery.
- Both credential fuzz targets completed bounded 10-second runs.
- `govulncheck`, Gitleaks, actionlint, zizmor, Yarn audit, and Retire.js: PASS.
- Gosec reports the same 14 documented pre-existing findings and no new finding.
- Two clean frontend builds produced the same sending-profile asset hash;
  dependency manifests and lockfiles remain unchanged.
- Independent final code and security reviews reported no findings.

## Iteration 4 validation

- The unmodified full browser suite was reproduced in CI-equivalent order; the
  local worker passed while both CI attempts consistently exposed the slower
  callback race.
- The full 12-test browser suite passed twice consecutively after the fix,
  preserving SMTP success/error and credential non-exposure assertions.
- `./scripts/verify.sh`: PASS with format, lint, module, vet, build, unit, race,
  frontend, action-pin, and pinned-toolchain vulnerability gates.
- Gitleaks, actionlint, and zizmor: PASS with no new findings.
- No product code, generated asset, dependency manifest, lockfile, timeout, or
  existing assertion was changed or weakened.

## Recommendations

- Preserve signed iteration commits rather than squashing them.
- Require independent cryptographic/security review and fully green CI before
  merge.
- Keep bulk SMTP key rotation, PostgreSQL schema support, clear-password UX, and
  all non-SMTP secret types in separate future changes.
