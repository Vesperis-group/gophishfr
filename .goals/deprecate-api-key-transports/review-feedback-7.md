# Independent Review Feedback — Iteration 7

## Verdict: FAIL

The security review passed. Code review found two semantic-context defects:

1. Any deprecation keyword anywhere in the paragraph suppresses a later,
   unrelated `api_key` recommendation.
2. A legitimate warning such as
   `Do not use the api_key query parameter; it is deprecated.` is rejected
   because generic nearby negation is assumed to negate `deprecated`.

## Required correction

- Associate affirmative deprecation context with the same sentence/clause and
  relevant `api_key` recommendation, not the whole paragraph.
- Treat negation as negating deprecation only when it directly governs the
  deprecation/legacy expression (for example `not deprecated`), not when it
  governs `use`.
- Add positive fixtures for unrelated prior/next deprecation plus a live
  recommendation.
- Add negative fixtures for `do not use …; it is deprecated`, equivalent warning
  wording, and wrapped forms.
- Preserve diagnostic redaction, fail-closed scanner and all previous rules.
- Keep runtime/auth/version/dependencies unchanged.
