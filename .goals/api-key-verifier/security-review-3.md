# Security Review Feedback — Iteration 3

## Verdict: FAIL

An independent final security review found one high-confidence authorization
gap.

## Finding — Locked accounts retain API-key access

**Severity:** High  
**Confidence:** 9/10  
**Affected path:** `models/user.go` / API-key authentication

Verifier lookup returns users regardless of `account_locked`. A locked
administrator's existing token therefore retains full API privileges even
though password login explicitly rejects that account. Newly created or updated
locked users can likewise retain verifier-backed access.

## Required correction

- Reject `account_locked` users during API-key authentication before returning a
  successful identity.
- Prefer a predicate in the indexed lookup and retain a defensive post-load
  validation if useful; do not leak whether the account exists or is locked.
- Apply the same lock condition to lazy-rekey rereads/CAS so a concurrent lock
  cannot permit continued verifier upgrade/authentication.
- Define concurrency explicitly:
  if the account becomes locked before authentication returns, reject and do not
  lazy-rekey; if lock commits after the request is already authenticated, normal
  per-request race semantics apply.
- Preserve session behavior from PR #62; do not redesign session invalidation in
  this PR unless a directly coupled bypass is proven.
- Add SQLite and real MySQL tests:
  migrated locked user rejects Bearer/raw/query/form;
  locked admin verifier rejects;
  unlock restores the same token;
  lock during lazy rekey rejects/no verifier change;
  reset/create for a locked user does not make authentication usable while
  locked;
  generic API error remains indistinguishable from invalid key;
  all explicit/session selection gates remain unchanged.
- Re-run full auth matrix, migration, browser, Docker, race, fuzz and scanners.
