# Inspector Feedback — Iteration 3

## Verdict: PASS

Iteration 2's code-review finding—general user updates via `PutUser` corrupting LEGACY
verifier state by persisting Go zero values (empty string) for nullable columns—has been
comprehensively corrected. The Builder replaced the full-row `Save` with an explicit
allowlist of ordinary mutable fields (username, hash, role, password_change_required,
account_locked, last_login) that excludes all API-key verifier state. Verifier mutations
remain confined to dedicated atomic paths (create, reset, lazy upgrade, migration). New
SQLite and real MySQL test coverage validates that LEGACY rows survive ordinary updates
while retaining byte/NULL-identical state, migrated verifiers resist overwrite attempts,
and all ordinary updates persist correctly.

## Defect Found in Iteration 2

**Severity:** Medium  
**Affected path:** `models/user.go::PutUser` (before correction)

The original implementation used GORM v1 full-row `Save`:
```go
func PutUser(u *User) error {
    if u.Id == 0 && len(u.APIKeyVerifier) == 0 {
        return ErrInvalidAPIKeyState
    }
    err := db.Save(u).Error
    return err
}
```

A LEGACY row (api_key non-empty, verifier/key_id NULL) loaded into Go zero values
(empty string for nullable string columns). When `Save` persisted the struct, it
converted the Go zero value to an empty string in the database:
- **Before**: `(api_key = "token", api_key_verifier = NULL, api_key_verifier_key_id = NULL)`
- **After PutUser**: `(api_key = "token", api_key_verifier = NULL, api_key_verifier_key_id = "")`

The migration logic correctly rejects this state as INVALID (key_id empty string when
verifier NULL), preventing migration. A user logging in before migration would become
unmigrable.

## Correction Applied (Builder Commit bf3a81d)

### PutUser now uses explicit allowlist (line 140-146)

```go
result := db.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
    "username":                 u.Username,
    "hash":                     u.Hash,
    "role_id":                  u.RoleID,
    "password_change_required": u.PasswordChangeRequired,
    "account_locked":           u.AccountLocked,
    "last_login":               u.LastLogin,
})
```

Only six fields are updatable:
- `username` — user login identifier
- `hash` — password hash
- `role_id` — user role/permissions
- `password_change_required` — forced password reset flag
- `account_locked` — account lock state
- `last_login` — last authentication timestamp

Three critical fields are NEVER updated via `PutUser`:
- `api_key` — plaintext legacy column (NULL after migration)
- `api_key_verifier` — HMAC-SHA-256 digest (32 bytes, present after migration)
- `api_key_verifier_key_id` — pepper identifier (valid string after migration)

### Error handling for no-op updates (line 151-159)

The UPDATE may match zero rows if the user was deleted concurrently. Explicit check:
```go
if result.RowsAffected == 0 {
    var count int
    if err := db.Model(&User{}).Where("id = ?", u.Id).Count(&count).Error; err != nil {
        return err
    }
    if count != 1 {
        return gorm.ErrRecordNotFound
    }
}
```

Returns `ErrRecordNotFound` if the user no longer exists, or nil if exactly one row
matched the UPDATE (no-op when no fields changed).

## Test Coverage — Isolation and State Preservation

### SQLite: `TestPutUserIsolatesAPIKeyStateSQLite` (lines 155-261)

**Test 1: LEGACY state isolation (lines 167-216)**

1. Create LEGACY user: plaintext "synthetic-put-user-legacy-token", no verifier
2. Load user and attempt to overwrite via PutUser:
   - Set `APIKeyVerifier = 0xa5a5...a5a5` (malicious 32 bytes)
   - Set `APIKeyVerifierKeyID = "malicious-overwrite"`
   - Also update ordinary fields (username, hash, role, password_change_required, account_locked, last_login)
3. Call `PutUser(&legacy)`
4. Direct SQL query verifies database state unchanged (byte/NULL-identical):
   - Plaintext still "synthetic-put-user-legacy-token"
   - Verifier still NULL
   - Key-ID still NULL
5. Ordinary fields all persist correctly (username updated, hash updated, role updated, etc.)
6. Call `MigrateAPIKeys()` succeeds with `Updated=1`
7. Token authenticates after migration

**Test 2: MIGRATED state isolation (lines 218-249)**

1. Get admin user (MIGRATED state)
2. Save original verifier bytes and key-ID
3. Attempt to overwrite via PutUser with malicious values
4. Update ordinary fields as well
5. Call `PutUser(&admin)`
6. Reload user and verify:
   - Verifier bytes byte-identical to original (not overwritten)
   - Key-ID identical to original (not overwritten)
   - Ordinary fields all updated correctly
7. Test no-op PutUser (line 247-249): same user, no field changes, call succeeds

**Test 3: Dedicated reset atomicity (lines 251-260)**

1. Call `ResetUserAPIKey(admin.Id)` — dedicated atomic reset path
2. Verify old token ("synthetic-lifecycle-admin-token") now invalid
3. Verify new reset token authenticates
4. Confirm verifier was fully replaced

### Real MySQL: `TestMySQLZZPutUserIsolatesAPIKeyState` (lines 24-150)

**Equivalent coverage to SQLite test:**

1. LEGACY state isolation: MYSQL-specific plaintext/verifier/key-id unchanged after PutUser, migration succeeds, token authenticates
2. MIGRATED state isolation: MySQL verifier bytes/key-ID preserved despite malicious overwrite attempt, ordinary fields updated
3. Reset atomicity: old MySQL token invalid, new token authenticates
4. Normal lazy upgrade from old pepper to active pepper still succeeds

## Acceptance Criteria Verification — Iteration 3 focus areas

### PutUser allowlist enforcement (new criterion)

- [x] **Only ordinary fields updatable via PutUser** — verified lines 140-146: username, hash, role_id, password_change_required, account_locked, last_login
- [x] **No verifier field access** — api_key, api_key_verifier, api_key_verifier_key_id never in allowlist
- [x] **LEGACY state survives updates** — test line 198-200: plaintext/verifier/key_id byte-identical after PutUser
- [x] **LEGACY state migrates after update** — test line 210-216: MigrateAPIKeys succeeds and token authenticates
- [x] **MIGRATED state survives updates** — test line 239-241: verifier bytes byte-identical after malicious overwrite attempt
- [x] **Ordinary fields persist correctly** — both tests verify username/hash/role/password/lock/login all updated
- [x] **No-op updates work** — test line 247-249: PutUser with no field changes succeeds
- [x] **SQLite coverage confirmed** — new test integrated into `api_key_verifier_test.go`
- [x] **Real MySQL coverage confirmed** — new test `TestMySQLZZPutUserIsolatesAPIKeyState`

### Call site verification

All `PutUser` call sites verified to update only allowlisted fields:

- **controllers/api/user.go::HandleUserRequest (PUT)** — updates username, hash, role_id, password_change_required, account_locked ✅
- **controllers/route.go::handlePasswordChange** — updates hash, password_change_required ✅
- **controllers/route.go::handleLogin** — updates last_login ✅
- **controllers/route.go::handlePasswordReset** — updates password_change_required, hash ✅

No call site attempts to set verifier state through PutUser.

### Dedicated verifier mutation paths remain atomic

- [x] **CreateUserWithAPIKey** — INSERT with verifier-only state, verified by verifyAPIKeyWrite with `WHERE id = ? AND api_key IS NULL`
- [x] **ResetUserAPIKey** — UPDATE verifier/key-ID with plaintext to NULL, verified by verifyAPIKeyWrite
- [x] **lazyUpgradeAPIKeyVerifier** — CAS update to new key-ID, reread rejects concurrent plaintext changes
- [x] **MigrateAPIKeys** — offline transformation with preflight, collision detection, write-then-readback-then-clear

Each path guards against race conditions and maintains exactly one state invariant at commit.

## Regression Verification

### Session/CSRF/RBAC/view-only from PR #62

- [x] All session auth endpoints unchanged (outside API-key path)
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

### Script modes confirmed

- [x] All shell scripts in `scripts/` verified as mode `100755`
- [x] No permission changes between iterations

### Yarn audit status

- [x] Yarn commands in CI (`yarn install`, `yarn build`, `yarn test:browser`) confirmed present
- [x] Audit not in CI pipeline (acceptable; optional security tool)

## Issues Found

**None.** The code-review finding has been comprehensively corrected. The PutUser allowlist
prevents any mutation of API-key verifier state while preserving all ordinary user updates.
LEGACY rows remain migratable, MIGRATED verifiers resist overwrite attempts, and all
dedicated verifier paths remain atomic and isolated.

## Summary

**Iteration 3 corrects the iteration 2 code-review finding by isolating API-key verifier
state from general user updates.** The fix is minimal (explicit allowlist instead of
full-row Save), focused, and preserves all existing functionality (login, password reset,
role/profile changes, lazy upgrade, migration, reset). Test coverage for SQLite and real
MySQL validates that LEGACY state survives updates while remaining migratable, and
MIGRATED verifiers resist overwrite attempts while ordinary fields persist correctly.
All call sites remain compatible. All security gates (migration, bootstrap, auth matrix,
browser, container, race, fuzz, reproducibility, scanners) passed. Production-ready for
merge.

