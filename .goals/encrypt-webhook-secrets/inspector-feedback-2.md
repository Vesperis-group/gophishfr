# Inspector Feedback — Iteration 2

## Verdict: PASS

## Summary

The Builder has successfully corrected the iteration-1 security finding. The NULL legacy
secret handling is now comprehensive and rigorous: all runtime write paths (PostWebhook,
PutWebhook) and offline rollback use NULL-aware predicates (`secret IS NULL OR secret = ''`);
full row verification with metadata fields ensures metadata-bundled updates persist and
are observable in active-delivery selection; MySQL changed-row semantics are handled
correctly with full-row comparison rather than RowsAffected; and all valid/ambiguous/
concurrent states are properly tested against both SQLite and real MySQL 8.4.11.

## Security Finding Resolution

The iteration-1 security review identified that legacy `secret IS NULL` rows could silently
ignore metadata-only updates because predicates that literally compare `secret = ''` never
match NULL columns in SQL. Deactivating a NULL-secret webhook would report success while
the webhook remained active and continued receiving campaign events.

**Resolution verified:**

### 1. NULL-Aware Predicates

- [x] **PostWebhook create encryption guard** (webhook.go:169):
  - Original: `WHERE id = ? AND secret = '' AND secret_ciphertext = ''`
  - Fixed: `WHERE id = ? AND (secret IS NULL OR secret = '') AND secret_ciphertext = ''`
  - Verified: Treats legacy NULL and empty string identically as valid no-secret states

- [x] **PutWebhook update guard** (webhook.go:290):
  - Original: `WHERE id = ? AND secret = '' AND secret_ciphertext = ?`
  - Fixed: `WHERE id = ? AND (secret IS NULL OR secret = '') AND secret_ciphertext = ?`
  - Verified: Metadata updates on NULL-secret rows will find and update the row

- [x] **RollbackWebhookSecrets predicate** (webhook_credentials.go:512):
  - Original: `WHERE id = ? AND secret = '' AND secret_ciphertext = ?`
  - Fixed: `WHERE id = ? AND (secret IS NULL OR secret = '') AND secret_ciphertext = ?`
  - Verified: Accepts valid NULL+ciphertext states during rollback restoration

### 2. Full Row Storage Verification

The critical fix: verification now compares **all columns** the transaction intended to
write, not just the secret columns. This distinguishes a genuine matched no-op (WHERE
clause matched, every column already held the intended value) from a silent failure
(WHERE clause matched zero rows due to NULL predicate issue, leaving old metadata).

- [x] **storedWebhookRow struct** (webhook_credentials.go:276-283):
  - Includes ID, Name, URL, IsActive, Secret (as sql.NullString), SecretCiphertext
  - Secret deliberately preserves NULL-ness rather than normalizing with COALESCE
  - This proves that runtime writes set concrete empty string (not NULL) or ciphertext

- [x] **readWebhookRowStorage()** (webhook_credentials.go:285-304):
  - Reads full row including metadata inside transaction
  - Preserves NULL state of secret column via sql.NullString
  - Returns gorm.ErrRecordNotFound for concurrent delete detection

- [x] **verifyWebhookRowStorage()** (webhook_credentials.go:306-329):
  - Strict equality check: `if stored != want`
  - Fails closed with ErrWebhookCredentialStorageMismatch on any column mismatch
  - Catches silent failures and concurrent mutations together
  - Distinguishes from MySQL no-op where RowsAffected=0 is valid

- [x] **PostWebhook verification** (webhook.go:179-187):
  - Changed from `verifyWebhookCredentialStorage()` to `verifyWebhookRowStorage()`
  - Verifies concrete ID, Name, URL, IsActive, empty Secret string, and SecretCiphertext
  - Proves all intended columns persisted

- [x] **PutWebhook verification** (webhook.go:307-314):
  - Changed from `verifyWebhookCredentialStorage()` to `verifyWebhookRowStorage()`
  - Verifies all columns including metadata (Name, URL, IsActive)
  - Prevents silent metadata loss when RowsAffected reports 0 change

### 3. NULL Handling Test Coverage

Six comprehensive test functions verify NULL-secret behavior across all scenarios:

- [x] **TestWebhookNullLegacySecretPreserveClearReplace** (webhook_credentials_test.go:811):
  - Tests preserve, clear, replace on NULL-secret rows
  - Both NULL + ciphertext and NULL + no-ciphertext seeds
  - Verifies each operation sets concrete empty string (not NULL) for legacy column
  - Verifies final credential state matches intent

- [x] **TestWebhookNullLegacySecretMetadataUpdatePersistsAndExcludesFromActiveDelivery** (webhook_credentials_test.go:917):
  - Core regression test for security finding
  - Creates active webhook with NULL secret
  - Deactivates it alongside metadata change (name, URL)
  - Verifies: persisted metadata change in storage
  - Verifies: GetActiveWebhooks excludes deactivated webhook
  - Verifies: ActiveWebhookEndpoints excludes deactivated webhook (prevents delivery)
  - Proves administrator action (deactivation) actually persists

- [x] **TestWebhookNullLegacySecretNoOpUpdateSucceeds** (webhook_credentials_test.go:971):
  - Proves MySQL changed-row semantics don't introduce spurious failures
  - Identical name/URL/active/preserved-ciphertext resubmission on NULL-secret row
  - Verifies: update succeeds (does not fail as concurrent change)
  - Verifies: row unchanged after update

- [x] **TestWebhookNullLegacySecretConcurrentChangeFailsClosed** (webhook_credentials_test.go:1000):
  - Proves NULL-aware predicate fix does not weaken concurrency detection
  - Silent-mutation trigger tampering with ciphertext after update
  - Verifies: update fails with ErrWebhookCredentialStorageMismatch
  - Verifies: transaction rolled back completely (metadata and ciphertext unchanged)
  - Verifies: NULL legacy state restored (no spurious normalization)

- [x] **TestRollbackWebhookSecretsAcceptsNullLegacySecretWithCiphertext** (webhook_credentials_test.go:1045):
  - Proves rollback accepts valid NULL+ciphertext states
  - Seeds encrypted webhook, forces legacy secret to NULL
  - Performs rollback: decrypts ciphertext, writes plaintext, clears ciphertext
  - Verifies: plaintext restored correctly
  - Verifies: ciphertext cleared
  - Verifies: idempotent re-run is safe no-op

- [x] **TestMySQLWebhookCredentialNullLegacySecretLifecycle** (webhook_credentials_test.go:1092):
  - Real MySQL 8.4.11 counterpart of SQLite NULL tests
  - Tests preserve/clear/replace with NULL source (both +/- ciphertext)
  - Tests deactivation of NULL-secret webhook (metadata persistence + delivery exclusion)
  - Tests no-op resubmission under MySQL's default changed-rows RowsAffected semantics
  - Tests rejection of missing row (not mistaken for no-op)
  - Tests rejection of ambiguous plaintext+ciphertext conflict state

### 4. Test Helper Infrastructure

- [x] **forceNullWebhookSecret()** (webhook_credentials_test.go:789):
  - Simulates pre-existing/imported/partially-restored NULL-secret rows
  - PostWebhook and PutWebhook never produce NULL on their own
  - Validates test setup actually produced NULL (not empty string)

### 5. CI Integration

- [x] **.github/workflows/ci.yml MySQL test update** (line 331):
  - Original pattern: `^TestMySQL(DriverIntegration|IMAPCredentialLifecycle|SMTPCredentialLifecycle|SMTPCredentialStorageBoundsNonStrict)$`
  - Updated: `^TestMySQL(DriverIntegration|IMAPCredentialLifecycle|SMTPCredentialLifecycle|SMTPCredentialStorageBoundsNonStrict|WebhookCredential.*)$`
  - Now runs: TestMySQLWebhookCredential* tests including all NULL handling tests
  - Verified: Regex pattern correctly captures TestMySQLWebhookCredentialNullLegacySecretLifecycle

### 6. Accepted State Transitions

The following matrix confirms all valid NULL/empty state transitions now succeed:

| Current State | Intent | Result | Secret Column | Ciphertext Column |
|---|---|---|---|---|
| NULL (no-secret) | Preserve | ✅ | → "" | unchanged |
| NULL (no-secret) | Clear | ✅ | → "" | → "" |
| NULL (no-secret) | Replace(X) | ✅ | → "" | → encrypted(X) |
| NULL + encrypted | Preserve | ✅ | → "" | unchanged |
| NULL + encrypted | Clear | ✅ | → "" | → "" |
| NULL + encrypted | Replace(Y) | ✅ | → "" | → encrypted(Y) |
| "" (no-secret) | Preserve | ✅ | "" | unchanged |
| "" (no-secret) | Clear | ✅ | "" | unchanged |
| "" (no-secret) | Replace(Z) | ✅ | "" | → encrypted(Z) |
| "" + encrypted | Preserve | ✅ | "" | unchanged |
| "" + encrypted | Clear | ✅ | "" | → "" |
| "" + encrypted | Replace(W) | ✅ | "" | → encrypted(W) |

### 7. Rejected/Ambiguous States

The following invalid or ambiguous states remain fail-closed:

- [x] **Non-empty plaintext + ciphertext**: Detected by validateWebhookSecretColumns()
  - Indicates out-of-sync state or incomplete migration
  - Rejected before any transaction begins
  - Prevents accidental ciphertext overwrite or plaintext re-encryption

- [x] **Concurrent row deletion**: Detected by gorm.ErrRecordNotFound from readWebhookRowStorage()
  - Concurrent delete between caller's read and transaction's update
  - Returns clean error, no partial updates

- [x] **Concurrent metadata mutation**: Detected by verifyWebhookRowStorage() full comparison
  - Silent trigger or concurrent update changing Name/URL/IsActive/ciphertext
  - Transaction rolled back, no partial state persisted

### 8. Backward Compatibility

- [x] **Create absent/null/empty semantics unchanged**:
  - WebhookSecretPreserve on create still produces no-secret webhook
  - WebhookSecretClear on create still produces no-secret webhook
  - Both result in empty string in secret column (not NULL)

- [x] **HMAC delivery unchanged**:
  - ActiveWebhookEndpoints decrypts at boundary, no change
  - No-secret webhooks still sign with empty-key HMAC
  - Migrated secrets produce identical signatures

- [x] **API contracts preserved**:
  - Write-only responses (no plaintext/ciphertext/key-ID leakage)
  - Tri-state decoder (absent/null/empty/non-empty) unchanged
  - PermissionModifySystem enforcement unchanged

- [x] **Migration/rollback contracts preserved**:
  - Offline operations remain transactional
  - Preflight validation unchanged
  - Rollback never invokes Goose Down

## Quality Gates

- [x] **Code structure**: NULL-aware predicates applied consistently to all secret-column writes
- [x] **Test coverage**: 6 new test functions (538+ new test lines total in webhook_credentials_test.go)
- [x] **Storage correctness**: Full-row verification proves all intended columns persist
- [x] **Database support**: Both SQLite and real MySQL 8.4.11 covered
- [x] **Concurrency safety**: Silent mutations detected and rolled back
- [x] **CI integration**: MySQL webhook tests enabled and configured
- [x] **No regressions**: Iteration-1 acceptance criteria remain satisfied
- [x] **No new findings**: No dependency changes, no new error paths, no API changes

## Issues Found

None. All security finding requirements have been addressed with comprehensive code changes
and thorough test coverage.

## Verification Method

1. **Code inspection**: Confirmed NULL-aware predicates in all three mutation paths
2. **Struct inspection**: Verified storedWebhookRow includes metadata fields and preserves NULL
3. **Function inspection**: Confirmed verifyWebhookRowStorage does strict full-row comparison
4. **Test inspection**: Located and reviewed all 6 NULL handling test functions
5. **CI inspection**: Confirmed MySQL test pattern includes WebhookCredential.* regex
6. **Acceptance matrix**: Traced all valid state transitions through code path
7. **Backward compatibility**: Confirmed no breaking changes to existing contracts

## Why This Matters

This correction prevents a class of data-integrity bugs where administrators receive false
success confirmations for operations that silently fail to persist (e.g., deactivating a
webhook that should no longer receive campaign events). By treating legacy NULL and empty
string as identical valid states in all predicates, and verifying all intended columns
(not just credentials) persist inside the same transaction, the system now guarantees that
metadata changes always succeed or fail atomically—never silently no-op while reporting
success.

The comprehensive test coverage ensures this behavior is explicitly validated against both
SQLite and real MySQL, including MySQL's non-standard changed-row RowsAffected semantics,
which could mask silent failures if only row counts were checked.
