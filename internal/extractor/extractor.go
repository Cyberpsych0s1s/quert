// Package extractor provides content extraction and processing functionality
// for web crawling with support for HTML, plain text, and XML content types.
// Uses goquery for robust HTML parsing and content extraction.
package extractor

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"
)

var whitespaceRegex = regexp.MustCompile(`\s+`)

// ExtractedContent represents the result of content extraction
type ExtractedContent struct {
	Title         string            `json:"title"`
	MainContent   string            `json:"main_content"`
	CleanText     string            `json:"clean_text"`
	Links         []ExtractedLink   `json:"links"`
	Images        []ExtractedImage  `json:"images"`
	Metadata      ContentMetadata   `json:"metadata"`
	QualityScore  float64           `json:"quality_score"`
	ProcessedAt   time.Time         `json:"processed_at"`
	ExtractionMap map[string]string `json:"extraction_map"`
}

// ExtractedLink represents a link found in the content
type ExtractedLink struct {
	URL      string `json:"url"`
	Text     string `json:"text"`
	Title    string `json:"title"`
	Rel      string `json:"rel"`
	Internal bool   `json:"internal"`
}

// ExtractedImage represents an image found in the content
type ExtractedImage struct {
	URL     string `json:"url"`
	Alt     string `json:"alt"`
	Title   string `json:"title"`
	Width   string `json:"width"`
	Height  string `json:"height"`
	Caption string `json:"caption"`
}

// ContentMetadata holds metadata about the extracted content
type ContentMetadata struct {
	Language       string            `json:"language"`
	Author         string            `json:"author"`
	PublishedDate  string            `json:"published_date"`
	ModifiedDate   string            `json:"modified_date"`
	Description    string            `json:"description"`
	Keywords       []string          `json:"keywords"`
	ContentLength  int               `json:"content_length"`
	WordCount      int               `json:"word_count"`
	SentenceCount  int               `json:"sentence_count"`
	ParagraphCount int               `json:"paragraph_count"`
	LinkCount      int               `json:"link_count"`
	ImageCount     int               `json:"image_count"`
	Tags           []string          `json:"tags"`
	Categories     []string          `json:"categories"`
	CustomMetadata map[string]string `json:"custom_metadata"`
}

// ContentExtractor interface defines content extraction methods
type ContentExtractor interface {
	ExtractContent(content []byte, contentType string, sourceURL string) (*ExtractedContent, error)
	ExtractText(content []byte, contentType string) (string, error)
	ExtractLinks(content []byte, baseURL string) ([]ExtractedLink, error)
	ExtractImages(content []byte, baseURL string) ([]ExtractedImage, error)
	ExtractMetadata(content []byte, contentType string) (*ContentMetadata, error)
	CalculateQualityScore(extractedContent *ExtractedContent) float64
	Close() error
}

// HTMLContentExtractor implements content extraction for HTML content using goquery
type HTMLContentExtractor struct {
	Logger           *zap.Logger
	Config           *ExtractorConfig
	BoilerplateRules []BoilerplateRule
}

// PlainTextExtractor implements content extraction for plain text content
type PlainTextExtractor struct {
	Logger *zap.Logger
	Config *ExtractorConfig
}

// XMLContentExtractor implements content extraction for XML content
type XMLContentExtractor struct {
	Logger *zap.Logger
	Config *ExtractorConfig
}

// ExtractorConfig holds configuration for content extractors
type ExtractorConfig struct {
	MinTextLength        int               `mapstructure:"min_text_length" yaml:"min_text_length" json:"min_text_length"`
	MaxTextLength        int               `mapstructure:"max_text_length" yaml:"max_text_length" json:"max_text_length"`
	RemoveBoilerplate    bool              `mapstructure:"remove_boilerplate" yaml:"remove_boilerplate" json:"remove_boilerplate"`
	ExtractMainContent   bool              `mapstructure:"extract_main_content" yaml:"extract_main_content" json:"extract_main_content"`
	PreserveFormatting   bool              `mapstructure:"preserve_formatting" yaml:"preserve_formatting" json:"preserve_formatting"`
	NormalizeWhitespace  bool              `mapstructure:"normalize_whitespace" yaml:"normalize_whitespace" json:"normalize_whitespace"`
	ExtractLinks         bool              `mapstructure:"extract_links" yaml:"extract_links" json:"extract_links"`
	ExtractImages        bool              `mapstructure:"extract_images" yaml:"extract_images" json:"extract_images"`
	ExtractMetadata      bool              `mapstructure:"extract_metadata" yaml:"extract_metadata" json:"extract_metadata"`
	CalculateQuality     bool              `mapstructure:"calculate_quality" yaml:"calculate_quality" json:"calculate_quality"`
	QualityThreshold     float64           `mapstructure:"quality_threshold" yaml:"quality_threshold" json:"quality_threshold"`
	ContentSelectors     []ContentSelector `mapstructure:"content_selectors" yaml:"content_selectors" json:"content_selectors"`
	BoilerplateSelectors []string          `mapstructure:"boilerplate_selectors" yaml:"boilerplate_selectors" json:"boilerplate_selectors"`
}

// ContentSelector defines CSS selectors for content extraction
type ContentSelector struct {
	Name     string `mapstructure:"name" yaml:"name" json:"name"`
	Selector string `mapstructure:"selector" yaml:"selector" json:"selector"`
	Priority int    `mapstructure:"priority" yaml:"priority" json:"priority"`
}

// BoilerplateRule defines rules for removing boilerplate content
type BoilerplateRule struct {
	Name         string   `json:"name"`
	Selectors    []string `json:"selectors"`
	TextPatterns []string `json:"text_patterns"`
	MinLength    int      `json:"min_length"`
	MaxLength    int      `json:"max_length"`
}

// NewHTMLContentExtractor creates a new HTML content extractor
func NewHTMLContentExtractor(config *ExtractorConfig, logger *zap.Logger) *HTMLContentExtractor {
	if config == nil {
		config = GetDefaultExtractorConfig()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &HTMLContentExtractor{
		Logger:           logger,
		Config:           config,
		BoilerplateRules: GetDefaultBoilerplateRules(),
	}
}

// NewPlainTextExtractor creates a new plain text content extractor
func NewPlainTextExtractor(config *ExtractorConfig, logger *zap.Logger) *PlainTextExtractor {
	if config == nil {
		config = GetDefaultExtractorConfig()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &PlainTextExtractor{
		Logger: logger,
		Config: config,
	}
}

// NewXMLContentExtractor creates a new XML content extractor
func NewXMLContentExtractor(config *ExtractorConfig, logger *zap.Logger) *XMLContentExtractor {
	if config == nil {
		config = GetDefaultExtractorConfig()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &XMLContentExtractor{
		Logger: logger,
		Config: config,
	}
}

// ExtractContent extracts all content from HTML using goquery
func (h *HTMLContentExtractor) ExtractContent(content []byte, contentType string, sourceURL string) (*ExtractedContent, error) {
	startTime := time.Now()

	if len(content) == 0 {
		return nil, fmt.Errorf("empty content provided")
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Initialize extracted content structure
	extractedContent := &ExtractedContent{
		ProcessedAt:   startTime,
		ExtractionMap: make(map[string]string),
	}

	// Extract title
	extractedContent.Title = h.ExtractTitle(doc)
	extractedContent.ExtractionMap["title_extraction_method"] = "html_title_tag"

	// Extract main content based on configuration
	if h.Config.ExtractMainContent {
		mainContent, method := h.ExtractMainContent(doc)
		extractedContent.MainContent = mainContent
		extractedContent.ExtractionMap["main_content_method"] = method

		// Extract clean text from main content
		extractedContent.CleanText = h.CleanTextContent(mainContent)
	} else {
		// Extract all text if not focusing on main content
		allText := h.ExtractAllText(doc)
		extractedContent.MainContent = allText
		extractedContent.CleanText = h.CleanTextContent(allText)
		extractedContent.ExtractionMap["main_content_method"] = "full_page_text"
	}

	// Extract links if configured
	if h.Config.ExtractLinks {
		links, err := h.ExtractLinksFromDocument(doc, sourceURL)
		if err != nil {
			h.Logger.Warn("failed to extract links", zap.Error(err))
		} else {
			extractedContent.Links = links
		}
	}

	// Extract images if configured
	if h.Config.ExtractImages {
		images, err := h.ExtractImagesFromDocument(doc, sourceURL)
		if err != nil {
			h.Logger.Warn("failed to extract images", zap.Error(err))
		} else {
			extractedContent.Images = images
		}
	}

	// Extract metadata if configured
	if h.Config.ExtractMetadata {
		metadata, err := h.ExtractMetadataFromDocument(doc, extractedContent.CleanText)
		if err != nil {
			h.Logger.Warn("failed to extract metadata", zap.Error(err))
		} else {
			extractedContent.Metadata = *metadata
		}
	}

	// Calculate quality score if configured
	if h.Config.CalculateQuality {
		extractedContent.QualityScore = h.CalculateQualityScore(extractedContent)
	}

	return extractedContent, nil
}

// ExtractTitle extracts the page title from HTML document
func (h *HTMLContentExtractor) ExtractTitle(doc *goquery.Document) string {
	// Try multiple methods for title extraction in order of preference
	var title string

	// Method 1: HTML title tag
	title = doc.Find("title").First().Text()
	if title != "" {
		return strings.TrimSpace(title)
	}

	// Method 2: Open Graph title
	doc.Find("meta[property='og:title']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			title = content
		}
	})
	if title != "" {
		return strings.TrimSpace(title)
	}

	// Method 3: Twitter Card title
	doc.Find("meta[name='twitter:title']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			title = content
		}
	})
	if title != "" {
		return strings.TrimSpace(title)
	}

	// Method 4: First h1 tag
	title = doc.Find("h1").First().Text()
	if title != "" {
		return strings.TrimSpace(title)
	}

	// Method 5: Meta title
	doc.Find("meta[name='title']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists && content != "" {
			title = content
		}
	})

	return strings.TrimSpace(title)
}

// ExtractMainContent extracts the main content from HTML using content selectors
func (h *HTMLContentExtractor) ExtractMainContent(doc *goquery.Document) (string, string) {
	var bestContent string
	var bestMethod string
	var bestScore float64

	// Strip non-content elements first. Without this, inline <style>/<script>
	// inside the matched content subtree (e.g. Wikipedia's <style> blocks inside
	// <main>) leak their CSS/JS source into the extracted text.
	doc.Find("script, style, noscript, template").Remove()

	// Remove boilerplate content next
	if h.Config.RemoveBoilerplate {
		h.RemoveBoilerplateContent(doc)
	}

	// Try configured content selectors first
	for _, selector := range h.Config.ContentSelectors {
		content := doc.Find(selector.Selector).Text()
		if content != "" {
			score := h.ScoreContentByLength(content)
			if score > bestScore {
				bestContent = content
				bestMethod = fmt.Sprintf("selector_%s", selector.Name)
				bestScore = score
			}
		}
	}

	// Try common content selectors if no configured selectors worked
	commonSelectors := []ContentSelector{
		{Name: "main", Selector: "main", Priority: 100},
		{Name: "article", Selector: "article", Priority: 90},
		{Name: "content", Selector: "#content, .content, [role='main']", Priority: 80},
		{Name: "post", Selector: ".post, .entry, .story", Priority: 70},
		{Name: "body_content", Selector: ".body, .text, .description", Priority: 60},
	}

	for _, selector := range commonSelectors {
		selection := doc.Find(selector.Selector)
		if selection.Length() > 0 {
			content := selection.Text()
			if content != "" {
				score := h.ScoreContentByLength(content)
				if score > bestScore {
					bestContent = content
					bestMethod = fmt.Sprintf("common_selector_%s", selector.Name)
					bestScore = score
				}
			}
		}
	}

	// Fallback to body content if no specific content area found
	if bestContent == "" {
		bestContent = doc.Find("body").Text()
		bestMethod = "body_fallback"
	}

	return bestContent, bestMethod
}

// ExtractAllText extracts all text content from the document
func (h *HTMLContentExtractor) ExtractAllText(doc *goquery.Document) string {
	// Remove script and style elements
	doc.Find("script, style, noscript").Remove()

	// Remove boilerplate content if configured
	if h.Config.RemoveBoilerplate {
		h.RemoveBoilerplateContent(doc)
	}

	return doc.Find("body").Text()
}

// RemoveBoilerplateContent removes common boilerplate content from the document
func (h *HTMLContentExtractor) RemoveBoilerplateContent(doc *goquery.Document) {
	// Remove common boilerplate selectors
	boilerplateSelectors := append(h.Config.BoilerplateSelectors,
		"nav", "header", "footer", ".nav", ".navigation", ".menu",
		".sidebar", ".ads", ".advertisement", ".social", ".share",
		".comments", ".related", ".recommended", ".popup", ".modal",
		"[role='navigation']", "[role='banner']", "[role='contentinfo']",
	)

	for _, selector := range boilerplateSelectors {
		doc.Find(selector).Remove()
	}

	// Apply custom boilerplate rules
	for _, rule := range h.BoilerplateRules {
		for _, selector := range rule.Selectors {
			doc.Find(selector).Remove()
		}
	}
}

func (h *HTMLContentExtractor) CleanTextContent(text string) string {
	if text == "" {
		return ""
	}

	text = html.UnescapeString(text)

	if h.Config.NormalizeWhitespace {
		text = whitespaceRegex.ReplaceAllString(text, " ")
		text = strings.TrimSpace(text)
	}

	if h.Config.MaxTextLength > 0 {
		text = truncateRunes(text, h.Config.MaxTextLength)
	}

	return text
}

// truncateRunes returns s limited to at most maxRunes runes without splitting a
// multi-byte UTF-8 rune. maxRunes <= 0 returns s unchanged. Length limits in the
// extractor are character counts, not byte counts; slicing on bytes corrupts the
// trailing rune of multi-byte (non-ASCII) text.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// runeLen reports the number of runes (characters) in s.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// ScoreContentByLength scores content based on length and structure
func (h *HTMLContentExtractor) ScoreContentByLength(content string) float64 {
	contentLen := len(strings.TrimSpace(content))

	if contentLen < h.Config.MinTextLength {
		return 0.0
	}

	score := float64(contentLen)

	// Penalize common non-content keywords (Copyright, Privacy, etc.)
	lower := strings.ToLower(content)
	if strings.Contains(lower, "all rights reserved") ||
	   strings.Contains(lower, "privacy policy") ||
	   strings.Contains(lower, "terms of service") {
		score *= 0.4
	}

	// Punctuation density. Real content has sentences.
	// Nav menus and lists often lack periods/commas.
	punctCount := strings.Count(content, ".") + strings.Count(content, ",") + strings.Count(content, "!")
	if contentLen > 0 {
		density := float64(punctCount) / float64(contentLen)
		if density < 0.005 { // Less than 1 punctuation per 200 chars -> likely not prose
			score *= 0.5
		}
	}

	// Optional range boost (still useful, but probably not the only factor)
	if contentLen >= 500 && contentLen <= 5000 {
		score *= 1.5
	}

	return score
}

// GetDefaultExtractorConfig returns default configuration for content extraction
func GetDefaultExtractorConfig() *ExtractorConfig {
	return &ExtractorConfig{
		MinTextLength:       100,
		MaxTextLength:       100000,
		RemoveBoilerplate:   true,
		ExtractMainContent:  true,
		PreserveFormatting:  false,
		NormalizeWhitespace: true,
		ExtractLinks:        true,
		ExtractImages:       true,
		ExtractMetadata:     true,
		CalculateQuality:    true,
		QualityThreshold:    0.7,
		ContentSelectors: []ContentSelector{
			{Name: "main", Selector: "main", Priority: 100},
			{Name: "article", Selector: "article", Priority: 90},
			{Name: "content", Selector: "#content, .content", Priority: 80},
		},
		BoilerplateSelectors: []string{
			"nav", "header", "footer", ".nav", ".sidebar", ".ads",
		},
	}
}

// GetDefaultBoilerplateRules returns default boilerplate removal rules
func GetDefaultBoilerplateRules() []BoilerplateRule {
	return []BoilerplateRule{
		{
			Name:      "navigation",
			Selectors: []string{"nav", ".nav", ".navigation", ".menu", "[role='navigation']"},
		},
		{
			Name:      "footer",
			Selectors: []string{"footer", ".footer", "[role='contentinfo']"},
		},
		{
			Name:      "ads",
			Selectors: []string{".ads", ".ad", ".advertisement", ".sponsor", ".promo"},
		},
		{
			Name:      "social",
			Selectors: []string{".social", ".share", ".sharing", ".follow"},
		},
	}
}

// Close closes the HTML content extractor and cleans up resources
func (h *HTMLContentExtractor) Close() error {
	h.Logger.Info("HTML content extractor closed")
	return nil
}
