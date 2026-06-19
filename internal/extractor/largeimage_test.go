package extractor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePixelDimension(t *testing.T) {
	cases := []struct {
		in     string
		wantV  int
		wantOK bool
	}{
		{"", 0, false},
		{"150", 150, true},
		{"150px", 150, true},
		{" 150px ", 150, true},
		{"10px", 10, true},
		{"50%", 0, false},
		{"auto", 0, false},
		{"99999px", 99999, true},
	}
	for _, c := range cases {
		v, ok := parsePixelDimension(c.in)
		assert.Equal(t, c.wantOK, ok, "ok for %q", c.in)
		if c.wantOK {
			assert.Equal(t, c.wantV, v, "value for %q", c.in)
		}
	}
}

func TestIsLargeImage(t *testing.T) {
	h := &HTMLContentExtractor{}
	cases := []struct {
		name string
		img  ExtractedImage
		want bool
	}{
		// Regression: old code returned true here ("100" has len 3) AND
		// returned false for genuinely small images. Verify by value now.
		{"small both px", ExtractedImage{Width: "10px", Height: "10px"}, false},
		{"large width px", ExtractedImage{Width: "300px", Height: "10px"}, true},
		{"at threshold", ExtractedImage{Width: "100px"}, true},
		{"just below threshold", ExtractedImage{Width: "99px"}, false},
		{"bare number large", ExtractedImage{Width: "640", Height: "480"}, true},
		{"bare number small", ExtractedImage{Width: "16", Height: "16"}, false},
		{"no dimensions defaults large", ExtractedImage{}, true},
		{"percentage ignored, defaults large", ExtractedImage{Width: "100%"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, h.IsLargeImage(c.img))
		})
	}
}
