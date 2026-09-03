# Security Review Feedback — Iteration 1

## Verdict: FAIL

An independent security specialist found two high-confidence issues after the
Goal Inspector's first PASS. Both are directly within the immutable goal and
must be corrected before reinspection.

## Finding 1 — Stored secret can be redirected

**Severity:** High
**Confidence:** 9/10
**Affected paths:** `controllers/api/util.go`, `models/smtp.go`

When a request preserves/reuses an existing stored password, it can combine that
decrypted secret with client-controlled SMTP host, username, and TLS policy.
This permits an authenticated user to redirect a stored password to an
attacker-controlled SMTP server. The same issue exists when PUT preserves a
ciphertext while changing connection/authentication fields.

**Required correction:**

- A request that reuses an existing stored password must use the stored
  connection/authentication context.
- For persistent PUT, reject changes to host, username, or TLS policy unless a
  non-empty replacement password is supplied in the same request.
- For test-email with empty/absent password, do not combine stored ciphertext
  with client-controlled host, username, or TLS settings; use the authorized
  stored profile's values or reject mismatches.
- Add regression tests proving a preserved stored password cannot be redirected
  by changing host, username, TLS policy, or equivalent authentication-routing
  fields, and proving rotation with a non-empty replacement remains possible.

## Finding 2 — MySQL truncation can destroy the only secret copy

**Severity:** Medium
**Confidence:** 8/10
**Affected path:** `models/smtp_credentials.go` and SMTP schema/write paths

The legacy MySQL `password` column is `VARCHAR(255)`, while encrypted input is
not bounded to a legacy-safe plaintext length. In non-strict MySQL mode,
rollback can silently truncate a long plaintext and clear its ciphertext,
destroying the only recoverable copy. Ciphertext writes can similarly exceed
their declared storage capacity without a post-write value check.

**Required correction:**

- Define and enforce explicit UTF-8 byte-length bounds compatible with the
  legacy plaintext column and ciphertext column before encryption and rollback.
- Ensure ciphertext capacity is derived conservatively from the envelope and
  plaintext bound.
- Re-read and compare stored values inside the same transaction before commit
  for create, rotate, migration, and rollback so silent truncation cannot be
  accepted.
- Add boundary regression tests, including real MySQL in non-strict mode, for
  maximum accepted length and first rejected length.
- Never clear ciphertext during rollback unless the full plaintext has been
  proven stored byte-for-byte.
