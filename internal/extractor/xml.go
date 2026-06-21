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
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ExtractContent extracts content from XML
func (x *XMLContentExtractor) ExtractContent(content []byte, contentType string, sourceURL string) (*ExtractedContent, error) {
	// Use goquery to parse XML similar to HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(content)))
	if err != nil {
		// Fallback to plain text extraction if XML parsing fails
		text := x.ExtractTextFromXML(string(content))
		return &ExtractedContent{
			Title:         x.ExtractTitleFromXMLText(text),
			MainContent:   text,
			CleanText:     x.CleanXMLText(text),
			Links:         []ExtractedLink{},
			Images:        []ExtractedImage{},
			Metadata:      x.CreateXMLMetadata(text),
			QualityScore:  x.CalculateQualityScore(&ExtractedContent{CleanText: x.CleanXMLText(text)}),
			ProcessedAt:   time.Now(),
			ExtractionMap: map[string]string{"extraction_method": "xml_fallback"},
		}, nil
	}

	// Extract text content from parsed XML
	text := doc.Text()
	cleanText := x.CleanXMLText(text)

	extractedContent := &ExtractedContent{
		Title:         x.ExtractTitleFromXML(doc),
		MainContent:   cleanText,
		CleanText:     cleanText,
		Links:         x.ExtractLinksFromXML(doc, sourceURL),
		Images:        []ExtractedImage{}, // XML typically doesn't have images like HTML
		Metadata:      x.CreateXMLMetadataFromDoc(doc, cleanText),
		QualityScore:  x.CalculateQualityScore(&ExtractedContent{CleanText: cleanText}),
		ProcessedAt:   time.Now(),
		ExtractionMap: map[string]string{"extraction_method": "xml_goquery"},
	}

	return extractedContent, nil
}

// ExtractText extracts clean text from XML content
func (x *XMLContentExtractor) ExtractText(content []byte, contentType string) (string, error) {
	text := x.ExtractTextFromXML(string(content))
	return x.CleanXMLText(text), nil
}

// ExtractLinks extracts links from XML content (if any)
func (x *XMLContentExtractor) ExtractLinks(content []byte, baseURL string) ([]ExtractedLink, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(content)))
	if err != nil {
		return []ExtractedLink{}, nil // No links if parsing fails
	}

	return x.ExtractLinksFromXML(doc, baseURL), nil
}

// ExtractImages returns empty slice for XML (typically no images)
func (x *XMLContentExtractor) ExtractImages(content []byte, baseURL string) ([]ExtractedImage, error) {
	return []ExtractedImage{}, nil
}

// ExtractMetadata extracts metadata from XML content
func (x *XMLContentExtractor) ExtractMetadata(content []byte, contentType string) (*ContentMetadata, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(content)))
	if err != nil {
		text := x.ExtractTextFromXML(string(content))
		metadata := x.CreateXMLMetadata(text)
		return &metadata, nil
	}

	text := doc.Text()
	metadata := x.CreateXMLMetadataFromDoc(doc, text)
	return &metadata, nil
}

// ExtractTextFromXML extracts text content from XML string
func (x *XMLContentExtractor) ExtractTextFromXML(xmlContent string) string {
	// Remove XML tags using regex
	tagRegex := regexp.MustCompile(`<[^>]*>`)
	text := tagRegex.ReplaceAllString(xmlContent, " ")

	return text
}

// ExtractTitleFromXML extracts title from parsed XML document
func (x *XMLContentExtractor) ExtractTitleFromXML(doc *goquery.Document) string {
	// Try common XML title elements
	titleSelectors := []string{"title", "name", "heading", "header", "subject"}

	for _, selector := range titleSelectors {
		title := doc.Find(selector).First().Text()
		if title != "" {
			return strings.TrimSpace(title)
		}
	}

	// Fallback to first text element that looks like a title
	var title string
	doc.Find("*").EachWithBreak(func(i int, s *goquery.Selection) bool {
		text := strings.TrimSpace(s.Text())
		if len(text) > 5 && len(text) < 200 && !strings.Contains(text, "\n") {
			title = text
			return false // Break out of loop
		}
		return true
	})

	return title
}

// ExtractTitleFromXMLText extracts title from XML text
func (x *XMLContentExtractor) ExtractTitleFromXMLText(text string) string {
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 5 && len(line) < 200 {
			return line
		}
	}

	// Fallback to first 50 characters
	if runeLen(text) > 50 {
		return strings.TrimSpace(truncateRunes(text, 50)) + "..."
	}

	return strings.TrimSpace(text)
}

// CleanXMLText cleans XML text content
func (x *XMLContentExtractor) CleanXMLText(text string) string {
	if text == "" {
		return ""
	}

	cleaned := text

	// Normalize whitespace if configured
	if x.Config.NormalizeWhitespace {
		// Replace multiple whitespace characters with single spaces
		whitespaceRegex := regexp.MustCompile(`\s+`)
		cleaned = whitespaceRegex.ReplaceAllString(cleaned, " ")

		// Remove leading/trailing whitespace
		cleaned = strings.TrimSpace(cleaned)
	}

	// Apply length constraints
	if x.Config.MaxTextLength > 0 {
		cleaned = truncateRunes(cleaned, x.Config.MaxTextLength)
	}

	return cleaned
}

// ExtractLinksFromXML extracts any link-like elements from XML
func (x *XMLContentExtractor) ExtractLinksFromXML(doc *goquery.Document, baseURL string) []ExtractedLink {
	var links []ExtractedLink
	seenLinks := make(map[string]bool)

	// Look for common link attributes in XML
	linkAttributes := []string{"href", "url", "link", "src"}

	for _, attr := range linkAttributes {
		doc.Find("*[" + attr + "]").Each(func(i int, s *goquery.Selection) {
			if linkURL, exists := s.Attr(attr); exists && linkURL != "" {
				// Simple validation - check if it looks like a URL
				if strings.HasPrefix(linkURL, "http://") || strings.HasPrefix(linkURL, "https://") {
					if !seenLinks[linkURL] {
						seenLinks[linkURL] = true

						text := strings.TrimSpace(s.Text())
						if text == "" {
							text = linkURL
						}

						link := ExtractedLink{
							URL:      linkURL,
							Text:     text,
							Title:    "",
							Rel:      "",
							Internal: false, // Assume external for XML
						}
						links = append(links, link)
					}
				}
			}
		})
	}

	return links
}

// CreateXMLMetadata creates metadata for XML content from text
func (x *XMLContentExtractor) CreateXMLMetadata(text string) ContentMetadata {
	return ContentMetadata{
		Language:       "en", // Default to English
		Author:         "",   // No standard author in XML
		PublishedDate:  "",   // No standard date in XML
		ModifiedDate:   "",   // No standard date in XML
		Description:    x.CreateDescriptionFromText(text),
		Keywords:       x.ExtractKeywordsFromText(text),
		ContentLength:  len(text),
		WordCount:      x.CountWords(text),
		SentenceCount:  x.CountSentences(text),
		ParagraphCount: x.CountParagraphs(text),
		LinkCount:      0,
		ImageCount:     0,
		Tags:           []string{},
		Categories:     []string{},
		CustomMetadata: map[string]string{"content_type": "xml"},
	}
}

// CreateXMLMetadataFromDoc creates metadata from parsed XML document
func (x *XMLContentExtractor) CreateXMLMetadataFromDoc(doc *goquery.Document, text string) ContentMetadata {
	metadata := x.CreateXMLMetadata(text)

	// Try to extract author from common XML elements
	authorSelectors := []string{"author", "creator", "by", "writer"}
	for _, selector := range authorSelectors {
		author := doc.Find(selector).First().Text()
		if author != "" {
			metadata.Author = strings.TrimSpace(author)
			break
		}
	}

	// Try to extract date from common XML elements
	dateSelectors := []string{"date", "created", "published", "modified", "updated"}
	for _, selector := range dateSelectors {
		date := doc.Find(selector).First().Text()
		if date != "" {
			metadata.PublishedDate = strings.TrimSpace(date)
			break
		}
	}

	// Extract description from common elements
	descSelectors := []string{"description", "summary", "abstract", "intro"}
	for _, selector := range descSelectors {
		desc := doc.Find(selector).First().Text()
		if desc != "" {
			metadata.Description = strings.TrimSpace(desc)
			if runeLen(metadata.Description) > 500 {
				metadata.Description = truncateRunes(metadata.Description, 500) + "..."
			}
			break
		}
	}

	return metadata
}

// CreateDescriptionFromText creates a description from XML text
func (x *XMLContentExtractor) CreateDescriptionFromText(text string) string {
	if text == "" {
		return ""
	}

	// Use first 200 characters as description
	if runeLen(text) > 200 {
		return strings.TrimSpace(truncateRunes(text, 200)) + "..."
	}

	return strings.TrimSpace(text)
}

// ExtractKeywordsFromText extracts keywords from XML text
func (x *XMLContentExtractor) ExtractKeywordsFromText(text string) []string {
	if text == "" {
		return []string{}
	}

	// Simple keyword extraction
	words := strings.Fields(strings.ToLower(text))
	wordCount := make(map[string]int)

	for _, word := range words {
		cleaned := regexp.MustCompile(`[^\w]`).ReplaceAllString(word, "")
		if len(cleaned) > 3 {
			wordCount[cleaned]++
		}
	}

	var keywords []string
	for word, count := range wordCount {
		if count >= 2 && len(word) > 4 {
			keywords = append(keywords, word)
		}
	}

	// Limit to top 10
	if len(keywords) > 10 {
		keywords = keywords[:10]
	}

	return keywords
}

// CountWords counts words in XML text
func (x *XMLContentExtractor) CountWords(text string) int {
	if text == "" {
		return 0
	}

	words := strings.Fields(text)
	return len(words)
}

// CountSentences counts sentences in XML text
func (x *XMLContentExtractor) CountSentences(text string) int {
	if text == "" {
		return 0
	}

	sentenceRegex := regexp.MustCompile(`[.!?]+`)
	matches := sentenceRegex.FindAllString(text, -1)
	return len(matches)
}

// CountParagraphs counts paragraphs in XML text
func (x *XMLContentExtractor) CountParagraphs(text string) int {
	if text == "" {
		return 0
	}

	paragraphs := strings.Split(text, "\n\n")
	nonEmptyParagraphs := 0

	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) != "" {
			nonEmptyParagraphs++
		}
	}

	return nonEmptyParagraphs
}

// CalculateQualityScore calculates quality score for XML content
func (x *XMLContentExtractor) CalculateQualityScore(extractedContent *ExtractedContent) float64 {
	if extractedContent == nil || extractedContent.CleanText == "" {
		return 0.0
	}

	var score float64 = 0.0

	// Content length score (0-35 points)
	contentLength := len(extractedContent.CleanText)
	if contentLength >= x.Config.MinTextLength {
		lengthScore := float64(contentLength) / 1500.0 * 35.0
		if lengthScore > 35.0 {
			lengthScore = 35.0
		}
		score += lengthScore
	}

	// Word count score (0-25 points)
	wordCount := x.CountWords(extractedContent.CleanText)
	if wordCount > 15 {
		wordScore := float64(wordCount) / 250.0 * 25.0
		if wordScore > 25.0 {
			wordScore = 25.0
		}
		score += wordScore
	}

	// Structure score (0-25 points)
	sentenceCount := x.CountSentences(extractedContent.CleanText)
	paragraphCount := x.CountParagraphs(extractedContent.CleanText)

	if sentenceCount > 2 {
		score += 12.5
	}
	if paragraphCount > 1 {
		score += 12.5
	}

	// Title presence (0-15 points)
	if extractedContent.Title != "" && len(extractedContent.Title) > 5 {
		score += 15.0
	}

	// Normalize to 0-1 range
	maxScore := 100.0
	normalizedScore := score / maxScore
	if normalizedScore > 1.0 {
		normalizedScore = 1.0
	}

	return normalizedScore
}

// Close closes the XML extractor
func (x *XMLContentExtractor) Close() error {
	x.Logger.Info("XML content extractor closed")
	return nil
}
