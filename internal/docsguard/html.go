package docsguard

import (
	"regexp"
	"strings"
)

// # Raw HTML blocks: a bounded, stdlib-only approximation
//
// CommonMark recognizes several kinds of "raw HTML block" (a line starting
// with `<script`/`<pre`/`<style`, an HTML comment opener, a processing
// instruction, a declaration, a CDATA section, or any other tag), each with
// its own precise opening/closing rule. This package does not implement
// that grammar exactly (in particular, it does not track whether a bare
// tag-opening line is actually "interrupting a paragraph", which changes
// whether CommonMark treats it as one of these blocks at all): instead, it
// uses one bounded, deliberately *more inclusive* rule -- any line starting
// with a syntactically valid tag opener is treated as starting a raw HTML
// block -- documented here so the limitation is explicit rather than
// silent. Treating a borderline case as an HTML block when CommonMark might
// not have only means this package additionally applies entity decoding and
// HTML-aware whitespace collapsing to it; it never *skips* a check ordinary
// Markdown prose would otherwise get.
//
// Once a block's extent is known, its *visible* text -- what a browser
// would actually display -- is extracted with a small, linear scanner
// (flattenHTMLBlockVisibleText) recognizing exactly the handful of
// constructs CommonMark's own HTML-block grammar defines: comments,
// processing instructions, declarations, CDATA sections, tag markup/
// attributes (all dropped -- none is ever visible text), and <script>/
// <style> element content specifically (dropped too, since neither ever
// displays as readable prose), while everything else -- including a
// <div>'s or <pre>'s inner text -- is kept. An unterminated comment/CDATA/
// processing-instruction/declaration/<script>/<style> element consumes the
// rest of the block the same conservative way a real browser's tokenizer
// does; anything this scanner cannot otherwise confidently classify (an
// unterminated tag) fails closed the other way, keeping it as ordinary
// visible text rather than risking silently hiding a real credential.
//
// <pre>/<code> content is visible text like any other element's, but its
// whitespace is never collapsed across lines for detection purposes (see
// ScanText): a real browser preserves <pre>/<code>'s line breaks visually
// instead of collapsing them into a single space the way it does for
// ordinary flowed text, so a credential split across <pre>/<code>'s own
// line breaks is, correctly, not treated as if it were on one line --
// exactly the same explicit choice this package already makes for an
// ordinary fenced or indented code block.

// htmlBlockOpenerPattern matches a line (ignoring up to three leading
// spaces, as CommonMark allows) starting with a syntactically valid HTML
// tag opener: "<" or "</", an ASCII letter, then more name characters,
// followed by whitespace, ">", "/>", or end of line. This is the bounded,
// more-inclusive rule described above.
var htmlBlockOpenerPattern = regexp.MustCompile(`^ {0,3}</?[A-Za-z][A-Za-z0-9-]*(?:[\s>]|/>|$)`)

// htmlCommentOpenPattern, htmlCDATAOpenPattern, htmlPIOpenPattern, and
// htmlDeclOpenPattern each recognize one of CommonMark's special
// HTML-block openers, used only to pick the right closing rule (see
// htmlBlockCloses); the tag markup itself is still stripped by
// flattenHTMLBlockVisibleText regardless of which of these matched.
var (
	htmlCommentOpenPattern = regexp.MustCompile(`^ {0,3}<!--`)
	htmlCDATAOpenPattern   = regexp.MustCompile(`^ {0,3}<!\[CDATA\[`)
	htmlPIOpenPattern      = regexp.MustCompile(`^ {0,3}<\?`)
	htmlDeclOpenPattern    = regexp.MustCompile(`^ {0,3}<![A-Za-z]`)
	htmlRawTextOpenPattern = regexp.MustCompile(`(?i)^ {0,3}<(?:script|style|pre|textarea)(?:[\s>]|/>|$)`)
)

// htmlRawTextClosePattern matches any of CommonMark's type-1 closing tags
// (script/pre/style/textarea), case-insensitively, anywhere in a line --
// matching CommonMark's own rule, which does not require the closer to name
// the same tag that opened the block.
var htmlRawTextClosePattern = regexp.MustCompile(`(?i)</(?:script|style|pre|textarea)>`)

// detectHTMLBlockOpen classifies the kind of raw HTML block a line starts,
// and whether line also closes it (a one-line block that opens and closes
// on the same line). kind is one of "comment", "cdata", "pi", "decl",
// "rawtext", or "generic" (CommonMark's merged type 6/7, see
// htmlBlockOpenerPattern's doc comment).
func detectHTMLBlockOpen(line string) (kind string, closedSameLine bool, ok bool) {
	switch {
	case htmlCommentOpenPattern.MatchString(line):
		return "comment", strings.Contains(line, "-->"), true
	case htmlCDATAOpenPattern.MatchString(line):
		return "cdata", strings.Contains(line, "]]>"), true
	case htmlPIOpenPattern.MatchString(line):
		return "pi", strings.Contains(line, "?>"), true
	case htmlDeclOpenPattern.MatchString(line):
		return "decl", strings.Contains(line, ">"), true
	case htmlRawTextOpenPattern.MatchString(line):
		return "rawtext", htmlRawTextClosePattern.MatchString(line), true
	case htmlBlockOpenerPattern.MatchString(line):
		return "generic", strings.TrimSpace(line) == "", true
	}
	return "", false, false
}

// htmlBlockCloses reports whether line is the closing line of an
// already-open raw HTML block of the given kind: "generic" closes at the
// next blank line (CommonMark's own rule for its merged type 6/7); every
// other kind closes at the line containing its specific closing marker.
func htmlBlockCloses(line string, kind string) bool {
	switch kind {
	case "comment":
		return strings.Contains(line, "-->")
	case "cdata":
		return strings.Contains(line, "]]>")
	case "pi":
		return strings.Contains(line, "?>")
	case "decl":
		return strings.Contains(line, ">")
	case "rawtext":
		return htmlRawTextClosePattern.MatchString(line)
	default: // "generic"
		return strings.TrimSpace(line) == ""
	}
}

// htmlRawTextTags are the only tag names whose content is never rendered as
// visible page text by a browser (the CommonMark/GFM "raw text" elements):
// <script> executes as code and <style> is interpreted as CSS -- neither
// ever displays as readable prose a person or client would see as
// documentation guidance, unlike every other element (including <pre> and
// <div>), whose inner text content is always visible.
var htmlRawTextTags = map[string]bool{
	"script": true,
	"style":  true,
}

// htmlVerbatimTags are the elements whose whitespace a browser never
// collapses: a <pre> or <code> element preserves its content's actual line
// breaks visually, instead of collapsing them into a single space the way
// it does for ordinary flowed text (see flattenHTMLBlockVisibleText).
var htmlVerbatimTags = map[string]bool{
	"pre":  true,
	"code": true,
}

// htmlVisibleSegment is one contiguous run of extracted visible text
// within one physical source line (see flattenHTMLBlockVisibleText),
// together with its source line number and whether it fell inside a
// <pre>/<code> element. A single physical line can produce more than one
// segment when it mixes verbatim and non-verbatim content ("<code>x</code>
// Use the api_key query parameter..."): each segment shares that line's
// lineNo but carries its own verbatim flag, so the surrounding, non-code
// prose is still scanned as ordinary prose (and still joinable with
// adjacent lines/paragraphs) while the code content is still scanned
// per-line as literal, exactly like any other mixed line.
type htmlVisibleSegment struct {
	lineNo   int
	text     string
	verbatim bool
}

// flattenHTMLBlockVisibleText extracts the plain, *visible* text a browser
// would display for one raw HTML block's lines (see the package-section
// doc comment above), returning one or more htmlVisibleSegments per
// physical source line (see htmlVisibleSegment). Comments, processing
// instructions, declarations, CDATA sections, tag markup, and attribute
// values are dropped; <script>/<style> content is dropped too; everything
// else is kept, with HTML character references decoded the same narrow
// way decodeEntities does for ordinary prose (backslash escapes are
// deliberately *not* decoded here: CommonMark never Markdown-processes raw
// HTML block content, only a browser's own HTML entity resolution applies
// to it).
func flattenHTMLBlockVisibleText(lines []string, startLineNo int) []htmlVisibleSegment {
	raw := strings.Join(lines, "\n")

	// lineStarts[k] is the byte offset in raw where lines[k] begins.
	lineStarts := make([]int, len(lines))
	pos := 0
	for k, l := range lines {
		lineStarts[k] = pos
		pos += len(l) + 1 // +1 for the "\n" strings.Join inserted
	}
	lineIndexAt := func(offset int) int {
		idx := 0
		for idx+1 < len(lineStarts) && lineStarts[idx+1] <= offset {
			idx++
		}
		return idx
	}

	var segments []htmlVisibleSegment

	// appendVisible attributes raw[from:to] (never containing a "<" that
	// starts a recognized construct) to its original line(s), splitting on
	// any embedded "\n" so a long run of plain text spanning several
	// physical lines still lands on the right ones. A new segment is
	// started whenever the line changes or the verbatim state changes
	// from the previous one; consecutive same-line, same-verbatim text is
	// appended to the current segment instead of starting a new one, so a
	// line untouched by any verbatim transition still produces exactly
	// one segment, same as before this supported mixed lines.
	appendVisible := func(from, to int, verbatimDepth int) {
		if from >= to {
			return
		}
		verbatim := verbatimDepth > 0
		segStart := from
		for _, part := range strings.SplitAfter(raw[from:to], "\n") {
			if part == "" {
				continue
			}
			idx := lineIndexAt(segStart)
			segStart += len(part)
			if idx < 0 || idx >= len(lines) {
				continue
			}
			decoded := decodeEntities(strings.TrimSuffix(part, "\n"))
			lineNo := startLineNo + idx
			if n := len(segments); n > 0 && segments[n-1].lineNo == lineNo && segments[n-1].verbatim == verbatim {
				segments[n-1].text += decoded
				continue
			}
			segments = append(segments, htmlVisibleSegment{lineNo: lineNo, text: decoded, verbatim: verbatim})
		}
	}

	verbatimDepth := 0
	i := 0
	for i < len(raw) {
		if raw[i] != '<' {
			j := i
			for j < len(raw) && raw[j] != '<' {
				j++
			}
			appendVisible(i, j, verbatimDepth)
			i = j
			continue
		}

		if end, ok := skipHTMLComment(raw, i); ok {
			i = end
			continue
		}
		if end, ok := skipHTMLCDATA(raw, i); ok {
			i = end
			continue
		}
		if end, ok := skipHTMLProcessingInstruction(raw, i); ok {
			i = end
			continue
		}
		if end, ok := skipHTMLDeclaration(raw, i); ok {
			i = end
			continue
		}
		if tagEnd, tagName, isOpen, isClose, ok := skipHTMLTag(raw, i); ok {
			lower := strings.ToLower(tagName)
			if isOpen && htmlRawTextTags[lower] {
				if bodyEnd, ok := skipRawTextContent(raw, tagEnd, tagName); ok {
					i = bodyEnd
					continue
				}
				// Unterminated <script>/<style>: fail closed toward
				// scanning, not away from it.
			}
			if htmlVerbatimTags[lower] {
				if isOpen {
					verbatimDepth++
				} else if isClose && verbatimDepth > 0 {
					verbatimDepth--
				}
			}
			i = tagEnd
			continue
		}

		// "<" not followed by a recognized construct at all, or a tag
		// that never finds its closing ">": keep the "<" itself as
		// literal visible text.
		appendVisible(i, i+1, verbatimDepth)
		i++
	}

	return segments
}

// skipHTMLComment, if raw[i:] begins with "<!--", returns the index just
// past its matching "-->" and ok=true. If no "-->" is found, it returns
// len(raw) (the rest of the block) and ok=true anyway: a real browser's
// tokenizer treats an unterminated comment opener as consuming everything
// up to the end of the document, so that remainder is genuinely never
// visible text either way, not a case needing a conservative fallback.
func skipHTMLComment(raw string, i int) (int, bool) {
	if !strings.HasPrefix(raw[i:], "<!--") {
		return i, false
	}
	if end := strings.Index(raw[i+4:], "-->"); end >= 0 {
		return i + 4 + end + 3, true
	}
	return len(raw), true
}

// skipHTMLCDATA is skipHTMLComment's counterpart for a `<![CDATA[ ... ]]>`
// section (same unterminated-consumes-to-EOF reasoning applies).
func skipHTMLCDATA(raw string, i int) (int, bool) {
	const open = "<![CDATA["
	if !strings.HasPrefix(raw[i:], open) {
		return i, false
	}
	if end := strings.Index(raw[i+len(open):], "]]>"); end >= 0 {
		return i + len(open) + end + 3, true
	}
	return len(raw), true
}

// skipHTMLProcessingInstruction is skipHTMLComment's counterpart for a
// `<? ... ?>` processing instruction.
func skipHTMLProcessingInstruction(raw string, i int) (int, bool) {
	if !strings.HasPrefix(raw[i:], "<?") {
		return i, false
	}
	if end := strings.Index(raw[i+2:], "?>"); end >= 0 {
		return i + 2 + end + 2, true
	}
	return len(raw), true
}

// skipHTMLDeclaration is skipHTMLComment's counterpart for a `<!DOCTYPE ...>`
// style declaration -- any "<!" not already recognized as a comment.
func skipHTMLDeclaration(raw string, i int) (int, bool) {
	if !strings.HasPrefix(raw[i:], "<!") || strings.HasPrefix(raw[i:], "<!--") {
		return i, false
	}
	if end := strings.IndexByte(raw[i+2:], '>'); end >= 0 {
		return i + 2 + end + 1, true
	}
	return len(raw), true
}

// skipHTMLTag, if raw[i:] begins with a plausible HTML tag opener ("<" then
// an optional "/" then an ASCII letter), scans the whole tag -- name,
// attributes, and an optional trailing "/" -- through its closing ">",
// correctly skipping over any ">" that appears inside a quoted attribute
// value, and returns the index just past that ">", the tag's name, whether
// it was an opening tag, whether it was a closing tag, and ok=true. If
// raw[i:] is not a plausible tag opener at all, or no closing ">" is found
// before the end of raw, it returns ok=false: the caller then fails closed
// by treating the "<" as ordinary literal text instead of risking silently
// skipping content that may not actually be a tag.
func skipHTMLTag(raw string, i int) (end int, name string, isOpen bool, isClose bool, ok bool) {
	j := i + 1
	closing := false
	if j < len(raw) && raw[j] == '/' {
		closing = true
		j++
	}
	nameStart := j
	for j < len(raw) && isHTMLTagNameByte(raw[j], j == nameStart) {
		j++
	}
	if j == nameStart {
		return 0, "", false, false, false
	}
	name = raw[nameStart:j]

	selfClosing := false
	for j < len(raw) {
		switch raw[j] {
		case '>':
			return j + 1, name, !closing && !selfClosing, closing, true
		case '"', '\'':
			quote := raw[j]
			j++
			closeQuote := strings.IndexByte(raw[j:], quote)
			if closeQuote < 0 {
				return 0, "", false, false, false
			}
			j += closeQuote + 1
		case '/':
			selfClosing = true
			j++
		default:
			j++
		}
	}
	return 0, "", false, false, false
}

// isHTMLTagNameByte reports whether b is a valid HTML tag-name byte: an
// ASCII letter for the first byte, or an ASCII letter/digit/hyphen after
// that.
func isHTMLTagNameByte(b byte, first bool) bool {
	if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') {
		return true
	}
	if first {
		return false
	}
	return (b >= '0' && b <= '9') || b == '-'
}

// skipRawTextContent, given that raw[:tagEnd] has just consumed a
// <script>/<style> opening tag named tagName, searches for its literal,
// case-insensitive closing tag ("</script>"/"</style>") starting at tagEnd
// and returns the index just past it and ok=true, or ok=false if none is
// found before the end of raw.
func skipRawTextContent(raw string, tagEnd int, tagName string) (int, bool) {
	closer := "</" + strings.ToLower(tagName)
	idx := strings.Index(strings.ToLower(raw[tagEnd:]), closer)
	if idx < 0 {
		return 0, false
	}
	rest := raw[tagEnd+idx+len(closer):]
	gt := strings.IndexByte(rest, '>')
	if gt < 0 {
		return 0, false
	}
	return tagEnd + idx + len(closer) + gt + 1, true
}
