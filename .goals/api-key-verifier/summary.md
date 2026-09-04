# API-Key HMAC Verifier Goal Summary

## Outcome

The goal passed Goal inspection, ultimate cryptographic/application security
review, and final code review after seven Builder iterations. GophishFR now
stores only dedicated-keyring HMAC-SHA-256 verifiers for API tokens, migrates
legacy plaintext explicitly and irreversibly, authenticates with bounded indexed
multi-pepper lookup, and reveals new/reset tokens only once.

## Acceptance Criteria Achieved

- Independent `internal/apikey` protocol and versioned keyring use only standard
  library cryptography and never reuse `internal/credentials` or its keys.
- Verifier domain framing is stable, output is exactly 32 bytes, and keyring
  loading is bounded, canonical, permission-checked, immutable, and load-once.
- SQLite/MySQL schema supports nullable legacy plaintext, raw verifier bytes,
  key ID, composite uniqueness, guarded Down, and preserved IDs/constraints.
- PostgreSQL is accurately unsupported because no application migration tree
  exists.
- `--migrate-api-keys` is explicit, offline, global-transactional, preflighted,
  readback-verified, idempotent, and clears exact legacy token bytes only after
  durable verifier storage.
- Migration is explicitly irreversible: rollback requires the tested
  pre-migration DB backup or key reissuance; no fake reverse command exists.
- Runtime performs no plaintext lookup/fallback and accepts only verifier-only,
  unlocked rows.
- One indexed logical lookup covers active/old peppers, fails on ambiguity, and
  lazily upgrades old verifiers with CAS and reset/lock-safe final linearization.
- Ordinary User updates cannot alter legacy/verifier state; dedicated guarded
  operations exclusively issue/reset/migrate/rekey.
- Generated token format remains 32 random bytes / 64 lowercase hex; existing
  legacy client token values remain unchanged after migration.
- Initial admin provided/generated token paths store verifier-only state with
  no logs or partial user on failure.
- GET/list/PUT/settings expose no token, verifier, key ID, mask, or presence
  oracle.
- User creation and reset preserve legacy JSON field locations while revealing
  the plaintext token only in the immediate successful response.
- Settings reveal is transient, serialized against duplicate submissions,
  clears DOM/state on close, and cannot display an out-of-order revoked token.
- Dedicated credential and API-verifier keyrings remain separated in Docker,
  images, logs, code, and configuration.
- PR #62 Bearer/raw/query/form/session selection, CSRF, RBAC, view-only,
  forced-reset, campaign mutation, and no-fallback contracts remain intact.
- SQLite, real MySQL, browser, Docker, race, fuzz, backup restore, pepper
  rotation, Unicode migration, reproducibility, and scanner suites pass.
- Dependencies and lockfiles remain unchanged.

## Corrective Iterations

1. Implemented complete verifier/keyring/schema/migration/auth/reveal lifecycle.
2. Required strict verifier-only runtime state (`api_key IS NULL`).
3. Isolated verifier columns from ordinary `PutUser` updates.
4. Applied account locks to API-key lookup and lazy rekey.
5. Linearized final auth against exact verifier state and restored legacy JSON
   response shapes.
6. Removed an incompatible 255-byte migration limit and preserved exact legacy
   Unicode/multibyte bytes.
7. Serialized first-party reset submissions to prevent stale reveal overwrite.
8. Aligned only the synthetic verifier-container database permissions across
   differing runner/image UIDs so the real CI lifecycle can construct legacy
   fixtures without changing production ownership or keyring permissions.

Each correction received independent tests and Inspector verification. The
ultimate security and code reviews returned PASS.

## Remaining Deliberate Backlogs

- Deprecate/remove query/form API-key transports because client tokens can enter
  access logs.
- Add API-key rate limiting for weak legacy tokens.
- Address `events.details` as a separate sensitive-data project.
