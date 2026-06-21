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

func TestHTMLContentExtractor_ExtractContent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	config := GetDefaultExtractorConfig()
	extractor := NewHTMLContentExtractor(config, logger)
	defer extractor.Close()

	htmlContent := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Test Page Title</title>
		<meta name="description" content="This is a test page description">
		<meta name="author" content="Test Author">
		<meta name="keywords" content="test, html, extraction">
	</head>
	<body>
		<header>
			<nav>Navigation</nav>
		</header>
		<main>
			<article>
				<h1>Main Article Title</h1>
				<p>This is the main content of the article. It contains important information.</p>
				<p>This is another paragraph with more content.</p>
				<a href="https://example.com">External Link</a>
				<a href="/internal">Internal Link</a>
			</article>
		</main>
		<aside>
			<div class="ads">Advertisement</div>
		</aside>
		<footer>
			Footer content
		</footer>
	</body>
	</html>
	`

	content, err := extractor.ExtractContent([]byte(htmlContent), "text/html", "https://test.com/page")
	require.NoError(t, err)
	require.NotNil(t, content)

	// Test title extraction
	assert.Equal(t, "Test Page Title", content.Title)

	// Test main content extraction
	assert.Contains(t, content.MainContent, "Main Article Title")
	assert.Contains(t, content.MainContent, "main content of the article")
	assert.NotContains(t, content.MainContent, "Navigation")    // Should be removed as boilerplate
	assert.NotContains(t, content.MainContent, "Advertisement") // Should be removed as boilerplate

	// Test clean text
	assert.NotEmpty(t, content.CleanText)
	assert.NotContains(t, content.CleanText, "<")
	assert.NotContains(t, content.CleanText, ">")

	// Test links extraction
	assert.Len(t, content.Links, 2)

	// Find links
	var externalLink, internalLink *ExtractedLink
	for i := range content.Links {
		if content.Links[i].URL == "https://example.com" {
			externalLink = &content.Links[i]
		}
		if content.Links[i].URL == "https://test.com/internal" {
			internalLink = &content.Links[i]
		}
	}

	require.NotNil(t, externalLink)
	require.NotNil(t, internalLink)
	assert.False(t, externalLink.Internal)
	assert.True(t, internalLink.Internal)

	// Test metadata
	assert.Equal(t, "Test Author", content.Metadata.Author)
	assert.Equal(t, "This is a test page description", content.Metadata.Description)
	assert.Contains(t, content.Metadata.Keywords, "test")
	assert.Contains(t, content.Metadata.Keywords, "html")
	assert.Contains(t, content.Metadata.Keywords, "extraction")

	// Test quality score
	assert.Greater(t, content.QualityScore, 0.0)
	assert.LessOrEqual(t, content.QualityScore, 1.0)
}

func TestPlainTextExtractor_ExtractContent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	config := GetDefaultExtractorConfig()
	extractor := NewPlainTextExtractor(config, logger)
	defer extractor.Close()

	plainText := `Sample Document Title

This is the main content of a plain text document. 
It has multiple paragraphs and sentences.

This is another paragraph with different content.
It provides more information about the topic.

Final paragraph with conclusion.`

	content, err := extractor.ExtractContent([]byte(plainText), "text/plain", "https://test.com/text")
	require.NoError(t, err)
	require.NotNil(t, content)

	// Test title extraction (should be first meaningful line)
	assert.Contains(t, content.Title, "Sample Document Title")

	// Test content extraction
	assert.Contains(t, content.MainContent, "main content")
	assert.Contains(t, content.MainContent, "multiple paragraphs")

	// Test metadata
	assert.Greater(t, content.Metadata.WordCount, 0)
	assert.Greater(t, content.Metadata.SentenceCount, 0)
	assert.Greater(t, content.Metadata.ParagraphCount, 0)

	// Test quality score
	assert.Greater(t, content.QualityScore, 0.0)
	assert.LessOrEqual(t, content.QualityScore, 1.0)

	// Should have no links or images for plain text
	assert.Empty(t, content.Links)
	assert.Empty(t, content.Images)
}

func TestXMLContentExtractor_ExtractContent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	config := GetDefaultExtractorConfig()
	extractor := NewXMLContentExtractor(config, logger)
	defer extractor.Close()

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
	<document>
		<title>XML Document Title</title>
		<author>XML Author</author>
		<date>2023-12-01</date>
		<content>
			<paragraph>This is the main content of the XML document.</paragraph>
			<paragraph>It contains structured information in XML format.</paragraph>
		</content>
		<link url="https://example.com">Example Link</link>
	</document>`

	content, err := extractor.ExtractContent([]byte(xmlContent), "application/xml", "https://test.com/xml")
	require.NoError(t, err)
	require.NotNil(t, content)

	// Test title extraction
	assert.Equal(t, "XML Document Title", content.Title)

	// Test content extraction
	assert.Contains(t, content.MainContent, "main content")
	assert.Contains(t, content.MainContent, "structured information")

	// Test metadata
	assert.Equal(t, "XML Author", content.Metadata.Author)
	assert.Equal(t, "2023-12-01", content.Metadata.PublishedDate)

	// Test quality score
	assert.Greater(t, content.QualityScore, 0.0)
	assert.LessOrEqual(t, content.QualityScore, 1.0)
}

func TestExtractorFactory_CreateExtractor(t *testing.T) {
	logger := zaptest.NewLogger(t)
	factory := NewExtractorFactory(nil, logger)
	defer factory.Close()

	tests := []struct {
		contentType  string
		expectedType string
	}{
		{"text/html", "*extractor.HTMLContentExtractor"},
		{"text/html; charset=utf-8", "*extractor.HTMLContentExtractor"},
		{"application/xhtml+xml", "*extractor.HTMLContentExtractor"},
		{"text/plain", "*extractor.PlainTextExtractor"},
		{"application/xml", "*extractor.XMLContentExtractor"},
		{"text/xml", "*extractor.XMLContentExtractor"},
		{"application/rss+xml", "*extractor.XMLContentExtractor"},
		{"unknown/type", "*extractor.HTMLContentExtractor"}, // Default to HTML
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			extractor := factory.CreateExtractor(tt.contentType)
			assert.NotNil(t, extractor)

			// Check that it implements the ContentExtractor interface
			assert.Implements(t, (*ContentExtractor)(nil), extractor)

			extractor.Close()
		})
	}
}

func TestExtractorFactory_ExtractContent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	config := GetDefaultExtractorConfig()
	config.QualityThreshold = 0.1 // Lower threshold for test
	factory := NewExtractorFactory(config, logger)
	defer factory.Close()

	htmlContent := `<html><head><title>Test</title></head><body><p>This is meaningful content that should pass quality checks. It has multiple sentences and provides value to readers.</p></body></html>`

	content, err := factory.ExtractContent([]byte(htmlContent), "text/html", "https://test.com")
	require.NoError(t, err)
	require.NotNil(t, content)

	assert.Equal(t, "Test", content.Title)
	assert.Contains(t, content.CleanText, "meaningful content")
}

func TestContentQualityFiltering(t *testing.T) {
	logger := zaptest.NewLogger(t)
	config := GetDefaultExtractorConfig()
	config.QualityThreshold = 0.3 // Lower threshold for test
	factory := NewExtractorFactory(config, logger)
	defer factory.Close()

	// Very short content that should fail quality check
	shortContent := `<html><head><title>Short</title></head><body><p>Hi</p></body></html>`

	_, err := factory.ExtractContent([]byte(shortContent), "text/html", "https://test.com")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrQualityBelowThreshold)

	// Longer content that should pass
	longContent := `<html><head><title>Comprehensive Article About Web Crawling</title><meta name="description" content="This is a comprehensive article about web crawling techniques"><meta name="author" content="Test Author"></head><body>` +
		strings.Repeat("<p>This is a detailed paragraph with meaningful content that provides substantial value to readers and contains comprehensive information about the topic being discussed. The content covers important aspects of web crawling, data extraction, and content processing techniques that are valuable for developers.</p>", 3) +
		`<h2>Section Title</h2><p>Additional content section with more detailed information.</p></body></html>`

	content, err := factory.ExtractContent([]byte(longContent), "text/html", "https://test.com")
	require.NoError(t, err)
	require.NotNil(t, content)
	assert.GreaterOrEqual(t, content.QualityScore, config.QualityThreshold)
}
