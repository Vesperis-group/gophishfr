// Package docsguard finds deprecated API-key transport examples in
// first-party Markdown documentation. It backs
// scripts/verify-docs-canonical-examples.sh and the "docs-guard" CI job: both
// exist so a future documentation change cannot silently reintroduce a
// `?api_key=`/form `api_key` example or a raw (non-scheme) Authorization
// header example as if it were a canonical, recommended transport.
//
// This package intentionally does not parse Markdown. It scans plain text
// line by line for the literal substrings that would make an example
// authenticate against the real credential extractor in
// middleware.extractExplicitAPICredential: the query/form parameter is named
// exactly "api_key" (decoded, since Go's URL/form parsing percent-decodes
// parameter names before comparing them), and the Authorization header is
// read by name case-insensitively per RFC 9110, but its value is matched
// against the runtime's exact, case-sensitive "Bearer " prefix. Matching that
// same literal contract is what keeps false positives and false negatives
// both low without a real HTTP/Markdown parser.
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

	// KindUndeprecatedParameterMention covers a Markdown table row or a
	// "presented as an alternative" prose sentence that lists `api_key` as
	// an ordinary, currently supported authentication option with no
	// deprecation context on the same line. Unlike KindParameterCredential,
	// this has no `=` sign and would never authenticate anything -- it is a
	// documentation-accuracy check, not a credential-syntax check: it
	// exists because a contract table can quietly fall out of sync with the
	// deprecation even when no example in it is directly copy-pasteable.
	KindUndeprecatedParameterMention Kind = "undeprecated_parameter_mention"
)

// deprecationContextPattern matches any of the words/strings that, if present
// on the same line as an `api_key` mention, show the mention already carries
// its required deprecation context (the removal version, the word
// "deprecated"/"deprecation", "legacy", "sunset", or "migrat(e/ion)"). This
// list is deliberately short: every entry is a plain-language signal a human
// reviewer would also accept as "this is clearly marked deprecated", which is
// what keeps the policy narrow and auditable rather than a loophole.
var deprecationContextPattern = regexp.MustCompile(`(?i)deprecat|0\.13\.0|remov|sunset|migrat|legacy`)

// tableRowPattern matches a Markdown table row: a line whose first
// non-whitespace character is a pipe.
var tableRowPattern = regexp.MustCompile(`^\s*\|`)

// offeredAsAlternativePattern matches prose that lists `api_key` as an
// alternative/option, e.g. "... or an `api_key` parameter" or
// "... or a api_key value". This is the exact phrasing of the original bug
// in docs/GROUP_IMPORT_LIMITS.md.
var offeredAsAlternativePattern = regexp.MustCompile("(?i)\\bor\\s+an?\\s+`?api_key`?")

// candidateKeyPattern finds every "key=" token in a line, in either a URL
// query string or a curl form body. The key is whatever Go's net/url would
// treat as a parameter name: any run of characters up to the next
// "&"/"?"/"="/whitespace/quote/backtick. Each match is percent-decoded and
// compared against "api_key", which is what actually makes it a working
// credential at runtime -- not its literal, possibly percent-encoded,
// spelling in the document.
var candidateKeyPattern = regexp.MustCompile("[^&?=\\s\"'`]+=")

// Violation is one deprecated-transport example found on one line.
type Violation struct {
	Line int
	Kind Kind
	Text string
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
// returns every deprecated-transport example it finds, in line order. It
// returns a non-nil error if the scanner itself failed -- most importantly
// bufio.ErrTooLong on a line that exceeds the internal buffer -- in which
// case the returned violations are necessarily incomplete and callers must
// fail closed rather than trust them as "no violations found".
func ScanText(text string) ([]Violation, error) {
	var violations []Violation
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if hasParameterCredential(line) {
			violations = append(violations, Violation{Line: lineNo, Kind: KindParameterCredential, Text: strings.TrimSpace(line)})
		}
		if reason, ok := rawAuthorization(line); ok {
			violations = append(violations, Violation{Line: lineNo, Kind: KindRawAuthorization, Text: strings.TrimSpace(reason)})
		}
		if undeprecatedParameterMention(line) {
			violations = append(violations, Violation{Line: lineNo, Kind: KindUndeprecatedParameterMention, Text: strings.TrimSpace(line)})
		}
	}
	if err := scanner.Err(); err != nil {
		return violations, fmt.Errorf("scanning line %d: %w", lineNo+1, err)
	}
	return violations, nil
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
func rawAuthorization(line string) (string, bool) {
	lowered := strings.ToLower(line)
	searchFrom := 0
	for {
		idx := strings.Index(lowered[searchFrom:], "authorization:")
		if idx < 0 {
			return "", false
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
				return fmt.Sprintf("Authorization: %s ... (wrong case; runtime requires exact %q)", token, canonicalBearerScheme+" "), true
			}
			continue
		}
		if !recognizedAuthSchemes[strings.ToLower(token)] {
			return fmt.Sprintf("Authorization: %s...", token), true
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

// undeprecatedParameterMention reports whether the line presents `api_key`
// as an ordinary, currently supported authentication option -- a Markdown
// table row, or prose offering it as an alternative to Bearer -- with no
// deprecation context (a removal version, "deprecated", "legacy", "sunset",
// or "migrat...") on that same line. It is case-insensitive on "api_key"
// itself to catch a capitalized table header or sentence start.
func undeprecatedParameterMention(line string) bool {
	if !strings.Contains(strings.ToLower(line), "api_key") {
		return false
	}
	if deprecationContextPattern.MatchString(line) {
		return false
	}
	return tableRowPattern.MatchString(line) || offeredAsAlternativePattern.MatchString(line)
}
