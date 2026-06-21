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

	"go.uber.org/zap"
)

// A messy page: real article wrapped in nav, ads, sidebar, and footer clutter.
// Readability-class extraction should keep the article prose and drop the rest.
const messyArticleHTML = `<!DOCTYPE html><html><head><title>The Article Title</title></head>
<body>
  <nav class="nav"><a href="/">Home</a><a href="/about">About</a><a href="/login">Log in</a></nav>
  <header class="banner">SiteName · Subscribe now · Newsletter signup</header>
  <aside class="sidebar"><div class="ads">Buy cheap widgets! Limited offer! Click here!</div>
    <ul><li><a href="/x">Related thing one</a></li><li><a href="/y">Related thing two</a></li></ul></aside>
  <article>
    <h1>The Article Title</h1>
    <p>The first paragraph explains the central idea in a few clear sentences, with enough words to read like genuine prose rather than a navigation label.</p>
    <p>A second paragraph continues the argument, adding detail and a comma-separated clause, the kind of structure that readability scoring rewards over link-dense boilerplate.</p>
    <p>The closing paragraph wraps things up, restating the point one final time so the reader leaves with a complete thought.</p>
  </article>
  <footer class="footer">Copyright 2026 · Privacy Policy · Terms of Service · Contact us</footer>
</body></html>`

func TestReadabilityExtractsArticleDropsClutter(t *testing.T) {
	ex := NewHTMLContentExtractor(GetDefaultExtractorConfig(), zap.NewNop())

	got, err := ex.ExtractContent([]byte(messyArticleHTML), "text/html", "https://example.com/post")
	if err != nil {
		t.Fatalf("ExtractContent: %v", err)
	}

	if method := got.ExtractionMap["main_content_method"]; method != "readability" {
		t.Errorf("expected readability extraction, got %q", method)
	}

	text := got.CleanText
	// Article prose must survive.
	for _, want := range []string{"central idea", "continues the argument", "wraps things up"} {
		if !strings.Contains(text, want) {
			t.Errorf("clean text missing article phrase %q\ngot: %s", want, text)
		}
	}
	// Clutter must be gone.
	for _, junk := range []string{"Buy cheap widgets", "Privacy Policy", "Newsletter signup", "Related thing one"} {
		if strings.Contains(text, junk) {
			t.Errorf("clean text still contains clutter %q\ngot: %s", junk, text)
		}
	}
}
