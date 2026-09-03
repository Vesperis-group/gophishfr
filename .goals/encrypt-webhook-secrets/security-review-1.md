# Security Review Feedback — Iteration 1

## Verdict: FAIL

An independent security specialist found one high-confidence data-integrity
issue within the immutable goal.

## Finding — NULL legacy rows can silently ignore updates

**Severity:** Medium  
**Confidence:** 9/10  
**Affected paths:** `models/webhook.go`, `models/webhook_credentials.go`

The webhook schema allows `secret IS NULL`. Runtime verification normalizes
NULL with `COALESCE(secret, '')`, but update predicates require `secret = ''`.
A preserve update on a valid NULL no-secret row can therefore affect zero rows
while later credential-only verification accepts the unchanged secret state and
returns success. An administrator may receive HTTP 200 after deactivating a
webhook even though it remains active and continues receiving campaign data.

The rollback path similarly rejects a valid encrypted row whose legacy secret
column remains NULL.

## Required correction

- Make create, preserve, clear, replace, migration, rollback, and concurrency
  predicates consistently NULL-aware wherever NULL and empty represent the same
  valid legacy-empty state.
- After runtime updates, verify every intended metadata field as well as exact
  credential state inside the same transaction before commit.
- Distinguish a matched no-op from a missing/conflicting row without accepting
  stale metadata, including MySQL changed-row semantics.
- Keep ambiguous non-empty legacy plus ciphertext states fail-closed.
- Add SQLite and real MySQL regression coverage for `secret IS NULL` across:
  preserve, clear, replace, deactivate/metadata update, migration, rollback,
  matched no-op, and conflicting/concurrent mutation.
- Prove deactivation of a NULL no-secret row actually persists and prevents
  subsequent active delivery.
- Preserve all original API, HMAC, migration, rollback, and no-secret contracts.
