package docsguard

import (
	"regexp"
	"strconv"
	"strings"
)

// backslashEscapePattern matches a backslash followed by one of the ASCII
// punctuation characters CommonMark/GitHub Flavored Markdown treats as
// escapable (`!"#$%&'()*+,-./:;<=>?@[\]^_`{|}~`). A renderer turns each such
// pair into the bare punctuation character alone -- `api\_key` and
// `Authorization\:` render identically to `api_key` and `Authorization:` --
// so this must be undone before any pattern matching, or an escaped example
// would read as canonical prose while still rendering as a working,
// deprecated credential example.
var backslashEscapePattern = regexp.MustCompile(`\\([\x21-\x2F\x3A-\x40\x5B-\x60\x7B-\x7E])`)

// numericCharRefPattern matches a decimal (`&#95;`) or hexadecimal
// (`&#x5f;`, `&#X5F;`, and any other case mix of the `x`/`X` prefix and hex
// digits) numeric character reference. Decoding every numeric reference this
// way is safe and unambiguous -- each one names exactly one Unicode code
// point -- unlike named references, which this package only recognizes for
// a narrow, explicit list (see namedCharRefReplacements); it is not a
// general HTML/XML entity parser, only this one well-defined, bounded
// transformation.
var numericCharRefPattern = regexp.MustCompile(`&#(?:[xX]([0-9a-fA-F]+)|([0-9]+));`)

// namedCharRefReplacements maps a narrow, explicit list of HTML named
// character references to the single literal character each one renders as,
// restricted to exactly the punctuation that appears in the protected
// anchors this package matches against: the underscore in `api_key`, the
// colon after `Authorization`, the `=`/`&`/`?`/`;` of query and form syntax,
// and the backslash/other delimiters that could themselves be used to
// reference one of those. This is deliberately not a general named-entity
// table (the full HTML5 list has over 2,000 entries): adding an unrelated
// one here would risk decoding something this package was never meant to
// touch, which is exactly the "general Markdown/HTML parser" scope this
// package avoids.
var namedCharRefReplacements = map[string]string{
	"&amp;":      "&",
	"&AMP;":      "&",
	"&lowbar;":   "_",
	"&UnderBar;": "_",
	"&colon;":    ":",
	"&equals;":   "=",
	"&num;":      "#",
	"&quest;":    "?",
	"&semi;":     ";",
	"&sol;":      "/",
	"&bsol;":     "\\",
	// &nbsp; decodes to U+00A0 (non-breaking space), deliberately *not*
	// a plain ASCII space: a browser renders it visually indistinguishably
	// from one, but it is a different byte, so "Authorization:
	// Bearer&nbsp;TOKEN" does not actually satisfy the runtime's exact
	// literal "Bearer " (ASCII space) prefix check -- decoding to the true
	// code point, rather than normalizing it away to a plain space, is
	// what correctly leaves rawAuthorizationMatches flagging it as
	// non-canonical.
	"&nbsp;": "\u00A0",
}

// namedCharRefPattern finds every candidate `&name;` token so
// decodeNamedCharRef can look each one up in namedCharRefReplacements
// without a full named-entity table: an unrecognized name (e.g. `&copy;`,
// `&hearts;`) is left exactly as written.
var namedCharRefPattern = regexp.MustCompile(`&[A-Za-z][A-Za-z0-9]*;`)

// safeInlineTagPattern matches an opening or closing tag for a narrow,
// explicit allow-list of safe inline HTML formatting elements GitHub's
// Markdown renders as real HTML, with or without attributes: em, i, b,
// strong, u, s, del, ins, mark, small, sub, sup, span, abbr, code, a. A
// documentation example can use one of these purely to visually split one
// of this package's protected anchors while still rendering as plain,
// readable text -- `api<em>_</em>key=TOKEN` renders exactly like
// `api_key=TOKEN`, and `api<a>_</a>key=TOKEN` the same way -- so these tags
// are stripped (never their content) before any other pattern runs. This is
// deliberately a short, explicit list of tag *names*, not a general HTML
// tag parser: anything else (a `<script>`, an `<img>`, an unknown or
// custom element) is left untouched. An `<a href="...">` tag's attribute
// (the destination URL) is dropped along with the tag markup itself, the
// same way a Markdown link's destination is (see markdownLinkPattern):
// neither is ever inspected as visible text.
var safeInlineTagPattern = regexp.MustCompile(`(?i)</?(?:em|i|b|strong|u|s|del|ins|mark|small|sub|sup|span|abbr|code|a)(?:\s[^>]*)?>`)

// htmlCommentInlinePattern matches an inline HTML comment within ordinary
// Markdown prose (as opposed to a raw HTML block, which html.go handles
// separately): `<!-- ... -->`. A comment never renders as visible text at
// all, so it is dropped entirely -- `api<!-- -->_key=TOKEN` renders
// exactly like `api_key=TOKEN`. This is bounded to a single physical line
// (CommonMark's own inline-comment-within-a-paragraph case is always one
// line in practice for the examples this package protects); a comment
// genuinely spanning multiple physical lines inside a raw HTML block is
// handled separately by html.go.
var htmlCommentInlinePattern = regexp.MustCompile(`(?s)<!--.*?-->`)

// markdownLinkPattern matches an inline Markdown link, `[text](url)`: only
// the bracketed link text ever renders as visible text; the parenthesized
// destination is never shown and must not be inspected as if it were --
// `[api](url)_key=TOKEN` renders exactly like `api_key=TOKEN`, and
// `Author[ization](url):` renders exactly like `Authorization:`. This is
// deliberately a single, non-nested pattern (no nested `[...]` or `(...)`
// inside the text or destination): a genuinely nested link is a rare,
// malformed-looking construct this package conservatively leaves
// unmatched (and therefore scanned as literal bracket/paren text) rather
// than risk mismatching bracket pairs.
var markdownLinkPattern = regexp.MustCompile(`\[([^\[\]]*)\]\([^()]*\)`)

// renderedMarkupSplitPattern matches a Markdown emphasis/strong-emphasis/
// strikethrough delimiter run (one to three asterisks, one to three
// underscores, or one to two tildes, independently on each side) wrapping
// exactly one of this package's protected punctuation characters (`_`,
// `:`, `=`, `&`). A documentation example can use this purely to visually
// split one of this package's protected anchors while still rendering as
// plain, readable text -- `api*_*key=TOKEN` renders exactly like
// `api_key=TOKEN` -- so the delimiter run on each side is stripped,
// keeping only the wrapped character. This intentionally recognizes only
// this one narrow, specific shape (a single wrapped protected character),
// not general Markdown emphasis: a delimiter run can equally be literal
// *content* rather than a wrapping delimiter (`**_**` strong-emphasizes a
// literal underscore), and there is no way to tell those apart without a
// real parser: recognizing only "exactly one wrapped protected character"
// avoids that ambiguity, since none of `_:=&` is itself a delimiter
// character this pattern also tries to match as wrapped content.
var renderedMarkupSplitPattern = regexp.MustCompile(
	`(?:\*{1,3}|_{1,3}|~{1,2})([_:=&])(?:\*{1,3}|~{1,2}|_{1,3})`,
)

// wholeSpanEmphasisPatterns strip a *matching-length* emphasis/strong/
// strikethrough delimiter run wrapping an entire credential-shaped span --
// `**api_key=TOKEN**` renders exactly like `api_key=TOKEN` (genuine,
// valid CommonMark strong emphasis around a whole non-whitespace run, not
// the single-wrapped-character case renderedMarkupSplitPattern handles).
// Go's regexp package has no backreferences (no way to write "a closing
// run of the same length as the opening run" as one pattern -- see
// inlineCodeSpanRanges), so this is a small, fixed enumeration of every
// exact delimiter length CommonMark recognizes (`*`, `**`, `***`, `_`,
// `__`, `___`, `~~`) instead, tried longest-first so a triple-delimiter
// run is never partially matched by a shorter alternative first.
//
// The `_`/`__`/`___` patterns additionally require a non-alphanumeric/
// non-underscore boundary (or start/end of string) immediately outside
// each delimiter run: CommonMark does not allow "_" to form emphasis
// *intraword* (unlike "*", which may), specifically so ordinary
// snake_case identifiers are never mistaken for emphasis -- without this,
// "my_variable_name" would be wrongly read as "my" + emphasis("variable")
// + "name" and stripped to "myvariablename", merging unrelated identifier
// parts together. `~~` has no such restriction (GFM strikethrough is
// commonly intraword in practice) and `*` is deliberately left
// unrestricted to match CommonMark's own, more permissive rule for it.
//
// Each wrapped span is matched via a non-greedy "." (Go's regexp "."
// never matches a newline unless the (?s) flag is set, which these
// patterns do not use, so this stays bounded to one physical line
// automatically), not a character class excluding the delimiter itself:
// the wrapped content can legitimately contain the delimiter character as
// *content* (most importantly, "api_key" itself contains a literal "_",
// which an underscore-excluding class would make unmatchable whenever the
// delimiter is also "_" -- e.g. a double-underscore-wrapped api_key
// credential would never complete a match, since the content capture
// could never cross that inner "_" to reach the closing run). Non-greedy
// "." still stops at the *nearest* subsequent delimiter run of the exact
// required length, so two separate, unrelated emphasised spans on the
// same line ("*first* and *second*") are still resolved independently
// rather than one over-matching into the other.
var wholeSpanEmphasisPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\*{3}(.+?)\*{3}`),
	regexp.MustCompile(`\*{2}(.+?)\*{2}`),
	regexp.MustCompile(`\*(.+?)\*`),
	regexp.MustCompile(`(^|[^A-Za-z0-9_])_{3}(.+?)_{3}([^A-Za-z0-9_]|$)`),
	regexp.MustCompile(`(^|[^A-Za-z0-9_])_{2}(.+?)_{2}([^A-Za-z0-9_]|$)`),
	regexp.MustCompile(`(^|[^A-Za-z0-9_])_(.+?)_([^A-Za-z0-9_]|$)`),
	regexp.MustCompile(`~~(.+?)~~`),
}

// wholeSpanEmphasisMaxPasses bounds how many times stripWholeSpanEmphasis
// repeats its substitution pass to resolve genuine nesting (for example
// `**_api_key=TOKEN_**`, strong wrapping emphasis): each pass can strip at
// most one "layer" of wrapping delimiters, and real documentation nests at
// most two or three layers deep, so this is a generous, still-bounded cap
// -- not an unbounded fixed-point loop -- guarding against any pathological
// input looping indefinitely.
const wholeSpanEmphasisMaxPasses = 5

// stripWholeSpanEmphasis repeatedly applies wholeSpanEmphasisPatterns until
// a pass makes no further change (or wholeSpanEmphasisMaxPasses is reached),
// so a nested combination of delimiters wrapping the same span -- strong
// wrapping emphasis, emphasis wrapping strikethrough, and so on -- is fully
// resolved down to its plain text, the same way a real renderer would
// process nested inline emphasis.
func stripWholeSpanEmphasis(s string) string {
	for i := 0; i < wholeSpanEmphasisMaxPasses; i++ {
		next := s
		for _, p := range wholeSpanEmphasisPatterns {
			// The "_" patterns capture their surrounding boundary
			// characters (to require them) alongside the wrapped text;
			// the "*"/"~~" patterns capture only the wrapped text. A
			// substitution template referencing a group a pattern does
			// not have is simply empty, so one shared "$1$2$3" template
			// is safe to reuse: for "*"/"~~" patterns, $1 is the wrapped
			// text and $2/$3 are empty; for "_" patterns, $1/$3 are the
			// boundary characters and $2 is the wrapped text.
			next = p.ReplaceAllString(next, "$1$2$3")
		}
		if next == s {
			return s
		}
		s = next
	}
	return s
}

// codeFenceLinePattern matches a Markdown fenced code block delimiter line:
// up to three leading spaces (per CommonMark) followed by three or more
// identical backticks or tildes. It is used only to detect where a fence
// opens and closes, never to parse an info string or the fence's contents.
var codeFenceLinePattern = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// matchCodeFence reports whether line is a fenced-code-block delimiter,
// returning the fence character and the length of its backtick/tilde run.
func matchCodeFence(line string) (ch byte, length int, ok bool) {
	m := codeFenceLinePattern.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	return m[1][0], len(m[1]), true
}

// indentedCodeLinePattern matches a line CommonMark would treat as part of
// an indented code block: four or more leading spaces, or a leading tab.
// This package does not track list-item context (which can change whether
// such indentation is actually a code block), so it conservatively treats
// every such line as code content for normalization purposes -- this only
// ever *skips* decoding an escape that would not actually render that way;
// it never skips detecting a literal forbidden example.
var indentedCodeLinePattern = regexp.MustCompile(`^(?: {4,}|\t)`)

// decodeNumericCharRef decodes one `&#NN;`/`&#xHH;` match (see
// numericCharRefPattern) into the literal character it names, or returns it
// unchanged if the numeric value is not a valid code point.
func decodeNumericCharRef(ref string) string {
	m := numericCharRefPattern.FindStringSubmatch(ref)
	var codePoint int64
	var err error
	if m[1] != "" {
		codePoint, err = strconv.ParseInt(m[1], 16, 32)
	} else {
		codePoint, err = strconv.ParseInt(m[2], 10, 32)
	}
	if err != nil || codePoint <= 0 || codePoint > 0x10FFFF {
		return ref
	}
	return string(rune(codePoint))
}

// decodeNamedCharRef decodes one `&name;` match (see namedCharRefPattern)
// via the narrow namedCharRefReplacements table, or returns it unchanged if
// the name is not recognized.
func decodeNamedCharRef(ref string) string {
	if replacement, ok := namedCharRefReplacements[ref]; ok {
		return replacement
	}
	return ref
}

// decodeEntities decodes only the narrow, explicit set of HTML character
// references this package recognizes (see numericCharRefPattern,
// namedCharRefReplacements): every numeric reference, and a short, explicit
// list of named ones. This is the one normalization step CommonMark's own
// rendering also applies inside a raw HTML block's text content -- a
// browser parsing that HTML resolves its entities regardless of Markdown --
// unlike a backslash escape or an emphasis delimiter, neither of which is
// ever Markdown-processed inside raw HTML (see decodeRenderedMarkup,
// flattenHTMLVisibleText).
func decodeEntities(s string) string {
	s = numericCharRefPattern.ReplaceAllStringFunc(s, decodeNumericCharRef)
	s = namedCharRefPattern.ReplaceAllStringFunc(s, decodeNamedCharRef)
	return s
}

// decodeRenderedMarkup applies every rendering-normalization transformation
// this package recognizes for *ordinary Markdown prose* (not raw HTML block
// content -- see decodeEntities/flattenHTMLVisibleText for that), in an
// order where later steps can resolve what earlier ones reveal (an
// HTML-tag-wrapped numeric reference, an emphasis-wrapped entity, and so
// on): stripping Markdown links (keeping only their visible text, never
// their destination), stripping inline HTML comments (never visible at
// all), stripping safe inline HTML tags (including `<a>`, the same way),
// decoding character references, undoing CommonMark backslash escapes,
// stripping an emphasis/strikethrough delimiter wrapping exactly one
// protected punctuation character, and stripping a *whole* matching
// emphasis/strong/strikethrough delimiter run wrapping an entire
// credential-shaped span (see stripWholeSpanEmphasis). Callers must only
// apply this to text that Markdown would actually render these ways --
// never to inline code span content or code block lines, where CommonMark
// renders every one of these mechanisms completely literally (see
// normalizeRenderedEscapes, ScanText).
func decodeRenderedMarkup(s string) string {
	s = markdownLinkPattern.ReplaceAllString(s, "$1")
	s = htmlCommentInlinePattern.ReplaceAllString(s, "")
	s = safeInlineTagPattern.ReplaceAllString(s, "")
	s = decodeEntities(s)
	s = backslashEscapePattern.ReplaceAllString(s, "$1")
	s = renderedMarkupSplitPattern.ReplaceAllString(s, "$1")
	s = stripWholeSpanEmphasis(s)
	return s
}

// inlineCodeSpanRanges returns the half-open byte ranges of line that fall
// inside a Markdown inline code span: a run of one or more backticks,
// followed by content, followed by a run of the exact same length. Per
// CommonMark, a code span's content renders completely literally -- no
// backslash escape, no character reference, no emphasis delimiter, and no
// raw HTML tag is processed inside one -- so decodeRenderedMarkup must
// never be applied to these ranges (see normalizeRenderedEscapes). Go's
// regexp package cannot express "a closing run of the same length as the
// opening run" (no backreferences), so this is a small manual scanner
// instead of a single regular expression; it is deliberately a simplified
// model of CommonMark's actual (more involved) code-span tokenization rule,
// sufficient for this package's narrow purpose without being a Markdown
// parser: it finds the first subsequent backtick run of matching length to
// close a span, and if none exists before the end of the line, treats the
// opening run as ordinary text (as CommonMark itself does).
func inlineCodeSpanRanges(line string) [][2]int {
	var ranges [][2]int
	i := 0
	for i < len(line) {
		if line[i] != '`' {
			i++
			continue
		}
		openStart := i
		for i < len(line) && line[i] == '`' {
			i++
		}
		openLen := i - openStart
		contentStart := i
		closed := false
		for i < len(line) {
			if line[i] != '`' {
				i++
				continue
			}
			closeStart := i
			for i < len(line) && line[i] == '`' {
				i++
			}
			if i-closeStart == openLen {
				ranges = append(ranges, [2]int{contentStart, closeStart})
				closed = true
				break
			}
		}
		if !closed {
			// No matching close before end of line: not a code span: fall
			// through and keep scanning for a new opening run starting
			// just after the one we tried, matching CommonMark's behavior
			// of treating an unmatched backtick run as literal text.
			i = contentStart
		}
	}
	return ranges
}

// normalizeRenderedEscapes rewrites a line into the plain text it would
// render as in GitHub's Markdown, undoing every rendering mechanism a
// canonical-looking documentation example could otherwise hide a
// deprecated-transport example behind (see decodeRenderedMarkup), while
// leaving any inline code span's content completely untouched (see
// inlineCodeSpanRanges): CommonMark renders code span content literally,
// so an escape or entity or emphasis delimiter written there never
// actually decodes when rendered, and normalizing it anyway would turn a
// harmless literal example ("`api\_key=TOKEN`", which renders as the
// non-functional literal text "api\_key=TOKEN") into a false positive. A
// line that is itself part of a fenced or indented code block, or a raw
// HTML block, is handled one level up, in ScanText, which does not call
// this function for such lines at all, for the same reason.
func normalizeRenderedEscapes(line string) string {
	ranges := inlineCodeSpanRanges(line)
	if len(ranges) == 0 {
		return decodeRenderedMarkup(line)
	}
	var b strings.Builder
	prev := 0
	for _, r := range ranges {
		b.WriteString(decodeRenderedMarkup(line[prev:r[0]]))
		b.WriteString(line[r[0]:r[1]])
		prev = r[1]
	}
	b.WriteString(decodeRenderedMarkup(line[prev:]))
	return b.String()
}
