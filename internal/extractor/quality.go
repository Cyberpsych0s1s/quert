package extractor

import (
	"math"
	"strings"
	"unicode"
)

// englishStopwords are common function words. Their density is the core quality
// signal: genuine prose is full of them, while navigation, link lists, and label
// text have almost none — and this holds regardless of length. A terse but real
// paragraph scores well; a long link-dump does not.
var englishStopwords = map[string]struct{}{}

func init() {
	for _, w := range strings.Fields(
		"a an the and or but nor so yet for of to in on at by from with as into over " +
			"under out up down off about above below between through during before after " +
			"is are was were be been being am it its this that these those there here " +
			"i you he she we they me him her us them my your his our their not no yes if " +
			"then than when while which who whom whose what where why how all any both each " +
			"few more most other some such only own same too very can will just do does did " +
			"have has had having would should could may might must shall because although " +
			"though since unless until whether either neither also however thus hence " +
			"i'm it's that's don't can't we're they're you're i've",
	) {
		englishStopwords[w] = struct{}{}
	}
}

// textSignals are length-independent shape metrics computed from clean text.
type textSignals struct {
	words         int
	stopwordRatio float64 // fraction of tokens that are function words
	sentences     int     // count of sentence-ending punctuation
}

// analyzeText derives prose-shape signals from already-clean text.
func analyzeText(text string) textSignals {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return textSignals{}
	}
	stop := 0
	for _, w := range fields {
		lw := strings.ToLower(strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '\''
		}))
		if _, ok := englishStopwords[lw]; ok {
			stop++
		}
	}
	sentences := strings.Count(text, ".") + strings.Count(text, "!") + strings.Count(text, "?")
	return textSignals{
		words:         len(fields),
		stopwordRatio: float64(stop) / float64(len(fields)),
		sentences:     sentences,
	}
}

// proseScore maps stopword ratio to 0..1. Genuine English prose sits roughly in
// the 0.20–0.55 band; below ~0.05 the text reads like labels/nav, above ~0.75 it
// is degenerate (stopword spam). The band is deliberately wide so terse writing
// still scores full marks.
func proseScore(stopwordRatio float64) float64 {
	switch {
	case stopwordRatio <= 0.05:
		return 0.0
	case stopwordRatio < 0.20:
		return (stopwordRatio - 0.05) / 0.15
	case stopwordRatio <= 0.55:
		return 1.0
	case stopwordRatio < 0.75:
		return 1.0 - (stopwordRatio-0.55)/0.20*0.5
	default:
		return 0.5
	}
}

// scoreContentQuality produces a 0..1 quality score that rewards prose shape over
// raw length. Inputs: the clean text, whether readability found article-shaped
// content, paragraph count, and whether a title is present. Length is only a
// minor floor — never the gate — so a short genuine post can outscore a long
// link-dump, which is the whole point.
func scoreContentQuality(cleanText string, viaReadability bool, paragraphCount int, hasTitle bool) float64 {
	if cleanText == "" {
		return 0.0
	}
	sig := analyzeText(cleanText)
	if sig.words == 0 {
		return 0.0
	}

	prose := proseScore(sig.stopwordRatio) // the centerpiece

	structure := 0.0
	if sig.sentences >= 2 {
		structure += 0.5
	}
	if paragraphCount > 1 || sig.sentences >= 4 {
		structure += 0.5
	}

	// Length as a gentle floor: ~120 words saturates it. Not the gate.
	length := math.Min(1.0, float64(sig.words)/120.0)

	readability := 0.0
	if viaReadability {
		readability = 1.0
	}

	title := 0.0
	if hasTitle {
		title = 1.0
	}

	score := 0.45*prose + 0.20*structure + 0.15*length + 0.12*readability + 0.08*title

	// Hard guard: text with almost no function words is labels/nav, not content,
	// no matter how long or how rich its metadata. Cap it below any threshold.
	if prose == 0.0 {
		score = math.Min(score, 0.2)
	}

	return math.Min(1.0, score)
}
