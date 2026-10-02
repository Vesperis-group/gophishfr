// Package docsguard finds deprecated API-key transport examples in
// first-party Markdown documentation. It backs
// scripts/verify-docs-canonical-examples.sh and the "docs-guard" CI job: both
// exist so a future documentation change cannot silently reintroduce a
// `?api_key=`/form `api_key` example, a raw (non-scheme) Authorization
// header example, or ordinary prose recommending a deprecated transport
// (a table row, an "or an api_key" alternative, or a sentence like "use the
// api_key query parameter", even one wrapped across several Markdown lines)
// as if it were a canonical, recommended transport.
//
// # Why this package does not parse Markdown
//
// An earlier iteration of this package parsed documents with a real
// CommonMark/GFM AST library. A later, explicit user decision reversed
// that: this repository's dependency policy (see CLAUDE.md) requires every
// new dependency to be justified by need, alternative, security impact,
// and maintenance impact, and the maintenance impact of an AST dependency
// for one internal documentation-linting tool was judged not worth it. This
// package is therefore, again, dependency-free: go.mod/go.sum carry no
// Markdown parser.
//
// This is a real, accepted trade-off, not a claim of completeness. This
// package scans plain text for the literal substrings that would make an
// example authenticate against the real credential extractor in
// middleware.extractExplicitAPICredential (see the "Credential-syntax
// contract" section below), plus a small number of bounded, explicitly
// documented, deterministic approximations of specific CommonMark rendering
// rules that past reviews found a canonical-looking example could hide
// behind: a backslash escape or HTML character reference (see
// normalizeRenderedEscapes), a safe inline HTML tag or narrow emphasis
// delimiter wrapping exactly one protected character (same), a fenced or
// indented code block (which renders every one of those completely
// literally, and so is deliberately never normalized -- see ScanText), and
// a raw HTML block (see html.go, which documents its own, separate, bounded
// approximation of CommonMark's HTML-block grammar). Each approximation is
// narrow and documented at its own definition; none of them is a general
// Markdown or HTML parser, and this package does not attempt to handle
// every construct CommonMark/GFM defines -- only the ones a real
// documentation page in this repository has actually used, or a real
// review has actually found exploitable. The canonical-documentation gate
// this package enforces (see scripts/verify-docs-canonical-examples.sh)
// remains enforceable specifically because its scope is this narrow: it
// only has to correctly read this repository's own first-party Markdown,
// not render arbitrary Markdown from the wild.
//
// # Credential-syntax contract
//
// The query/form parameter name and the Authorization scheme are matched
// against the same literal contract the real credential extractor in
// middleware.extractExplicitAPICredential uses: the parameter name is
// exactly "api_key" (percent-decoded, since Go's URL/form parsing
// percent-decodes parameter names before comparing them), and the
// Authorization header's value is matched against the runtime's exact
// prefix, `strings.TrimPrefix(authorization, "Bearer ")` -- the literal
// word "Bearer", one literal space, and nothing else; anything else (wrong
// casing, a tab or two spaces instead of that one space, or no credential
// token following it at all) never actually extracts a working canonical
// credential, so it is flagged as raw/legacy too (see
// rawAuthorizationMatches).
//
// # Diagnostics
//
// Every reported Violation carries only a file-relative line range and a
// Kind; it never carries the matched text, so a forbidden example that
// happens to contain a real secret is never echoed by a caller that prints
// violations (see Kind.Explanation, which returns a fixed, generic
// description instead). If the scanner itself fails -- most importantly
// bufio.ErrTooLong on a line that exceeds its internal buffer -- ScanText
// returns a non-nil error, and callers must fail closed rather than trust
// the returned violations as "no violations found".
package docsguard

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// Kind identifies which deprecated transport a Violation demonstrates.
type Kind string

const (
	// KindParameterCredential covers every `api_key=` syntax: a query
	// parameter (`?api_key=`/`&api_key=`), a form body (`curl -d`/`--data`/
	// `--data-raw`/`--data-urlencode`), and a multipart form field
	// (`curl -F`/`--form`). The real extractor reads the exact key
	// "api_key" from both the URL query and application/x-www-form-urlencoded
	// POST bodies -- after percent-decoding, exactly like Go's net/url and
	// net/http do -- so a literal `api_key=` and a percent-encoded
	// equivalent such as `api%5Fkey=` are both working examples of one of
	// those two deprecated transports.
	KindParameterCredential Kind = "parameter_credential" // #nosec G101 -- a label string, not a credential value.

	// KindRawAuthorization covers an `Authorization` header (any
	// letter-casing, since HTTP header names are case-insensitive) whose
	// value does not have the runtime's exact, case-sensitive "Bearer "
	// prefix followed by a non-empty credential token. See the package
	// doc comment's "Credential-syntax contract" section.
	KindRawAuthorization Kind = "raw_authorization"

	// KindUndeprecatedParameterMention covers a Markdown table row, a
	// "presented as an alternative" sentence, or ordinary recommendation
	// prose (e.g. "use the api_key query parameter", "authenticate via
	// api_key") -- including one wrapped across several physical lines --
	// that presents `api_key` as an ordinary, currently supported
	// authentication option with no *affirmative* deprecation context that
	// (a) is in the same sentence/clause, (b) is not separated from it by
	// an adversative connector, (c) is close enough to an actual `api_key`
	// mention to describe that transport specifically, (d) describes the
	// same specific transport (query/form/raw) rather than a different
	// one, and (e) if the only context is the bare removal version
	// number, is accompanied by an explicit deprecation/removal
	// construction word rather than standing alone. A negated context
	// ("not deprecated", "no longer legacy") where the negation directly
	// governs it does not suppress this: it means the surrounding text is
	// actively asserting the opposite of the real contract, which is
	// itself the violation. A negation that instead governs the
	// recommendation verb -- "do NOT use the api_key parameter; it is
	// deprecated" -- is a legitimate warning and does not violate. Unlike
	// KindParameterCredential, this has no `=` sign and would never
	// authenticate anything -- it is a documentation-accuracy check, not
	// a credential-syntax check.
	KindUndeprecatedParameterMention Kind = "undeprecated_parameter_mention"
)

// Explanation returns a fixed, non-sensitive description of what this kind
// of violation means. It deliberately never includes any text from the
// document being scanned: callers must report only a Violation's file, line
// range, Kind, and this Explanation -- never source text, so a forbidden
// example that happens to contain a real secret is never echoed into a CI
// log.
func (k Kind) Explanation() string {
	switch k {
	case KindParameterCredential:
		return "a query or form parameter here decodes to exactly \"api_key\", the deprecated credential parameter name"
	case KindRawAuthorization:
		return "the Authorization header value here is not the runtime's exact canonical \"Bearer \" + token form (or another recognized scheme), so it is a raw/legacy credential"
	case KindUndeprecatedParameterMention:
		return "this text presents the deprecated api_key parameter as an ordinary, currently supported option without clear affirmative deprecation context"
	default:
		return "a deprecated API-key transport example"
	}
}

// Violation is one deprecated-transport example found in a document. It
// carries only its location and Kind -- never the matched text -- so a
// caller can safely print every field of a Violation (see Kind.Explanation)
// without risk of echoing a real secret a forbidden example happened to
// contain.
type Violation struct {
	// StartLine and EndLine are the 1-indexed, inclusive line range the
	// violation was found in. For a single-line violation (parameter
	// credential, raw Authorization, a table row) they are equal; a
	// recommendation-prose or soft-wrapped-header violation spanning
	// several physical lines reports that span's full line range.
	StartLine int
	EndLine   int
	Kind      Kind
}

// tableRowPattern matches a Markdown table row: a line whose first
// non-whitespace character is a pipe.
var tableRowPattern = regexp.MustCompile(`^\s*\|`)

// lineKind classifies one physical source line for ScanText's scanning
// loop.
type lineKind int

const (
	// kindPlain is an ordinary Markdown prose line: escape/entity/
	// markup-normalized before any check runs (see
	// normalizeRenderedEscapes).
	kindPlain lineKind = iota
	// kindCode is a fenced or indented code-block line: scanned
	// completely literally, never joined with neighbors, and never
	// checked for a recommendation mention (code is an example, not
	// prose).
	kindCode
	// kindHTML is a line inside a raw HTML block: its checkText is that
	// line's extracted *visible* text (see flattenHTMLBlockVisibleText),
	// already entity-decoded but never backslash-escape-decoded.
	// verbatim additionally marks a line that fell inside a <pre>/<code>
	// element, which (like kindCode) is never joined with neighbors.
	kindHTML
)

// scannedLine is one physical source line after ScanText's first pass:
// classified, and with its checkText already normalized appropriately for
// its kind.
type scannedLine struct {
	lineNo    int
	checkText string
	kind      lineKind
	verbatim  bool
}

// ScanText scans arbitrary text (typically one Markdown file's contents) and
// returns every deprecated-transport example it finds. It returns a non-nil
// error if the scanner itself failed -- most importantly bufio.ErrTooLong on
// a line that exceeds the internal buffer -- in which case the returned
// violations are necessarily incomplete and callers must fail closed rather
// than trust them as "no violations found".
func ScanText(text string) ([]Violation, error) {
	var violations []Violation
	var scannedLines []scannedLine

	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var inFence bool
	var fenceChar byte
	var fenceLen int

	var inHTML bool
	var htmlKind string
	var htmlBlockLines []string
	var htmlBlockStartLine int

	flushHTMLBlock := func() {
		if len(htmlBlockLines) == 0 {
			return
		}
		for _, vl := range flattenHTMLBlockVisibleText(htmlBlockLines, htmlBlockStartLine) {
			scannedLines = append(scannedLines, scannedLine{
				lineNo:    vl.lineNo,
				checkText: vl.text,
				kind:      kindHTML,
				verbatim:  vl.verbatim,
			})
		}
		htmlBlockLines = nil
	}

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()

		if inHTML {
			htmlBlockLines = append(htmlBlockLines, raw)
			if htmlBlockCloses(raw, htmlKind) {
				inHTML = false
				flushHTMLBlock()
			}
			continue
		}

		if inFence {
			if ch, length, ok := matchCodeFence(raw); ok && ch == fenceChar && length >= fenceLen {
				inFence = false
			}
			scannedLines = append(scannedLines, scannedLine{lineNo: lineNo, checkText: raw, kind: kindCode})
			continue
		}

		if ch, length, ok := matchCodeFence(raw); ok {
			fenceChar, fenceLen = ch, length
			inFence = true
			scannedLines = append(scannedLines, scannedLine{lineNo: lineNo, checkText: raw, kind: kindCode})
			continue
		}
		if indentedCodeLinePattern.MatchString(raw) {
			scannedLines = append(scannedLines, scannedLine{lineNo: lineNo, checkText: raw, kind: kindCode})
			continue
		}
		if kind, closedSameLine, ok := detectHTMLBlockOpen(raw); ok {
			htmlKind = kind
			htmlBlockStartLine = lineNo
			htmlBlockLines = []string{raw}
			if closedSameLine {
				flushHTMLBlock()
			} else {
				inHTML = true
			}
			continue
		}

		scannedLines = append(scannedLines, scannedLine{lineNo: lineNo, checkText: normalizeRenderedEscapes(raw), kind: kindPlain})
	}
	if err := scanner.Err(); err != nil {
		return violations, fmt.Errorf("scanning line %d: %w", lineNo+1, err)
	}
	if inHTML {
		// The file ended while still inside an open HTML block that never
		// reached its specific closing marker (or, for the "generic"
		// kind, a blank line): CommonMark itself still treats this as one
		// HTML block running to the end of the document, so flush what
		// was collected rather than silently dropping it.
		flushHTMLBlock()
	}

	// Parameter-credential and table-row checks always run per physical
	// line, for every kind: a credential or a Markdown table row is
	// always meant to be read on its own line regardless of surrounding
	// context, and neither is ever split across an ordinary soft line
	// break the way an Authorization header's value can be (see below).
	for _, sl := range scannedLines {
		if hasParameterCredential(sl.checkText) {
			violations = append(violations, Violation{StartLine: sl.lineNo, EndLine: sl.lineNo, Kind: KindParameterCredential})
		}
		if tableRowMention(sl.checkText) {
			violations = append(violations, Violation{StartLine: sl.lineNo, EndLine: sl.lineNo, Kind: KindUndeprecatedParameterMention})
		}
	}

	// A fenced/indented code-block line, or an HTML line that fell inside
	// a <pre>/<code> element, is never joined with its neighbors (see
	// joinableTextRuns): neither ever collapses whitespace when actually
	// rendered, so each such line still needs its own, independent raw-
	// Authorization check.
	for _, sl := range scannedLines {
		if (sl.kind == kindCode || sl.verbatim) && rawAuthorization(sl.checkText) {
			violations = append(violations, Violation{StartLine: sl.lineNo, EndLine: sl.lineNo, Kind: KindRawAuthorization})
		}
	}

	// Raw-Authorization and recommendation-prose checks otherwise run
	// over *joined* text runs -- consecutive ordinary prose lines, or
	// consecutive non-verbatim lines of one HTML block -- instead of one
	// physical line at a time, so an ordinary soft line break between a
	// header name and its value, or a recommendation sentence wrapped
	// across several lines, is still caught. This is the only place
	// either check runs over more than one physical line, so a
	// single-line header or a one-line paragraph is still reported
	// exactly once, and two unrelated runs are never joined together.
	for _, run := range joinableTextRuns(scannedLines) {
		for _, m := range rawAuthorizationMatches(run.text) {
			violations = append(violations, Violation{
				StartLine: run.lineAt(m[0]),
				EndLine:   run.lineAt(m[1] - 1),
				Kind:      KindRawAuthorization,
			})
		}
		if recommendationMention(run.text) {
			violations = append(violations, Violation{StartLine: run.startLine, EndLine: run.endLine, Kind: KindUndeprecatedParameterMention})
		}
	}

	return dedupeViolations(violations), nil
}

// dedupeViolations removes exact (StartLine, EndLine, Kind) duplicates,
// which can occur when a single-line run independently matches more than
// one documentation-accuracy check.
func dedupeViolations(violations []Violation) []Violation {
	seen := make(map[Violation]bool, len(violations))
	deduped := violations[:0]
	for _, v := range violations {
		if seen[v] {
			continue
		}
		seen[v] = true
		deduped = append(deduped, v)
	}
	return deduped
}

// textRun is one group of consecutive, joinable scannedLines (see
// joinableTextRuns), combined into a single whitespace-joined string for
// the raw-Authorization and recommendation-prose checks, together with a
// way to map a byte offset in that combined string back to the physical
// source line it came from.
type textRun struct {
	startLine int
	endLine   int
	text      string
	lineAt    func(offset int) int
}

// joinableTextRuns groups scannedLines into textRuns: consecutive lines
// that are either ordinary prose (kindPlain) or a raw HTML block's visible,
// non-verbatim text (kindHTML, verbatim=false), each non-blank after
// normalization, joined by a single space -- mirroring the single space an
// ordinary Markdown soft line break, or a browser's own whitespace
// collapsing of ordinary (non-<pre>/<code>) HTML text, both render as. A
// run ends at a blank line, a kind change, a code line, or a verbatim HTML
// line, so two unrelated runs (different paragraphs, different HTML
// blocks, or prose on either side of a code block) are never joined
// together.
func joinableTextRuns(lines []scannedLine) []textRun {
	var runs []textRun
	var group []scannedLine

	flush := func() {
		if len(group) == 0 {
			return
		}
		var sb strings.Builder
		var lineAtByte []int
		for i, sl := range group {
			if i > 0 {
				lineAtByte = append(lineAtByte, group[i-1].lineNo)
				sb.WriteByte(' ')
			}
			for range len(sl.checkText) {
				lineAtByte = append(lineAtByte, sl.lineNo)
			}
			sb.WriteString(sl.checkText)
		}
		first, last := group[0].lineNo, group[len(group)-1].lineNo
		runs = append(runs, textRun{
			startLine: first,
			endLine:   last,
			text:      sb.String(),
			lineAt: func(offset int) int {
				if len(lineAtByte) == 0 {
					return first
				}
				return lineAtByte[clampIndex(offset, len(lineAtByte)-1)]
			},
		})
		group = nil
	}

	for _, sl := range lines {
		joinable := (sl.kind == kindPlain || (sl.kind == kindHTML && !sl.verbatim)) && strings.TrimSpace(sl.checkText) != ""
		sameKind := len(group) == 0 || group[len(group)-1].kind == sl.kind
		if !joinable || !sameKind {
			flush()
		}
		if joinable {
			group = append(group, sl)
		}
	}
	flush()
	return runs
}

// tableRowMention reports whether a single physical line is a Markdown
// table row presenting `api_key` with no affirmative deprecation context.
// Table rows stay a per-line check (unlike recommendationMention) because a
// Markdown table row is always exactly one physical line by construction.
// recommendationPos is set to len(line): the row itself is the thing being
// judged, not a verb at a particular position, so any affirmative context
// anywhere in the row counts (subject to the usual negation, adversative-
// boundary, proximity, bare-version, and transport-matching rules).
func tableRowMention(line string) bool {
	if !strings.Contains(strings.ToLower(line), "api_key") {
		return false
	}
	if !tableRowPattern.MatchString(line) {
		return false
	}
	return !hasAffirmativeDeprecationContext(line, len(line))
}
