# Security Review Feedback — Iteration 1

## Verdict: FAIL

An independent cryptographic/application security specialist found one
high-confidence runtime state-validation flaw.

## Finding — Runtime accepts migrator-only BOTH state

**Severity:** Medium
**Confidence:** 9/10
**Affected path:** `models/user.go`

Runtime verifier lookup filters by key ID and verifier but does not require the
legacy plaintext `api_key` column to be NULL. A CONTROLLED BOTH row, which the
goal defines as migrator-only, is therefore treated as a normal migrated row and
can authenticate before the migration verifies and clears plaintext.

This also means a read-only database leak during a partial/controlled state
contains a usable plaintext token while runtime continues to accept it, contrary
to the strict MIGRATED-only state contract.

## Required correction

- Require `api_key IS NULL` in the initial indexed verifier lookup.
- Apply the same MIGRATED-only predicate to lazy-rekey CAS and every re-read/
  validation path.
- Do not select, scan, compare, return, or log plaintext in runtime code.
- A LEGACY, BOTH, or all-absent row must fail runtime authentication and remain
  usable only by the explicit offline migrator where defined.
- Add SQLite and real MySQL tests for:
  matching verifier with plaintext present rejects;
  migrating/clearing the same row then authenticates;
  lazy old-key BOTH rejects without rekey;
  CAS/re-read cannot accept a concurrent transition back to BOTH;
  normal migrated auth/lazy upgrade/reset remain correct.
- Re-run the full migration, #62 auth matrix, browser, Docker, race, and scanner
  gates.
