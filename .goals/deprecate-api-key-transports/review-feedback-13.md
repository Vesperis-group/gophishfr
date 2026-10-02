# Independent Security/Code Review Finding — After Iteration 13

## Verdict: FAIL (three root-cause gaps within the structured parser)

Final reviews of the iteration-13 goldmark v2 AST rewrite found three
remaining gaps. These are not a reason to abandon the structured-parser
design; they are gaps *within* it that need closing with the same
deterministic approach, not ad-hoc patching.

## Finding 1 (security, MEDIUM, 10/10, also code)

`ast.HTMLBlock` was not walked by `ScanText`'s AST dispatch at all, so a raw
HTML block's visible rendered content — a `<div>`, `<p>`, or `<pre>`
containing a credential or a recommendation — was never scanned.

### Required correction

- Process visible raw HTML block content through the same
  transport/recommendation checks a paragraph gets.
- Correctly exclude comments, tag markup/attributes, and other
  non-visible/non-rendered elements/content under GitHub/GFM semantics.
- Cover block `<div>`, `<p>`, `<pre>`, multi-line/nested tags, comments,
  attributes containing fake credential text, `<script>`/`<style>` or other
  non-visible cases as appropriate.
- Handle malformed HTML conservatively/fail-closed.
- Diagnostics must still report only redacted line ranges, never source
  text.

## Finding 2 (code)

Soft-wrapped raw `Authorization:` credential detection did not scan fully
rendered/flattened ordinary Markdown blocks, so
`Authorization:\nTOKEN` (which renders as `Authorization: TOKEN`, an
ordinary Markdown soft line break collapsing to a single space) was not
detected: credential scanning ran per physical line only.

### Required correction

- Also scan the fully rendered/flattened block text for this case, not
  just one physical line at a time.
- Retain a precise, contributing line range.
- Ensure code-span and hard-break semantics, and ordinary multi-line
  query/form examples, remain correct.
- Avoid duplicate diagnostics or false merging across unrelated
  block/cell boundaries.

## Finding 3 (code)

`transportKindNear`'s symmetric, flat character-window binding could not
correctly classify a compact sentence naming two transports close together
with no anchor word to space them apart (`api_key form deprecated; use
api_key query` must violate), and — more importantly — treated a genuinely
ambiguous classification (two conflicting transport words both found in the
same window) the same as "no transport information available", letting it
default to suppression instead of failing safe.

### Required correction

- Replace the symmetric, ambiguous char-window binding with directional,
  occurrence-specific transport association.
- `api_key form deprecated; use api_key query` must violate.
- A deprecation for the response/form/raw transport must not suppress a
  live query/form recommendation, and vice versa.
- An ambiguous classification must fail safe and never suppress.
- Preserve natural variants and prior accepted warnings.

## Scope

Add all reviewer examples plus adversarial/multiline/table/HTML fixtures.
Preserve the existing dependency pin (no dependency change). No runtime
behavior changes.

User selected: fix all final AST findings. The nonconforming historical
commit flagged separately by the reviewers remains explicitly out of scope
for this iteration; the orchestrator will obtain explicit user
authorization for that separately.
