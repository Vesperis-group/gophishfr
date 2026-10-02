package docsguard

import (
	"regexp"
	"strings"
)

// deprecationConstructionWordPattern matches an explicit deprecation/removal
// construction word or phrase: "deprecated"/"deprecation", "legacy",
// "sunset", "migrat(e/ion)", or "remov(e/ed/al)". A match here is only
// treated as *affirmative* context if hasAffirmativeDeprecationContext also
// confirms it is not negated (see negationWordPattern), not separated from
// the recommendation by an adversative connector, close enough to an actual
// `api_key` mention, and about the same specific transport. This list is
// deliberately short: every entry is a plain-language signal a human
// reviewer would also accept as "this is clearly marked deprecated", which
// is what keeps the policy narrow and auditable rather than a loophole.
var deprecationConstructionWordPattern = regexp.MustCompile(`(?i)deprecat\w*|remov\w*|sunset\w*|migrat\w*|legacy`)

// bareVersionPattern matches the removal version number on its own, with no
// accompanying construction word. A match is only ever examined via
// deprecationContextPattern (below), which also matches the construction
// words; hasAffirmativeDeprecationContext uses bareVersionPattern
// specifically to detect when a deprecationContextPattern match *is* the
// version number, so it can require a nearby construction word before
// treating it as affirmative (see the bare-version handling below): a
// sentence that merely mentions "0.13.0" -- a release number, nothing more
// -- is not itself an assertion that anything is deprecated.
var bareVersionPattern = regexp.MustCompile(`^0\.13\.0$`)

// deprecationContextPattern matches any of the words/strings that can show a
// nearby `api_key` mention already carries its required deprecation
// context: an explicit construction word (see
// deprecationConstructionWordPattern) or the bare removal version number
// (see bareVersionPattern), which additionally requires a nearby
// construction word to count (see hasAffirmativeDeprecationContext).
var deprecationContextPattern = regexp.MustCompile(`(?i)deprecat\w*|remov\w*|sunset\w*|migrat\w*|legacy|0\.13\.0`)

// bareVersionConstructionWindowChars bounds how many characters of context
// around a bare "0.13.0" match are searched for an explicit construction
// word. This is a plain character window (rather than a word count, as
// deprecationContextProximityWords uses) because a release version is
// typically named right next to the construction word describing what
// happens to it ("targeted for removal in 0.13.0", "deprecated ... in
// 0.13.0"), and a generous fixed window comfortably covers that without
// needing to be exact.
const bareVersionConstructionWindowChars = 60

// clampIndex constrains i to the valid range [0, n], guarding every
// hand-written window/slice computation in this file against an
// out-of-range index -- from a future caller, or a match position computed
// slightly outside its nominal bounds -- turning into a slice-bounds panic.
func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

// hasNearbyConstructionWord reports whether an explicit deprecation/removal
// construction word appears within bareVersionConstructionWindowChars
// characters of pos in clauseText.
func hasNearbyConstructionWord(clauseText string, pos int) bool {
	pos = clampIndex(pos, len(clauseText))
	windowStart := clampIndex(pos-bareVersionConstructionWindowChars, len(clauseText))
	windowEnd := clampIndex(pos+bareVersionConstructionWindowChars, len(clauseText))
	return deprecationConstructionWordPattern.MatchString(clauseText[windowStart:windowEnd])
}

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

// adversativeBoundaryPattern matches a bounded adversative/contrastive
// connector -- "but", "however", "yet", "though", "although", "nevertheless",
// "nonetheless", "whereas" -- the specific word class that signals two
// clauses make independent, often opposing, claims (unlike a semicolon or
// an em/en dash, which this package also recognizes as a clause separator
// but which, in every example seen so far, introduces an *elaboration* of
// the very same claim -- "Do not use the api_key query parameter; it is
// deprecated" -- and must continue to count as affirmative context; see
// hasAffirmativeDeprecationContext). A deprecation-context word on one side
// of one of these connectors must not be read as qualifying a recommendation
// on the other side, regardless of which comes first: "X is deprecated, but
// use api_key" and "Use api_key, but X is deprecated" both describe X, not
// api_key's status, when X is a different subject.
var adversativeBoundaryPattern = regexp.MustCompile(`(?i)\b(?:but|however|yet|though|although|nevertheless|nonetheless|whereas)\b`)

// apiKeyMentionPattern matches a literal `api_key` occurrence
// (case-insensitive). deprecationContextProximityDistance and
// nearestAPIKeyOccurrence use it to bind a deprecation-context word to the
// specific transport syntax it must describe, rather than any similarly
// placed but unrelated subject sharing the same sentence or clause -- "Use
// the api_key query parameter for authentication; our legacy billing system
// also needs migration, with removal scheduled separately" mentions
// "legacy"/"migration"/"removal", but none of them are about api_key's own
// deprecation.
var apiKeyMentionPattern = regexp.MustCompile(`(?i)api_key`)

// deprecationContextProximityWords bounds how many words may separate a
// deprecation-context word from the nearest `api_key` mention in the same
// clause and still count as describing that transport's own status. This
// is deliberately generous enough for a context word to sit just past a
// short verb phrase ("the api_key parameter is deprecated", "recommended
// the api_key query parameter; it is now deprecated") while still excluding
// a word that is merely nearby in the same sentence but describing an
// unrelated subject.
const deprecationContextProximityWords = 6

// deprecationContextProximityDistance returns the number of
// whitespace-separated words between pos and the closest `api_key` mention
// in clauseText, or -1 if clauseText contains no such mention at all.
func deprecationContextProximityDistance(clauseText string, pos int) int {
	best := -1
	for _, loc := range apiKeyMentionPattern.FindAllStringIndex(clauseText, -1) {
		var between string
		if loc[0] >= pos {
			between = clauseText[pos:loc[0]]
		} else {
			between = clauseText[loc[1]:pos]
		}
		n := len(strings.Fields(between))
		if best == -1 || n < best {
			best = n
		}
	}
	return best
}

// nearestAPIKeyOccurrence returns the byte range of the `api_key` mention in
// clauseText closest to byte offset pos, and whether one exists at all.
// Distance is measured in intervening words, exactly like
// deprecationContextProximityDistance, rather than raw byte count: two
// mentions can be a very different number of CHARACTERS away from pos while
// being an equally short, or even reversed, number of WORDS away (for
// example "...Authorization header is deprecated; use the api_key query
// parameter..." -- "deprecated" is only 3 words from either the "raw"
// mention before it or the "query" mention after it, but far closer in
// characters to the latter). Using a different distance metric than the
// proximity check that gates whether a context word may describe a mention
// at all would let the two disagree on which mention it is actually
// describing, silently misclassifying its transport.
func nearestAPIKeyOccurrence(clauseText string, pos int) (start, end int, found bool) {
	best := -1
	for _, loc := range apiKeyMentionPattern.FindAllStringIndex(clauseText, -1) {
		var between string
		if loc[0] >= pos {
			between = clauseText[pos:loc[0]]
		} else {
			between = clauseText[loc[1]:pos]
		}
		d := len(strings.Fields(between))
		if best == -1 || d < best {
			best = d
			start, end, found = loc[0], loc[1], true
		}
	}
	return start, end, found
}

// queryTransportWordPattern, formTransportWordPattern,
// rawTransportWordPattern, and responseFieldWordPattern each recognize
// prose naming one specific api_key transport (or, for the last, a
// different kind of api_key mention entirely -- a response/body field,
// not a request transport at all). transportKindNear uses them to classify
// which transport a particular `api_key` mention is actually about, so a
// deprecation notice about one transport cannot suppress a live
// recommendation of a different one ("the form parameter is deprecated"
// must not qualify "use the query parameter").
var (
	queryTransportWordPattern = regexp.MustCompile(`(?i)\bquery\b`)
	formTransportWordPattern  = regexp.MustCompile(`(?i)\bform\b`)
	rawTransportWordPattern   = regexp.MustCompile(`(?i)\b(?:raw|header|authorization)\b`)
	responseFieldWordPattern  = regexp.MustCompile(`(?i)\bresponse\b`)
)

// transportWordKinds lists the transport-word patterns transportKindNear
// chooses among, in a fixed order used only to break a genuine tie between
// two kinds at the exact same word-distance deterministically (favoring the
// earlier entry); it does not affect which kind is nearest in the normal
// case where the distances differ.
var transportWordKinds = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"query", queryTransportWordPattern},
	{"form", formTransportWordPattern},
	{"raw", rawTransportWordPattern},
	{"response", responseFieldWordPattern},
}

// transportWordProximityWords bounds how many words may separate an
// `api_key` occurrence from the transport word that describes it and still
// count as describing it specifically, reusing the same bound
// deprecationContextProximityWords uses for binding a deprecation-context
// word to the mention it describes.
const transportWordProximityWords = deprecationContextProximityWords

// transportKindNear classifies which specific transport (or non-transport
// "response" field mention) the `api_key` occurrence at [start, end) in
// clauseText is about: the transport word, among every occurrence of every
// kind within transportWordProximityWords words of it in clauseText, that
// is *nearest* to it by word distance in either direction -- a directional,
// occurrence-specific association, not "any transport word present
// somewhere in a flat character window around it", which could not
// distinguish a word immediately describing this specific mention from a
// different mention's own transport word merely falling in the same window
// in a compact sentence naming two transports close together.
//
// ambiguous is true only when two or more *different* kinds are tied for
// nearest -- a genuine conflict, not simply the common case of no transport
// word being nearby at all (which instead returns kind="", ambiguous=false:
// "no information", not "conflicting information"). Callers must never let
// an ambiguous classification suppress a recommendation (see
// hasAffirmativeDeprecationContext): unlike "no information", which leaves
// existing non-transport-aware suppression untouched, "conflicting
// information" must fail safe and never suppress, since the actual
// transport cannot be determined at all.
func transportKindNear(clauseText string, start, end int) (kind string, ambiguous bool) {
	start = clampIndex(start, len(clauseText))
	end = clampIndex(end, len(clauseText))
	if end < start {
		end = start
	}

	bestDistance := -1
	tied := false
	for _, tw := range transportWordKinds {
		for _, loc := range tw.pattern.FindAllStringIndex(clauseText, -1) {
			var between string
			switch {
			case loc[0] >= end:
				between = clauseText[end:loc[0]]
			case loc[1] <= start:
				between = clauseText[loc[1]:start]
			default:
				between = ""
			}
			d := len(strings.Fields(between))
			if d > transportWordProximityWords {
				continue
			}
			switch {
			case bestDistance == -1 || d < bestDistance:
				bestDistance = d
				kind = tw.kind
				tied = false
			case d == bestDistance && tw.kind != kind:
				tied = true
			}
		}
	}
	if bestDistance == -1 {
		return "", false
	}
	if tied {
		return "", true
	}
	return kind, false
}

// hasAffirmativeDeprecationContext reports whether clauseText contains at
// least one deprecation-context word or phrase that is:
//
//   - not directly negated (see negationWordPattern/negationBoundaryPattern);
//   - not separated from recommendationPos -- the byte offset, within
//     clauseText, of the recommendation match being evaluated -- by an
//     adversativeBoundaryPattern connector (see adversativeBoundaryPattern);
//   - within deprecationContextProximityWords of an actual `api_key`
//     mention, so it describes that transport specifically rather than
//     some other subject merely sharing the sentence or clause;
//   - if the match is the bare removal version number, accompanied by an
//     explicit construction word nearby (see hasNearbyConstructionWord) --
//     a release number alone never asserts anything is deprecated; and
//   - about the same specific transport (query/form/raw) as the
//     recommendation, whenever both are unambiguously classified (see
//     transportKindNear) -- a deprecation notice about one transport must
//     not suppress a live recommendation of a different one, and an
//     ambiguous classification on either side never suppresses either,
//     since which transport it actually describes cannot be determined.
//
// It is used both for a table cell (with recommendationPos set to
// len(cellText), so any qualifying context anywhere in the cell counts, as
// a whole cell is a small, self-contained scope) and for recommendation
// prose (scoped per candidate match to its own enclosing sentence via
// sentenceBounds), so a negated mention, one that belongs to a different,
// adversatively contrasted clause, one that is simply too far from any
// `api_key` mention to describe it, an unaccompanied bare version number, or
// one describing a different (or ambiguous) transport, cannot accidentally
// suppress a genuine violation.
func hasAffirmativeDeprecationContext(clauseText string, recommendationPos int) bool {
	// Defensive clamp: every caller is expected to pass a valid index into
	// clauseText, but guarding here means a future caller's off-by-one
	// cannot turn into an out-of-range slice panic below.
	if recommendationPos < 0 {
		recommendationPos = 0
	}
	if recommendationPos > len(clauseText) {
		recommendationPos = len(clauseText)
	}

	recAPIStart, recAPIEnd, recAPIFound := nearestAPIKeyOccurrence(clauseText, recommendationPos)
	var recTransport string
	var recAmbiguous bool
	if recAPIFound {
		recTransport, recAmbiguous = transportKindNear(clauseText, recAPIStart, recAPIEnd)
	}

	for _, match := range deprecationContextPattern.FindAllStringIndex(clauseText, -1) {
		start := match[0]
		windowStart := start - 40
		if windowStart < 0 {
			windowStart = 0
		}
		preceding := clauseText[windowStart:start]
		if locs := negationBoundaryPattern.FindAllStringIndex(preceding, -1); len(locs) > 0 {
			last := locs[len(locs)-1]
			preceding = preceding[last[1]:]
		}
		words := strings.Fields(preceding)
		if len(words) > negationProximityWords {
			words = words[len(words)-negationProximityWords:]
		}
		if negationWordPattern.MatchString(strings.Join(words, " ")) {
			continue
		}

		var between string
		if start <= recommendationPos {
			between = clauseText[start:recommendationPos]
		} else {
			between = clauseText[recommendationPos:start]
		}
		if adversativeBoundaryPattern.MatchString(between) {
			continue
		}

		if d := deprecationContextProximityDistance(clauseText, start); d == -1 || d > deprecationContextProximityWords {
			continue
		}

		if bareVersionPattern.MatchString(clauseText[match[0]:match[1]]) && !hasNearbyConstructionWord(clauseText, start) {
			continue
		}

		if ctxAPIStart, ctxAPIEnd, ctxAPIFound := nearestAPIKeyOccurrence(clauseText, start); ctxAPIFound {
			ctxTransport, ctxAmbiguous := transportKindNear(clauseText, ctxAPIStart, ctxAPIEnd)
			if recAmbiguous || ctxAmbiguous {
				continue
			}
			if recTransport != "" && ctxTransport != "" && recTransport != ctxTransport {
				continue
			}
		}

		return true
	}
	return false
}

// sentenceTerminators are the characters that end an English sentence, used
// by sentenceBounds to scope deprecation-context association to "the same
// sentence as a recommendation": an unrelated deprecation mention about a
// different subject, in an earlier or later sentence of the same paragraph,
// must not be able to suppress a live recommendation elsewhere in that
// paragraph.
const sentenceTerminators = ".!?"

// isDottedVersionPeriod reports whether the '.' at text[idx] sits between
// two ASCII digits, as in the removal version "0.13.0". Such a period is
// part of a dotted version number, never a sentence boundary: treating it as
// one would split "...targeted for removal in 0.13.0." into a fragment
// ending at "0" and a separate fragment starting at "13.0.", stranding the
// removal-version context away from whatever recommendation sentence it was
// meant to qualify.
func isDottedVersionPeriod(text string, idx int) bool {
	if idx <= 0 || idx+1 >= len(text) || text[idx] != '.' {
		return false
	}
	isDigit := func(b byte) bool { return b >= '0' && b <= '9' }
	return isDigit(text[idx-1]) && isDigit(text[idx+1])
}

// nextSentenceTerminator returns the index of the first real sentence
// terminator in text at or after fromIdx, skipping any '.' that is part of a
// dotted version number (see isDottedVersionPeriod). It returns -1 if none
// is found.
func nextSentenceTerminator(text string, fromIdx int) int {
	for i := fromIdx; i < len(text); i++ {
		if !strings.ContainsRune(sentenceTerminators, rune(text[i])) {
			continue
		}
		if text[i] == '.' && isDottedVersionPeriod(text, i) {
			continue
		}
		return i
	}
	return -1
}

// prevSentenceTerminator returns the index of the last real sentence
// terminator in text at or before fromIdx, skipping any '.' that is part of
// a dotted version number (see isDottedVersionPeriod). It returns -1 if none
// is found.
func prevSentenceTerminator(text string, fromIdx int) int {
	for i := fromIdx; i >= 0; i-- {
		if !strings.ContainsRune(sentenceTerminators, rune(text[i])) {
			continue
		}
		if text[i] == '.' && isDottedVersionPeriod(text, i) {
			continue
		}
		return i
	}
	return -1
}

// sentenceBounds returns the [start, end) byte offsets of the sentence in
// text containing offset pos, bounded by the nearest real sentence
// terminator on each side (or the start/end of text if none is found). A
// '.' inside a dotted version number such as "0.13.0" is never treated as
// that boundary.
func sentenceBounds(text string, pos int) (start, end int) {
	start = 0
	if idx := prevSentenceTerminator(text, pos-1); idx != -1 {
		start = idx + 1
	}
	end = len(text)
	if idx := nextSentenceTerminator(text, pos); idx != -1 {
		end = idx + 1
	}
	return start, end
}

// tableRowAlternativePattern matches prose that lists `api_key` as an
// alternative/option, e.g. "... or an `api_key` parameter" or
// "... or a api_key value". This is the exact phrasing of the original bug
// in docs/GROUP_IMPORT_LIMITS.md.
var offeredAsAlternativePattern = regexp.MustCompile("(?i)\\bor\\s+an?\\s+`?api_key`?")

// recommendationProximity bounds how many intervening words a recommendation
// verb may be from "api_key" and still count as describing it, rather than
// coincidentally sharing a (potentially multi-sentence) block of text with
// it. Without this bound, a purely descriptive paragraph that happens to use
// the word "authentication" in one sentence and mention `api_key` in an
// unrelated sentence nearby would falsely violate.
const recommendationProximity = `(?:\s+\S+){0,4}\s+`

// strongRecommendationPattern matches the word "authenticate" (and its
// inflections: authenticates, authenticated, authenticating,
// authentication) within recommendationProximity words of `api_key`, in
// either order. Paired with no affirmative deprecation context, this word
// alone is specific enough to signal a recommendation ("authenticate via
// api_key", "authenticate with the api_key parameter") without needing a
// separate anchor word -- but only when it is actually close to the
// `api_key` mention, not merely present somewhere in the same block.
var strongRecommendationPattern = regexp.MustCompile(
	`(?i)\bauthenticat\w*\b` + recommendationProximity + `api_key\b|\bapi_key\b` + recommendationProximity + `authenticat\w*\b`,
)

// weakRecommendationVerbPattern matches common, much more generic verbs --
// "use" (use/uses/used/using -- an explicit inflection list, not `use\w*`,
// which would also incidentally match unrelated words like "user" or
// "username"), "via", "with", "accept" (accept/accepts/accepted/accepting),
// "recommend" (recommend/recommends/recommended/recommending) -- within
// recommendationProximity words of `api_key`. These words are far too
// common in ordinary prose (including this package's own documentation) to
// use unbounded across a whole block; requiring proximity to `api_key`, in
// addition to the recommendationAnchorPattern check below, is what keeps
// this narrow.
var weakRecommendationVerbPattern = regexp.MustCompile(
	`(?i)\b(?:use|uses|used|using|via|with|accept\w*|recommend\w*)\b` + recommendationProximity + `api_key\b|\bapi_key\b` + recommendationProximity + `\b(?:use|uses|used|using|via|with|accept\w*|recommend\w*)\b`,
)

// recommendationAnchorPattern matches a word that anchors a weak
// recommendation verb to the authentication-transport meaning of `api_key`,
// as opposed to an incidental mention (a curl flag, a value, a variable
// name) that happens to share the text with one of those common verbs.
var recommendationAnchorPattern = regexp.MustCompile(`(?i)\b(parameter|param|query|field|header|credential)\b`)

// recommendationMention reports whether blockText (a flattened paragraph,
// heading, or table cell) presents `api_key` as an alternative or
// recommended authentication option -- "or an api_key parameter", "use the
// api_key query parameter", "authenticate via api_key" -- with no
// affirmative deprecation context in the same sentence as that particular
// mention. Each candidate recommendation match is judged against only its
// own enclosing sentence (see sentenceBounds), not the whole block: an
// unrelated deprecation notice about a different subject, in an earlier or
// later sentence of the same block, must not suppress a live recommendation
// elsewhere in it. Within that sentence, a context word separated from the
// match by an adversativeBoundaryPattern connector ("but", "however", ...)
// -- in either direction -- is likewise excluded, since it describes a
// contrasting clause's claim, not this one's (see
// hasAffirmativeDeprecationContext).
func recommendationMention(blockText string) bool {
	if !strings.Contains(strings.ToLower(blockText), "api_key") {
		return false
	}

	checkMatch := func(loc []int, requireAnchor bool) bool {
		sentenceStart, sentenceEnd := sentenceBounds(blockText, loc[0])
		sentence := blockText[sentenceStart:sentenceEnd]
		if requireAnchor && !recommendationAnchorPattern.MatchString(sentence) {
			return false
		}
		// The match span (loc) can, in principle, extend past the detected
		// sentence boundary (sentenceEnd) -- recommendationProximity allows
		// up to four intervening words, one of which could itself contain
		// a sentence-ending character within a larger token. Clamp the
		// resulting position into [0, len(sentence)] so it is always a
		// valid index into sentence, rather than risking an out-of-range
		// slice inside hasAffirmativeDeprecationContext.
		recommendationPos := loc[1] - sentenceStart
		if recommendationPos < 0 {
			recommendationPos = 0
		}
		if recommendationPos > len(sentence) {
			recommendationPos = len(sentence)
		}
		return !hasAffirmativeDeprecationContext(sentence, recommendationPos)
	}

	for _, loc := range offeredAsAlternativePattern.FindAllStringIndex(blockText, -1) {
		if checkMatch(loc, false) {
			return true
		}
	}
	for _, loc := range strongRecommendationPattern.FindAllStringIndex(blockText, -1) {
		if checkMatch(loc, false) {
			return true
		}
	}
	for _, loc := range weakRecommendationVerbPattern.FindAllStringIndex(blockText, -1) {
		if checkMatch(loc, true) {
			return true
		}
	}
	return false
}
