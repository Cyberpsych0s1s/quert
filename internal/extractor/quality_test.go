package extractor

import (
	"strings"
	"testing"
)

// The "crawled myself" lesson, encoded: a short genuine paragraph must outscore
// a long link/label dump. Length must not be the discriminator — prose shape is.
func TestQualityProseBeatsLongLinkDump(t *testing.T) {
	// ~40 words of real prose (terse, but full of function words).
	shortProse := "the hard part was never fetching. it was knowing when to stop, " +
		"when a host had given all it was going to give. so i taught the crawler " +
		"to wait, and to listen, and to leave when it was asked to."

	// A much longer wall of labels and link text — no function words, no sentences.
	longLinkDump := ""
	for i := 0; i < 60; i++ {
		longLinkDump += "Home About Login Signup Pricing Docs Blog Careers Contact " +
			"Privacy Terms Download Products Solutions Enterprise Support "
	}

	proseScore := scoreContentQuality(shortProse, true, 1, true)
	dumpScore := scoreContentQuality(longLinkDump, false, 1, true)

	t.Logf("prose(%d words)=%.2f  linkdump(%d words)=%.2f",
		len(strings.Fields(shortProse)), proseScore, len(strings.Fields(longLinkDump)), dumpScore)

	if proseScore <= dumpScore {
		t.Errorf("short prose (%.2f) should outscore long link-dump (%.2f)", proseScore, dumpScore)
	}
	if proseScore < 0.6 {
		t.Errorf("genuine prose scored too low: %.2f", proseScore)
	}
	if dumpScore > 0.3 {
		t.Errorf("label/link dump scored too high: %.2f", dumpScore)
	}
}
