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
  peppers, rejects zero/ambiguous/malformed matches, and never selects or
  searches the legacy plaintext column.

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
- Security tools: govulncheck found no called vulnerabilities; Yarn audit found
  zero vulnerabilities; Gitleaks found no working-tree leak; actionlint and
  zizmor passed. Gosec reported only the 12 pre-existing findings outside this
  change. Retire.js passed; GitHub dependency review was unavailable locally.
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

## Limitations

HMAC protects token confidentiality after a read-only database leak, including
weak legacy tokens when the pepper remains separate. It does not protect
against host/process plus pepper compromise, write-capable database record
substitution, reveal-time browser compromise, query/form logging, or online
guessing. JavaScript memory zeroization is not claimed.
