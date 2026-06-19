package extractor

import (
	"regexp"
	"strings"
	"time"
)

// ExtractContent extracts content from plain text
func (p *PlainTextExtractor) ExtractContent(content []byte, contentType string, sourceURL string) (*ExtractedContent, error) {
	text := string(content)
	cleanText := p.CleanTextContent(text)

	extractedContent := &ExtractedContent{
		Title:         p.ExtractTitleFromText(cleanText),
		MainContent:   cleanText,
		CleanText:     cleanText,
		Links:         []ExtractedLink{},  // No links in plain text
		Images:        []ExtractedImage{}, // No images in plain text
		Metadata:      p.CreatePlainTextMetadata(cleanText),
		QualityScore:  p.CalculateQualityScore(&ExtractedContent{CleanText: cleanText}),
		ProcessedAt:   time.Now(),
		ExtractionMap: map[string]string{"extraction_method": "plain_text"},
	}

	return extractedContent, nil
}

// ExtractText extracts and cleans plain text content
func (p *PlainTextExtractor) ExtractText(content []byte, contentType string) (string, error) {
	text := string(content)
	return p.CleanTextContent(text), nil
}

// ExtractLinks returns empty slice for plain text (no links)
func (p *PlainTextExtractor) ExtractLinks(content []byte, baseURL string) ([]ExtractedLink, error) {
	return []ExtractedLink{}, nil
}

// ExtractImages returns empty slice for plain text (no images)
func (p *PlainTextExtractor) ExtractImages(content []byte, baseURL string) ([]ExtractedImage, error) {
	return []ExtractedImage{}, nil
}

// ExtractMetadata extracts metadata from plain text content
func (p *PlainTextExtractor) ExtractMetadata(content []byte, contentType string) (*ContentMetadata, error) {
	text := string(content)
	cleanText := p.CleanTextContent(text)
	metadata := p.CreatePlainTextMetadata(cleanText)
	return &metadata, nil
}

// ExtractTitleFromText attempts to extract a title from plain text
func (p *PlainTextExtractor) ExtractTitleFromText(text string) string {
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Use first non-empty line that looks like a title
		if len(line) > 5 && len(line) < 200 {
			// Check if it looks like a title (not too many special chars)
			specialCharCount := 0
			for _, char := range line {
				if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
					(char >= '0' && char <= '9') || char == ' ' || char == '-' || char == ':') {
					specialCharCount++
				}
			}

			// If less than 10% special characters, consider it a potential title
			if float64(specialCharCount)/float64(len(line)) < 0.1 {
				return line
			}
		}
	}

	// If no good title found, use first 50 characters
	if runeLen(text) > 50 {
		return strings.TrimSpace(truncateRunes(text, 50)) + "..."
	}

	return strings.TrimSpace(text)
}

// CleanTextContent cleans plain text content
func (p *PlainTextExtractor) CleanTextContent(text string) string {
	if text == "" {
		return ""
	}

	cleaned := text

	// Normalize whitespace if configured
	if p.Config.NormalizeWhitespace {
		// Replace multiple whitespace characters with single spaces
		whitespaceRegex := regexp.MustCompile(`\s+`)
		cleaned = whitespaceRegex.ReplaceAllString(cleaned, " ")

		// Remove leading/trailing whitespace
		cleaned = strings.TrimSpace(cleaned)
	}

	// Apply length constraints
	if p.Config.MaxTextLength > 0 {
		cleaned = truncateRunes(cleaned, p.Config.MaxTextLength)
	}

	return cleaned
}

// CreatePlainTextMetadata creates metadata for plain text content
func (p *PlainTextExtractor) CreatePlainTextMetadata(text string) ContentMetadata {
	return ContentMetadata{
		Language:       "en", // Default to English for plain text
		Author:         "",   // No author info in plain text
		PublishedDate:  "",   // No date info in plain text
		ModifiedDate:   "",   // No date info in plain text
		Description:    p.CreateDescriptionFromText(text),
		Keywords:       p.ExtractKeywordsFromText(text),
		ContentLength:  len(text),
		WordCount:      p.CountWords(text),
		SentenceCount:  p.CountSentences(text),
		ParagraphCount: p.CountParagraphs(text),
		LinkCount:      0, // No links in plain text
		ImageCount:     0, // No images in plain text
		Tags:           []string{},
		Categories:     []string{},
		CustomMetadata: map[string]string{"content_type": "plain_text"},
	}
}

// CreateDescriptionFromText creates a description from the text content
func (p *PlainTextExtractor) CreateDescriptionFromText(text string) string {
	if text == "" {
		return ""
	}

	// Use first 200 characters as description
	if runeLen(text) > 200 {
		// Find the last complete sentence within 200 characters
		excerpt := truncateRunes(text, 200)
		lastSentence := strings.LastIndexAny(excerpt, ".!?")
		if lastSentence > 50 { // Ensure we have at least 50 chars
			return strings.TrimSpace(excerpt[:lastSentence+1])
		}
		return strings.TrimSpace(excerpt) + "..."
	}

	return strings.TrimSpace(text)
}

// ExtractKeywordsFromText extracts simple keywords from text
func (p *PlainTextExtractor) ExtractKeywordsFromText(text string) []string {
	if text == "" {
		return []string{}
	}

	// Simple keyword extraction: find words that appear multiple times
	words := strings.Fields(strings.ToLower(text))
	wordCount := make(map[string]int)

	for _, word := range words {
		// Clean word of punctuation
		cleaned := regexp.MustCompile(`[^\w]`).ReplaceAllString(word, "")
		if len(cleaned) > 3 { // Only consider words longer than 3 chars
			wordCount[cleaned]++
		}
	}

	var keywords []string
	for word, count := range wordCount {
		if count >= 3 && len(word) > 4 { // Words that appear 3+ times and are 5+ chars
			keywords = append(keywords, word)
		}
	}

	// Limit to top 10 keywords
	if len(keywords) > 10 {
		keywords = keywords[:10]
	}

	return keywords
}

// CountWords counts words in plain text
func (p *PlainTextExtractor) CountWords(text string) int {
	if text == "" {
		return 0
	}

	words := strings.Fields(text)
	return len(words)
}

// CountSentences counts sentences in plain text
func (p *PlainTextExtractor) CountSentences(text string) int {
	if text == "" {
		return 0
	}

	// Count sentence endings
	sentenceRegex := regexp.MustCompile(`[.!?]+`)
	matches := sentenceRegex.FindAllString(text, -1)
	return len(matches)
}

// CountParagraphs counts paragraphs in plain text
func (p *PlainTextExtractor) CountParagraphs(text string) int {
	if text == "" {
		return 0
	}

	// Count paragraphs separated by double newlines
	paragraphs := strings.Split(text, "\n\n")
	nonEmptyParagraphs := 0

	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) != "" {
			nonEmptyParagraphs++
		}
	}

	return nonEmptyParagraphs
}

// CalculateQualityScore calculates quality score for plain text
func (p *PlainTextExtractor) CalculateQualityScore(extractedContent *ExtractedContent) float64 {
	if extractedContent == nil || extractedContent.CleanText == "" {
		return 0.0
	}

	var score float64 = 0.0

	// Content length score (0-40 points)
	contentLength := len(extractedContent.CleanText)
	if contentLength >= p.Config.MinTextLength {
		lengthScore := float64(contentLength) / 1000.0 * 40.0 // Optimal around 1000 chars
		if lengthScore > 40.0 {
			lengthScore = 40.0
		}
		score += lengthScore
	}

	// Word count score (0-30 points)
	wordCount := p.CountWords(extractedContent.CleanText)
	if wordCount > 20 {
		wordScore := float64(wordCount) / 300.0 * 30.0 // Optimal around 300 words
		if wordScore > 30.0 {
			wordScore = 30.0
		}
		score += wordScore
	}

	// Structure score (0-20 points)
	sentenceCount := p.CountSentences(extractedContent.CleanText)
	paragraphCount := p.CountParagraphs(extractedContent.CleanText)

	if sentenceCount > 3 {
		score += 10.0
	}
	if paragraphCount > 1 {
		score += 10.0
	}

	// Text coherence score (0-10 points)
	if wordCount > 0 && sentenceCount > 0 {
		avgWordsPerSentence := float64(wordCount) / float64(sentenceCount)
		if avgWordsPerSentence >= 8 && avgWordsPerSentence <= 25 { // Reasonable range
			score += 10.0
		}
	}

	// Normalize to 0-1 range
	maxScore := 100.0
	normalizedScore := score / maxScore
	if normalizedScore > 1.0 {
		normalizedScore = 1.0
	}

	return normalizedScore
}

// Close closes the plain text extractor
func (p *PlainTextExtractor) Close() error {
	p.Logger.Info("plain text extractor closed")
	return nil
}
