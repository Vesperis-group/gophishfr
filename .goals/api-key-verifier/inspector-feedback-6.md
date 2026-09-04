# Inspector Feedback — Iteration 6 (Unicode Migration Compatibility)

## Verdict: PASS

Release-review iteration 5 identified one legacy-compatibility defect: the offline
migration imposed an incompatible 255-byte preflight limit that rejected valid
legacy tokens stored in SQLite beyond that limit and all multibyte tokens in MySQL
that exceeded 255 bytes while remaining valid 255-character tokens. Builder commit
f64266d removes this constraint and correctly preserves exact token bytes through
migration and subsequent authentication.

## Defect Found in Iteration 5

**Severity:** High (blocks migration for any legacy tokens beyond 255 bytes)
**Affected path:** `models/api_key_migration.go::classifyAPIKeyMigrationRow` (line 157)

The migration's preflight validation rejected any legacy token with byte length >255,
blocking the entire migration when even one such token existed.

```go
// Before: incompatible byte-length check
const maxLegacyAPIKeyBytes = 255
if hasToken && (row.token.String == "" || len([]byte(row.token.String)) > maxLegacyAPIKeyBytes) {
	return 0, errors.New("invalid legacy API credential state")
}
```

**Impact:** This constraint violated the HMAC protocol and legacy compatibility:

1. **SQLite:** Declared column is `VARCHAR(255)` but SQLite does not enforce that
   limit. Operators could have stored API tokens longer than 255 bytes. Migration
   would fail with generic "invalid state" error and require manual intervention.

2. **MySQL:** Column is `VARCHAR(255)` character limit, NOT byte limit. Valid UTF-8
   tokens with 255 multibyte characters can exceed 255 bytes (e.g., 255 Japanese
   characters × 3 bytes/char = 765 bytes). Migration would silently reject these
   clients.

3. **HMAC protocol:** The verifier computes HMAC over exact token bytes. No
   specification version constraint on token length exists; generated tokens are
   32 random bytes but legacy `GOPHISH_INITIAL_ADMIN_API_TOKEN` permits arbitrary
   values including long ones.

## Correction Applied (Builder Commit f64266d)

### Removed incompatible byte-length check (line 157)

```go
// After: accept any non-empty token
if hasToken && row.token.String == "" {
	return 0, errors.New("invalid legacy API credential state")
}
```

**Changed behavior:**
- Accept any non-empty legacy token value (whether >255 bytes or not)
- Hash the exact byte sequence from the database
- No normalization, trimming, case folding, or truncation
- Preserve for subsequent authentication

### Updated documentation (API_KEY_VERIFIER.md, lines 135-141)

```markdown
Migration adds no token byte-length limit: it hashes the exact non-empty bytes
read from the legacy column without trimming, normalization, case folding, or
truncation. SQLite may contain values longer than its declared
`VARCHAR(255)`. MySQL's limit is 255 characters, so valid multibyte values can
exceed 255 bytes. Strict MySQL rejects values beyond the character limit;
non-strict MySQL may already have truncated them on insertion. Migration
preserves and hashes whatever exact value is readable from the database.
```

Explicitly documents:
1. No byte-length limit imposed by migration
2. Exact byte preservation without transformation
3. SQLite >VARCHAR(255) possible
4. MySQL character vs byte limit distinction
5. Strict vs non-strict MySQL behavior
6. Exact readable value is what matters

## Test Coverage — Exact Legacy Bytes

### SQLite: `TestOfflineAPIKeyMigrationPreservesExactLongTokensSQLite` (lines 629-672)

**Test 1: Long token beyond 255 bytes**

```go
tokens := []struct{
    username string
    value    string
}{
    {username: "sqlite-long-byte-token",
     value: strings.Repeat("synthetic-long-token-", 20)},  // >255 bytes
    ...
}
```

1. Creates token with 21-character string repeated 20 times = 420 bytes total
2. Verifies `len([]byte(token)) > 255` (line 644-646)
3. Inserts directly into SQLite without triggering any application-level limit
4. Migration processes without error
5. Exact token authenticates after migration: `GetUserByAPIKey(token)` succeeds (line 660-662)
6. Byte-truncated version does NOT authenticate: `GetUserByAPIKey(truncated[:255])` rejects (line 664-666)
7. Re-run migration is idempotent: zero updates, unchanged count matches (line 668-671)

**Test 2: Multibyte token (255 characters, >255 bytes)**

```go
{username: "sqlite-multibyte-token",
 value: strings.Repeat("界", 255)},  // 255 chars × 3 bytes = 765 bytes
```

1. Creates token of 255 Japanese characters (3 UTF-8 bytes each)
2. Verifies total byte length > 255 (line 644-646)
3. Inserts into SQLite (no truncation by SQLite)
4. Migration processes
5. Exact token (255 characters) authenticates
6. Byte-truncated version (first 255 bytes only = 85 characters) does NOT authenticate
7. Idempotent rerun confirmed

### Real MySQL: `TestMySQLZZOfflineAPIKeyMigrationPreservesCharacterBoundary` (lines 575-675)

**Test 1: 255 multibyte characters (valid, exceeds 255 bytes)**

```go
multibyteToken := strings.Repeat("界", 255)  // 255 chars × 3 bytes = 765 bytes
```

**Setup: MySQL strict mode test (lines 615-623)**
1. Set `STRICT_TRANS_TABLES` mode
2. Insert 255 multibyte characters
3. Verify MySQL accepts it (line 622: "MySQL rejected 255 valid multibyte characters")
4. Characters are valid; byte limit doesn't apply to character column

**Test 2: Over-character-limit detection (lines 625-630)**
1. Create 256 ASCII characters string
2. Attempt insertion in strict MySQL
3. Verify rejection (line 629-630: "strict MySQL accepted more than 255 token characters")
4. Confirms MySQL enforces character limit, not byte limit

**Test 3: Non-strict MySQL truncation behavior (lines 632-650)**
1. Switch to non-strict mode: `SET SESSION sql_mode = ''`
2. Insert 256 ASCII characters
3. Verify no error (line 638-639)
4. Query back exact stored value
5. Verify stored as exactly 255 characters, not 256 (line 648-650)
6. Non-strict MySQL silently truncated on insertion

**Test 4: Migration preserves exact readable values (lines 655-667)**
1. Migrate with service containing active key
2. Result: Updated = 2 rows (multibyte + truncated non-strict) (line 656)
3. Multibyte token authenticates with full 255 characters (line 663-665)
4. Non-strict truncated token authenticates with the 255-character value actually stored (line 663-665)
5. Pre-truncation 256-character value does NOT authenticate (line 668-670)

**Test 5: Idempotent migration on character boundary (lines 671-674)**
1. Rerun migration
2. Result: Updated = 0 (no changes)
3. Unchanged = 2 (both tokens remain as-is)
4. No re-truncation or re-normalization

### Verification: Exact Byte Authentication

Both SQLite and MySQL tests verify the critical invariant:
- **Exact stored token authenticates:** After migration, the exact value stored in
  the database authenticates successfully.
- **Truncated version rejects:** A truncated version (e.g., first 255 bytes) does
  NOT authenticate, proving no silent truncation or normalization occurred during
  migration.
- **Idempotence:** Re-running migration on already-migrated tokens preserves
  byte-for-byte identity.

## Migration Behavior Analysis

### SQLite >VARCHAR(255)

SQLite `VARCHAR(255)` is not enforced. Long tokens are stored as-is. Migration
converts the exact string to bytes and computes HMAC without modification.

**Migration decision:** Accept and hash exactly what's readable.

### MySQL 255 characters, variable bytes

MySQL `VARCHAR(255)` limits characters, not bytes. A 255-character multibyte
string can exceed 255 bytes.

**Strict mode:** Rejects insertion if >255 characters. Migration receives exactly
what's stored.

**Non-strict mode:** Silently truncates on insertion. Migration receives the
truncated value and hashes that exact readable value.

**Migration decision:** Accept and hash exactly what's readable, accounting for
any truncation that already occurred at insertion time.

## Regression Verification

### All 62 acceptance criteria from goal.md remain satisfied

✅ Especially criterion "LEGACY rows use exact token bytes to compute active-key HMAC"
(line 178 of goal.md) — now correctly implemented without byte-limit imposed.

### All prior iterations' fixes remain compatible

✅ **Iteration 1:** Verifier protocol (HMAC framing, keyring, schema) unchanged
✅ **Iteration 2:** Runtime verifier-only enforcement unchanged
✅ **Iteration 3:** PutUser allowlist unchanged
✅ **Iteration 4:** Account-lock enforcement unchanged
✅ **Iteration 5:** Final-state linearization unchanged, response shapes unchanged

### Migration atomicity and idempotence

✅ Global transaction unchanged
✅ Exact readback-before-clear unchanged
✅ Guarded row counts unchanged
✅ Rerun handling unchanged (zero-update no-op confirmed by tests)

### Authentication after migration

✅ Exact tokens authenticate
✅ Variants (truncated, normalized, case-folded) do NOT authenticate
✅ Proves no silent transformation occurred

### All transport paths

✅ Bearer, raw Authorization, query parameter, form body all accept migrated tokens

### All test gates passed

✅ Go tests: `./scripts/verify.sh` full suite (gofmt, lint, vet, build, tests, race)
✅ SQLite migration tests: `TestOfflineAPIKeyMigrationPreservesExactLongTokensSQLite`
✅ Real MySQL tests: `TestMySQLZZOfflineAPIKeyMigrationPreservesCharacterBoundary`
✅ Browser Playwright: 13/13 pass (including settings, user-create, reset)
✅ Container suites: API verifier, auth, config, bootstrap all pass
✅ Fuzz: verifier and keyring 10-second passes
✅ Race detector: passed
✅ Security scanners:
   - govulncheck: no called vulnerabilities
   - Gitleaks: no leaks (goal prose excluded)
   - Gosec: 12 pre-existing findings only
   - Zizmor: no unsuppressed findings
   - Retire.js: passed
   - actionlint: passed

### Yarn audit

Yarn audit registry timed out during verification (network issue, not code).
All dependency manifests (`go.mod`, `go.sum`, `package.json`, `yarn.lock`) are
byte-unchanged from initial SHA. Prior successful audit (iteration 2-3) found
zero vulnerabilities. Repeated canonical frontend builds produced identical hashes.

### Frontend reproducibility

Confirmed: two independent builds of static/js/dist produce identical hashes,
proving no toolchain-dependent changes or secrets embedded.

## Issues Found

**None.** Release-review defect comprehensively corrected. Migration no longer
imposes incompatible 255-byte limit. Exact legacy token bytes are preserved
through migration and authenticate correctly after, including SQLite >255-byte
tokens and MySQL multibyte characters exceeding 255 bytes. Strict and non-strict
MySQL behavior documented and tested. Idempotence confirmed. All prior security
fixes, auth contracts, and regression tests remain satisfied.

## Summary

**Iteration 6 corrects the release-review finding by removing the incompatible
255-byte migration preflight limit.** The offline migration now accepts any
non-empty legacy token and hashes its exact byte sequence without
normalization, trimming, truncation, or case folding. SQLite long tokens
(>255 bytes) and MySQL multibyte characters (255 chars, >255 bytes) migrate
successfully. Exact tokens authenticate after migration; truncated variants
reject. Non-strict MySQL truncation is handled correctly (hash what's actually
stored). Strict MySQL constraints are documented and tested. Full test
coverage for SQLite >255-byte and multibyte tokens; real MySQL multibyte and
strict/non-strict boundary cases. Migration atomicity, idempotence, and all
prior security fixes verified compatible. All quality gates passed. Yarn audit
registry unavailable (temporary); manifests unchanged. **Production-ready
for merge.**
