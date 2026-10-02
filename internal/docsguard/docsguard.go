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
// This package intentionally does not parse Markdown. It scans plain text
// for the literal substrings that would make an example authenticate
// against the real credential extractor in
// middleware.extractExplicitAPICredential: the query/form parameter is named
// exactly "api_key" (decoded, since Go's URL/form parsing percent-decodes
// parameter names before comparing them), and the Authorization header is
// read by name case-insensitively per RFC 9110, but its value is matched
// against the runtime's exact, case-sensitive "Bearer " prefix. Matching that
// same literal contract is what keeps false positives and false negatives
// both low without a real HTTP/Markdown parser.
//
// Every reported Violation carries only a file-relative line range and a
// Kind; it never carries the matched text, so a forbidden example that
// happens to contain a real secret is never echoed by a caller that prints
// violations (see Kind.Explanation, which returns a fixed, generic
// description instead).
package docsguard

import (
	"bufio"
	"fmt"
	"net/url"
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
	// value is not the runtime's exact, case-sensitive canonical form. The
	// real extractor only strips a literal "Bearer " prefix
	// (strings.TrimPrefix is case-sensitive); any other casing of the word
	// "bearer" is therefore not recognized as Bearer at runtime and falls
	// into the same raw/legacy bucket as a bare token, so it is flagged
	// here too. Other real schemes such as `Authorization: Basic ...` are
	// matched case-insensitively, per RFC 7235, and are not flagged.
	KindRawAuthorization Kind = "raw_authorization"

	// KindUndeprecatedParameterMention covers a Markdown table row, a
	// "presented as an alternative" sentence, or ordinary recommendation
	// prose (e.g. "use the api_key query parameter", "authenticate via
	// api_key") -- including one wrapped across several physical lines --
	// that presents `api_key` as an ordinary, currently supported
	// authentication option with no *affirmative* deprecation context in
	// the same sentence. A deprecation-context word elsewhere in the
	// paragraph, about an unrelated subject, does not suppress this. Nor
	// does a negated context ("not deprecated", "no longer legacy") where
	// the negation directly governs it: that means the surrounding text is
	// actively asserting the opposite of the real contract, which is
	// itself the violation. A negation that instead governs the
	// recommendation verb -- "do NOT use the api_key parameter; it is
	// deprecated" -- is a legitimate warning and does not violate. Unlike
	// KindParameterCredential, this has no `=` sign and would never
	// authenticate anything -- it is a documentation-accuracy
	// check, not a credential-syntax check.
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

// deprecationContextPattern matches any of the words/strings that can show a
// nearby `api_key` mention already carries its required deprecation context
// (the removal version, the word "deprecated"/"deprecation", "legacy",
// "sunset", or "migrat(e/ion)"). A match here is only treated as *affirmative*
// context if hasAffirmativeDeprecationContext also confirms it is not
// negated (see negationWordPattern). This list is deliberately short: every
// entry is a plain-language signal a human reviewer would also accept as
// "this is clearly marked deprecated", which is what keeps the policy narrow
// and auditable rather than a loophole.
var deprecationContextPattern = regexp.MustCompile(`(?i)deprecat\w*|0\.13\.0|remov\w*|sunset\w*|migrat\w*|legacy`)

// negationWordPattern matches common negation words/contractions. A match
// only counts as negating a particular deprecationContextPattern match when
// it is within negationProximityWords words of it, on the near side of any
// negationBoundaryPattern punctuation (see hasAffirmativeDeprecationContext):
// "not deprecated" and "no longer legacy" assert the opposite of the real
// contract and must remain a violation, but a negation word governing some
// other, earlier part of the same clause -- most commonly the recommendation
// verb itself, as in "do NOT use the api_key parameter; it is deprecated" --
// must not be read as negating "deprecated" too.
var negationWordPattern = regexp.MustCompile(`(?i)\b(not|never|no longer|isn't|aren't|wasn't|weren't|doesn't|didn't|won't|wont)\b`)

// negationBoundaryPattern marks a punctuation boundary strong enough to stop
// a negation word from being read as governing a deprecation-context word on
// the other side of it: a sentence-ending mark, a semicolon, or a dash (em,
// en, or a double hyphen standing in for one in plain-text Markdown). Without
// this, "Do NOT use the api_key query parameter; it is deprecated" would
// wrongly read the leading "not" -- which governs "use", not "deprecated" --
// as negating the deprecation notice that follows it.
var negationBoundaryPattern = regexp.MustCompile(`[.!?;—–]|--`)

// negationProximityWords bounds how many words may separate a negation
// word/phrase from the deprecation-context word it must directly govern to
// count as negating it (for example "is not actually deprecated"). Keeping
// this small, in addition to negationBoundaryPattern, is what ensures only a
// negation that grammatically governs the deprecation-context word itself --
// not an unrelated negation earlier in the same clause -- suppresses it.
const negationProximityWords = 3

// hasAffirmativeDeprecationContext reports whether block contains at least
// one deprecation-context word or phrase that is not directly negated. It is
// used both per-line (for table rows) and per-sentence (for recommendation
// prose, scoped via enclosingSentence), so a negated mention, or one that
// belongs to an unrelated clause, cannot accidentally suppress a genuine
// violation.
func hasAffirmativeDeprecationContext(block string) bool {
	for _, match := range deprecationContextPattern.FindAllStringIndex(block, -1) {
		start := match[0]
		windowStart := start - 40
		if windowStart < 0 {
			windowStart = 0
		}
		preceding := block[windowStart:start]
		if locs := negationBoundaryPattern.FindAllStringIndex(preceding, -1); len(locs) > 0 {
			last := locs[len(locs)-1]
			preceding = preceding[last[1]:]
		}
		words := strings.Fields(preceding)
		if len(words) > negationProximityWords {
			words = words[len(words)-negationProximityWords:]
		}
		if !negationWordPattern.MatchString(strings.Join(words, " ")) {
			return true
		}
	}
	return false
}

// sentenceTerminators are the characters that end an English sentence, used
// by enclosingSentence to scope deprecation-context association to "the
// same sentence as a recommendation": an unrelated deprecation mention about
// a different subject, in an earlier or later sentence of the same
// paragraph, must not be able to suppress a live recommendation elsewhere in
// that paragraph.
const sentenceTerminators = ".!?"

// enclosingSentence returns the sentence of text containing byte offset pos,
// bounded by the nearest sentenceTerminators character on each side (or the
// start/end of text if none is found).
func enclosingSentence(text string, pos int) string {
	start := 0
	if idx := strings.LastIndexAny(text[:pos], sentenceTerminators); idx != -1 {
		start = idx + 1
	}
	end := len(text)
	if idx := strings.IndexAny(text[pos:], sentenceTerminators); idx != -1 {
		end = pos + idx + 1
	}
	return text[start:end]
}

// tableRowPattern matches a Markdown table row: a line whose first
// non-whitespace character is a pipe.
var tableRowPattern = regexp.MustCompile(`^\s*\|`)

// offeredAsAlternativePattern matches prose that lists `api_key` as an
// alternative/option, e.g. "... or an `api_key` parameter" or
// "... or a api_key value". This is the exact phrasing of the original bug
// in docs/GROUP_IMPORT_LIMITS.md.
var offeredAsAlternativePattern = regexp.MustCompile("(?i)\\bor\\s+an?\\s+`?api_key`?")

// recommendationProximity bounds how many intervening words a recommendation
// verb may be from "api_key" and still count as describing it, rather than
// coincidentally sharing a (now paragraph-wide, potentially multi-sentence)
// block of text with it. Without this bound, a purely descriptive paragraph
// that happens to use the word "authentication" in one sentence and mention
// `api_key` in an unrelated sentence nearby would falsely violate; this is
// exactly the false positive a real paragraph in docs/API_AUTHENTICATION.md
// surfaced once detection moved from single lines to whole paragraphs.
const recommendationProximity = `(?:\s+\S+){0,4}\s+`

// strongRecommendationPattern matches the word "authenticate" (and its
// inflections: authenticates, authenticated, authenticating,
// authentication) within recommendationProximity words of `api_key`, in
// either order. Paired with no affirmative deprecation context, this word
// alone is specific enough to signal a recommendation ("authenticate via
// api_key", "authenticate with the api_key parameter") without needing a
// separate anchor word -- but only when it is actually close to the
// `api_key` mention, not merely present somewhere in the same paragraph.
var strongRecommendationPattern = regexp.MustCompile(
	`(?i)\bauthenticat\w*\b` + recommendationProximity + `api_key\b|\bapi_key\b` + recommendationProximity + `authenticat\w*\b`,
)

// weakRecommendationVerbPattern matches common, much more generic verbs --
// "use", "via", "with", "accept" -- within recommendationProximity words of
// `api_key`. These words are far too common in ordinary prose (including
// this package's own documentation) to use unbounded across a whole
// paragraph; requiring proximity to `api_key`, in addition to the
// recommendationAnchorPattern check below, is what keeps this narrow.
var weakRecommendationVerbPattern = regexp.MustCompile(
	`(?i)\b(?:use\w*|via|with|accept\w*)\b` + recommendationProximity + `api_key\b|\bapi_key\b` + recommendationProximity + `\b(?:use\w*|via|with|accept\w*)\b`,
)

// recommendationAnchorPattern matches a word that anchors a weak
// recommendation verb to the authentication-transport meaning of `api_key`,
// as opposed to an incidental mention (a curl flag, a value, a variable
// name) that happens to share the text with one of those common verbs.
var recommendationAnchorPattern = regexp.MustCompile(`(?i)\b(parameter|param|query|field|header|credential)\b`)

// candidateKeyPattern finds every "key=" token in a line, in either a URL
// query string or a curl form body. The key is whatever Go's net/url would
// treat as a parameter name: any run of characters up to the next
// "&"/"?"/"="/whitespace/quote/backtick. Each match is percent-decoded and
// compared against "api_key", which is what actually makes it a working
// credential at runtime -- not its literal, possibly percent-encoded,
// spelling in the document.
var candidateKeyPattern = regexp.MustCompile("[^&?=\\s\"'`]+=")

// Violation is one deprecated-transport example found in a document. It
// carries only its location and Kind -- never the matched text -- so a
// caller can safely print every field of a Violation (see Kind.Explanation)
// without risk of echoing a real secret a forbidden example happened to
// contain.
type Violation struct {
	// StartLine and EndLine are the 1-indexed, inclusive line range the
	// violation was found in. For a single-line violation (parameter
	// credential, raw Authorization, a table row) they are equal; a
	// recommendation-prose violation spanning a wrapped Markdown paragraph
	// reports that paragraph's full line range.
	StartLine int
	EndLine   int
	Kind      Kind
}

// canonicalBearerScheme is the exact, case-sensitive prefix the real
// credential extractor strips (see middleware.extractExplicitAPICredential:
// strings.TrimPrefix(authorization, "Bearer ")). Any other casing of the
// word "bearer" is not recognized as this scheme at runtime.
const canonicalBearerScheme = "Bearer"

// recognizedAuthSchemes lists HTTP Authorization scheme tokens, other than
// Bearer, that are not the deprecated raw API-key transport. Bearer is
// handled separately because, unlike these real, independently registered
// schemes, this repository's own runtime does not recognize it
// case-insensitively. Keeping this list short and explicit is what keeps the
// exemption "narrow and auditable": every entry is a real HTTP
// authentication scheme, not a loophole.
var recognizedAuthSchemes = map[string]bool{
	"basic":     true,
	"digest":    true,
	"negotiate": true,
	"ntlm":      true,
}

// ScanText scans arbitrary text (typically one Markdown file's contents) and
// returns every deprecated-transport example it finds. It returns a non-nil
// error if the scanner itself failed -- most importantly bufio.ErrTooLong on
// a line that exceeds the internal buffer -- in which case the returned
// violations are necessarily incomplete and callers must fail closed rather
// than trust them as "no violations found".
func ScanText(text string) ([]Violation, error) {
	var violations []Violation
	var lines []string

	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		lines = append(lines, line)
		if hasParameterCredential(line) {
			violations = append(violations, Violation{StartLine: lineNo, EndLine: lineNo, Kind: KindParameterCredential})
		}
		if rawAuthorization(line) {
			violations = append(violations, Violation{StartLine: lineNo, EndLine: lineNo, Kind: KindRawAuthorization})
		}
		if tableRowMention(line) {
			violations = append(violations, Violation{StartLine: lineNo, EndLine: lineNo, Kind: KindUndeprecatedParameterMention})
		}
	}
	if err := scanner.Err(); err != nil {
		return violations, fmt.Errorf("scanning line %d: %w", lineNo+1, err)
	}

	// Recommendation prose is analyzed per Markdown paragraph (a run of
	// non-blank lines), not per physical line, so a recommendation wrapped
	// across several lines by normal Markdown line-wrapping is still
	// caught.
	for _, p := range paragraphsFromLines(lines) {
		if recommendationMention(p.text) {
			violations = append(violations, Violation{StartLine: p.startLine, EndLine: p.endLine, Kind: KindUndeprecatedParameterMention})
		}
	}

	return dedupeViolations(violations), nil
}

// dedupeViolations removes exact (StartLine, EndLine, Kind) duplicates,
// which can occur when a single-line paragraph independently matches both
// the per-line table-row check and the per-paragraph recommendation check.
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

// paragraph is a run of consecutive non-blank lines, normalized into one
// whitespace-collapsed string so a sentence wrapped across several physical
// lines reads as continuous text for the recommendation-prose checks.
type paragraph struct {
	startLine int
	endLine   int
	text      string
}

// paragraphsFromLines groups lines into paragraphs the way Markdown does:
// runs of non-blank lines separated by one or more blank (or
// whitespace-only) lines.
func paragraphsFromLines(lines []string) []paragraph {
	var paragraphs []paragraph
	var current []string
	start := 0

	flush := func(end int) {
		if len(current) == 0 {
			return
		}
		text := strings.Join(strings.Fields(strings.Join(current, " ")), " ")
		paragraphs = append(paragraphs, paragraph{startLine: start, endLine: end, text: text})
		current = nil
	}

	for i, line := range lines {
		lineNo := i + 1
		if strings.TrimSpace(line) == "" {
			flush(lineNo - 1)
			continue
		}
		if len(current) == 0 {
			start = lineNo
		}
		current = append(current, line)
	}
	flush(len(lines))
	return paragraphs
}

// hasParameterCredential reports whether the line contains a "key=" token
// that decodes to exactly "api_key" -- the exact parameter name the real
// credential extractor reads from a URL query or an
// application/x-www-form-urlencoded POST body (see
// middleware.extractExplicitAPICredential), after the same percent-decoding
// Go's net/url and net/http apply before the comparison. This covers every
// curl encoding that can produce such a token: "?api_key=", "&api_key=",
// `-d`/`--data`/`--data-raw "api_key=..."`, `--data-urlencode "api_key=..."`,
// `-F`/`--form "api_key=..."`, a standalone "api_key=..." with no flag at
// all, and a percent-encoded key such as "api%5Fkey=" in any of those forms.
func hasParameterCredential(line string) bool {
	for _, match := range candidateKeyPattern.FindAllString(line, -1) {
		key := strings.TrimSuffix(match, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil {
			decoded = key
		}
		if decoded == "api_key" {
			return true
		}
	}
	return false
}

// rawAuthorization reports whether the line shows an Authorization header
// carrying a bare token or a non-canonically-cased "bearer", instead of the
// runtime's exact "Bearer" scheme or another recognized scheme. It matches
// the header name case-insensitively, because HTTP header names are
// case-insensitive and the real middleware reads it via net/http's
// canonicalized http.Header.Values("Authorization").
func rawAuthorization(line string) bool {
	lowered := strings.ToLower(line)
	searchFrom := 0
	for {
		idx := strings.Index(lowered[searchFrom:], "authorization:")
		if idx < 0 {
			return false
		}
		idx += searchFrom
		rest := line[idx+len("authorization:"):]
		token := firstToken(rest)
		searchFrom = idx + len("authorization:")
		if token == "" {
			// "Authorization:" with no value on this line is not an example
			// of anything; keep scanning in case the line repeats the header.
			continue
		}
		if strings.EqualFold(token, canonicalBearerScheme) {
			if token != canonicalBearerScheme {
				// Wrong-case "bearer": the real extractor only strips the
				// exact "Bearer " prefix, so this value authenticates (if at
				// all) as a raw token, not as Bearer -- a canonical example
				// must not show a casing that cannot possibly work that way.
				return true
			}
			continue
		}
		if !recognizedAuthSchemes[strings.ToLower(token)] {
			return true
		}
	}
}

// firstToken returns the first run of characters after any leading
// punctuation (backticks, quotes, whitespace) typical of Markdown and curl
// examples, stopping at the next whitespace, backtick, or quote.
func firstToken(s string) string {
	s = strings.TrimLeft(s, " \t`\"'")
	end := strings.IndexAny(s, " \t`\"'\n\r")
	if end == -1 {
		end = len(s)
	}
	return s[:end]
}

// tableRowMention reports whether a single physical line is a Markdown
// table row presenting `api_key` with no affirmative deprecation context.
// Table rows stay a per-line check (unlike recommendationMention) because a
// Markdown table row is always exactly one physical line by construction.
func tableRowMention(line string) bool {
	if !strings.Contains(strings.ToLower(line), "api_key") {
		return false
	}
	if !tableRowPattern.MatchString(line) {
		return false
	}
	return !hasAffirmativeDeprecationContext(line)
}

// recommendationMention reports whether normalized paragraph text presents
// `api_key` as an alternative or recommended authentication option -- "or
// an api_key parameter", "use the api_key query parameter", "authenticate
// via api_key" -- with no affirmative deprecation context in the same
// sentence as that particular mention. Each candidate recommendation match
// is judged against only its own enclosing sentence (see enclosingSentence),
// not the whole paragraph: an unrelated deprecation notice about a different
// subject, in an earlier or later sentence of the same paragraph, must not
// suppress a live recommendation elsewhere in it. Operating paragraph-wide
// to *find* candidate matches (rather than one physical line at a time) is
// what still catches a recommendation wrapped across several physical lines
// by ordinary Markdown line-wrapping.
func recommendationMention(paragraphText string) bool {
	if !strings.Contains(strings.ToLower(paragraphText), "api_key") {
		return false
	}

	for _, loc := range offeredAsAlternativePattern.FindAllStringIndex(paragraphText, -1) {
		if !hasAffirmativeDeprecationContext(enclosingSentence(paragraphText, loc[0])) {
			return true
		}
	}
	for _, loc := range strongRecommendationPattern.FindAllStringIndex(paragraphText, -1) {
		if !hasAffirmativeDeprecationContext(enclosingSentence(paragraphText, loc[0])) {
			return true
		}
	}
	for _, loc := range weakRecommendationVerbPattern.FindAllStringIndex(paragraphText, -1) {
		sentence := enclosingSentence(paragraphText, loc[0])
		if !recommendationAnchorPattern.MatchString(sentence) {
			continue
		}
		if !hasAffirmativeDeprecationContext(sentence) {
			return true
		}
	}
	return false
}
