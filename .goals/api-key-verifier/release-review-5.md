# Release Code Review Feedback — Iteration 5

## Verdict: FAIL

The final security review returned PASS. The complete-diff code review found one
legacy-compatibility issue in offline migration.

## Finding — Migration imposes an invalid 255-byte limit

Legacy `users.api_key` is `VARCHAR(255)`, but SQLite does not enforce that
declared length and MySQL limits 255 characters rather than 255 UTF-8 bytes.
The migration's byte-length check rejects valid multibyte tokens and globally
blocks migration.

The HMAC verifier accepts exact arbitrary token bytes and needs no such client
contract.

## Required correction

- Remove the incompatible 255-byte preflight rule.
- HMAC the exact non-empty bytes already stored; never normalize, trim, fold, or
  truncate.
- Add SQLite tests for legacy tokens beyond 255 bytes and multibyte tokens.
- Add real MySQL tests for 255 multibyte characters whose UTF-8 byte length is
  greater than 255, preserving exact token authentication after migration.
- Characterize strict/non-strict MySQL over-limit insertion, but migrate exact
  readable stored data rather than imposing a new limit.
- Re-run migration atomicity/idempotence, token compatibility, browser, Docker,
  race, fuzz, reproducibility and all gates.
