# Independent Review Feedback — Iteration 6

## Verdict: FAIL

The security review passed. Code review found two release-blocking guard issues.

## Paragraph and negation bypasses

Line-by-line scanning misses recommendations split across Markdown lines.
Additionally, any occurrence of a context keyword suppresses detection even
when negated, for example "not deprecated."

**Required correction:**

- Analyze normalized Markdown paragraphs/blocks in addition to line-local
  assignment/header patterns.
- Detect recommendation signals split across wrapped lines.
- Suppress only affirmative deprecation/legacy/migration context; negated forms
  such as "not deprecated" must remain violations.
- Add positive fixtures for wrapped recommendations and negated context.
- Add negative fixtures for genuine affirmative deprecation, historical,
  internal-schema and meta-policy paragraphs.

## Diagnostic secret echo

The CLI prints complete offending lines. If a forbidden example contains a real
credential/query/form/header value, CI copies it into logs.

**Required correction:**

- Diagnostics output only file, line/range and violation kind, with a fixed
  non-sensitive explanation.
- Never include source text, URL, query, form body, Authorization header, token,
  verifier or derived fingerprint.
- Ensure internal APIs/Violation values do not require storing secret-bearing
  text, or redact before every output.
- Add tests using distinctive synthetic secret strings and assert they are absent
  from stdout/stderr/errors while violation location/type remains useful.

Preserve all previous rules, CI enforcement, runtime behavior, version and
dependencies.
