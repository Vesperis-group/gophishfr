# Independent Security/Code Review Finding — After Iteration 12

## Verdict: Insufficient (bounded heuristics)

Final reviews of the iteration-12 state found the bounded regex/heuristic
approach in `internal/docsguard` fundamentally insufficient. Iterations 11-12
each closed a specific bypass by extending the hand-written pseudo-Markdown
pattern set, but kept reopening new ones (see `review-feedback-10.md`), which
confirms the approach cannot be made complete this way.

## Required correction

Fix all high-confidence issues as one coherent root-cause change: use a
deterministic CommonMark/GFM-aware structured analysis (prefer an existing
dependency/prior art; if no suitable dependency exists, justify the smallest
pinned Go dependency and its maintenance/security impact per `CLAUDE.md`,
updating `go.mod`/`go.sum` deterministically). Do not keep growing ad-hoc
pseudo-Markdown parsing if a tested AST parser is needed.

Required cases:

- Flatten rendered inline text across Markdown links, safe/raw HTML anchors,
  comments, emphasis/strong/strike around whole or partial anchors,
  nested/chained constructs, tables/wrapped prose, and cross-node inline code
  boundaries according to actual rendering. Examples: `[api](url)_key`,
  `api<a>_</a>key`, `api<!-- -->_key`, `Author[ization](url):`,
  `**api_key**=TOKEN`.
- Correct semantics for inline code including multi-line spans and variable
  delimiters: escapes/entities stay literal in code nodes, but literal
  forbidden forms remain detectable.
- Correct fenced code rules (valid open/close, info strings, backtick
  restrictions), tilde fences, and list continuation vs actual indented code.
- Bind deprecation context to the exact recommended `api_key` occurrence AND
  the specific query/form/raw transport, within the appropriate
  clause/table cell. A deprecation of a response field or form transport must
  not suppress a query recommendation. Bare `0.13.0` is not affirmative;
  require an explicit removal/deprecation construction word nearby. Preserve
  genuine warnings/deprecations/do-not-use statements and all prior tests.
- Preserve redacted diagnostics, fail closed on parser/scanner errors/limits,
  no runtime auth changes.

Update docs/self-review and status if the dependency or design changes. Add
adversarial regression fixtures for every security/code review example and
their compositions. Do not rewrite historical commits.

User selected: fix all high-confidence findings as one coherent root-cause
change. The nonconforming historical commit flagged separately by the
reviewers remains explicitly out of scope for this iteration; the
orchestrator will obtain explicit user authorization for that separately.
