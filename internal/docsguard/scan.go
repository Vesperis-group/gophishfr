package docsguard

import (
	"strings"

	"github.com/yuin/goldmark/v2/ast"
)

// flattenInline appends the rendered plain text of n (and its descendants)
// to sb, exactly as GitHub's Markdown would render it:
//
//   - Ordinary text: already decoded (backslash escapes and HTML character
//     references resolved) by goldmark's parser at AST-construction time --
//     see the package doc comment.
//   - A code span's content: never decoded (CommonMark renders it
//     completely literally), but still included verbatim, since it still
//     appears as plain, visible (monospaced) text inline in the paragraph.
//   - A link or autolink: only its visible label text is included; the
//     destination URL is dropped, since it is never shown as text (this is
//     what correctly flattens `[api](url)_key` into `api_key`).
//   - Raw inline HTML -- any tag or an HTML comment, regardless of name or
//     attributes -- contributes nothing: neither ever renders as visible
//     text (this is what correctly flattens `api<em>_</em>key` and
//     `api<!-- -->_key` into `api_key`).
//   - A soft or hard line break becomes a single space, so a sentence
//     wrapped across physical lines by ordinary Markdown line-wrapping
//     still reads as continuous text.
//
// Everything else (Emphasis, Strong, GFM Strikethrough, Image, and any
// other container inline node) is flattened by simply recursing into its
// children: its delimiter syntax carries no text of its own, and an
// emphasis/strong/strikethrough delimiter run that does not actually form
// valid emphasis per CommonMark's own delimiter-flanking rules (for example
// a single punctuation character wrapped in intraword asterisks) is never
// parsed into one of these node kinds in the first place -- it already
// remains in the tree as plain Text, so no special handling is needed for
// that case either.
func flattenInline(source []byte, n ast.Node, sb *strings.Builder) {
	switch v := n.(type) {
	case *ast.Text:
		sb.WriteString(v.Value.Value(source))
		if v.SoftLineBreak() || v.HardLineBreak() {
			sb.WriteByte(' ')
		}
		return
	case *ast.CodeSpan:
		sb.WriteString(v.Value.Value(source))
		return
	case *ast.RawHTML:
		return
	case *ast.AutoLink:
		sb.WriteString(v.Label.Value(source))
		return
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		flattenInline(source, c, sb)
	}
}

// inlineLineRun is one physical source line's worth of rendered inline text
// within a block. Credential-syntax matches (hasParameterCredential,
// rawAuthorization) are checked per run, so a match inside a paragraph
// wrapped across several physical lines is still reported at its own exact
// line, rather than the whole paragraph's line range; recommendation-prose
// checks (recommendationMention, tableCellMention) still need the full,
// line-joined text for correct sentence/clause scoping and continue to use
// flattenedText instead.
type inlineLineRun struct {
	lineNo int
	text   string
}

// flattenInlineByLine is like flattenInline, but splits its output back
// into one run per original physical source line (a run ends at a soft or
// hard line break).
func flattenInlineByLine(source []byte, lines *lineIndex, n ast.Node) []inlineLineRun {
	var runs []inlineLineRun
	var current strings.Builder
	currentLine := -1

	noteLine := func(offset int) {
		if currentLine == -1 {
			currentLine = lines.lineAt(offset)
		}
	}
	flush := func() {
		if current.Len() > 0 {
			runs = append(runs, inlineLineRun{lineNo: currentLine, text: current.String()})
		}
		current.Reset()
		currentLine = -1
	}

	var walk func(ast.Node)
	walk = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.Text:
			noteLine(v.Value.Index().Start)
			current.WriteString(v.Value.Value(source))
			if v.SoftLineBreak() || v.HardLineBreak() {
				flush()
			}
			return
		case *ast.CodeSpan:
			noteLine(v.Value.Index().Start)
			current.WriteString(v.Value.Value(source))
			return
		case *ast.RawHTML:
			return
		case *ast.AutoLink:
			noteLine(v.Label.Index().Start)
			current.WriteString(v.Label.Value(source))
			return
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(n)
	flush()
	return runs
}

// flattenedText returns the fully flattened plain text of block (see
// flattenInline).
func flattenedText(source []byte, block ast.Node) string {
	var sb strings.Builder
	flattenInline(source, block, &sb)
	return sb.String()
}

// scanInlineCredentials runs the two credential-syntax checks over block's
// content one physical line at a time (see flattenInlineByLine), so a match
// inside a paragraph, heading, or table cell wrapped across several
// physical lines is reported at its own exact line.
func scanInlineCredentials(source []byte, lines *lineIndex, block ast.Node) []Violation {
	var violations []Violation
	for _, run := range flattenInlineByLine(source, lines, block) {
		if hasParameterCredential(run.text) {
			violations = append(violations, Violation{StartLine: run.lineNo, EndLine: run.lineNo, Kind: KindParameterCredential})
		}
		if rawAuthorization(run.text) {
			violations = append(violations, Violation{StartLine: run.lineNo, EndLine: run.lineNo, Kind: KindRawAuthorization})
		}
	}
	return violations
}

// scanCodeBlock scans one indented or fenced code block's raw source lines
// literally for the two credential-syntax violations. CommonMark renders
// code block content completely literally -- no backslash escape,
// character reference, emphasis, or raw HTML is ever processed inside one
// -- so these lines are matched exactly as written, with no decoding: a
// literal forbidden example is still caught, while a backslash-escaped or
// character-referenced one -- which never actually decodes there -- is not
// (see the package doc comment).
func scanCodeBlock(source []byte, lines *lineIndex, block *ast.CodeBlock) []Violation {
	var violations []Violation
	for _, seg := range block.Value.Segments() {
		line := strings.TrimRight(seg.Str(source), "\r\n")
		lineNo := lines.lineAt(seg.Start)
		if hasParameterCredential(line) {
			violations = append(violations, Violation{StartLine: lineNo, EndLine: lineNo, Kind: KindParameterCredential})
		}
		if rawAuthorization(line) {
			violations = append(violations, Violation{StartLine: lineNo, EndLine: lineNo, Kind: KindRawAuthorization})
		}
	}
	return violations
}

// scanTableCell scans one GFM table cell's flattened content: the
// credential-syntax checks (as any prose block gets), tableCellMention
// (the lenient "a cell simply lists api_key" check a Markdown table row
// always got, now scoped to the cell that actually contains it instead of
// the row's raw line text), and recommendationMention (so an explicit
// recommendation verb inside a cell is caught too). Each cell is its own
// independent clause/context scope: an unrelated cell's deprecation notice
// in the same row, or a different row's, cannot suppress this one's -- see
// hasAffirmativeDeprecationContext.
func scanTableCell(source []byte, lines *lineIndex, cell ast.Node) []Violation {
	block, ok := cell.(ast.BlockNode)
	if !ok {
		return nil
	}
	text := flattenedText(source, cell)
	startLine, endLine := blockLineRange(lines, source, block)

	violations := scanInlineCredentials(source, lines, cell)
	if tableCellMention(text) || recommendationMention(text) {
		violations = append(violations, Violation{StartLine: startLine, EndLine: endLine, Kind: KindUndeprecatedParameterMention})
	}
	return violations
}

// scanProseBlock scans one paragraph or heading's flattened content for all
// three violation kinds, the same way scanTableCell does for a table cell
// (minus tableCellMention, which is specific to a table cell's lenient
// "merely listed" semantics -- ordinary prose requires an actual
// recommendation signal, via recommendationMention).
func scanProseBlock(source []byte, lines *lineIndex, block ast.Node) []Violation {
	bn, ok := block.(ast.BlockNode)
	if !ok {
		return nil
	}
	text := flattenedText(source, block)
	startLine, endLine := blockLineRange(lines, source, bn)

	violations := scanInlineCredentials(source, lines, block)
	if recommendationMention(text) {
		violations = append(violations, Violation{StartLine: startLine, EndLine: endLine, Kind: KindUndeprecatedParameterMention})
	}
	return violations
}
