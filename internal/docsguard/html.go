package docsguard

import (
	"strings"

	"github.com/yuin/goldmark/v2/ast"
)

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

// flattenHTMLBlockText extracts the plain, *visible* text a browser would
// actually display for one raw HTML block's content: comments
// (`<!-- ... -->`), processing instructions (`<? ... ?>`), declarations
// (`<!DOCTYPE ...>`), CDATA sections (`<![CDATA[ ... ]]>`), tag markup
// itself, and attribute values are all dropped (none of them is ever
// visible text), a <script>/<style> element's entire content is dropped
// too (see htmlRawTextTags), and everything else -- ordinary text, and any
// other element's inner content, including <pre>'s -- is kept.
//
// This intentionally does not attempt to be a general HTML parser: it is a
// small, linear state machine recognizing only the handful of constructs
// CommonMark's own HTML-block grammar defines, which is exactly the set
// this package needs to decide what is, and is not, visible text. An
// unterminated comment/CDATA/processing-instruction/declaration is handled
// the same way a real browser's tokenizer does -- the rest of the block is
// still never visible, so it is still dropped -- but anything this state
// machine cannot otherwise confidently classify (an unterminated tag, or an
// unterminated <script>/<style> element) fails closed the other way:
// rather than risk silently treating genuinely visible text as invisible
// markup, scanning falls back to keeping it as ordinary visible text, so a
// forbidden example is never hidden from detection merely because some
// other part of the same block happens to be malformed.
//
// The returned offsets slice has the same length as the returned text and
// maps each byte of it back to its absolute offset in source, so a caller
// can report a precise, contributing line range for any match found within
// it (see scanHTMLBlock).
func flattenHTMLBlockText(source []byte, block *ast.HTMLBlock) (text string, offsets []int) {
	segs := block.Value.Segments()
	if len(segs) == 0 {
		return "", nil
	}
	start := segs[0].Start
	stop := segs[len(segs)-1].Stop
	if stop > len(source) {
		stop = len(source)
	}
	raw := string(source[start:stop])

	var sb strings.Builder
	i := 0
	for i < len(raw) {
		if raw[i] != '<' {
			j := i
			for j < len(raw) && raw[j] != '<' {
				j++
			}
			sb.WriteString(raw[i:j])
			for k := i; k < j; k++ {
				offsets = append(offsets, start+k)
			}
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
		if tagEnd, tagName, isOpen, ok := skipHTMLTag(raw, i); ok {
			if isOpen && htmlRawTextTags[strings.ToLower(tagName)] {
				if bodyEnd, ok := skipRawTextContent(raw, tagEnd, tagName); ok {
					i = bodyEnd
					continue
				}
				// Unterminated <script>/<style>: fail closed toward
				// scanning, not away from it. Keep the tag markup itself
				// skipped (it is never visible either way) but fall
				// through to scanning the remainder as ordinary text.
			}
			i = tagEnd
			continue
		}

		// "<" not followed by a recognized construct at all, or a tag
		// that never finds its closing ">": keep the "<" itself as
		// literal visible text and continue scanning normally right
		// after it, rather than silently dropping it.
		sb.WriteByte('<')
		offsets = append(offsets, start+i)
		i++
	}
	return sb.String(), offsets
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
// it was an opening tag (neither a closing tag nor self-closing), and
// ok=true. If raw[i:] is not a plausible tag opener at all, or no closing
// ">" is found before the end of raw, it returns ok=false: the caller then
// fails closed by treating the "<" as ordinary literal text instead of
// risking silently skipping content that may not actually be a tag.
func skipHTMLTag(raw string, i int) (end int, name string, isOpen bool, ok bool) {
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
		return 0, "", false, false
	}
	name = raw[nameStart:j]

	selfClosing := false
	for j < len(raw) {
		switch raw[j] {
		case '>':
			return j + 1, name, !closing && !selfClosing, true
		case '"', '\'':
			quote := raw[j]
			j++
			closeQuote := strings.IndexByte(raw[j:], quote)
			if closeQuote < 0 {
				return 0, "", false, false
			}
			j += closeQuote + 1
		case '/':
			selfClosing = true
			j++
		default:
			j++
		}
	}
	return 0, "", false, false
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

// scanHTMLBlock scans one raw HTML block's *visible* rendered text (see
// flattenHTMLBlockText) for the same three violation kinds a paragraph or
// heading gets: a credential-syntax match is reported at the precise
// line(s) it was found on, even if the block spans several physical lines
// (see parameterCredentialMatches/rawAuthorizationMatches), and a
// recommendation-prose match is reported against the block's whole line
// range, exactly like scanProseBlock.
func scanHTMLBlock(source []byte, lines *lineIndex, block *ast.HTMLBlock) []Violation {
	text, offsets := flattenHTMLBlockText(source, block)
	if text == "" {
		return nil
	}

	lineFor := func(textOffset int) int {
		textOffset = clampIndex(textOffset, len(offsets)-1)
		return lines.lineAt(offsets[textOffset])
	}

	var violations []Violation
	for _, m := range parameterCredentialMatches(text) {
		violations = append(violations, Violation{
			StartLine: lineFor(m[0]),
			EndLine:   lineFor(m[1] - 1),
			Kind:      KindParameterCredential,
		})
	}
	for _, m := range rawAuthorizationMatches(text) {
		violations = append(violations, Violation{
			StartLine: lineFor(m[0]),
			EndLine:   lineFor(m[1] - 1),
			Kind:      KindRawAuthorization,
		})
	}
	if recommendationMention(text) {
		startLine, endLine := blockLineRange(lines, source, block)
		violations = append(violations, Violation{StartLine: startLine, EndLine: endLine, Kind: KindUndeprecatedParameterMention})
	}
	return violations
}
