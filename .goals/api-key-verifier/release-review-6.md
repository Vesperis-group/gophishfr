# Release Code Review Feedback — Iteration 6

## Verdict: FAIL

The consolidated security review returned PASS. Code review found one
first-party reveal-lifecycle race.

## Finding — Concurrent reset submissions can display a revoked token

The settings reset form remains active while a request is pending. Multiple
submissions can issue separate rotations, and response order need not match DB
commit order. An older response can overwrite the reveal UI with a token already
invalidated by a later reset.

## Required correction

- Add one in-flight guard and disable reset controls before dispatch.
- Additional click/submit/keyboard attempts issue no second request.
- On failure, clear guard and re-enable controls for retry.
- On success, reveal once without stale overwrite; closing clears DOM/state.
- Add deterministic tests with deferred/out-of-order mocks and rapid repeated
  submits proving exactly one request, valid reveal, failure recovery, and no
  stale update.
- Regenerate assets and rerun browser/reproducibility/Docker/full gates.
- Document that concurrent external reset callers retain immediate invalidation
  semantics and must rerotate after a lost/stale response; no backend grace is
  introduced.
