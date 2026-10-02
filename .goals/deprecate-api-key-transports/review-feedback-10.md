# Independent Security Review Finding — After Iteration 10

## Verdict: MEDIUM (confidence 9/10)

`internal/docsguard/docsguard.go` scans raw Markdown and does not normalize
Markdown escapes or HTML character references. Canonical docs may pass while
rendering functional deprecated examples, e.g. `api\_key=TOKEN`,
`api&#95;key=TOKEN`, `Authorization&#58; TOKEN`.

## Required correction

- Correct defensively without turning docsguard into a general Markdown
  parser.
- Normalize/reject security-relevant encodings before matching.
- Preserve diagnostic redaction/fail-closed behavior.
- Add comprehensive positive fixtures for backslash escapes and named/numeric
  (decimal/hex, sensible case variants) HTML references affecting `api_key`
  and `Authorization:`.
- Include negative fixtures so unrelated entities/escapes do not create false
  positives.
- Consider encoded characters throughout protected anchors, not just the
  three examples, where GitHub Markdown rendering would create the forbidden
  form.
- Preserve all prior rules and no runtime behavior changes.

User selected: Fix all issues.
