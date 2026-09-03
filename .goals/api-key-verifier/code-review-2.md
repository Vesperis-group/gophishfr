# Code Review Feedback — Iteration 2

## Verdict: FAIL

The first code-review report about executable modes was rechecked against the
committed range and proven to be a transient UNC working-tree artifact: every
entrypoint/script remains `100755`. The clean committed-range review then found
one real migration-integrity defect.

## Finding — General user updates corrupt LEGACY verifier state

**Severity:** Medium  
**Affected path:** `models/user.go`

`PutUser` uses GORM v1 full-row `Save`. A LEGACY row loads nullable verifier
columns into Go zero values. Login/settings updates therefore persist an empty
string for `api_key_verifier_key_id`, changing the valid state
`(api_key non-NULL, verifier NULL, key_id NULL)` into
`(api_key non-NULL, verifier NULL, key_id '')`.

The explicit offline migrator correctly classifies this as invalid and refuses
the row, so merely logging in before migration can make migration impossible.

## Required correction

- Replace general `PutUser` full-row Save with an explicit allowlist of ordinary
  mutable user columns that never writes legacy API-key, verifier, or verifier
  key-ID state.
- Preserve create semantics where a new user must be inserted with verifier-only
  state through the dedicated issuance path.
- Keep verifier/reset/lazy/migration writes in dedicated guarded functions.
- Audit every `PutUser` caller (login last-login, password reset, role/profile
  updates, user API PUT) and prove required fields still persist without secret
  columns.
- Add SQLite and real MySQL tests:
  valid LEGACY row + login/PutUser remains byte/NULL-identical and migrates;
  MIGRATED row + ordinary updates preserves verifier/key ID exactly;
  user role/password/last-login/profile updates still work;
  malicious/general DTO cannot overwrite verifier state;
  reset/dedicated verifier operations still replace atomically.
- Re-run full migration, bootstrap, auth matrix, browser, Docker, race and
  scanner gates.
