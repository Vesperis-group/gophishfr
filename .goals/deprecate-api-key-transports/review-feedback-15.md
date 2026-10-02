# Review feedback — iteration 15 security review (addressed in iteration 16)

Source: user-relayed security-review findings against the iteration-15
dependency-free `internal/docsguard` rebuild. Two security findings, two
code findings. All four fixed in iteration 16 with bounded, deterministic
stdlib logic; no dependency added; `go.mod`/`go.sum` remain byte-for-byte
identical to the pre-goldmark base.

## Finding 1 — SECURITY: rendered-markup splits not fully covered

Protected anchors interrupted by rendered Markdown constructs this
package did not yet normalize could still slip through:

- A Markdown link splitting the anchor: `[api](url)_key=TOKEN`,
  `Author[ization](url): TOKEN` — only the link text renders.
- An inline HTML comment splitting the anchor: `api<!-- -->_key=TOKEN` —
  a comment never renders as visible text.
- An `<a>` tag, including with an `href` attribute, splitting the anchor
  the same way the already-allow-listed safe tags do:
  `api<a>_</a>key=TOKEN`.
- `&nbsp;` (a whitespace-producing named character reference) was
  missing from the entity table, risking an inconsistent/incomplete
  normalization of a whitespace-separated header example.

**Fix**: `markdownLinkPattern` and `htmlCommentInlinePattern` added and
wired into `decodeRenderedMarkup` ahead of the existing safe-tag
stripper; `<a>` added to the safe-inline-tag allow-list; `&nbsp;` added
to the named-character-reference table, decoded to the real non-breaking-
space code point (not a plain space) so a `&nbsp;`-separated example is
still correctly flagged non-canonical rather than normalized to look
canonical. See `TestScanTextRenderedMarkupSplit` (extended) and
`TestScanTextRenderedEscapeNormalization` (extended).

## Finding 2 — SECURITY: deprecation context bound to transport kind, not the exact occurrence

`"The old api_key query parameter is deprecated; use the api_key query
parameter in new integrations."` was incorrectly accepted: the
deprecation context bound to "query" as a transport kind anywhere nearby,
rather than to the specific occurrence of `api_key` actually being
recommended. A genuine deprecation of one occurrence must not suppress a
live recommendation of a *different* occurrence of the same transport
kind.

**Fix**: `hasAffirmativeDeprecationContext` now requires exact-occurrence
binding (via the existing `nearestAPIKeyOccurrence` nearest-by-word-
distance tie-break) between a context word and the recommendation it
evaluates, falling back to the prior transport-kind-only behavior only
when neither side names any transport at all (fully ambiguous).
A narrow `restrictiveQualifierPattern` ("only for ...") additionally
excludes a same-occurrence context word that merely grants conditional
continued use rather than reasserting deprecation.
`negationWordPattern` gained "avoid"/"avoids"/"avoiding", with the shared
negation check (`isNegatedAt`) now also excluding a directly negated
recommendation from consideration in `recommendationMention` itself.
See `TestScanTextDeprecationBoundToExactOccurrence` (new),
`TestScanTextTransportMismatchDoesNotSuppress` and
`TestScanTextCompactMixedTransportDirectional` (updated).

## Finding 3 — CODE: whole-value balanced emphasis/strikethrough not recognized

`**api_key=TOKEN**` (and `*...*`, `___...___`, `__...__`, `_..._`,
`~~...~~`, nested variants) render as the plain credential-shaped text
once the delimiters are resolved, but were not normalized before
matching; conversely a canonical header example wrapped the same way
must remain correctly accepted.

**Fix**: `wholeSpanEmphasisPatterns` (a fixed enumeration, longest-
delimiter-first, since Go's RE2-based `regexp` has no backreferences)
and `stripWholeSpanEmphasis`, run up to 5 passes to resolve nesting,
wired as the last step of `decodeRenderedMarkup`. Underscore-based
patterns require a non-alphanumeric/non-underscore boundary outside the
delimiter run so `api_key_backup`/`my_variable_name`-style identifiers
are never mis-stripped. See `TestScanTextWholeSpanEmphasis` (new).

## Finding 4 — CODE: mixed raw-HTML lines treated as all-or-nothing verbatim

`<code>x</code> Use the api_key query parameter for authentication.` was
incorrectly treated as entirely verbatim (no check at all), because
`flattenHTMLBlockVisibleText` tracked one verbatim boolean per whole
physical line rather than per segment.

**Fix**: `htmlVisibleLine` renamed to `htmlVisibleSegment`;
`flattenHTMLBlockVisibleText` now starts a new segment whenever the line
number or verbatim state changes, coalescing consecutive same-state text.
No change needed in `docsguard.go`: `joinableTextRuns` and the per-line
check loops already operate generically over however many segments share
one line number. See `TestScanTextHTMLMixedVerbatimSegments` (new).

## Verification

- `go build ./...`, `go vet ./...`, `gofmt -l` — clean.
- `go test ./...` — all packages pass, no regressions in the iteration
  1-15 corpus.
- `go.mod`/`go.sum` re-verified byte-for-byte identical to the
  pre-goldmark base via `sha256sum` (not merely `git diff`).
- `scripts/verify-docs-canonical-examples.sh` — 0 violations across 28
  tracked Markdown files, no doc wording changed.
- `./scripts/verify.sh` — all gates pass (gofmt, golangci-lint, go vet,
  go build, go test, docs gate, go test -race, frontend build,
  govulncheck).
- `gosec -quiet ./...` — 12 issues (unchanged baseline).
- `gitleaks detect --no-git --source . --redact` — 1 leak (unchanged
  known fixture).
- `actionlint`, `zizmor .github/workflows/` — clean.
- `./scripts/test-browser.sh` — pass.
- Docker build + `./scripts/test-docker-config-no-log.sh` — pass.

No runtime authentication/middleware/header/status/version/dependency
behavior change. `VERSION` unchanged.
