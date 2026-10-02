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
	return len(parameterCredentialMatches(text)) > 0
}

// parameterCredentialMatches returns the byte span, within text, of every
// "key=" token that decodes to exactly "api_key" (see
// hasParameterCredential). A caller with a position-to-line mapping for
// text can use these spans to report a precise, contributing line range
// instead of merely the whole block's.
func parameterCredentialMatches(text string) [][2]int {
	var spans [][2]int
	for _, loc := range candidateKeyPattern.FindAllStringIndex(text, -1) {
		key := strings.TrimSuffix(text[loc[0]:loc[1]], "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil {
			decoded = key
		}
		if decoded == "api_key" {
			spans = append(spans, [2]int{loc[0], loc[1]})
		}
	}
	return spans
}

// httpBearerScheme is the exact, case-sensitive scheme word the real
// credential extractor's prefix check requires (see
// middleware.extractExplicitAPICredential). Any other casing of this word
// is not recognized as this scheme at runtime.
const httpBearerScheme = "Bearer"

// httpBearerPrefix is the exact literal prefix
// middleware.extractExplicitAPICredential strips with
// strings.TrimPrefix(authorization, httpBearerScheme+" "): the scheme word,
// followed by exactly one space, and nothing else -- not a tab, not two
// spaces, not the word alone. strings.TrimPrefix only strips this *exact*
// byte sequence; anything else (a different separator, or no token at all
// afterward) leaves the whole original string as the "credential" value,
// which is never a working canonical example, only a raw/legacy one (see
// rawAuthorizationMatches).
const httpBearerPrefix = httpBearerScheme + " "

// recognizedAuthSchemes lists HTTP Authorization scheme tokens, other than
// the canonical one, that are not the deprecated raw API-key transport.
// Basic/Digest/Negotiate/NTLM are handled separately because, unlike these
// real, independently registered schemes, this repository's own runtime
// does not recognize them as credentials at all. Keeping this list short
// and explicit is what keeps the exemption "narrow and auditable": every
// entry is a real HTTP authentication scheme, not a loophole.
var recognizedAuthSchemes = map[string]bool{
	"basic":     true,
	"digest":    true,
	"negotiate": true,
	"ntlm":      true,
}

// rawAuthorization reports whether text shows an Authorization header
// carrying a bare token, a non-canonically-cased scheme word, or the
// canonical scheme word separated from its token by anything other than
// the runtime's exact single-space prefix (or followed by no token at
// all). It matches the header name case-insensitively, because HTTP header
// names are case-insensitive and the real middleware reads it via
// net/http's canonicalized http.Header.Values("Authorization").
func rawAuthorization(text string) bool {
	return len(rawAuthorizationMatches(text)) > 0
}

// rawAuthorizationMatches returns the byte span, within text, of every
// Authorization header example rawAuthorization would flag: from the start
// of the literal (case-insensitive) "authorization:" through the end of the
// scheme word (or, for a value correctly starting with the canonical
// prefix but with no credential token after it, through the end of the
// scheme word itself), for every such occurrence. A caller with a
// position-to-line mapping for text can use these spans to report a
// precise, contributing line range.
func rawAuthorizationMatches(text string) [][2]int {
	var spans [][2]int
	lowered := strings.ToLower(text)
	searchFrom := 0
	for {
		idx := strings.Index(lowered[searchFrom:], "authorization:")
		if idx < 0 {
			return spans
		}
		idx += searchFrom
		afterColon := idx + len("authorization:")
		rest := text[afterColon:]
		trimmed := strings.TrimLeft(rest, " \t`\"'")
		searchFrom = afterColon
		if trimmed == "" {
			// "Authorization:" with no value here is not an example of
			// anything; keep scanning in case the text repeats the header.
			continue
		}
		valueStart := afterColon + (len(rest) - len(trimmed))
		token := firstToken(rest)

		switch {
		case strings.HasPrefix(trimmed, httpBearerPrefix):
			// The real extractor's strings.TrimPrefix only strips this
			// exact literal prefix (the scheme word plus one space,
			// nothing else).
			afterPrefix := trimmed[len(httpBearerPrefix):]
			switch {
			case startsWithCredentialToken(afterPrefix):
				// A genuine, working canonical example.
			case strings.HasPrefix(afterPrefix, "`"):
				// The scheme is immediately closed by a code-span
				// backtick with no real token in between -- see the
				// narrow exemption on the EqualFold branch below for
				// why this is not flagged.
			default:
				// Either a second separator character remains exactly
				// where the real extractor's TrimPrefix would leave it
				// inside the extracted value (one space is already
				// consumed by the exact prefix match), or nothing
				// follows at all (whitespace/quote/end-of-line only) --
				// neither is a working canonical example.
				spans = append(spans, [2]int{idx, valueStart + len(httpBearerScheme)})
			}
		case strings.EqualFold(token, httpBearerScheme):
			// The scheme word is (case-insensitively) a match for the
			// canonical one, but the value does not have the runtime's
			// exact prefix: either the casing is wrong, or the separator
			// between the scheme and its token is not the single space
			// strings.TrimPrefix requires (a tab, two spaces, or no
			// separator at all) -- strings.TrimPrefix would leave the
			// scheme word itself (or extra whitespace) inside the
			// extracted value, so this never actually works as the
			// canonical scheme.
			//
			// Narrow, documented exemption: the scheme word immediately
			// closed by a code-span backtick, with nothing at all in
			// between, is ordinary prose naming the header/scheme
			// generically (a pattern that appears in this repository's
			// own canonical documentation), not an attempted-but-broken
			// worked example; a real worked example inside a code span
			// still has a real token before that backtick and is still
			// caught by the HasPrefix(httpBearerPrefix) case above
			// instead, which this exemption does not affect.
			if tokenEnd := valueStart + len(token); tokenEnd >= len(text) || text[tokenEnd] != '`' {
				spans = append(spans, [2]int{idx, tokenEnd})
			}
		case !recognizedAuthSchemes[strings.ToLower(token)]:
			spans = append(spans, [2]int{idx, valueStart + len(token)})
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

// startsWithCredentialToken reports whether s begins immediately (with no
// intervening separator at all) with a real, non-empty credential token --
// unlike firstToken, which treats a leading backtick/quote/whitespace as
// example-wrapper noise to skip past, this must not skip anything: it is
// used to check exactly what the real extractor's TrimPrefix call would
// leave immediately after the canonical literal prefix, where any further
// whitespace, or a terminator with nothing real before it, means the
// credential is not what it appears to be.
func startsWithCredentialToken(s string) bool {
	return s != "" && !strings.ContainsAny(s[:1], " \t`\"'\n\r")
}
