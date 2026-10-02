package docsguard

import (
	"net/url"
	"regexp"
	"strings"
)

// candidateKeyPattern finds every "key=" token in a line, in either a URL
// query string or a curl form body. The key is whatever Go's net/url would
// treat as a parameter name: any run of characters up to the next
// "&"/"?"/"="/whitespace/quote/backtick. Each match is percent-decoded and
// compared against "api_key", which is what actually makes it a working
// credential at runtime -- not its literal, possibly percent-encoded,
// spelling in the document.
var candidateKeyPattern = regexp.MustCompile("[^&?=\\s\"'`]+=")

// hasParameterCredential reports whether text contains a "key=" token that
// decodes to exactly "api_key" -- the exact parameter name the real
// credential extractor reads from a URL query or an
// application/x-www-form-urlencoded POST body (see
// middleware.extractExplicitAPICredential), after the same percent-decoding
// Go's net/url and net/http apply before the comparison. This covers every
// curl encoding that can produce such a token: "?api_key=", "&api_key=",
// `-d`/`--data`/`--data-raw "api_key=..."`, `--data-urlencode "api_key=..."`,
// `-F`/`--form "api_key=..."`, a standalone "api_key=..." with no flag at
// all, and a percent-encoded key such as "api%5Fkey=" in any of those forms.
func hasParameterCredential(text string) bool {
	for _, match := range candidateKeyPattern.FindAllString(text, -1) {
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

// rawAuthorization reports whether text shows an Authorization header
// carrying a bare token or a non-canonically-cased "bearer", instead of the
// runtime's exact "Bearer" scheme or another recognized scheme. It matches
// the header name case-insensitively, because HTTP header names are
// case-insensitive and the real middleware reads it via net/http's
// canonicalized http.Header.Values("Authorization").
func rawAuthorization(text string) bool {
	lowered := strings.ToLower(text)
	searchFrom := 0
	for {
		idx := strings.Index(lowered[searchFrom:], "authorization:")
		if idx < 0 {
			return false
		}
		idx += searchFrom
		rest := text[idx+len("authorization:"):]
		token := firstToken(rest)
		searchFrom = idx + len("authorization:")
		if token == "" {
			// "Authorization:" with no value here is not an example of
			// anything; keep scanning in case the text repeats the header.
			continue
		}
		if strings.EqualFold(token, canonicalBearerScheme) {
			if token != canonicalBearerScheme {
				// Wrong-case "bearer": the real extractor only strips the
				// exact "Bearer " prefix, so this value authenticates (if at
				// all) as a raw token, not as canonical Bearer; a canonical
				// example must not show a casing that cannot possibly work
				// that way.
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
