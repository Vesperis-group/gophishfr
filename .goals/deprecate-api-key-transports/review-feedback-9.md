# Independent Review Feedback — Iteration 9

## Verdict: FAIL

The security review passed. A deprecation keyword in an unrelated clause of the
same sentence still suppresses a live recommendation:

```text
The old session login is deprecated, but use the api_key query parameter.
```

## Required correction

- Scope affirmative deprecation context to the clause containing the matched
  `api_key` recommendation, not merely the sentence.
- Recognize clear clause/adversative boundaries such as semicolons, em/en dashes,
  and `but`/`however`/equivalent bounded connectors.
- Add positive fixtures for unrelated same-sentence prior/following clauses.
- Add negative fixtures where the same clause genuinely marks api_key deprecated,
  including wrapped forms and warning phrasing.
- Preserve dotted-version, direct-negation, paragraph/proximity, encoded-name,
  scanner fail-closed and diagnostic-redaction behavior.
- Keep runtime/auth/version/dependencies unchanged.
