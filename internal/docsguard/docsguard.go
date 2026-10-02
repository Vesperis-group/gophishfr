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
// exactly "api_key", and the Authorization header is read by name
// case-insensitively per RFC 9110. Matching that same literal contract is
// what keeps false positives and false negatives both low without a real
// HTTP/Markdown parser.
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
	// POST bodies, so any literal `api_key=` is a working example of one of
	// those two deprecated transports.
	KindParameterCredential Kind = "parameter_credential" // #nosec G101 -- a label string, not a credential value.

	// KindRawAuthorization covers an `Authorization` header (any
	// letter-casing, since HTTP header names are case-insensitive) whose
	// value does not start with a recognized authentication scheme token.
	// `Authorization: Bearer ...` and other real schemes such as
	// `Authorization: Basic ...` are not flagged; a bare token is.
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

// Violation is one deprecated-transport example found on one line.
type Violation struct {
	Line int
	Kind Kind
	Text string
}

// recognizedAuthSchemes lists HTTP Authorization scheme tokens that are not
// the deprecated raw API-key transport. Keeping this list short and explicit
// is what keeps the exemption "narrow and auditable": every entry is a real,
// independently registered HTTP authentication scheme, not a loophole.
var recognizedAuthSchemes = map[string]bool{
	"bearer":    true,
	"basic":     true,
	"digest":    true,
	"negotiate": true,
	"ntlm":      true,
}

// ScanText scans arbitrary text (typically one Markdown file's contents) and
// returns every deprecated-transport example it finds, in line order.
func ScanText(text string) []Violation {
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
	return violations
}

// hasParameterCredential reports whether the line contains the literal
// "api_key=" substring, which is the exact parameter name the real
// credential extractor reads from a URL query or an
// application/x-www-form-urlencoded POST body (see
// middleware.extractExplicitAPICredential). This single substring check
// covers every curl encoding that can produce it: "?api_key=", "&api_key=",
// `-d`/`--data`/`--data-raw "api_key=..."`, `--data-urlencode "api_key=..."`,
// `-F`/`--form "api_key=..."`, and a standalone "api_key=..." with no flag at
// all.
func hasParameterCredential(line string) bool {
	return strings.Contains(line, "api_key=")
}

// rawAuthorization reports whether the line shows an Authorization header
// carrying a bare token instead of a recognized scheme. It matches the
// header name case-insensitively, because HTTP header names are
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
