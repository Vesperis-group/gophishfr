// Package docsguard finds deprecated API-key transport examples in
// first-party Markdown documentation. It backs
// scripts/verify-docs-canonical-examples.sh and the "docs-guard" CI job: both
// exist so a future documentation change cannot silently reintroduce a
// `?api_key=`/form `api_key` example, a raw (non-scheme) Authorization
// header example, or ordinary prose recommending a deprecated transport
// (a table cell, an "or an api_key" alternative, or a sentence like "use the
// api_key query parameter", even one wrapped across several Markdown lines)
// as if it were a canonical, recommended transport.
//
// # Why a real Markdown/GFM parser
//
// Earlier iterations of this package scanned plain text with an ever-growing
// set of hand-written regular expressions approximating Markdown rendering:
// backslash escapes, HTML character references, inline HTML tags, emphasis
// delimiters, code-span/fence boundaries, and indented-code detection were
// each bolted on as rendering-bypass findings were discovered one at a time.
// That approach is fundamentally unable to keep up: CommonMark/GFM rendering
// has real, interacting structural rules (delimiter-run flanking for
// emphasis, matching fence character/length, list-continuation indentation
// versus a genuine indented code block, variable-length code-span
// delimiters) that a flat set of regexes cannot soundly approximate without
// either missing real bypasses or inventing false ones.
//
// This package instead parses each document with goldmark
// (github.com/yuin/goldmark/v2), a tested, widely used, actively maintained
// CommonMark/GFM-compliant parser, and analyzes the resulting syntax tree.
// goldmark's AST nodes already carry the *decoded* text a renderer would
// actually display (backslash escapes and HTML character references are
// resolved once, at parse time, by a pluggable text.Decoder -- see
// flattenInline) and already encode every one of the structural rules above
// correctly, so this package no longer needs to reimplement any of them.
//
// # What is scanned, and how
//
// Three kinds of block are scanned, each according to how it actually
// renders:
//
//   - A code block (indented or fenced, backtick or tilde, with or without
//     an info string) renders every line completely literally: no
//     backslash escape, character reference, emphasis, or raw HTML is ever
//     processed inside one. Its lines are matched against
//     hasParameterCredential/rawAuthorization verbatim, with no decoding.
//   - A paragraph, heading, or GFM table cell's inline content is flattened
//     into the plain text it would actually render as (see flattenInline),
//     then checked for both credential syntax
//     (hasParameterCredential/rawAuthorization) and documentation-accuracy
//     recommendation prose (recommendationMention, tableCellMention),
//     exactly as it would read to a person or a client copying the example.
//   - An inline code span's content, even though it sits inside a
//     paragraph's otherwise-decoded text, renders literally (same rule as a
//     code block) and is included in the flattened text unmodified, so a
//     literal forbidden example inside one is still caught while a
//     backslash-escaped or character-referenced one -- which never actually
//     decodes there -- is not.
//
// A raw inline HTML tag or comment, and a Markdown link's destination, never
// contribute visible text and are dropped entirely during flattening; an
// emphasis/strong/strikethrough delimiter that does not actually form valid
// emphasis per CommonMark's own delimiter-run rules (for example a single
// punctuation character wrapped in intraword asterisks) is never parsed as
// one in the first place, and so already appears in the flattened text as
// the literal, un-rendered source -- no special-case handling is needed for
// either of these: they fall directly out of using a real parser.
//
// # Credential-syntax contract
//
// The query/form parameter name and the Authorization scheme are matched
// against the same literal contract the real credential extractor in
// middleware.extractExplicitAPICredential uses: the parameter name is
// exactly "api_key" (percent-decoded, since Go's URL/form parsing
// percent-decodes parameter names before comparing them), and the
// Authorization header's value is matched against the runtime's exact,
// case-sensitive "Bearer " prefix (the header *name* stays case-insensitive,
// per RFC 9110).
//
// # Diagnostics
//
// Every reported Violation carries only a file-relative line range and a
// Kind; it never carries the matched text, so a forbidden example that
// happens to contain a real secret is never echoed by a caller that prints
// violations (see Kind.Explanation, which returns a fixed, generic
// description instead). If the document fails to parse (which the
// underlying parser does not do for any input in practice, but this package
// still checks defensively), ScanText returns an error and callers must fail
// closed rather than trust an empty violation list.
package docsguard

import (
	"fmt"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	extast "github.com/yuin/goldmark/v2/extension/ast"
	"github.com/yuin/goldmark/v2/parser"
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
	// value is not the runtime's exact, case-sensitive canonical form. The
	// real extractor only strips a literal "Bearer " prefix
	// (strings.TrimPrefix is case-sensitive); any other casing of the word
	// "bearer" is therefore not recognized as canonical at runtime and falls
	// into the same raw/legacy bucket as a bare token, so it is flagged
	// here too. Other real schemes such as `Authorization: Basic ...` are
	// matched case-insensitively, per RFC 7235, and are not flagged.
	KindRawAuthorization Kind = "raw_authorization"

	// KindUndeprecatedParameterMention covers a GFM table cell, a
	// "presented as an alternative" sentence, or ordinary recommendation
	// prose (e.g. "use the api_key query parameter", "authenticate via
	// api_key") -- including one wrapped across several physical lines --
	// that presents `api_key` as an ordinary, currently supported
	// authentication option with no *affirmative* deprecation context that
	// (a) is in the same sentence/clause, (b) is not separated from it by an
	// adversative connector, (c) is close enough to an actual `api_key`
	// mention to describe that transport specifically, (d) describes the
	// same specific transport (query/form/raw) rather than a different one,
	// and (e) if the only context is the bare removal version number, is
	// accompanied by an explicit deprecation/removal construction word
	// rather than standing alone. A negated context ("not deprecated", "no
	// longer legacy") where the negation directly governs it does not
	// suppress this: it means the surrounding text is actively asserting the
	// opposite of the real contract, which is itself the violation. A
	// negation that instead governs the recommendation verb -- "do NOT use
	// the api_key parameter; it is deprecated" -- is a legitimate warning
	// and does not violate. Unlike KindParameterCredential, this has no `=`
	// sign and would never authenticate anything -- it is a
	// documentation-accuracy check, not a credential-syntax check.
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
		return "the Authorization header value here is not the runtime's exact canonical \"Bearer\" scheme (or another recognized scheme), so it is a raw/legacy credential"
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
	// credential, raw Authorization, a table cell occupying one line) they
	// are equal; a recommendation-prose violation spanning a wrapped
	// Markdown paragraph reports that paragraph's full line range.
	StartLine int
	EndLine   int
	Kind      Kind
}

// mdParser is this package's single, shared CommonMark+GFM parser instance.
// It is stateless and safe for concurrent use across calls to ScanText.
var mdParser = parser.New(parser.WithExtensions(extension.GFMParser))

// maxLineLength bounds the length of any single physical line this package
// will scan. The previous, bufio.Scanner-based implementation failed closed
// on bufio.ErrTooLong for a line exceeding its internal buffer; this
// explicit check preserves the exact same fail-closed guarantee under the
// new AST-based implementation, which has no equivalent built-in limit of
// its own.
const maxLineLength = 1024 * 1024

// oversizedLine reports the 1-indexed line number of the first physical
// line in source exceeding maxLineLength, or -1 if none does.
func oversizedLine(source []byte) int {
	lineNo := 1
	start := 0
	for i, b := range source {
		if b == '\n' {
			if i-start > maxLineLength {
				return lineNo
			}
			lineNo++
			start = i + 1
		}
	}
	if len(source)-start > maxLineLength {
		return lineNo
	}
	return -1
}

// ScanText scans arbitrary text (typically one Markdown file's contents) and
// returns every deprecated-transport example it finds. It parses the text
// with mdParser and walks the resulting syntax tree; it returns a non-nil
// error if any line exceeds maxLineLength or if parsing/walking fails, in
// which case the returned violations are necessarily incomplete and callers
// must fail closed rather than trust them as "no violations found".
func ScanText(text string) (violations []Violation, err error) {
	source := []byte(text)

	if lineNo := oversizedLine(source); lineNo != -1 {
		return nil, fmt.Errorf("scanning line %d: line exceeds maximum length of %d bytes", lineNo, maxLineLength)
	}

	lines := newLineIndex(source)

	// goldmark's parser does not itself return errors for malformed input
	// (CommonMark has no concept of a syntactically invalid document), but
	// this package fails closed defensively against a panic in the parser
	// or walker -- for example from an unexpectedly deep or malformed tree
	// -- rather than silently reporting "no violations found".
	defer func() {
		if r := recover(); r != nil {
			violations = nil
			err = fmt.Errorf("parsing document: %v", r)
		}
	}()

	doc := mdParser.Parse(source)

	walkErr := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.CodeBlock:
			violations = append(violations, scanCodeBlock(source, lines, v)...)
			return ast.WalkSkipChildren, nil
		case *extast.TableCell:
			violations = append(violations, scanTableCell(source, lines, n)...)
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph, *ast.Heading:
			violations = append(violations, scanProseBlock(source, lines, n)...)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	if walkErr != nil {
		return violations, fmt.Errorf("walking document: %w", walkErr)
	}

	return dedupeViolations(violations), nil
}

// dedupeViolations removes exact (StartLine, EndLine, Kind) duplicates,
// which can occur when a block independently matches more than one
// documentation-accuracy check (for example both tableCellMention and
// recommendationMention on the same short table cell).
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

// lineIndex maps a byte offset into a source document to its 1-indexed line
// number, so block nodes (whose positions are byte offsets) can be reported
// in the Violation{StartLine, EndLine} contract this package has always
// used.
type lineIndex struct {
	// starts[i] is the byte offset at which line i+1 begins. starts[0] is
	// always 0 (line 1 begins at the start of the document).
	starts []int
}

// newLineIndex builds a lineIndex for source.
func newLineIndex(source []byte) *lineIndex {
	starts := make([]int, 1, 64)
	starts[0] = 0
	for i, b := range source {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &lineIndex{starts: starts}
}

// lineAt returns the 1-indexed line number containing byte offset.
func (li *lineIndex) lineAt(offset int) int {
	lo, hi := 0, len(li.starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if li.starts[mid] <= offset {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// blockLineRange returns the 1-indexed, inclusive [startLine, endLine] range
// of the given block node's source, falling back to a single line at offset
// 0 if the node reports no source segments at all (which should not happen
// for a parsed Paragraph/Heading/TableCell/CodeBlock, but is handled
// defensively rather than risking a panic or a silently wrong line number).
func blockLineRange(lines *lineIndex, source []byte, block ast.BlockNode) (start, end int) {
	segs := block.Source()
	if len(segs) == 0 {
		return 1, 1
	}
	first := segs[0]
	last := segs[len(segs)-1]
	start = lines.lineAt(first.Start)
	stop := last.Stop
	if stop > len(source) {
		stop = len(source)
	}
	if stop > first.Start {
		stop--
	}
	end = lines.lineAt(stop)
	if end < start {
		end = start
	}
	return start, end
}
