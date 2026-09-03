# CI Feedback — Iteration 3

## Verdict: FAIL

PR #55 failed the `Frontend browser smoke` job twice with the same deterministic
failure. All other CI jobs passed.

## Failure

Test:

`tests/browser/frontend-smoke.spec.ts:305:5`
`settings controls keep their state contracts`
`settings form preserves failure and retry behavior`

Assertion:

`tests/browser/settings-form-contract.ts:341`

The retry helper expected only the accumulated error flash events but received
four extra success events, including `Empty settings accepted`. The poll timed
out after five seconds. Both the initial run and the failed-job rerun produced
the same result; this is not dismissible as a transient failure.

The SMTP/browser additions changed the suite environment or test state such that
the later settings contract observes stale success events.

## Required correction

- Reproduce the full browser-smoke suite in CI-equivalent order.
- Identify the exact state leak or event-lifecycle assumption; do not merely
  raise the timeout or weaken assertions.
- Isolate/clear only stale pre-existing flash-event state at the correct test or
  helper boundary while preserving verification of each settings failure and
  retry event.
- Ensure the SMTP tests still assert success/error behavior and no credential
  exposure.
- Add or strengthen a regression proving the complete browser suite is
  order-independent for this state.
- Re-run the full browser suite repeatedly, then all local gates.
- Do not change product behavior, dependencies, or unrelated frontend code.
