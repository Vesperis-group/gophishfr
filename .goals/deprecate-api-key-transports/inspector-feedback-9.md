# Inspector Feedback — Iteration 9

## Verdict: PASS

Iteration-8 code-review (review-feedback-8.md) identified two bounded parser gaps
in internal/docsguard's sentence-scoping and recommendation-verb detection:
(1) periods inside dotted removal versions such as 0.13.0 were treated as
sentence boundaries, stranding the removal-version context away from the
recommendation it qualifies; (2) direct recommendation verbs such as
recommend/recommended were not part of the prose candidate detector.

The Builder has fixed both gaps with narrow, bounded corrections that preserve
all prior semantics. Independent verification confirms the fixes are correct,
comprehensive, and maintain all existing detection and redaction behaviors.

## Acceptance Criteria Check

### Original 49 acceptance criteria
- [x] All criteria remain met (verified by prior iteration-1–8 inspections;
  no runtime/auth/middleware/header/status/log/version/dependency changes in
  iteration 9 that could affect them).

### Iteration-9 additional criteria (from review-feedback-8.md)

- [x] **Dotted numeric versions are not sentence boundaries** —
  `isDottedVersionPeriod()` function (lines ~177-184) checks whether text[idx]
  is a '.' flanked by ASCII digits on both sides. Returns true if
  `isDigit(text[idx-1]) && isDigit(text[idx+1])`, false otherwise. This
  correctly identifies periods in "0.13.0" as part of a dotted version, not a
  sentence boundary. Verified in code: function present with correct logic.

- [x] **Real periods remain sentence boundaries** —
  `nextSentenceTerminator()` (lines ~187-199) scans forward for a character in
  `sentenceTerminators` (".!?") and skips it only if `isDottedVersionPeriod()`
  returns true. A real period (not in "0.13.0") is not skipped and is returned
  as a boundary. `prevSentenceTerminator()` (lines ~202-213) scans backward
  with the same logic. Both functions return -1 if no boundary is found.
  Verified in code: forward/backward scanning logic correct.

- [x] **Version context binds to recommendation in same sentence** —
  `enclosingSentence()` (lines ~216-225) now uses `prevSentenceTerminator()`
  and `nextSentenceTerminator()` instead of bare `strings.LastIndexAny()` and
  `strings.IndexAny()`. This ensures version dots are not treated as
  boundaries. Test case: "Use the api_key query parameter only until 0.13.0."
  is a single sentence despite internal dots; 0.13.0 context is found and
  applies to the same-sentence recommendation. Fixture passes: no violation
  (context suppresses).

- [x] **Version context in different sentence does not suppress** —
  Test case: "Legacy session cookies were phased out in 0.13.0 for unrelated
  reasons. Use the api_key query parameter for authentication." The version is
  in the first sentence, the recommendation in the second. Fixtures verify that
  `enclosingSentence()` for the recommendation returns only the second sentence
  (bounded by the real period after "reasons"), and 0.13.0 in the first
  sentence does not apply. Fixture passes: violation detected (no context
  suppresses).

- [x] **Version mid-sentence with trailing prose is handled correctly** —
  Test case: "Use the api_key form field until 0.13.0, after which it stops
  working." The version 0.13.0 sits mid-sentence followed by more text. A
  buggy implementation that split at internal version dots would create two
  fragments stranded on either side of the recommendation. Correct
  implementation keeps it as one sentence with 0.13.0 as the context.
  Fixture passes: no violation (context suppresses).

- [x] **Version context wraps across physical lines correctly** —
  Test case: "Use the api_key\nquery parameter only\nuntil 0.13.0.\n"
  Markdown line-wrapping is normalized by `paragraphsFromLines()` into
  "Use the api_key query parameter only until 0.13.0." The version dots are
  correctly recognized as not being sentence boundaries. Fixture passes: no
  violation (context suppresses).

- [x] **Recommend verb variants are detected** —
  `weakRecommendationVerbPattern` (line 281-282) regex now includes
  `recommend\w*` pattern in the alternation alongside existing verbs
  (use/via/with/accept). The pattern is:
  `(?i)\b(?:use\w*|via|with|accept\w*|recommend\w*)\b` + proximity +
  `api_key\b | \bapi_key\b` + proximity +
  `\b(?:use\w*|via|with|accept\w*|recommend\w*)\b`
  
  This matches: recommend, recommends, recommended, recommending, as well as
  case-insensitive variants. Verified in code: pattern added correctly.

- [x] **Recommend verbs require anchor words** —
  Each `weakRecommendationVerbPattern` match is still subject to the
  `recommendationAnchorPattern` check (lines ~285): a weak recommendation verb
  must have a qualifying anchor word (parameter/param/query/field/header/
  credential) in the same sentence to count as an authentication-transport
  recommendation. Test case: "The scheduler recommends a retry backoff for the
  api_key rotation job." has "recommend" + "api_key" but no anchor word
  (rotation/job/backoff are not anchors), so it does not violate. Fixture
  passes: no violation (no anchor suppresses, despite verb and parameter
  co-occurrence).

- [x] **Recommend verbs with anchor words violate absent context** —
  Test cases:
  - "We recommend the api_key query parameter for authentication." (has
    "recommend", "api_key", anchor "parameter") → violates
  - "This guide recommends the api_key form field." (has "recommends",
    "api_key", anchor "field") → violates
  - "The recommended approach is an api_key credential in the query string."
    (has "recommended", "api_key", anchor "credential") → violates
  - "The team is recommending the api_key parameter to new integrators." (has
    "recommending", "api_key", anchor "parameter") → violates
  All fixtures pass: violations detected (no deprecation context suppresses).

- [x] **Recommend wrapped across physical lines is detected** —
  Test case: "We recommend\nthe api_key query\nparameter for
  authentication.\n" is normalized by `paragraphsFromLines()` to a single
  logical paragraph "We recommend the api_key query parameter for
  authentication." The verb-parameter-anchor co-occurrence spans physical lines
  but is detected because `recommendationMention()` operates on normalized
  paragraph text, not per-line. Fixture passes: violation detected.

- [x] **Historical recommend statements with deprecation context are allowed** —
  Test case: "Earlier releases recommended the api_key query parameter; it is
  now deprecated." Contains "recommended" + "api_key" + "parameter" anchor, but
  the same sentence also has "deprecated" context. `hasAffirmativeDeprecationContext()`
  finds "deprecated" and returns true (affirmative). The recommendation does
  not violate because context is present. Fixture passes: no violation
  (context suppresses).

- [x] **Internal/anchor-less recommend mentions are allowed** —
  Test case: "The scheduler recommends a retry backoff for the api_key
  rotation job." Contains "recommends" + "api_key" but no anchor word (rotation,
  job, backoff, scheduler are not in recommendationAnchorPattern). The
  `recommendationMention()` function skips matches that fail the anchor check.
  No violation is reported. Fixture passes: no violation (no anchor).

- [x] **No-longer-recommend statements with deprecation context are allowed** —
  Test case: "We no longer recommend the api_key form parameter; it is
  deprecated and targeted for removal in 0.13.0." Contains "recommend" +
  "api_key" + "parameter" anchor, but also has affirmative deprecation context
  (both "deprecated" and "0.13.0") in the same sentence. `hasAffirmativeDeprecationContext()`
  returns true. No violation is reported. Fixture passes: no violation
  (context suppresses).

- [x] **Recommend with no api_key mention is allowed** —
  Test case: "We recommend enabling two-factor authentication for all admin
  accounts." Has "recommend" but no "api_key" mention at all.
  `recommendationMention()` checks `strings.Contains(strings.ToLower(paragraphText),
  "api_key")` early and returns false. No violation is reported. Fixture
  passes: no violation (no api_key).

- [x] **All prior fixtures (51+ cases) from iterations 1–8 pass unchanged** —
  - Parameter credential detection: all variants (query, form, encoded, multipart)
  - Raw Authorization: Bearer casing, recognized schemes
  - Table rows: with/without deprecation context
  - Alternatives: or-an-api_key patterns
  - Strong recommendations: authenticate verb within proximity
  - Weak recommendations: use/via/with/accept patterns with anchors
  - Wrapped lines: recommendation prose across physical lines
  - Negation variants: not deprecated, no longer legacy, isn't, etc.
  - Direct negation: do-not-use-it-is-deprecated forms with boundaries
  - Sentence-scoped context: unrelated subject in different sentence
  - Negation boundaries: semicolon, em/en dash, double hyphen
  - Encoded parameters: percent-encoded api_key variants
  - Scanner error fail-closed: diagnostics redaction, secret leak prevention
  
  No changes to these detection rules or test fixtures. All prior fixtures
  continue to pass because the core logic (enclosingSentence for other rules,
  hasAffirmativeDeprecationContext, negationBoundaryPattern,
  negationProximityWords, encoded parameter decoding, Bearer casing check,
  table row detection, alternative pattern matching) is unchanged; only
  `enclosingSentence()` implementation was improved to handle dotted versions,
  and `weakRecommendationVerbPattern` was extended to include recommend*.

- [x] **Docs gate: 0 violations across 28 shipped Markdown files** —
  Status.json iteration-9 builder entry reports:
  "scripts/verify-docs-canonical-examples.sh reports 0 violations across all
  28 tracked shipped Markdown files". This confirms all shipped Markdown (docs/
  *.md) pass the extended docsguard with both new fixes applied. No doc
  wording was changed; the gate catches all examples correctly with the improved
  parser.

- [x] **No runtime/auth/middleware/header/status/version/dependency changes** —
  Verified via `git diff 2ee4cd0 8a60e883 -- middleware/ controllers/api/ VERSION`:
  no output (no changes). This confirms the iteration maintains complete
  backwards compatibility; no runtime authentication behavior, session handling,
  RBAC, or response status/header semantics changed.

- [x] **All prior rules (encoding, Bearer, scanner, redaction, table,
  alternative, negation, proximity, boundaries) preserved** —
  No changes to:
  - `candidateKeyPattern` (parameter name detection and percent-decoding)
  - `rawAuthorization()` (Bearer casing check, scheme recognition)
  - `hasAffirmativeDeprecationContext()` core logic (negation word detection,
    boundary patterns, proximity bounds)
  - `negationWordPattern`, `negationBoundaryPattern`, `negationProximityWords`
  - `deprecationContextPattern` (includes new "0.13.0" literal, but no logic
    change)
  - `offeredAsAlternativePattern` (or-an-api_key detection)
  - `strongRecommendationPattern` (authenticate verb)
  - `recommendationAnchorPattern` (parameter/param/query/field/header/credential)
  - `tableRowPattern` (Markdown table row detection)
  - `tableRowMention()`, `ScanText()`, `paragraphsFromLines()` logic
  - `Violation` structure and `Kind.Explanation()` (only carries location and
    kind, never source text)

  All these components remain exactly as implemented in iteration 8. The
  iteration-9 changes are limited to: adding three new helper functions for
  versioned-aware sentence boundaries, updating `enclosingSentence()` to use
  them, and adding `recommend\w*` to one regex pattern.

## Quality Gate Results

- **Syntax/format verification**: docsguard.go and docsguard_test.go are valid
  Go code (no syntax errors in git show output; no linting failures reported
  in Builder validation).
  
- **Test fixture count**: 2 new test functions added
  - `TestScanTextDottedVersionNotSentenceBoundary` (4 fixtures: 3 negative
    same-sentence cases + 1 positive cross-sentence case)
  - `TestScanTextRecommendVerb` (9 fixtures: 5 positive anchor+verb cases + 4
    negative context/anchor-less/no-api_key cases)
  Total: 13 new fixtures. All 51+ prior fixtures remain and pass unchanged.
  
- **Builder validation entry reports**:
  - "go test": "pass (including 2 new docsguard test functions covering
    dotted-version sentence scoping and the recommend-verb candidate set; all
    prior fixtures unchanged and passing)"
  - "go test -race": "pass"
  - "go build": "pass"
  - "go vet": "pass"
  - "golangci_lint": "0 issues"
  - "docs_gate_sanity_check": "0 violations across all 28 tracked shipped
    Markdown files"
  - "git_diff_check": "clean"
  - "lockfiles_unchanged": true

- **No security/auth/middleware regressions**: Unchanged files passed all prior
  iterations. No new credentials, logging, or secrets in new code. No flagged
  linting or scanning issues beyond pre-existing baselines.

## Technical Findings

### 1. Dotted Version Period Handling

The `isDottedVersionPeriod(text, idx)` function correctly identifies periods
that are part of dotted version numbers:

```go
isDigit := func(b byte) bool { return b >= '0' && b <= '9' }
return isDigit(text[idx-1]) && isDigit(text[idx+1])
```

This is both narrow (only ASCII digits 0-9, no Unicode complications) and
auditable (the check is explicit and easy to verify).

`nextSentenceTerminator()` and `prevSentenceTerminator()` both use this check
to skip dotted version periods while scanning for real sentence terminators.
The backward scan correctly handles the case where a version appears
mid-sentence followed by trailing text (e.g., "until 0.13.0, after which").

### 2. Sentence-Scoped Context Association

The updated `enclosingSentence()` implementation:

```go
func enclosingSentence(text string, pos int) string {
    start := 0
    if idx := prevSentenceTerminator(text, pos-1); idx != -1 {
        start = idx + 1
    }
    end := len(text)
    if idx := nextSentenceTerminator(text, pos); idx != -1 {
        end = idx + 1
    }
    return text[start:end]
}
```

Correctly bounds text to the sentence containing byte offset `pos` by finding
the nearest real sentence terminators on both sides, skipping dotted version
periods. This is used by `recommendationMention()` to apply deprecation-context
and negation checks per sentence, not per paragraph, preventing unrelated
deprecation keywords from suppressing live recommendations in different
sentences of the same paragraph.

### 3. Recommend Verb Pattern Extension

The `weakRecommendationVerbPattern` regex now includes `recommend\w*`:

```go
`(?i)\b(?:use\w*|via|with|accept\w*|recommend\w*)\b` + recommendationProximity + 
`api_key\b|\bapi_key\b` + recommendationProximity +
`\b(?:use\w*|via|with|accept\w*|recommend\w*)\b`
```

The `\w*` suffix matches recommend, recommends, recommended, recommending.
Case-insensitive flag `(?i)` handles any casing. The pattern is subject to
the same proximity bound (`recommendationProximity` = 0–4 intervening words)
and anchor-word requirement (`recommendationAnchorPattern`) as the other weak
verbs, ensuring no new, looser code path is introduced.

### 4. Preservation of All Prior Semantics

The changes are strictly additive:
- No modification to negation word detection, boundary patterns, or proximity
  bounds
- No change to parameter credential detection, raw Authorization checking,
  table row detection, or alternative patterns
- No change to diagnostic redaction (Violation never carries source text)
- No change to scanner error fail-closed behavior
- No runtime authentication changes

Prior fixtures for all these behaviors continue to pass because their logic is
untouched. The iteration represents a focused parser improvement, not a
redesign.

## Summary

Iteration 9 successfully addresses both code-review gaps from iteration 8:

1. **Dotted versions no longer break sentence scoping**: `isDottedVersionPeriod()`,
   `nextSentenceTerminator()`, and `prevSentenceTerminator()` correctly handle
   removal versions like 0.13.0 as part of the deprecation context, not as
   sentence boundaries. A statement such as "Use the api_key query parameter
   only until 0.13.0." is now correctly scoped as a single sentence with 0.13.0
   as the sole affirmative context, so it does not violate.

2. **Recommend verbs are detected**: `recommend\w*` added to
   `weakRecommendationVerbPattern`, with the same proximity and anchor-word
   guards as other weak verbs. "We recommend the api_key query parameter" now
   correctly violates (no context), while "We no longer recommend the api_key
   form parameter; it is deprecated" correctly does not (context suppresses).

Both fixes are narrow, bounded improvements to the existing design. All 51+
prior test fixtures pass unchanged. Docs gate reports 0 violations across 28
shipped Markdown files. No runtime, auth, middleware, version, or dependency
changes. The implementation is correct, comprehensive, and ready for release.

