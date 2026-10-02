# Independent Review Feedback — Iteration 8

## Verdict: FAIL

The security review passed. Code review found two bounded parser gaps:

1. Sentence splitting treats periods inside dotted numeric versions such as
   `0.13.0` as sentence boundaries, excluding the removal-version context.
2. Direct recommendation verbs such as `recommend`/`recommended` are not part of
   the prose candidate detector.

## Required correction

- Treat dots between digits as part of a dotted version, not a sentence boundary.
- Add a fixture where `0.13.0` is the only affirmative deprecation/removal
  context after a recommendation.
- Detect `recommend*` near `api_key` plus query/form/parameter/credential anchors.
- Add positive recommendation fixtures and negative historical/internal/
  deprecated recommendation fixtures.
- Preserve all previous paragraph, negation, proximity, encoding, scanner and
  diagnostic-redaction behavior.
- Keep runtime/auth/version/dependencies unchanged.
