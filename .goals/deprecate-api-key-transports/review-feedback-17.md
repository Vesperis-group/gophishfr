# Review feedback — iteration 17 (user-directed scope reduction)

**Role:** user-directed decision, applied by the Builder.
**Verdict:** scope reduction accepted; replaces the open finding carried
into iteration 17 ("normalize.go bypass via links/comments/attributes/
multiline/code-span/emphasis compositions").

## Why

Iterations 11 through 16 repeatedly tried to make `internal/docsguard`
generally understand what an arbitrary rendered Markdown/HTML composition
would *render as*, so that a canonical-looking documentation example
could never hide a deprecated `api_key`/raw-`Authorization` example
behind one. Each iteration closed the specific compositions the previous
independent review had found (links, inline comments, quoted attributes,
multiline HTML-block comments, single-character and whole-span emphasis/
strikethrough, named/numeric character references, code spans, fences,
lists, soft line wraps, directional transport binding, sentence/clause-
scoped context suppression), and each time review found another valid
CommonMark/GFM composition the fix had not considered. This is an
unbounded cat-and-mouse game against the full space of CommonMark/GFM
rendering; a bounded, dependency-free, stdlib-only scanner cannot ever
finish winning it.

The user made an explicit decision to stop: **this gate is not a runtime
security boundary.** It is a narrow CI assertion tool scoped only to this
PR's (#64) canonical documents, which are hand-authored and hand-reviewed
by the same people who would introduce a regression — not adversarial
input a general Markdown/HTML parser needs to defend against.

## What changed

- `internal/docsguard/normalize.go`, `html.go`, `context.go`, and
  `credential.go` — the entire class of rendered-Markdown/HTML
  normalization, HTML-block visible-text extraction, sentence/clause-
  scoped negation and deprecation-context proximity binding, and
  percent-decoding-aware credential matching — are **deleted**, not
  patched again.
- `internal/docsguard/docsguard.go` is a complete rewrite. It no longer
  scans every tracked Markdown file in the repository. It has a fixed,
  hardcoded registry of exactly the four documentation files this PR
  ships: `docs/API_AUTHENTICATION.md`, `docs/GROUP_IMPORT_LIMITS.md`,
  `docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md`, and
  `docs/API_KEY_TRANSPORT_DEPRECATION.md` (the migration guide).
  No other file is read by this gate at all.
- For each registered file, the gate makes narrow, explicit, deterministic
  assertions directly against its literal source text:
  - a **required fact** is satisfied if a small, bounded regular
    expression matches *somewhere* in the document, after one bounded
    preprocessing step (`flattenWhitespace`: collapses whitespace runs,
    including an ordinary Markdown soft line break, into a single space;
    it does not touch punctuation, links, entities, or markup).
  - a **forbidden literal** (`api_key=`, or `Authorization:` not
    immediately followed by `Bearer`) is flagged wherever it appears, for
    the three files that are not the migration guide.
  - the migration guide's canonical examples are checked via its own
    pre-existing, explicit `Before (...)`/`After (...)` heading
    convention: every fenced code block following an `After (` heading
    must contain the literal `Authorization: Bearer`. This is the
    migration guide's one, file/section-scoped exemption from the
    forbidden-literal checks, satisfying this session's requirement that
    legacy/old examples inside explicitly known migration sections be
    allow-listed by file/section marker, not by generic context
    heuristics.
- `scripts/verify-docs-canonical-examples.sh` is now a three-line
  wrapper: `cd` to the repository root, `go run ./cmd/docsguard`. No file
  list, no `-exempt` flags: the registry lives entirely in
  `internal/docsguard`.
- `cmd/docsguard/main.go` takes no file arguments at all (an optional
  `-root` flag for tests); it fails closed with a clear message if a
  registered canonical document cannot be read (renamed or deleted).

## Item (2): the "legacy clients" false-suppression finding in context.go

`context.go`'s entire proximity/negation/adversative-boundary/sentence-
and-clause-scoping machinery is deleted along with the rest of the old
implementation, per the instructions' fallback option: a clean fix would
have required more linguistic/Markdown analysis, so the heuristic is
deleted entirely rather than patched again. The four documents this gate
now checks carry no occurrence-suppression logic of any kind to retest
against, so this specific failure mode cannot recur.

## Validation

See the "Iteration 17" section appended to
`docs/API_KEY_TRANSPORT_DEPRECATION_SELF_REVIEW.md` for the full gate
evidence (build/vet/fmt/lint/test/race/mod-verify/gosec/gitleaks/
actionlint/govulncheck/docsguard-gate results, and the `go.mod`/`go.sum`
byte-identical confirmation against commit
`99c9a50848e8a826360ea7585a57d7e6f753a34c`).

## Hard constraints confirmed

- Zero new dependency; `go.mod`/`go.sum` byte-identical to the base
  commit (`sha256sum` verified).
- No Goldmark, no hand-rolled general Markdown/HTML parser of any kind.
- No runtime auth/middleware/version change: this iteration touches only
  `internal/docsguard`, `cmd/docsguard`, `scripts/verify-docs-canonical-examples.sh`,
  and this goal's own tracking documents.
- The simplification still provably enforces, via `go run ./cmd/docsguard`
  passing with 0 violations against this PR's real documents: Bearer
  canonical, query/form deprecated with `0.13.0` removal, raw
  `Authorization` deprecated with no removal date, rotation + log-cleanup
  guidance present in the documents that state it.
