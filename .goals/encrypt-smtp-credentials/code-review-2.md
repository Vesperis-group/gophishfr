# Final Code Review Feedback — Iteration 2

## Verdict: FAIL

A final high-confidence read-only review found three correctness/data-integrity
issues. They are within the immutable goal and require Builder iteration 3.

## Finding 1 — SQLite Down can reuse deleted IDs

**Severity:** Medium
**Path:** `db/db_sqlite3/migrations/20260903010000_encrypt_smtp_credentials.sql`

Rebuilding `smtp` during Down resets `sqlite_sequence` to the highest surviving
ID rather than preserving the prior high-water mark. If a highest-ID SMTP
profile was deleted but its historical ID remains referenced by a campaign, a
new profile after rollback can reuse that ID and make the campaign resolve to
the wrong profile.

**Required correction:**

- Preserve the pre-Down `smtp` sequence high-water mark across table rebuild.
- Restore a value at least as high as both the prior sequence and current max ID.
- Add a SQLite regression test that creates IDs, deletes the highest ID,
  performs credential rollback and schema Down, creates a profile, and proves
  the deleted ID is not reused and historical references cannot retarget.

## Finding 2 — Test email discards safe unsaved edits

**Severity:** Medium
**Path:** `controllers/api/util.go`

For any existing profile with an empty request password, assigning the complete
stored SMTP model discards all submitted form fields. Test email therefore
ignores safe unsaved fields such as From address and headers. For a no-secret
profile it unnecessarily ignores host, username, interface, and TLS changes
even though no stored credential can be redirected.

**Required correction:**

- When the authorized stored profile has ciphertext and request password is
  empty/absent, bind only the stored credential plus all routing/authentication
  fields needed to prevent redirect; retain safe submitted fields such as From
  address and headers.
- When the authorized stored profile has no secret, use the submitted context;
  there is no stored credential to protect.
- Keep a non-empty request password runtime-only and compatible with submitted
  connection fields.
- Add controller/browser/dialer regression tests for safe unsaved fields,
  encrypted stored routing context, and editable no-secret profiles.

## Finding 3 — Valid MySQL no-op PUT can fail

**Severity:** Medium
**Path:** `models/smtp.go`

`PutSMTP` currently requires `RowsAffected == 1`. MySQL reports changed rows by
default, not matched rows. A header-only or otherwise identical update inside
the same second can change no SMTP column because `modified_date` has
second-level precision, yielding zero affected rows and causing a false
not-found/conflict error before headers are updated.

**Required correction:**

- Treat zero changed rows as success only after querying inside the same
  transaction and proving the owner-scoped row plus all expected concurrency/
  preservation predicates still match.
- Continue failing closed for missing rows, wrong owner, or changed routing
  context.
- Add a real MySQL regression test, including a header-only/no-op update in the
  same timestamp second, and prove ownership and concurrency guards remain
  effective.
