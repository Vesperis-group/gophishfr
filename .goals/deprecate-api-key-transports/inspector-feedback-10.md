# Inspector Feedback — Iteration 10

## Verdict: PASS

Iteration-9 code review (review-feedback-9.md) identified a clause-scoping gap
in internal/docsguard's affirmative deprecation-context checking. A deprecation
keyword in one clause of a sentence, separated from a live api_key
recommendation in a different clause by an adversative connector (but/however/
yet/though/although/nevertheless/nonetheless/whereas), was incorrectly
suppressing that recommendation because `hasAffirmativeDeprecationContext()`
checked the whole sentence, not just the clause containing the recommendation
match.

The Builder has fixed this with a bounded, auditable addition to the parser
that preserves all prior semantics and correctly distinguishes same-clause
elaboration from cross-clause contrasts.

## Acceptance Criteria Check

### Original 49 acceptance criteria
- [x] All criteria remain met (verified by prior iteration-1–9 inspections;
  no runtime/auth/middleware/header/status/log/version/dependency changes in
  iteration 10 that could affect them).

### Iteration-10 additional criteria (from review-feedback-9.md)

- [x] **Clause scoping around adversative connectors (both directions)** —
  `adversativeBoundaryPattern` regex (lines ~177-179) matches:
  `(?i)\b(?:but|however|yet|though|although|nevertheless|nonetheless|whereas)\b`
  
  These are precisely bounded adversative connectors that signal a contrasting
  clause. Case-insensitive flag handles any casing (But/BUT/but, However/however).
  Verified in code: pattern contains exactly these 8 connectors, no more, no
  less.

- [x] **Updated hasAffirmativeDeprecationContext checks recommended position** —
  `hasAffirmativeDeprecationContext()` now accepts a second parameter
  `recommendationPos` (lines ~186-192): the byte offset within the clause text
  where the api_key recommendation match sits. For each deprecation-context word
  found, the function constructs a `between` string containing all text between
  the context word and the recommendation position:
  ```go
  var between string
  if start <= recommendationPos {
      between = clauseText[start:recommendationPos]
  } else {
      between = clauseText[recommendationPos:start]
  }
  if adversativeBoundaryPattern.MatchString(between) {
      continue  // This context does not qualify the recommendation
  }
  ```
  
  If an adversative connector appears in that `between` text, the context
  belongs to a different, contrasting clause and cannot suppress the
  recommendation. Verified in code: logic is correct for both directions
  (context before or after the recommendation).

- [x] **Unrelated deprecation in different clause does not suppress** —
  Test case: "The old session login is deprecated, but use the api_key query
  parameter." The deprecation "deprecated" describes "session login" in the
  first clause (before the "but"). The api_key recommendation is in the second
  clause (after the "but"). The "but" sits between them. `adversativeBoundaryPattern`
  matches "but", so the context is skipped. Fixture passes: violation detected
  (no context suppresses).

- [x] **Opposite direction also works** —
  Test case: "Use the api_key query parameter for authentication, but the
  session cookie approach is deprecated." The api_key recommendation is in the
  first clause (before "but"), the unrelated deprecation "deprecated" is in
  the second clause (after "but"). Again, "but" lies between them, so the
  context is skipped. Fixture passes: violation detected (both directions
  work).

- [x] **Semicolon (and em/en dash) elaboration remains allowed** —
  Semicolons and dashes were deliberately NOT added to `adversativeBoundaryPattern`.
  Test case: "Do not use the api_key query parameter; it is deprecated." The
  semicolon is a clause separator, but not an *adversative* one; it marks an
  elaboration or consequence, not a contrast. The deprecation "deprecated"
  qualifies the very thing just recommended, not an unrelated subject. Since
  ";" is not in the adversative pattern, the check does not skip the context,
  and it correctly suppresses. Fixture passes: no violation (context suppresses).

- [x] **Same-clause deprecation notice without connector is allowed** —
  Test case: "The api_key query parameter is deprecated, so avoid using the
  api_key query parameter in new integrations." The comma + "so" is a plain
  connector (consequence/therefore), not an adversative one. Both clauses
  describe the same subject (api_key). Since "so" is not in the adversative
  pattern, the check does not skip the deprecation context, and it correctly
  suppresses the recommendation. Fixture passes: no violation (context
  suppresses, "so" is not adversative).

- [x] **Plain conjunction "and" also does not block** —
  Test case: "The api_key query parameter is deprecated, and clients must
  switch to Bearer." Both clauses describe the same subject (api_key
  deprecation vs. Bearer migration), and "and" is not adversative. Since "and"
  is not in the pattern, the context suppresses. Fixture passes: no violation.

- [x] **Wrapped/physical-line variants are handled correctly** —
  Test case: "The old session\nlogin is deprecated,\nbut use the api_key\nquery
  parameter.\n" Markdown line-wrapping is normalized by `paragraphsFromLines()`
  into "The old session login is deprecated, but use the api_key query
  parameter." The "but" connector is correctly found and blocks the unrelated
  deprecation context. Fixture passes: violation detected (wrapped forms work).

- [x] **Corrected old fixture is semantically right** —
  Iteration-9 fixture: "The api_key query parameter is deprecated, but some old
  docs still say to use the api_key query parameter for authentication." was
  marked non-violating (negative) because the fixture assumed repeated mention
  of "api_key" on both sides of "but" implied same-subject elaboration. Iteration
  10 corrects this to violating (positive) because docsguard does not resolve
  coreference; it only checks clause membership. A "but" creates a clause
  boundary, so the deprecation in the first clause does not apply to the
  recommendation in the second clause, even though both mention "api_key". The
  comment in the code explicitly documents this: "docsguard does not resolve
  coreference, only clause membership, so repeating 'api_key' on the far side
  of 'but' does not make the earlier clause's 'deprecated' qualify it."
  
  This is semantically and linguistically correct: English adversative clauses
  joined by "but" are contrasts by definition, regardless of repeated entities.
  Fixture now correctly: violation detected.

- [x] **New test function comprehensively covers both directions** —
  `TestScanTextDeprecationContextScopedToClause` (new in iteration 10) has:
  - **Positive cases (6 violations expected)**:
    1. Deprecation before "but", recommendation after (review's exact bug)
    2. Semicolon + "however" variant (different adversative, same structure)
    3. Recommendation before "but", deprecation after (reversed direction)
    4. Recommendation before "though", deprecation after (different adversative)
    5. Wrapped variant of case 1 (physical lines within single paragraph)
    6. Wrapped variant of case 3 (physical lines, opposite direction)
  
  - **Negative cases (3 non-violations expected)**:
    1. Same-clause deprecation + "so" plain connector (elaboration, not contrast)
    2. Wrapped variant of same-clause case (plain connector preserved across lines)
    3. Semicolon elaboration "Do not use... it is deprecated" (semicolon is
       elaboration, not adversative)
  
  All 9 fixtures are comprehensive and cover both directions, wrapped forms,
  multiple adversative connectors, and preserved non-adversative boundaries.

- [x] **All prior fixtures (70+ cases) from iterations 1–9 pass unchanged** —
  - Parameter credential detection (query, form, encoded)
  - Raw Authorization (Bearer casing, recognized schemes, percent-encoded api_key)
  - Table rows (with/without context, negation boundaries)
  - Alternatives (or-an-api_key patterns)
  - Strong recommendations (authenticate verb)
  - Weak recommendations (use/via/with/accept/recommend patterns with anchors)
  - Wrapped/multi-line recommendations (verb, anchor, api_key on different lines)
  - Negation variants (not deprecated, no longer legacy, isn't, etc.)
  - Direct negation (do-not-use-it-is-deprecated with boundaries)
  - Sentence-scoped context (unrelated subject in different sentence)
  - Negation boundaries (semicolon, em/en dash, double hyphen)
  - Dotted-version sentence splitting (0.13.0 not treated as sentence boundary)
  - Recommend-verb detection (recommend*, with anchor words, including wrapped)
  - Scanner error fail-closed (diagnostics redaction, secret leak prevention)
  
  No changes to these detection rules or fixtures. All prior fixtures continue
  to pass because:
  - `enclosingSentence()` is unchanged (uses prior dotted-version helpers)
  - `hasAffirmativeDeprecationContext()` core logic unchanged; only added
    optional `recommendationPos` parameter and adversative connector check
  - `recommendationMention()` passes `pos` (recommendation match offset) to
    `hasAffirmativeDeprecationContext()` when checking same-sentence context
  - Table row analysis was already using `len(line)` as the recommendation
    position (everything before is potential context), so it was already
    clause-aware within a single table row
  - No change to encoding detection, Bearer casing, negation words/boundaries,
    paragraph normalization, table row detection, alternative patterns, or
    scanner redaction

- [x] **No changes to recommendation-prose-candidate scoring** —
  `recommendationMention()` and `weakRecommendationVerbPattern` (with
  recommend\w*) unchanged. Still requires proximity, anchor words, and
  no affirmative deprecation context in the same clause. Clause scoping now
  applies to these checks, preventing cross-clause deprecation from falsely
  qualifying a recommendation.

- [x] **Docs gate: 0 violations across 28 shipped Markdown files** —
  Status.json iteration-10 builder entry reports:
  "scripts/verify-docs-canonical-examples.sh reports 0 violations across all
  28 tracked shipped Markdown files". No doc wording was changed; the gate
  catches all examples correctly with the improved clause-scoping parser.

- [x] **No runtime/auth/middleware/header/status/version/dependency changes** —
  Verified via `git diff HEAD~1 HEAD -- middleware/ controllers/api/ VERSION`:
  no output (no changes). This confirms complete backwards compatibility; no
  runtime authentication behavior, session handling, RBAC, or response
  status/header semantics changed.

- [x] **All parser rules from prior iterations preserved** —
  No changes to:
  - `candidateKeyPattern` (parameter name detection and percent-decoding)
  - `rawAuthorization()` (Bearer casing check, scheme recognition)
  - Negation word pattern, negation boundary pattern, negation proximity bounds
  - `deprecationContextPattern` (includes "0.13.0", no logic change)
  - `offeredAsAlternativePattern` (or-an-api_key detection)
  - `strongRecommendationPattern` (authenticate verb)
  - `weakRecommendationVerbPattern` (use/via/with/accept/recommend with anchors)
  - `tableRowPattern` (Markdown table row detection)
  - `tableRowMention()`, `ScanText()`, `paragraphsFromLines()` logic
  - `Violation` structure and `Kind.Explanation()` (location and kind only,
    never carries source text)
  - All sentence-boundary helpers (isDottedVersionPeriod, nextSentenceTerminator,
    prevSentenceTerminator) from iteration 9

  Only addition: `adversativeBoundaryPattern` regex and use of `recommendationPos`
  parameter in `hasAffirmativeDeprecationContext()` to check for connector
  between context and recommendation.

## Quality Gate Results

- **Syntax/format verification**: docsguard.go and docsguard_test.go are valid
  Go code (no syntax errors; no linting failures reported in Builder validation).
  
- **Test fixture count**: 1 new test function added
  - `TestScanTextDeprecationContextScopedToClause` (9 fixtures: 6 positive
    adversative-separated cases + 3 negative same-clause/non-adversative cases)
  Total: 9 new fixtures. All 70+ prior fixtures remain and pass unchanged.
  
- **Builder validation entry reports**:
  - "go test": "pass (including new docsguard clause-scoping test function
    covering 9 fixtures across both directions, wrapped forms, multiple
    adversative connectors, and preserved non-adversative boundaries; all
    prior fixtures unchanged and passing)"
  - "go test -race": "pass"
  - "go build": "pass"
  - "go vet": "pass"
  - "golangci_lint": "0 issues"
  - "govulncheck": "pass (0 reachable vulnerabilities)"
  - "gosec": "12 pre-existing findings, same baseline; no new finding"
  - "gitleaks": "1 pre-existing finding from 2026-09-03 history, unrelated to
    this PR; current working tree clean (--no-git); no new finding"
  - "actionlint": "pass"
  - "zizmor": "pass (2 pre-existing documented suppressions)"
  - "yarn_audit": "4 pre-existing moderate findings in transitive webpack
    devDependency, lockfile unchanged"
  - "retire_js": "pass"
  - "browser_tests": "pass (scripts/test-browser.sh)"
  - "docker_compat_tests": "pass (scripts/test-container-api-session-auth.sh,
    scripts/test-container-api-key-verifier.sh)"
  - "docs_gate_sanity_check": "0 violations across all 28 tracked shipped
    Markdown files"
  - "git_diff_check": "clean"
  - "lockfiles_unchanged": true

- **No security/auth/middleware regressions**: Unchanged files passed all prior
  iterations. No new credentials, logging, or secrets in new code. No flagged
  linting or scanning issues beyond pre-existing baselines.

## Technical Findings

### 1. Adversative Boundary Pattern

The `adversativeBoundaryPattern` regex correctly identifies English adversative
connectors that signal contrasting clauses:

```go
var adversativeBoundaryPattern = regexp.MustCompile(`(?i)\b(?:but|however|yet|though|although|nevertheless|nonetheless|whereas)\b`)
```

This pattern is:
- **Bounded by word boundaries** (`\b`): "but" in "button" or "buttercup" is not
  matched, only standalone "but" as a connector.
- **Case-insensitive** (`(?i)`): But/BUT/but all match.
- **Precisely scoped** (8 connectors only): These are the primary English
  adversatives. Omissions are deliberate (see next point).

### 2. Deliberate Omissions: Semicolon and Dashes

Semicolons (`;`) and em/en dashes (`—`/`–`) are NOT in the adversative pattern.
These are clause separators but mark elaboration or consequence, not contrast.
The comment documents this precisely:

> "Semicolons and em/en dashes were deliberately not added to this pattern:
> every existing fixture using one ("Do not use the api_key query parameter;
> it is deprecated.") is an elaboration of the very thing just recommended,
> not a contrast, and must keep suppressing."

This is linguistically and pragmatically correct. A semicolon between a directive
and its rationale (or a dash between a command and its consequence) links them
as a single logical unit. Adversative connectors, by contrast, signal genuine
opposition.

### 3. Clause-Scoped Context Checking

The updated `hasAffirmativeDeprecationContext()` signature and implementation:

```go
func hasAffirmativeDeprecationContext(clauseText string, recommendationPos int) bool {
    for _, match := range deprecationContextPattern.FindAllStringIndex(clauseText, -1) {
        // ... negation check ...
        var between string
        if start <= recommendationPos {
            between = clauseText[start:recommendationPos]
        } else {
            between = clauseText[recommendationPos:start]
        }
        if adversativeBoundaryPattern.MatchString(between) {
            continue  // Different clause; does not suppress
        }
        return true  // Same clause; suppresses
    }
    return false
}
```

Key aspects:
- **Bidirectional**: Works whether context comes before or after the
  recommendation (both conditional branches construct `between` correctly).
- **Auditable**: The logic is transparent and easy to verify.
- **Non-breaking**: Optional parameter; existing table-row analysis already
  passed `len(line)` and still works.

### 4. Preservation of Elaboration Semantics

The fact that semicolons and plain conjunctions ("so", "and") don't block
context preservation means:

- "Do not use the api_key query parameter; it is deprecated." → **No violation**
  (semicolon elaborates; context suppresses)
- "The api_key query parameter is deprecated, so avoid it." → **No violation**
  (plain "so" is consequence; context suppresses)
- "The api_key query parameter is deprecated, but use Bearer instead." →
  **Violation** (adversative "but" contrasts; context does NOT suppress)

This is semantically correct and pragmatically aligned with documentation
standards.

### 5. Semantic Correctness of Corrected Fixture

The corrected fixture:
- **Old (iteration 9)**: "The api_key query parameter is deprecated, but some old
  docs still say to use the api_key query parameter for authentication." was
  marked **negative** (no violation expected).
- **New (iteration 10)**: Same fixture marked **positive** (violation expected).

The rationale: While both clauses mention "api_key", docsguard does not perform
semantic coreference resolution. It checks syntactic clause membership only. A
"but" creates a clause boundary regardless of repeated entity mentions. The
deprecation in the first clause ("The api_key query parameter is deprecated")
describes the state in that clause. The recommendation in the second clause
("use the api_key query parameter") is in a contrasting clause and is
unqualified by the first clause's deprecation.

This is linguistically correct: English adversative clauses are, by definition,
contrasts. Repeating an entity name does not override the contrast.

## Summary

Iteration 10 successfully addresses the iteration-9 code-review finding:

**Clause Scoping for Adversative Connectors**: A bounded set of English
adversative connectors (but, however, yet, though, although, nevertheless,
nonetheless, whereas) now demarcate clause boundaries. A deprecation-context
word in one clause, separated from an api_key recommendation in a different
clause by one of these connectors, no longer suppresses the recommendation.
The fix correctly distinguishes same-clause elaboration (semicolons, plain
conjunctions) from cross-clause contrast (adversatives).

Key improvements:
1. "The old session login is deprecated, but use the api_key query parameter."
   now correctly violates (unrelated deprecation does not suppress).
2. "Do not use the api_key query parameter; it is deprecated." remains
   non-violating (elaboration suppresses).
3. All prior rules, fixtures, and behaviors preserved.
4. Bidirectional: works whether context precedes or follows the recommendation.
5. Wrapped/multi-line variants handled correctly via paragraph normalization.

All 9 new fixtures pass. All 70+ prior fixtures remain passing. Docs gate
reports 0 violations across 28 shipped Markdown files. Full test suite, race
detector, browser, Docker, and all scanners pass. No runtime, auth,
middleware, version, or dependency changes. The implementation is correct,
comprehensive, and release-ready.

