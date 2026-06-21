// Copyright 2026 Omar Almahri and the Quert contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
