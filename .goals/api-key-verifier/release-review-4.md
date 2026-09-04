# Release Review Feedback — Iteration 4

## Verdict: FAIL

Release-candidate security and code reviews found two medium-severity,
high-confidence issues.

## Finding 1 — Active-key reset race

An active-key request can verify the current pair, then a concurrent reset
commits a replacement, and the old-token request still authenticates because
the final check validates only account-lock state.

**Required correction:**

- Before returning any API-key identity, perform one atomic indexed final query
  requiring exact user ID, unlocked state, `api_key IS NULL`, and the exact
  verifier/key-ID pair accepted by the request.
- Apply the same final linearization after active-key and lazy-rekey resolution.
- Reset/replacement/lock committed before this query must reject generically.
- Add deterministic SQLite/MySQL races for active reset, lazy reset, lock/reset,
  and zero stale-token handler execution.

## Finding 2 — Reveal responses break legacy JSON shapes

The reveal-once implementation nests created-user fields under `user` and
changes reset `data` from a token string to an object, breaking existing clients.

**Required correction:**

- Keep successful reset `data` as the plaintext token string returned once.
- Keep user-create fields at their legacy top-level locations and expose the
  one-time token at the legacy top-level `api_key` field in that immediate
  response only.
- Subsequent GET/list/PUT/settings remain secret-free and persistent User
  serialization remains unable to recover or expose key material.
- Build explicit response objects; do not reintroduce plaintext persistent state.
- Adapt frontend/tests and add raw compatibility/reveal-only assertions.

Preserve every previous security correction, migration boundary, #62 auth matrix,
backends, browser lifecycle, dependencies and scope.
