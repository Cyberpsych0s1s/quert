package extractor

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"
)

// ExtractMetadata extracts metadata from HTML content
func (h *HTMLContentExtractor) ExtractMetadata(content []byte, contentType string) (*ContentMetadata, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(content)))
	if err != nil {
		return nil, err
	}

	cleanText := h.ExtractAllText(doc)
	return h.ExtractMetadataFromDocument(doc, cleanText)
}

// ExtractMetadataFromDocument extracts comprehensive metadata from a goquery document
func (h *HTMLContentExtractor) ExtractMetadataFromDocument(doc *goquery.Document, cleanText string) (*ContentMetadata, error) {
	metadata := &ContentMetadata{
		CustomMetadata: make(map[string]string),
		Keywords:       []string{},
		Tags:           []string{},
		Categories:     []string{},
	}

	// Extract basic content statistics
	metadata.ContentLength = len(cleanText)
	metadata.WordCount = h.CountWords(cleanText)
	metadata.SentenceCount = h.CountSentences(cleanText)
	metadata.ParagraphCount = h.CountParagraphs(doc)

	// Extract language
	metadata.Language = h.ExtractLanguage(doc)

	// Extract author information
	metadata.Author = h.ExtractAuthor(doc)

	// Extract dates
	metadata.PublishedDate = h.ExtractPublishedDate(doc)
	metadata.ModifiedDate = h.ExtractModifiedDate(doc)

	// Extract description
	metadata.Description = h.ExtractDescription(doc)

	// Extract keywords
	metadata.Keywords = h.ExtractKeywords(doc)

	// Extract tags and categories
	metadata.Tags = h.ExtractTags(doc)
	metadata.Categories = h.ExtractCategories(doc)

	// Count links and images (will be filled by main extraction process)
	metadata.LinkCount = doc.Find("a[href]").Length()
	metadata.ImageCount = doc.Find("img").Length()

	// Extract custom metadata
	h.ExtractCustomMetadata(doc, metadata)

	h.Logger.Debug("extracted metadata from document",
		zap.Int("word_count", metadata.WordCount),
		zap.Int("sentence_count", metadata.SentenceCount),
		zap.String("language", metadata.Language),
		zap.String("author", metadata.Author))

	return metadata, nil
}

// ExtractLanguage attempts to extract the document language
func (h *HTMLContentExtractor) ExtractLanguage(doc *goquery.Document) string {
	// Check html lang attribute
	if lang, exists := doc.Find("html").Attr("lang"); exists && lang != "" {
		return strings.ToLower(strings.Split(lang, "-")[0]) // Extract main language code
	}

	// Check meta language
	var language string
	doc.Find("meta[name='language'], meta[http-equiv='content-language']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			language = strings.ToLower(strings.Split(content, "-")[0])
		}
	})
	if language != "" {
		return language
	}

	// Check Open Graph locale
	doc.Find("meta[property='og:locale']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			language = strings.ToLower(strings.Split(content, "_")[0])
		}
	})

	if language != "" {
		return language
	}

	// Default to English if not found
	return "en"
}

// ExtractAuthor attempts to extract author information
func (h *HTMLContentExtractor) ExtractAuthor(doc *goquery.Document) string {
	// Check various author meta tags
	var author string

	// Standard meta author
	doc.Find("meta[name='author']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			author = content
		}
	})
	if author != "" {
		return author
	}

	// Open Graph author
	doc.Find("meta[property='article:author']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			author = content
		}
	})
	if author != "" {
		return author
	}

	// Twitter author
	doc.Find("meta[name='twitter:creator']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			author = content
		}
	})
	if author != "" {
		return author
	}

	// Look for author in content
	doc.Find(".author, .byline, .writer, [class*='author'], [id*='author']").Each(func(i int, s *goquery.Selection) {
		if text := strings.TrimSpace(s.Text()); text != "" && len(text) < 100 {
			author = text
		}
	})

	return author
}

// ExtractPublishedDate attempts to extract the published date
func (h *HTMLContentExtractor) ExtractPublishedDate(doc *goquery.Document) string {
	var publishedDate string

	// Check various date meta tags
	dateSelectors := []string{
		"meta[property='article:published_time']",
		"meta[name='date']",
		"meta[name='publish-date']",
		"meta[name='publication-date']",
		"meta[property='og:article:published_time']",
		"time[datetime]",
		"time[pubdate]",
	}

	for _, selector := range dateSelectors {
		doc.Find(selector).Each(func(i int, s *goquery.Selection) {
			if content, exists := s.Attr("content"); exists && content != "" {
				publishedDate = content
				return
			}
			if datetime, exists := s.Attr("datetime"); exists && datetime != "" {
				publishedDate = datetime
				return
			}
		})
		if publishedDate != "" {
			break
		}
	}

	// Look for date in content elements
	if publishedDate == "" {
		doc.Find(".date, .published, .publish-date, [class*='date'], [class*='published']").Each(func(i int, s *goquery.Selection) {
			if text := strings.TrimSpace(s.Text()); text != "" && len(text) < 50 {
				publishedDate = text
			}
		})
	}

	return publishedDate
}

// ExtractModifiedDate attempts to extract the modified date
func (h *HTMLContentExtractor) ExtractModifiedDate(doc *goquery.Document) string {
	var modifiedDate string

	// Check various modified date meta tags
	dateSelectors := []string{
		"meta[property='article:modified_time']",
		"meta[name='last-modified']",
		"meta[name='modified-date']",
		"meta[property='og:updated_time']",
	}

	for _, selector := range dateSelectors {
		doc.Find(selector).Each(func(i int, s *goquery.Selection) {
			if content, exists := s.Attr("content"); exists && content != "" {
				modifiedDate = content
				return
			}
		})
		if modifiedDate != "" {
			break
		}
	}

	return modifiedDate
}

// ExtractDescription attempts to extract the page description
func (h *HTMLContentExtractor) ExtractDescription(doc *goquery.Document) string {
	var description string

	// Check meta description
	doc.Find("meta[name='description']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			description = content
		}
	})
	if description != "" {
		return description
	}

	// Check Open Graph description
	doc.Find("meta[property='og:description']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			description = content
		}
	})
	if description != "" {
		return description
	}

	// Check Twitter description
	doc.Find("meta[name='twitter:description']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			description = content
		}
	})

	return description
}

// ExtractKeywords attempts to extract keywords from the document
func (h *HTMLContentExtractor) ExtractKeywords(doc *goquery.Document) []string {
	var keywords []string
	seenKeywords := make(map[string]bool)

	// Extract from meta keywords
	doc.Find("meta[name='keywords']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			keywordList := strings.Split(content, ",")
			for _, keyword := range keywordList {
				keyword = strings.TrimSpace(keyword)
				if keyword != "" && !seenKeywords[keyword] {
					keywords = append(keywords, keyword)
					seenKeywords[keyword] = true
				}
			}
		}
	})

	// Extract from article tags
	doc.Find("meta[property='article:tag']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			content = strings.TrimSpace(content)
			if content != "" && !seenKeywords[content] {
				keywords = append(keywords, content)
				seenKeywords[content] = true
			}
		}
	})

	return keywords
}

// ExtractTags attempts to extract tags from the document
func (h *HTMLContentExtractor) ExtractTags(doc *goquery.Document) []string {
	var tags []string
	seenTags := make(map[string]bool)

	// Look for tag-related elements
	doc.Find(".tag, .tags, .label, .category, [class*='tag'], [rel='tag']").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" && len(text) < 50 && !seenTags[text] {
			tags = append(tags, text)
			seenTags[text] = true
		}
	})

	return tags
}

// ExtractCategories attempts to extract categories from the document
func (h *HTMLContentExtractor) ExtractCategories(doc *goquery.Document) []string {
	var categories []string
	seenCategories := make(map[string]bool)

	// Look for category-related elements
	doc.Find(".category, .categories, .section, [class*='category'], [class*='section']").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" && len(text) < 100 && !seenCategories[text] {
			categories = append(categories, text)
			seenCategories[text] = true
		}
	})

	return categories
}

// ExtractCustomMetadata extracts additional custom metadata
func (h *HTMLContentExtractor) ExtractCustomMetadata(doc *goquery.Document, metadata *ContentMetadata) {
	// Extract OpenGraph metadata
	doc.Find("meta[property^='og:']").Each(func(i int, s *goquery.Selection) {
		if property, exists := s.Attr("property"); exists {
			if content, contentExists := s.Attr("content"); contentExists && content != "" {
				key := "og_" + strings.Replace(strings.TrimPrefix(property, "og:"), ":", "_", -1)
				metadata.CustomMetadata[key] = content
			}
		}
	})

	// Extract Twitter Card metadata
	doc.Find("meta[name^='twitter:']").Each(func(i int, s *goquery.Selection) {
		if name, exists := s.Attr("name"); exists {
			if content, contentExists := s.Attr("content"); contentExists && content != "" {
				key := "twitter_" + strings.Replace(strings.TrimPrefix(name, "twitter:"), ":", "_", -1)
				metadata.CustomMetadata[key] = content
			}
		}
	})

	// Extract JSON-LD structured data (basic extraction)
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		jsonLD := strings.TrimSpace(s.Text())
		if jsonLD != "" {
			metadata.CustomMetadata["json_ld_"+strconv.Itoa(i)] = jsonLD
		}
	})
}

// CountWords counts words in the given text
func (h *HTMLContentExtractor) CountWords(text string) int {
	if text == "" {
		return 0
	}

	// Simple word count using regex
	wordRegex := regexp.MustCompile(`\S+`)
	matches := wordRegex.FindAllString(text, -1)
	return len(matches)
}

// CountSentences counts sentences in the given text
func (h *HTMLContentExtractor) CountSentences(text string) int {
	if text == "" {
		return 0
	}

	// Simple sentence count using regex for sentence endings
	sentenceRegex := regexp.MustCompile(`[.!?]+`)
	matches := sentenceRegex.FindAllString(text, -1)
	return len(matches)
}

// CountParagraphs counts paragraphs in the document
func (h *HTMLContentExtractor) CountParagraphs(doc *goquery.Document) int {
	return doc.Find("p").Length()
}

// CalculateQualityScore scores extracted content 0..1 by prose shape rather than
// length. The dominant signal is stopword density (genuine prose is full of
// function words; nav/link-dumps are not), so a terse real post can outscore a
// long link farm — the opposite of the old length-and-link-count heuristic.
func (h *HTMLContentExtractor) CalculateQualityScore(extractedContent *ExtractedContent) float64 {
	if extractedContent == nil {
		return 0.0
	}
	viaReadability := extractedContent.ExtractionMap["main_content_method"] == "readability"
	return scoreContentQuality(
		extractedContent.CleanText,
		viaReadability,
		extractedContent.Metadata.ParagraphCount,
		extractedContent.Title != "",
	)
}

// ExtractText extracts plain text from HTML content
func (h *HTMLContentExtractor) ExtractText(content []byte, contentType string) (string, error) {
	if !strings.Contains(strings.ToLower(contentType), "html") {
		return string(content), nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(content)))
	if err != nil {
		return "", err
	}

	// Extract main content if configured
	if h.Config.ExtractMainContent {
		mainContent, _ := h.ExtractMainContent(doc)
		return h.CleanTextContent(mainContent), nil
	}

	// Otherwise extract all text
	allText := h.ExtractAllText(doc)
	return h.CleanTextContent(allText), nil
}
