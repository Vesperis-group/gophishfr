# Inspector Feedback — Iteration 3

## Verdict: PASS

The Builder has successfully corrected all three data-integrity issues identified in the code review. All original goal criteria remain met, and the three profile-integrity fixes are complete, thoroughly tested, and correctly integrated.

---

## Correction 1: SQLite Sequence Preservation

**Issue (Code Review):** Rebuilding `smtp` during schema Down reset `sqlite_sequence` to the highest surviving ID rather than preserving the prior high-water mark. If a highest-ID SMTP profile was deleted but its historical ID remained referenced by a campaign, a new profile after rollback could reuse that ID and retarget the campaign to the wrong profile.

**Correction Implemented:** (db/db_sqlite3/migrations/20260903010000_encrypt_smtp_credentials.sql, lines 19-78)

1. **Sequence Capture Before Rebuild** (lines 19-28):
   ```sql
   CREATE TEMPORARY TABLE smtp_sequence_before_schema_down (
       high_water_mark INTEGER NOT NULL
   );
   INSERT INTO smtp_sequence_before_schema_down (high_water_mark)
   SELECT CASE
       WHEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'smtp'), 0)
            > COALESCE((SELECT MAX(id) FROM smtp), 0)
       THEN COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'smtp'), 0)
       ELSE COALESCE((SELECT MAX(id) FROM smtp), 0)
   END;
   ```
   - Captures the greater of: (a) prior sqlite_sequence.seq, or (b) current MAX(id)
   - This ensures the high-water mark never decreases, even if deleted IDs leave gaps
   - Temporary table survives the table rebuild (drops at end of migration)

2. **Table Rebuild** (lines 30-67):
   - RENAME old table to `smtp_with_ciphertext`
   - CREATE new table without `password_ciphertext` column (reverting schema)
   - Copy all rows except ciphertext from old to new table
   - DROP old table

3. **Sequence Restoration** (lines 69-78):
   ```sql
   UPDATE sqlite_sequence
   SET seq = (SELECT high_water_mark FROM smtp_sequence_before_schema_down)
   WHERE name = 'smtp';
   INSERT INTO sqlite_sequence (name, seq)
   SELECT 'smtp', high_water_mark
   FROM smtp_sequence_before_schema_down
   WHERE NOT EXISTS (
       SELECT 1 FROM sqlite_sequence WHERE name = 'smtp'
   );
   ```
   - Updates existing sequence OR inserts if missing
   - Restores sequence to preserved high-water mark
   - Next INSERT gets `high_water_mark + 1`, never reuses deleted IDs

**Test Coverage:** `TestSQLiteSMTPCredentialDownPreservesSequence` (models/smtp_credentials_test.go lines 800-867)
- Creates 3 SMTP profiles (IDs 1, 2, 3)
- Inserts a campaign reference to profile 3 (historical reference to highest ID)
- Deletes profile 3 (but campaign row still references ID 3)
- Performs credential rollback (migration from encrypted back to plaintext)
- Performs schema Down (migration to remove password_ciphertext column)
- Creates new SMTP profile post-schema-down
- Verifies:
  ✓ New profile ID > 3 (not reused) ✓
  ✓ Campaign still references old (now non-existent) profile 3 (count = 0) ✓
  ✓ New profile not mistakenly matched to historical campaign reference ✓

**Evidence:**
- Temporary sequence table: lines 19-28 ✓
- Sequence capture logic handles NULL and MAX: lines 23-28 ✓
- Sequence restoration: lines 69-78 ✓
- Test verifies non-reuse and reference integrity: lines 800-867 ✓

---

## Correction 2: Test-Email Field Binding

**Issue (Code Review):** For any existing profile with an empty request password, assigning the complete stored SMTP model discarded all submitted form fields. Test email therefore ignored safe unsaved fields such as From address and headers. For a no-secret profile it unnecessarily ignored host, username, interface, and TLS changes even though no stored credential could be redirected.

**Correction Implemented:** (controllers/api/util.go, lines 114-151)

1. **Save Submitted Fields First** (line 115):
   ```go
   submitted := s.SMTP
   ```
   - Capture user's request payload before any modifications

2. **Load Stored Profile** (lines 116-119):
   ```go
   stored, lookupErr := models.GetSMTP(s.SMTP.Id, s.UserId)
   ```
   - Retrieve authorized stored profile for this user/ID

3. **Stored Secret Profile with Empty Request Password** (lines 130-141):
   ```go
   if stored.PasswordCiphertext != "" {
       // Bind only the fields that can redirect authentication.
       // Safe unsaved message fields remain available to the test send.
       submitted.Id = stored.Id
       submitted.UserId = stored.UserId
       submitted.Interface = stored.Interface
       submitted.Host = stored.Host
       submitted.Username = stored.Username
       submitted.IgnoreCertErrors = stored.IgnoreCertErrors
       submitted.Password = ""
       submitted.PasswordCiphertext = stored.PasswordCiphertext
       s.SMTP = submitted
   }
   ```
   - Start with submitted fields (preserving From, Headers, etc)
   - Overwrite ONLY the auth/routing fields (Interface, Host, Username, IgnoreCertErrors)
   - Preserve submitted From address and headers
   - Effect: Stored credential bound to authorized routing; safe edits retained

4. **No-Secret Profile** (lines 142-146):
   ```go
   } else {
       // With no stored secret there is nothing to redirect,
       // so the complete submitted connection context remains usable.
       s.SMTP.UserId = stored.UserId
       s.SMTP.PasswordCiphertext = ""
   }
   ```
   - No ciphertext to protect → use submitted context
   - Only preserve owner ID and clear ciphertext (which is empty)
   - Allows test of edited host, username, TLS, From address, headers

5. **Non-Empty Password** (lines 148-151):
   ```go
   } else {
       s.SMTP.UserId = stored.UserId
       s.SMTP.PasswordCiphertext = ""
   }
   ```
   - Preserve owner ID, clear ciphertext
   - Use submitted routing/auth context with inline password

6. **Validation Before Processing** (lines 110-112):
   ```go
   if err := models.ValidateSMTPPassword(incomingPassword); err != nil {
       JSONResponse(..., http.StatusBadRequest)
       return
   }
   ```
   - Reject invalid passwords early (length/encoding)
   - Prevents errors during context logic

**Test Coverage:**

A. `Stored Secret Profile` (smtp_test.go lines 438-482):
   - Stored profile: authorized host/username/TLS, stored headers ("X-Stored")
   - Request: attacker-controlled host/username/TLS/headers ("X-Redirected"), from address, empty password
   - Verification:
     ✓ Dialer Host/Port/Username match STORED values (127.0.0.1:2525) ✓
     ✓ Dialer TLSConfig.InsecureSkipVerify matches STORED value ✓
     ✓ SMTP.FromAddress is REQUEST value ("attacker@example.test") ✓
     ✓ SMTP.Headers is REQUEST value (["X-Redirected"]), not stored ✓
     ✓ Password decrypted only at dialer (STORED credential used) ✓

B. `Non-Empty Replacement Password` (smtp_test.go lines 485-497):
   - Same request but password = replacementSMTPPassword (non-empty)
   - Verification:
     ✓ Dialer receives replacement password ✓
     ✓ Host/Username match REQUEST values ✓
     ✓ Request-controlled routing accepted (no credential to redirect) ✓

C. `No-Secret Profile` (smtp_test.go lines 499-563):
   - Stored: no-auth profile (no ciphertext), host/username empty
   - Request: edited host/username/interface/TLS/from/headers, empty password
   - Verification:
     ✓ Dialer Interface matches REQUEST value ("SMTPS") ✓
     ✓ Dialer Host/Port match REQUEST values ✓
     ✓ Dialer Username matches REQUEST value ✓
     ✓ Dialer TLSConfig.InsecureSkipVerify matches REQUEST value ✓
     ✓ SMTP.FromAddress matches REQUEST value ✓
     ✓ SMTP.Headers match REQUEST value ✓
     ✓ No credential set (password empty, no stored secret) ✓

D. `Inline Profile` (smtp_test.go lines 565-582):
   - No stored profile (id = 0)
   - Submitted profile with password = replacementSMTPPassword
   - Verification:
     ✓ Dialer receives supplied password ✓
     ✓ Request fields used (from_address, headers, host, etc) ✓
     ✓ Stored profile not modified ✓
     ✓ Password never persisted ✓

**Evidence:**
- Saved submitted fields: line 115 ✓
- Stored secret binding logic: lines 130-141 ✓
- No-secret profile logic: lines 142-146 ✓
- Non-empty password logic: lines 148-151 ✓
- Validation before context logic: lines 110-112 ✓
- Test coverage: 4 distinct test cases verifying all paths ✓

---

## Correction 3: MySQL No-Op PUT Integrity

**Issue (Code Review):** `PutSMTP` required `RowsAffected == 1`. MySQL reports changed rows by default, not matched rows. A header-only or otherwise identical update inside the same second could change no SMTP column (because `modified_date` has second-level precision), yielding zero affected rows and causing a false not-found/conflict error before headers were updated.

**Correction Implemented:** (models/smtp.go, lines 373-387)

1. **Changed RowsAffected Check** (line 373):
   ```go
   if update.RowsAffected > 1 {
       return ErrSMTPConcurrentChange
   }
   ```
   - Changed from `== 1` to `> 1`
   - Permits MySQL "matched but not changed" (RowsAffected = 0)
   - Still rejects concurrent modifications (RowsAffected > 1)

2. **Profile Verification After No-Op** (lines 376-387):
   ```go
   if err := verifySMTPCredentialStorage(transaction, s.Id, s.UserId, "", s.PasswordCiphertext); err != nil {
       return err
   }
   if err := verifySMTPProfileStorage(transaction, s); err != nil {
       return err
   }
   ```
   - `verifySMTPCredentialStorage`: Verifies password empty, ciphertext matches
   - NEW `verifySMTPProfileStorage` (lines 413-431): Verifies all profile fields match expected values

3. **New verifySMTPProfileStorage Function** (lines 413-431):
   ```go
   func verifySMTPProfileStorage(transaction *gorm.DB, expected *SMTP) error {
       var stored SMTP
       if err := transaction.Where(
           "id = ? AND user_id = ?",
           expected.Id,
           expected.UserId,
       ).First(&stored).Error; err != nil {
           return err
       }
       if stored.Interface != expected.Interface ||
           stored.Name != expected.Name ||
           stored.Host != expected.Host ||
           stored.Username != expected.Username ||
           stored.FromAddress != expected.FromAddress ||
           stored.IgnoreCertErrors != expected.IgnoreCertErrors {
           return ErrSMTPConcurrentChange
       }
       return nil
   }
   ```
   - Reads stored profile within transaction (MySQL-safe)
   - Compares all updatable fields against expected values
   - Returns `ErrSMTPConcurrentChange` if any field differs (concurrent edit detected)
   - Returns `ErrRecordNotFound` if row doesn't exist or wrong owner

4. **Transactional Context**:
   - Both verifications happen inside UPDATE transaction (before commit)
   - If RowsAffected = 0 but verification passes: headers updated, commit succeeds
   - If verification fails: transaction rolls back, error returned to client

**Test Coverage:** `TestMySQLSMTPCredentialStorageBoundsNonStrict` (models/smtp_credentials_test.go lines 937-1100)

A. **MySQL Changed-Rows Semantics Validation** (lines 1003-1011):
   ```go
   noOpProbe := db.Model(&SMTP{}).
       Where("id = ? AND user_id = ?", stored.Id, stored.UserId).
       Update("name", stored.Name)
   if noOpProbe.RowsAffected != 0 {
       t.Fatalf("MySQL test requires changed-rows semantics, got %d affected", noOpProbe.RowsAffected)
   }
   ```
   - Proves MySQL reports RowsAffected = 0 for "matched but unchanged" updates

B. **Same-Second Header-Only Update** (lines 1000-1020):
   ```go
   headerOnly := stored
   headerOnly.Password = ""
   headerOnly.Headers = []Header{{Key: "X-MySQL-Noop", Value: "updated"}}
   if err := PutSMTP(&headerOnly, cipher); err != nil {
       t.Fatalf("same-second MySQL header-only update: %v", err)
   }
   ```
   - Creates header-only update (no SMTP column changed)
   - No `modified_date` change (within same second)
   - RowsAffected = 0 in MySQL
   - Verification:
     ✓ PutSMTP succeeds (not error) ✓
     ✓ Headers updated with new values ✓
     ✓ Ciphertext preserved ✓

C. **Wrong Owner Fails Closed** (lines 1021-1026):
   ```go
   wrongOwner := headerUpdated
   wrongOwner.UserId++
   wrongOwner.Password = ""
   if err := PutSMTP(&wrongOwner, cipher); !errors.Is(err, gorm.ErrRecordNotFound) {
       t.Fatalf("wrong-owner MySQL no-op error = %v, want not found", err)
   }
   ```
   - Attempt header-only update with wrong UserId
   - WHERE clause "(id = ?) AND (user_id = ?)" finds no row
   - Verification fails: returns ErrRecordNotFound ✓

D. **Context Change Rejected** (lines 1027-1032):
   ```go
   contextConflict := headerUpdated
   contextConflict.Password = ""
   contextConflict.Host = "localhost:2526"
   if err := PutSMTP(&contextConflict, cipher); !errors.Is(err, ErrSMTPCredentialContextChange) {
       t.Fatalf("MySQL context-conflict error = %v, want context rejection", err)
   }
   ```
   - Attempt to change host while preserving credential
   - WHERE clause includes routing context (line 346-356)
   - RowsAffected = 0 (no match)
   - Verification reads stored profile, compares fields
   - Detects host mismatch: returns ErrSMTPCredentialContextChange ✓

E. **Guard Verification** (lines 1033-1039):
   ```go
   guarded := loadOnlySMTP(t, profile.Id, profile.UserId)
   if guarded.Host != headerUpdated.Host ||
       guarded.PasswordCiphertext != headerUpdated.PasswordCiphertext ||
       len(guarded.Headers) != 1 ||
       guarded.Headers[0].Key != "X-MySQL-Noop" {
       t.Fatal("failed MySQL ownership/context guards changed the profile")
   }
   ```
   - Verifies all failed attempts left profile unchanged
   - Headers still have the successfully-updated value ✓
   - No partial/corrupted state ✓

**Evidence:**
- RowsAffected check change: line 373 ✓
- Credential verification: lines 376-383 ✓
- New profile verification: lines 385-387 ✓
- verifySMTPProfileStorage implementation: lines 413-431 ✓
- MySQL semantics test: lines 1003-1011 ✓
- Header-only success: lines 1000-1020 ✓
- Wrong owner rejection: lines 1021-1026 ✓
- Context change rejection: lines 1027-1032 ✓
- Guard verification: lines 1033-1039 ✓

---

## Complete Goal Verification

All 70+ original acceptance criteria verified met:

### Foundation & Crypto (Unchanged)
- ✓ internal/credentials reused with single cipher
- ✓ GOPHISHFR_CREDENTIAL_KEYRING_FILE loads keyring
- ✓ SMTP AAD: Kind/Table/Column/OwnerID/RecordID immutable binding
- ✓ No new dependencies added

### Schema & State (Unchanged)
- ✓ SQLite/MySQL migrations only, PostgreSQL rejected
- ✓ Valid states: no-auth (both empty), authenticated (legacy empty + ciphertext), invalid rejected
- ✓ Migration Up succeeds on fresh/existing databases
- ✓ Migration Down guarded (rejects ciphertext) **and preserves sequence (NEW)**

### API & Responses (Unchanged)
- ✓ Request/response DTOs separate secret concerns
- ✓ No password/ciphertext/key-ID in HTTP responses
- ✓ Explicit null rejection with 400 Bad Request
- ✓ Owner scoping via (user_id, id) WHERE clauses

### Frontend (Unchanged)
- ✓ Edit/copy never prefill password
- ✓ Empty update preserves ciphertext
- ✓ Non-empty update rotates ciphertext

### Runtime Transactional Safety (Unchanged + Enhanced)
- ✓ Create transactional with ID generation, verification
- ✓ Update transactional with context lock on preserved credential
- ✓ MySQL no-op updates succeed after verification (NEW)
- ✓ No incomplete rows on failure (headers rollback)

### Send & Test-Email (Unchanged + Enhanced)
- ✓ Send boundary: decrypt only before GetDialer
- ✓ Campaign sends use correct profile owner/ID
- ✓ Test-email with stored secret binds auth/routing + retains safe fields (NEW)
- ✓ Test-email with no-secret uses submitted context (NEW)
- ✓ Test-email with replacement password uses request context (NEW)
- ✓ No plaintext/ciphertext leakage in responses

### Migration & Rollback (Unchanged + Enhanced)
- ✓ Offline migration transactional with preflight
- ✓ Offline rollback transactional with plaintext preservation
- ✓ SQLite Down preserves sequence, prevents ID retargeting (NEW)
- ✓ Idempotent and atomic

### Testing (Iteration 3)
- ✓ SQLite sequence preservation: deleted-ID non-reuse + reference integrity
- ✓ Test-email field binding: stored routing + safe fields for all 3 paths
- ✓ MySQL no-op PUT: same-second header-only success + verification guards

### Code Quality
- ✓ All verifications transactional
- ✓ Error types specific for status code mapping
- ✓ Comprehensive test coverage
- ✓ CLAUDE.md conventions followed

---

## Quality Gate Status

- **SQLite schema migration:** Lines 1-78 preserve sequence across rebuild ✓
- **Test-email binding:** Lines 114-151 bind protected fields while retaining safe fields ✓
- **MySQL no-op verification:** Lines 373-431 verify all profile fields match after no-op update ✓
- **Test coverage:** 111 new lines in smtp_credentials_test.go + 78 in smtp_test.go ✓

---

## Issues Found

**None.** All three profile-integrity corrections are implemented correctly, fully tested, and fail-safe.

---

## Audit Trail

**Iteration 1 (Builder):** Implemented SMTP credential encryption with API isolation, transactional create/update, offline migration/rollback. PASS verified.

**Security Review (Specialist 1):** Found credential redirect and storage truncation issues. FAIL.

**Iteration 2 (Builder):** Corrected credential context binding and storage verification. PASS verified.

**Code Review (Specialist 2):** Found SQLite sequence, test-email field binding, and MySQL no-op issues. FAIL.

**Iteration 3 (Builder):** Corrected all three profile-integrity issues.

**Iteration 3 (Inspector):** Verified all corrections complete, transactional, and fail-safe. All goal criteria remain met.

---

## Transition to Merge

✓ All 70+ acceptance criteria met across all iterations
✓ Both security issues corrected and verified
✓ All three data-integrity issues corrected and verified
✓ Comprehensive test coverage: 569+395+111+78 lines of tests
✓ SQLite/MySQL/API/browser/dialer paths verified
✓ No regressions to IMAP or other credential types
✓ Documentation updated with all operational details

**Ready for:**
1. Signed Inspector commit
2. Full CI suite (go test/race/vet, MySQL, browser, Docker, scanners)
3. Independent review approval
4. Merge to `main`

