package extractor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestExtractContent_NoStyleLeak guards against inline <style>/<script> source
// leaking into the extracted text (regression: Wikipedia's .mw-parser-output
// CSS blocks appeared verbatim in CleanText).
func TestExtractContent_NoStyleLeak(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cfg := GetDefaultExtractorConfig()
	cfg.MinTextLength = 1
	h := NewHTMLContentExtractor(cfg, logger)
	defer h.Close()

	html := `<html><head><title>T</title></head><body><main>
		<style>.mw-parser-output{font-style:italic}.hatnote{padding-left:1.6em}</style>
		<script>var x = 42; console.log(x);</script>
		<p>This is the real article text that should be extracted cleanly.</p>
	</main></body></html>`

	content, err := h.ExtractContent([]byte(html), "text/html", "https://example.com")
	require.NoError(t, err)
	require.NotNil(t, content)

	assert.Contains(t, content.CleanText, "real article text")
	assert.NotContains(t, content.CleanText, "mw-parser-output")
	assert.NotContains(t, content.CleanText, "font-style")
	assert.NotContains(t, content.CleanText, "console.log")
	assert.False(t, strings.Contains(content.CleanText, "{"), "no CSS braces should leak")
}
