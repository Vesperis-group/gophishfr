# Inspector Feedback — Iteration 2

## Verdict: PASS

Iteration 1's security-review finding—runtime authenticated migrator-only CONTROLLED_BOTH
rows lacking `api_key IS NULL` requirement—has been comprehensively corrected. All three
runtime code paths (initial indexed lookup, lazy-rekey CAS, and conflict reread) now enforce
strict MIGRATED-only state (plaintext NULL, verifier 32 bytes, valid key ID) without selecting,
comparing, or returning plaintext. Comprehensive new test coverage for SQLite and real MySQL
validates rejection of BOTH/LEGACY/all-absent states, successful migration/clearing, old-pepper
BOTH rejection without rekey, concurrent transition rejection, normal lazy upgrade, and
reset-invalidation invariants.

## Corrections Applied (Builder Commit d32e02b)

### Runtime state enforcement — three critical paths

#### 1. Initial indexed lookup (`GetUserByAPIKey`, line 99)

**Before:**
```sql
SELECT api_key_verifier, api_key_verifier_key_id FROM users
WHERE candidate_where(indexed verifiers)
```

**After:**
```sql
SELECT api_key_verifier, api_key_verifier_key_id FROM users
WHERE api_key IS NULL
AND candidate_where(indexed verifiers)
LIMIT 2
```

Plaintext column now explicitly required NULL. Any LEGACY (plaintext non-empty), CONTROLLED_BOTH
(both plaintext and verifier present), or all-absent row fails with `ErrInvalidAPICredential` during
database SELECT stage (zero matches) before any subsequent verification logic.

#### 2. Verifier-write validation (`verifyAPIKeyWrite`, line 324)

**Before:**
```sql
SELECT api_key_verifier, api_key_verifier_key_id FROM users WHERE id = ?
```

**After:**
```sql
SELECT api_key_verifier, api_key_verifier_key_id FROM users
WHERE id = ? AND api_key IS NULL
```

Called from both `CreateUserWithAPIKey` (line 169) and `ResetUserAPIKey` (line 215) after UPDATE,
to verify that the new verifier/key-ID persisted exactly. Now rejects any row where plaintext was
not cleared by the preceding UPDATE. If CAS failed or a race condition left plaintext present,
verification fails and the transaction aborts.

#### 3. Lazy-rekey CAS conflict reread (`lazyUpgradeAPIKeyVerifier`, line 370)

**Before:**
```sql
SELECT api_key_verifier, api_key_verifier_key_id FROM users WHERE id = ?
```

**After:**
```sql
SELECT api_key_verifier, api_key_verifier_key_id FROM users
WHERE id = ? AND api_key IS NULL
```

When UPDATE with exact CAS (line 346-353) returns zero rows (concurrent rekey/reset by another
process), reread the current state to verify the token still matches the stored verifier and no
races occurred. Now explicitly rejects any row with plaintext present, treating concurrent
transition to CONTROLLED_BOTH as auth failure without attempting rekey.

## Test Coverage — New SQLite and MySQL validation

### Iteration 1 tests preserved

- `TestAPIKeyVerifierCreateResetAndMissingKeyring` — fresh admin bootstrap  
- `TestFreshBootstrapMissingVerifierIsAtomic` — missing keyring atomicity  
- `TestOfflineAPIKeyMigrationAtomicIdempotentAndIrreversible` — migration lifecycle  
- `TestSQLiteAPIKeySchemaRebuildPreservesLegacyState` — SQLite rebuild and sequence  

### Iteration 2 NEW test: `TestRuntimeRequiresVerifierOnlyState` (SQLite)

**Test 1a: CONTROLLED_BOTH initial state rejects before migration**
- Create user with matching plaintext + verifier + key ID (CONTROLLED_BOTH state)
- Verify `GetUserByAPIKey()` returns `ErrInvalidAPICredential` (zero matches from `WHERE api_key IS NULL`)
- Verify no attempt to authenticate or rekey  
- Call `MigrateAPIKeys()`, verify `Updated=1`

**Test 1b: After migration (plaintext cleared), same token authenticates**
- Rerun `GetUserByAPIKey()` with same token
- Verify authentication succeeds with `User.Id == 1`
- Confirm verifier/key-ID preserved and api_key NULL

**Test 2: LEGACY state (plaintext only, no verifier) rejects at runtime**
- INSERT new user with plaintext token only (no verifier/key-ID columns)
- Verify `GetUserByAPIKey()` with same token returns `ErrInvalidAPICredential` (zero matches)
- Verify no attempt to fall back to plaintext comparison

**Test 3: All-absent state (NULL plaintext, no verifier, no key-ID) rejects at runtime**
- INSERT new user with NULL api_key and absent verifier/key-ID
- Verify `GetUserByAPIKey()` returns `ErrInvalidAPICredential`

**Test 4: Old-key CONTROLLED_BOTH (plaintext + old-pepper verifier) rejects without rekey**
- INSERT user with plaintext + old-pepper verifier/key-ID (CONTROLLED_BOTH state with old pepper)
- Verify `GetUserByAPIKey()` returns `ErrInvalidAPICredential` (rejected by initial WHERE)
- Verify lazy rekey did NOT occur (key-ID remains "old" in database)
- Verify no UPDATE attempted

**Test 5: Lazy-rekey CAS conflict with concurrent BOTH transition rejects**
- INSERT user in verifier-only state (plaintext NULL, old-pepper verifier)
- Call `lazyUpgradeAPIKeyVerifier()` to trigger CAS upgrade to active pepper
- During the upgrade, simulate concurrent writer transitioning plaintext to a value (race condition)
- Verify lazy rekey CAS reread fails with `ErrInvalidAPICredential` (WHERE api_key IS NULL returns zero rows)
- Verify key-ID remains "old" (no rekey attempted despite CAS failure)

### Iteration 2 NEW test: `TestMySQLZZRuntimeRequiresVerifierOnlyState` (real MySQL)

**Equivalent coverage to SQLite test above, running against real MySQL database:**

- Matching CONTROLLED_BOTH state rejects
- After migration clears plaintext, same token authenticates
- Reset old token, verify new token authenticates
- LEGACY state rejects
- All-absent state rejects
- Old-key CONTROLLED_BOTH rejects without rekey
- Lazy-rekey CAS conflict on transition to BOTH rejects without rekey
- Normal lazy upgrade from old pepper to active pepper succeeds when state is verifier-only

## Acceptance Criteria Verification — Iteration 2 focus areas

### Runtime rejects non-MIGRATED (Criterion #58, #124)

- [x] **Initial indexed lookup requires `api_key IS NULL`** — verified line 99 in `GetUserByAPIKey`
- [x] **Verifier-write validation requires `api_key IS NULL`** — verified line 324 in `verifyAPIKeyWrite`
- [x] **Lazy-rekey reread requires `api_key IS NULL`** — verified line 370 in `lazyUpgradeAPIKeyVerifier`
- [x] **No fallback to plaintext comparison** — all three paths reject at SQL WHERE stage
- [x] **LEGACY state (plaintext only) rejects** — test `TestRuntimeRequiresVerifierOnlyState` line 182-184
- [x] **CONTROLLED_BOTH state rejects** — test lines 161-163 and 205-207
- [x] **All-absent state rejects** — test lines 191-193
- [x] **SQLite coverage confirmed** — new test integrated into `models/api_key_verifier_test.go`
- [x] **Real MySQL coverage confirmed** — new test `TestMySQLZZRuntimeRequiresVerifierOnlyState` in `zz_api_key_verifier_mysql_test.go`

### Migration preserves correctness after plaintext clear (Criterion #62, #63)

- [x] **Pre-migration BOTH state rejects** — test line 161-163
- [x] **Migration clears plaintext** — test line 164-167
- [x] **Post-clear verifier authenticates** — test line 168-170
- [x] **Reset produces new token** — MySQL test lines 59-65
- [x] **Old token invalid after reset** — MySQL test line 62-65

### Lazy-rekey safety with concurrent races (Criterion #73)

- [x] **Normal lazy upgrade succeeds when verifier-only** — MySQL test lines 110-118
- [x] **Old-key BOTH rejected without rekey** — test line 205-216
- [x] **CAS failure on transition to BOTH rejected** — test line 237-248
- [x] **No rekeying occurs on auth failure** — all BOTH-state tests verify key-ID unchanged

### Old-pepper invariants (Criterion #73)

- [x] **Old-key CONTROLLED_BOTH state rejects at runtime** — test line 205-207
- [x] **No lazy rekey triggered for rejected BOTH** — test line 214-216
- [x] **Concurrent transition to BOTH rejected by WHERE** — test line 237-239
- [x] **Old-key verifier-only (valid) still lazily upgrades** — MySQL test line 110-118

## Quality Gate Verification

### Code paths protected

Three distinct WHERE clauses now enforce `api_key IS NULL`:

1. **Initial lookup** (user.go:99): Prevents LEGACY/BOTH/absent rows from matching
2. **Write validation** (user.go:324): Verifies plaintext was cleared after UPDATE
3. **Lazy reread** (user.go:370): Rejects concurrent transition to BOTH during CAS failure

### No plaintext selection in runtime

All three paths read only:
- `api_key_verifier` (32 bytes binary)
- `api_key_verifier_key_id` (64-char string identifier)

Plaintext `api_key` column is neither selected, compared, nor returned in any runtime code path.

### Verification logic unchanged

- Indexed lookup via `candidateWhere()` and `Candidates()` method unchanged
- HMAC comparison via `hmac.Equal()` unchanged
- Active key detection and lazy upgrade trigger unchanged
- Reset/create token generation and atomicity unchanged

## Regression Verification

### Session/CSRF/RBAC/view-only from PR #62

- [x] All session auth endpoints preserved (outside API-key path)
- [x] API-key Bearer/raw/query/form selection unchanged
- [x] Conflict/fallback semantics unchanged
- [x] Middleware contract unchanged

### Browser reveal lifecycle

- [x] Settings page create/reset reveal-once DTOs unchanged
- [x] Transient state cleared on close/reload unchanged
- [x] No DOM persistence changed

### Docker keyring separation

- [x] Separate `GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE` validation unchanged
- [x] No cross-fallback logic changed
- [x] Independent error messages unchanged

### Migration atomicity

- [x] Guarded Down logic unchanged
- [x] Collision preflight unchanged
- [x] Write-then-readback-then-clear unchanged
- [x] One global transaction unchanged

## Issues Found

**None.** The security review finding has been comprehensively corrected. No logic errors,
no state-machine inconsistencies, no new plaintext exposure paths.

## Summary

**Iteration 2 corrects the iteration 1 security-review finding by enforcing verifier-only
runtime state across all three authentication and validation code paths.** The fix is minimal,
focused, and preserves all existing functionality (migration, lazy upgrade, reset, create,
session fallback). Test coverage for SQLite and real MySQL validates that LEGACY, CONTROLLED_BOTH,
and all-absent states are rejected while normal MIGRATED, lazy-upgrade, and reset flows remain
correct. Production-ready for merge.

