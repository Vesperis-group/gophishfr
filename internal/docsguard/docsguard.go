// Package docsguard asserts that a fixed, small set of first-party
// documentation files this repository actually ships for the API-key
// transport deprecation (`security/deprecate-legacy-api-key-transports`,
// PR #64) still state the facts that PR requires, and that the two of
// those files which are not migration/compatibility guides have not
// silently regressed back toward recommending a deprecated transport. It
// backs scripts/verify-docs-canonical-examples.sh and the "docs-guard" CI
// job.
//
// # Why this package stopped trying to understand rendered Markdown
//
// Earlier iterations of this package tried to generally defeat every
// Markdown/HTML composition (links, comments, quoted attributes, multiline
// comments, emphasis, entities, code spans, fences, lists, soft wraps,
// transport binding, context proximity) that a canonical-looking
// documentation example could hide a deprecated transport behind.
// Independent review repeatedly found another valid composition that
// bypassed the previous one: that is an unbounded cat-and-mouse game
// against the full space of CommonMark/GFM rendering, not a problem a
// bounded, dependency-free, stdlib-only scanner can ever finish winning.
//
// An explicit user decision (iteration 17 of this goal) stopped that
// approach entirely: this package is **not** a runtime security boundary,
// and the documents it checks are not adversarial input -- they are four
// specific files this same PR authors, reviews, and merges by hand. Given
// that, this package no longer tries to understand what an arbitrary
// Markdown/HTML composition would *render as*. It instead makes a small
// number of narrow, explicit, deterministic assertions directly against
// each named file's literal source text:
//
//   - a required fact is satisfied if a small, explicit, bounded regular
//     expression matches *somewhere* in the document (see requiredFact).
//     Facts are checked independently rather than bound to one sentence or
//     clause: this package does not track which specific occurrence of
//     "api_key" or "Authorization" a given deprecation notice is "about".
//     That is a real, accepted reduction in precision, not an oversight --
//     see the package-level doc comment in the parent goal's review
//     evidence for the full rationale.
//   - a forbidden literal is flagged wherever it appears at all (see
//     forbiddenLiteral), for the two files that are not migration guides
//     and therefore have no legitimate reason to show a deprecated
//     transport example at all.
//   - the migration guide's own canonical ("After (...)") examples are
//     checked directly, bound to that guide's own pre-existing, explicit
//     "Before (...)"/"After (...)" heading convention (see
//     checkCanonicalExamples) -- not inferred from generic surrounding
//     prose.
//
// None of this is a Markdown or HTML parser, a link-destination parser, an
// entity decoder, or an emphasis-delimiter matcher: every check here is a
// plain regular expression or literal substring search over the document's
// raw bytes, after at most one trivial, bounded preprocessing step
// (flattenWhitespace, which only collapses runs of whitespace -- including
// a Markdown soft line break -- into a single space, the same way a
// browser's own whitespace collapsing does for ordinary flowed text; it
// does not touch punctuation, links, entities, or markup of any kind).
//
// # The canonical documents this PR ships
//
// See canonicalDocs for the authoritative, maintained list. As of this
// writing: docs/API_AUTHENTICATION.md (the primary contract description),
// docs/GROUP_IMPORT_LIMITS.md (one pre-existing table row this PR updated
// to describe the same contract), docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md
// (the draft release-note artifact), and docs/API_KEY_TRANSPORT_DEPRECATION.md
// (the migration guide, whose deliberate "Before" examples are the one
// explicit, file/section-scoped exemption from the forbidden-literal
// checks). docs/API_KEY_TRANSPORT_DEPRECATION_SELF_REVIEW.md -- which
// necessarily quotes and discusses these same deprecated-transport examples
// and this package's own prior/current detection rules while recording
// review history -- is not part of this list at all: it is an internal
// review record, not first-party guidance aimed at an external API client.
//
// No other tracked Markdown file is scanned. Earlier iterations scanned
// every tracked Markdown file in the repository, which is what forced this
// package to defend against arbitrary rendering compositions in files it
// does not own and did not write for this PR. Narrowing the scan to just
// the files this PR actually ships removes that need entirely.
package docsguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Violation is one failed deterministic assertion against one canonical
// document. Line is 1-indexed and set only for a line-attributable finding
// (a forbidden literal, or a canonical-example check); it is 0 for a
// document-wide required fact, which is not about any one line. Violation
// never carries the matched source text, only File, Line, and a fixed
// Message, so a forbidden example that happens to contain a real secret is
// never echoed into a CI log.
type Violation struct {
	File    string
	Line    int
	Message string
}

// requiredFact is one named, document-wide fact a canonical document must
// state somewhere in its text. All of Patterns must match (in any order,
// anywhere in the document) for the fact to be considered satisfied; see
// the package doc comment for why presence-anywhere, rather than binding
// patterns to the same sentence/clause, is the deliberate scope here.
type requiredFact struct {
	name     string
	patterns []*regexp.Regexp
}

func (f requiredFact) satisfiedBy(flattened string) bool {
	for _, p := range f.patterns {
		if !p.MatchString(flattened) {
			return false
		}
	}
	return true
}

// The required facts shared across the canonical documents below. Each is
// a small, explicit, bounded regular expression -- never a link, entity,
// HTML, or emphasis construct -- matched against flattenWhitespace(content).
var (
	factBearerCanonical = requiredFact{
		name: "Authorization: Bearer must be presented as the canonical/recommended transport",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`Authorization:\s*Bearer\b`),
			regexp.MustCompile(`(?i)\b(?:canonical|recommended)\b`),
		},
	}
	factQueryDeprecated0130 = requiredFact{
		name: "the api_key query parameter must be marked deprecated with removal targeted for 0.13.0",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)query.{0,30}api_key.{0,20}parameter|api_key.{0,20}query.{0,10}parameter`),
			regexp.MustCompile(`(?i)deprecat\w*`),
			regexp.MustCompile(`0\.13\.0`),
		},
	}
	factFormDeprecated0130 = requiredFact{
		name: "the api_key form parameter must be marked deprecated with removal targeted for 0.13.0",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)form.{0,30}api_key.{0,20}parameter|api_key.{0,20}form.{0,10}parameter`),
			regexp.MustCompile(`(?i)deprecat\w*`),
			regexp.MustCompile(`0\.13\.0`),
		},
	}
	factRawDeprecatedNoRemovalVersion = requiredFact{
		name: "raw Authorization (no Bearer prefix) must be marked deprecated legacy with no removal version stated",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)raw.{0,30}Authorization`),
			regexp.MustCompile(`(?i)deprecat\w*`),
			regexp.MustCompile(`(?i)no.{0,30}removal version`),
		},
	}
	factRotationGuidance = requiredFact{
		name: "leaked query-key rotation guidance must be present",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)rotat\w*`),
			regexp.MustCompile(`(?i)query`),
		},
	}
	factLogCleanupGuidance = requiredFact{
		name: "historical log cleanup/retention guidance must be present",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)log`),
			regexp.MustCompile(`(?i)retent\w*|remov\w*.{0,20}log|review.{0,20}log`),
		},
	}
)

// authorizationHeaderPattern finds every literal "Authorization:" header
// name in a canonical document's raw text (case-sensitive: every one of
// this PR's own canonical documents always spells it this way). It is used
// only to locate where forbidNonBearerAuthorization must check what
// immediately follows, never to decide whether some rendered composition
// would also produce this text -- this package no longer tries to answer
// that question at all (see the package doc comment).
var authorizationHeaderPattern = regexp.MustCompile(`Authorization:`)

// parameterCredentialLiteralPattern matches the literal, working
// query/form credential syntax: "api_key=". It is intentionally only this
// one literal substring (not a percent-decoding-aware matcher, as an
// earlier iteration had): the two documents this is checked against today
// contain no credential syntax of any kind, so this exists only to catch a
// future regression reintroducing one, and a literal, undisguised
// "api_key=" is exactly what a hand-authored regression would look like.
var parameterCredentialLiteralPattern = regexp.MustCompile(`api_key=`)

// canonicalExampleHeadingPattern matches this repository's own,
// pre-existing "After (...)" heading convention in the migration guide
// (docs/API_KEY_TRANSPORT_DEPRECATION.md): a line beginning with the
// literal text "After (" introduces that guide's canonical, migrated
// example, as opposed to a "Before (...)" line, which introduces the
// deprecated example it replaces. This is the explicit, file-owned
// section-marker convention checkCanonicalExamples binds to, rather than
// inferring "this is a canonical example" from surrounding prose.
var canonicalExampleHeadingPattern = regexp.MustCompile(`^After \(`)

// fenceLinePattern matches a fenced code block delimiter line: this
// package only needs to find the next complete ``` ... ``` block after a
// canonicalExampleHeadingPattern match, never to parse an info string or
// nested fences, so a plain three-backtick line match is sufficient.
var fenceLinePattern = regexp.MustCompile("^```")

// canonicalDoc describes one of the four documentation files this PR
// ships and the deterministic checks that apply to it.
type canonicalDoc struct {
	// path is repository-root-relative.
	path string
	// requiredFacts must each be satisfied somewhere in the document.
	requiredFacts []requiredFact
	// forbidNonBearerAuthorization, if true, flags every
	// "Authorization:" occurrence not immediately followed by the literal
	// "Bearer": this document is not a migration guide, so it has no
	// legitimate reason to show any other Authorization value.
	forbidNonBearerAuthorization bool
	// forbidParameterCredentialLiteral, if true, flags every literal
	// "api_key=" occurrence, for the same reason.
	forbidParameterCredentialLiteral bool
	// checkCanonicalExamples, if true, additionally requires that every
	// fenced code block following an "After (" heading (see
	// canonicalExampleHeadingPattern) contains the literal
	// "Authorization: Bearer". This is the migration guide's own
	// exemption from the two forbid* checks above (its "Before (...)"
	// examples are expected, explicit, and exempt by definition), paired
	// with a positive assertion that its "After (...)" examples still
	// show the canonical transport.
	checkCanonicalExamples bool
}

// canonicalDocs is the authoritative, maintained list of documentation
// files this PR (security/deprecate-legacy-api-key-transports, #64) ships
// for the API-key transport deprecation, and the deterministic facts each
// one must state. See the package doc comment's "The canonical documents
// this PR ships" section for why exactly these four, and no others.
var canonicalDocs = []canonicalDoc{
	{
		path: "docs/API_AUTHENTICATION.md",
		requiredFacts: []requiredFact{
			factBearerCanonical,
			factQueryDeprecated0130,
			factFormDeprecated0130,
			factRawDeprecatedNoRemovalVersion,
		},
		forbidNonBearerAuthorization:     true,
		forbidParameterCredentialLiteral: true,
	},
	{
		path: "docs/GROUP_IMPORT_LIMITS.md",
		requiredFacts: []requiredFact{
			factBearerCanonical,
			factQueryDeprecated0130,
		},
		forbidNonBearerAuthorization:     true,
		forbidParameterCredentialLiteral: true,
	},
	{
		path: "docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md",
		requiredFacts: []requiredFact{
			factBearerCanonical,
			factQueryDeprecated0130,
			factFormDeprecated0130,
			factRawDeprecatedNoRemovalVersion,
			factRotationGuidance,
			factLogCleanupGuidance,
		},
		forbidNonBearerAuthorization:     true,
		forbidParameterCredentialLiteral: true,
	},
	{
		path: "docs/API_KEY_TRANSPORT_DEPRECATION.md",
		requiredFacts: []requiredFact{
			factBearerCanonical,
			factQueryDeprecated0130,
			factFormDeprecated0130,
			factRawDeprecatedNoRemovalVersion,
			factRotationGuidance,
			factLogCleanupGuidance,
		},
		// The migration guide: deliberate "Before (...)" examples are
		// expected, so the generic forbidden-literal checks do not apply
		// (see checkCanonicalExamples instead).
		checkCanonicalExamples: true,
	},
}

// flattenWhitespace collapses every run of whitespace (including a
// Markdown soft line break) into a single space, so a required fact whose
// matching text happens to be wrapped across two physical source lines is
// still found. This is the one, bounded, documented preprocessing step
// this package applies before checking a requiredFact; it does not
// decode, strip, or otherwise interpret any Markdown/HTML construct.
func flattenWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// lineAt returns the 1-indexed line number containing byte offset in s.
func lineAt(s string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	return strings.Count(s[:offset], "\n") + 1
}

// checkRequiredFacts returns one Violation per requiredFact in doc that is
// not satisfied somewhere in content.
func checkRequiredFacts(doc canonicalDoc, content string) []Violation {
	var violations []Violation
	flattened := flattenWhitespace(content)
	for _, fact := range doc.requiredFacts {
		if !fact.satisfiedBy(flattened) {
			violations = append(violations, Violation{
				File:    doc.path,
				Message: "missing required fact: " + fact.name,
			})
		}
	}
	return violations
}

// checkForbiddenAuthorization flags every literal "Authorization:" in
// content not immediately (after optional whitespace/quote/backtick
// wrapper characters) followed by the literal "Bearer".
func checkForbiddenAuthorization(doc canonicalDoc, content string) []Violation {
	if !doc.forbidNonBearerAuthorization {
		return nil
	}
	var violations []Violation
	for _, loc := range authorizationHeaderPattern.FindAllStringIndex(content, -1) {
		rest := content[loc[1]:]
		trimmed := strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(trimmed, "Bearer") {
			continue
		}
		violations = append(violations, Violation{
			File:    doc.path,
			Line:    lineAt(content, loc[0]),
			Message: "Authorization: here is not immediately followed by the literal \"Bearer\"; this document is not the migration guide and must not show any other Authorization value",
		})
	}
	return violations
}

// checkForbiddenParameterCredential flags every literal "api_key=" in
// content.
func checkForbiddenParameterCredential(doc canonicalDoc, content string) []Violation {
	if !doc.forbidParameterCredentialLiteral {
		return nil
	}
	var violations []Violation
	for _, loc := range parameterCredentialLiteralPattern.FindAllStringIndex(content, -1) {
		violations = append(violations, Violation{
			File:    doc.path,
			Line:    lineAt(content, loc[0]),
			Message: "literal \"api_key=\" here is a working deprecated-transport example; this document is not the migration guide and must not show one",
		})
	}
	return violations
}

// checkCanonicalExamples requires that every fenced code block following
// this file's own "After (" heading convention (see
// canonicalExampleHeadingPattern) contains the literal
// "Authorization: Bearer". It intentionally does not inspect any "Before
// (...)" block at all: those are the migration guide's expected, explicit,
// deprecated examples.
func checkCanonicalExamples(doc canonicalDoc, content string) []Violation {
	if !doc.checkCanonicalExamples {
		return nil
	}
	var violations []Violation
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		if !canonicalExampleHeadingPattern.MatchString(lines[i]) {
			continue
		}
		headingLine := i + 1

		// Find the next fenced code block (open then close), within the
		// rest of the document.
		openAt := -1
		for j := i + 1; j < len(lines); j++ {
			if fenceLinePattern.MatchString(lines[j]) {
				openAt = j
				break
			}
		}
		if openAt == -1 {
			violations = append(violations, Violation{
				File:    doc.path,
				Line:    headingLine,
				Message: "an \"After (\" canonical-example heading here is not followed by any fenced code block",
			})
			continue
		}
		closeAt := -1
		for j := openAt + 1; j < len(lines); j++ {
			if fenceLinePattern.MatchString(lines[j]) {
				closeAt = j
				break
			}
		}
		if closeAt == -1 {
			violations = append(violations, Violation{
				File:    doc.path,
				Line:    headingLine,
				Message: "an \"After (\" canonical-example heading here is followed by an unterminated fenced code block",
			})
			continue
		}
		block := strings.Join(lines[openAt+1:closeAt], "\n")
		if !strings.Contains(block, "Authorization: Bearer") {
			violations = append(violations, Violation{
				File:    doc.path,
				Line:    headingLine,
				Message: "the canonical example fenced code block here does not contain the literal \"Authorization: Bearer\"",
			})
		}
	}
	return violations
}

// CheckFile runs every check canonicalDocs registers for path against
// content, returning every Violation found. It returns nil, nil if path is
// not a registered canonical document (CheckAll is the usual entry point;
// this is exported separately so a caller -- most importantly this
// package's own tests -- can check one in-memory fixture directly).
func CheckFile(path, content string) []Violation {
	for _, doc := range canonicalDocs {
		if doc.path != path {
			continue
		}
		var violations []Violation
		violations = append(violations, checkRequiredFacts(doc, content)...)
		violations = append(violations, checkForbiddenAuthorization(doc, content)...)
		violations = append(violations, checkForbiddenParameterCredential(doc, content)...)
		violations = append(violations, checkCanonicalExamples(doc, content)...)
		return violations
	}
	return nil
}

// CheckAll reads every file in canonicalDocs from root and returns every
// Violation found across all of them, plus the number of files
// successfully scanned. It returns a non-nil error, and callers must fail
// closed, if any registered canonical document cannot be read at all (most
// importantly: it was renamed or deleted) -- a missing canonical document
// is itself the kind of regression this gate exists to catch.
func CheckAll(root string) ([]Violation, int, error) {
	var violations []Violation
	scanned := 0
	for _, doc := range canonicalDocs {
		full := filepath.Join(root, doc.path)
		contents, err := os.ReadFile(full) // #nosec G304 -- path is this package's own fixed, hardcoded registry (canonicalDocs), never external input.
		if err != nil {
			return violations, scanned, fmt.Errorf("reading canonical document %s: %w", doc.path, err)
		}
		scanned++
		violations = append(violations, CheckFile(doc.path, string(contents))...)
	}
	return violations, scanned, nil
}
