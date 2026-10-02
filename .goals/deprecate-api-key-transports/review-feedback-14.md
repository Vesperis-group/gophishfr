# User-Directed Decision — After Iteration 14

## Decision: Remove the goldmark dependency; return to a stdlib-only guard

This overrides the iteration-13 approach (a deterministic CommonMark/GFM
AST parser) and reaffirms the immutable goal. The user decided:

- Remove `github.com/yuin/goldmark/v2` and all AST-dependent
  implementation entirely.
- `go.mod` and `go.sum` must be byte-for-byte unchanged from the
  initial/base commit of this goal. No new dependency.
- Do not build a full Markdown parser.
- Limit the gate to canonical documentation files and deterministic
  stdlib checks for actually forbidden patterns.
- If a security finding truly cannot be fixed cleanly under this
  constraint, stop and report BLOCKED, precisely naming which one; do not
  add a dependency or silently weaken the gate.

## Required work

- Revert/remove `github.com/yuin/goldmark/v2` and all AST-dependent
  implementation, restoring base `go.mod`/`go.sum` exactly.
- Design a bounded stdlib-only guard for the specific canonical docs/files
  and prohibited rendered guidance, with explicit conservative rejection
  acceptable where necessary. Prefer deterministic fail-closed
  source-pattern checks/normalization over pretending to implement
  CommonMark. Document limitations and why the canonical docs gate
  remains enforceable.
- Fix the then-current security findings: HTML-block entity decoding;
  collapsible whitespace for visible raw HTML including Authorization
  across lines (while pre/code behavior explicit); deprecation context
  bound to the exact occurrence, same transport cannot cross-suppress.
- Fix the then-current code findings: exact canonical runtime header
  requires literal `Bearer ` + a non-empty token (tab/double-space
  noncanonical); line ranges accurate enough for multiline violations;
  retain redacted diagnostics.
- Cover all prior high-confidence bypass fixtures (links, HTML tags/
  comments, emphasis, entities/escapes, code/fence/list/soft wrap/table/
  clause/version/transport occurrence) using deterministic source
  handling. Conservative false positives should be narrowly documented/
  tested, not broad.
- Update self-review/status/review feedback with the user decision and
  dependency removal. Run full gates and prove `go.mod`/`go.sum` identical
  to base. No runtime auth behavior changes.

## Outcome

The dependency-free redesign closed every one of the above findings
without any high-confidence case requiring a BLOCKED report: the
bare-version-construction, directional transport-occurrence-binding, and
clause/sentence-scoping logic from iterations 9-14 was already pure
stdlib code and carried over unchanged; a new, narrowly bounded raw-HTML-
block scanner (documenting its own, explicit limitations) closed the
HTML-block entity-decoding and whitespace-collapsing findings; a
paragraph/HTML-block joinable-text-run mechanism closed the soft-wrapped-
Authorization finding; and a corrected Bearer-prefix check (with a
narrow, tested exemption for a real false positive it surfaced against
this repository's own documentation) closed the Bearer-exactness finding.
