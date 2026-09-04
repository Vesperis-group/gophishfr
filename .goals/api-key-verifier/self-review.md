# API-key verifier self-review

## Security and protocol

- `internal/apikey` is independent of `internal/credentials` and uses only
  stdlib HMAC-SHA-256 over length-framed
  `gophishfr-api-key-verifier:v1` plus exact token bytes.
- Verifiers are raw 32-byte values; comparisons use `hmac.Equal`. Keyring
  parsing is versioned, bounded, canonical, permission-checked, duplicate-aware,
  immutable after load, and has no default, generated, database, or credential
  keyring fallback.
- Runtime authentication performs one bounded indexed lookup across retained
  peppers, rejects zero/ambiguous/malformed matches, and uses the legacy column
  only for an `api_key IS NULL` state predicate. It never selects or compares
  plaintext.

## Data lifecycle

- SQLite and MySQL migrations make legacy plaintext nullable and add internal
  verifier/key-ID fields plus their composite unique index. SQLite tests verify
  the complete current users shape, constraints, IDs, and sequence high-water
  mark. Guarded Down fails before destructive mutation for verifier state.
- `--migrate-api-keys` preflights all rows and collisions, then uses one
  transaction with guarded row counts and exact readback before clearing
  plaintext. Re-runs are no-ops. There is deliberately no reverse transform:
  rollback means restoring the tested pre-migration database backup or reissue.
- Bootstrap, create, and reset persist verifier-only state atomically. Generated
  tokens retain 32 random bytes / 64 lowercase hex. Missing keyrings leave
  existing session administration usable while API-key operations fail closed.
- Old-pepper matches use an exact-pair CAS. A zero-row CAS rereads and verifies
  current state, so concurrent reset wins and stale tokens remain invalid.

## Exposure and compatibility

- Explicit DTOs keep keys, verifiers, IDs, masks, and presence signals out of
  GET/PUT/settings. Create and reset return a token only in their immediate
  successful response.
- Settings and user creation use transient accessible reveal UI. Close removes
  the token from DOM and retained application state; reload cannot recover it.
- PR #62 explicit credential extraction/conflicts, no fallback, session CSRF,
  forced-password-change, RBAC/view-only, campaign completion, and legacy
  Bearer/raw/query/form transports remain covered by Go, browser, and container
  regression suites.

## Validation evidence

- `./scripts/verify.sh`: pass (gofmt, golangci-lint, module verification, vet,
  build, all Go tests, race tests, frontend reproducibility, govulncheck).
- Browser Playwright suite: 13/13 pass, including settings and user-create
  reveal lifecycle.
- Real MySQL 8.4.6 `TestMySQL*` suite: pass; SQLite migration/lifecycle tests:
  pass.
- Container suites: API verifier lifecycle, API/session auth, config no-log,
  credential keyring, and secure bootstrap pass. Test resources were removed.
- Verifier and keyring fuzzers: pass for 10 seconds each.
- Security tools: govulncheck (binary built with patched Go 1.25.13) found no
  called vulnerabilities; Yarn audit found zero vulnerabilities; Gitleaks found
  no source-tree leak after excluding goal-review prose (one generic-key false
  positive there); actionlint passed. Zizmor reported two existing low-severity
  release-workflow findings. Gosec reported only the 12 pre-existing findings
  outside this change. Retire.js passed; GitHub dependency review was
  unavailable locally.
- `go.mod`, `go.sum`, `package.json`, and `yarn.lock` are byte-unchanged from
  the initial SHA. Repeated canonical frontend builds produced identical hashes.

## Final plaintext classification

Production `api_key` occurrences are limited to request transport extraction,
the immediate reveal DTO field, setting the legacy column to NULL, the
lazy-rekey NULL guard, and the offline migration implementation. Plaintext
SELECT/compare operations exist only in `models/api_key_migration.go`.
Schema occurrences provide legacy compatibility. Remaining occurrences are
tests, documentation, and local lifecycle scripts. Production runtime
plaintext reads and authentication fallbacks are zero.

## Iteration 2 runtime-state correction

- The initial indexed verifier query, verifier-write readback, lazy-rekey CAS,
  and its zero-row conflict reread all require `api_key IS NULL`. LEGACY, BOTH,
  and all-absent rows therefore fail closed without selecting or comparing
  plaintext; BOTH remains migrator-only.
- SQLite and real MySQL tests reject matching active-key and old-key BOTH rows,
  accept the same active row only after verified migration clearing, and prove
  that a concurrent transition to BOTH between lookup and lazy CAS cannot
  authenticate or rekey. Normal migrated authentication, lazy upgrade, and
  immediate reset invalidation remain covered.
- The complete Go/race, real-MySQL, browser, API-verifier, API/session-auth,
  config-no-log, credential-keyring, secure-bootstrap, migration, Docker, fuzz,
  scanner, and reproducible-frontend gates were rerun. Dependency manifests
  remain unchanged.

## Iteration 3 user-update isolation

- `PutUser` now has an explicit allowlist containing only username, password
  hash, role ID, password-change requirement, account lock, and last-login.
  It cannot write `api_key`, `api_key_verifier`, or
  `api_key_verifier_key_id`, even if its in-memory `User` carries attacker-
  supplied verifier values. New users still go through the dedicated atomic
  verifier issuance path.
- All production callers were audited: login persists last-login, settings and
  forced reset persist password changes, and the user API persists profile,
  role, password policy, and lock fields. A no-op update remains successful on
  MySQL while a missing ID fails rather than creating a user.
- SQLite and real MySQL tests prove a LEGACY row remains plaintext/NULL/NULL
  byte-for-byte across login-style and general updates and then migrates
  successfully. They also prove ordinary MIGRATED updates preserve exact
  verifier bytes/key ID, ordinary fields persist, malicious verifier values are
  ignored, and dedicated reset atomically invalidates and replaces the token.
- Full Go/race, clean real-MySQL, browser, container, migration/bootstrap/auth,
  fuzz, scanner, and reproducibility gates passed with the established scanner
  baseline. The iteration-3 Yarn audit endpoint returned HTTP 503 on three
  attempts; manifests are unchanged and the iteration-2 audit found zero
  vulnerabilities.

## Iteration 4 account-lock enforcement

- Indexed verifier lookup rejects locked accounts without changing the generic
  invalid-key response. Defensive loaded-user validation, lazy-rekey CAS and
  conflict reread, and the final account-state linearization check all enforce
  the same rule without selecting, comparing, or logging legacy plaintext.
- A lock committed before authentication completes rejects the request. A lock
  committed before lazy rekey also prevents verifier mutation; a lock committed
  after completed authentication retains normal per-request race semantics.
  Existing session selection behavior is deliberately unchanged.
- SQLite and clean real MySQL tests cover locked migrated users and
  administrators, Bearer/raw/query/form transports, byte-identical invalid-key
  responses, unlock restoration of the same token, locked verifier-only
  create/reset, and lock-before-lazy-CAS rejection without rekey. The full PR
  #62 transport/auth matrix and normal migration/lazy/reset behavior remain
  covered.
- Final `./scripts/verify.sh`, race tests, real-MySQL suite, browser, all five
  container suites, two 10-second fuzzers, scanners, and two-build frontend
  reproducibility passed. Gosec and Zizmor retained only their established
  baselines. Yarn audit was retried but the registry timed out; dependency
  manifests are byte-unchanged and iteration 2's successful audit found zero
  vulnerabilities.

## Iteration 5 final-state and response compatibility

- Authentication now ends with one exact-pair query requiring the accepted user
  ID, verifier bytes, verifier key ID, `api_key IS NULL`, and unlocked state.
  The in-memory pair follows successful lazy CAS or its verified conflict reread,
  so active and old-pepper paths share the same final linearization point.
- Deterministic SQLite and real-MySQL tests mutate state after active/lazy
  verification but before the final query. Active reset, lazy reset, and lazy
  lock all reject the stale request before protected-handler execution; reset
  replacement authentication and lazy resolution remain functional.
- Explicit create/reset response objects preserve legacy wire compatibility:
  create user fields and its one-time `api_key` are top-level, while reset
  `data` is the one-time token string. Raw-body tests reject nested create
  fields or object-shaped reset data and confirm persistent User, GET/list/PUT,
  and settings surfaces remain secret-free.
- Full Go/race, clean real-MySQL, browser, all container, migration/auth,
  two 10-second fuzz, scanner, and two-build reproducibility gates passed.
  Gosec retained its 12 pre-existing findings; Zizmor reported no unsuppressed
  findings; Gitleaks found no production-diff leak; Retire.js and actionlint
  passed. Yarn audit was retried but the registry timed out. Dependency
  manifests remain byte-unchanged.

## Iteration 6 exact legacy-token preservation

- Offline migration no longer imposes a 255-byte preflight limit. Every
  non-empty legacy value is converted directly from the database string to its
  exact byte sequence for candidate generation, HMAC, guarded writes, and
  readback; there is no trim, normalization, case folding, or truncation.
- SQLite tests migrate and authenticate both a value longer than 255 bytes and
  a 255-character multibyte value longer than 255 bytes. Byte-truncated variants
  fail, and a second migration is a byte-preserving no-op.
- Real MySQL tests migrate and authenticate exactly 255 multibyte characters
  whose UTF-8 representation exceeds 255 bytes. They also characterize the
  backend boundary: strict mode rejects 256 characters, while non-strict mode
  truncates on insertion and migration hashes the exact 255-character value
  subsequently readable from the database.
- Full Go/race and migration/idempotence suites, clean real MySQL, browser, all
  container suites, both 10-second fuzzers, scanners, and two-build frontend
  reproducibility passed. Gosec retained 12 established findings; Zizmor had no
  unsuppressed findings; Gitleaks, Retire.js, and actionlint passed. Yarn audit
  was retried but the registry timed out. Dependency manifests are unchanged.

## Limitations

HMAC protects token confidentiality after a read-only database leak, including
weak legacy tokens when the pepper remains separate. It does not protect
against host/process plus pepper compromise, write-capable database record
substitution, reveal-time browser compromise, query/form logging, or online
guessing. JavaScript memory zeroization is not claimed.
