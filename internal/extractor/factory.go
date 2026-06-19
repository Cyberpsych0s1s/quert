package extractor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/net/html/charset"
)

// ErrQualityBelowThreshold is returned by ExtractContent when extraction
// succeeded but the content's quality score is below the configured threshold.
// Callers can use errors.Is to distinguish a quality rejection (the page parsed
// fine; links are still usable for discovery) from a genuine extraction failure.
var ErrQualityBelowThreshold = errors.New("content quality below threshold")

// decodeToUTF8 converts content to UTF-8 using the charset declared in the
// Content-Type header, a byte-order mark, or (for HTML/XML) a <meta charset>
// sniff of the leading bytes. Non-UTF-8 pages would otherwise be parsed as
// mojibake. It fails open: on any decode error the original bytes are returned.
func decodeToUTF8(content []byte, contentType string) []byte {
	reader, err := charset.NewReader(bytes.NewReader(content), contentType)
	if err != nil {
		return content
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return content
	}
	return decoded
}

// ExtractorFactory creates appropriate content extractors based on content type
type ExtractorFactory struct {
	Config *ExtractorConfig
	Logger *zap.Logger
}

// NewExtractorFactory creates a new extractor factory
func NewExtractorFactory(config *ExtractorConfig, logger *zap.Logger) *ExtractorFactory {
	if config == nil {
		config = GetDefaultExtractorConfig()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &ExtractorFactory{
		Config: config,
		Logger: logger,
	}
}

// CreateExtractor creates the appropriate content extractor based on content type
func (f *ExtractorFactory) CreateExtractor(contentType string) ContentExtractor {
	contentTypeLower := strings.ToLower(contentType)

	switch {
	case strings.Contains(contentTypeLower, "text/html"):
		return NewHTMLContentExtractor(f.Config, f.Logger)
	case strings.Contains(contentTypeLower, "application/xhtml+xml"):
		return NewHTMLContentExtractor(f.Config, f.Logger)
	case strings.Contains(contentTypeLower, "text/plain"):
		return NewPlainTextExtractor(f.Config, f.Logger)
	case strings.Contains(contentTypeLower, "application/xml"):
		return NewXMLContentExtractor(f.Config, f.Logger)
	case strings.Contains(contentTypeLower, "text/xml"):
		return NewXMLContentExtractor(f.Config, f.Logger)
	case strings.Contains(contentTypeLower, "application/rss+xml"):
		return NewXMLContentExtractor(f.Config, f.Logger)
	case strings.Contains(contentTypeLower, "application/atom+xml"):
		return NewXMLContentExtractor(f.Config, f.Logger)
	default:
		// Default to HTML extractor for unknown types
		f.Logger.Debug("unknown content type, defaulting to HTML extractor",
			zap.String("content_type", contentType))
		return NewHTMLContentExtractor(f.Config, f.Logger)
	}
}

// ExtractContent is a convenience method that creates an extractor and extracts content
func (f *ExtractorFactory) ExtractContent(content []byte, contentType string, sourceURL string) (*ExtractedContent, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("empty content provided")
	}

	content = decodeToUTF8(content, contentType)

	extractor := f.CreateExtractor(contentType)
	defer extractor.Close()

	extractedContent, err := extractor.ExtractContent(content, contentType, sourceURL)
	if err != nil {
		f.Logger.Error("content extraction failed",
			zap.String("content_type", contentType),
			zap.String("source_url", sourceURL),
			zap.Error(err))
		return nil, fmt.Errorf("content extraction failed: %w", err)
	}

	// Apply quality filtering if configured
	if f.Config.CalculateQuality && extractedContent.QualityScore < f.Config.QualityThreshold {
		f.Logger.Debug("content quality below threshold",
			zap.Float64("quality_score", extractedContent.QualityScore),
			zap.Float64("threshold", f.Config.QualityThreshold),
			zap.String("source_url", sourceURL))
		return nil, fmt.Errorf("%w: score %.2f < threshold %.2f",
			ErrQualityBelowThreshold, extractedContent.QualityScore, f.Config.QualityThreshold)
	}

	f.Logger.Debug("content extracted successfully",
		zap.String("content_type", contentType),
		zap.String("source_url", sourceURL),
		zap.Int("content_length", len(extractedContent.CleanText)),
		zap.Int("word_count", extractedContent.Metadata.WordCount),
		zap.Float64("quality_score", extractedContent.QualityScore))

	return extractedContent, nil
}

// ExtractText is a convenience method for extracting just the text content
func (f *ExtractorFactory) ExtractText(content []byte, contentType string) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("empty content provided")
	}

	content = decodeToUTF8(content, contentType)

	extractor := f.CreateExtractor(contentType)
	defer extractor.Close()

	text, err := extractor.ExtractText(content, contentType)
	if err != nil {
		return "", fmt.Errorf("text extraction failed: %w", err)
	}

	return text, nil
}

// ExtractLinks is a convenience method for extracting just the links
func (f *ExtractorFactory) ExtractLinks(content []byte, contentType string, baseURL string) ([]ExtractedLink, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("empty content provided")
	}

	content = decodeToUTF8(content, contentType)

	extractor := f.CreateExtractor(contentType)
	defer extractor.Close()

	links, err := extractor.ExtractLinks(content, baseURL)
	if err != nil {
		return nil, fmt.Errorf("link extraction failed: %w", err)
	}

	return links, nil
}

// BatchExtractContent processes multiple content items concurrently
func (f *ExtractorFactory) BatchExtractContent(requests []ContentExtractionRequest, workers int) []ContentExtractionResult {
	if workers <= 0 {
		workers = 5 // Default worker count
	}

	jobs := make(chan ContentExtractionRequest, len(requests))
	results := make(chan ContentExtractionResult, len(requests))

	// Start workers
	for i := 0; i < workers; i++ {
		go f.extractionWorker(jobs, results)
	}

	// Send jobs
	for _, request := range requests {
		jobs <- request
	}
	close(jobs)

	// Collect results
	var extractionResults []ContentExtractionResult
	for i := 0; i < len(requests); i++ {
		result := <-results
		extractionResults = append(extractionResults, result)
	}

	return extractionResults
}

// extractionWorker processes extraction jobs
func (f *ExtractorFactory) extractionWorker(jobs <-chan ContentExtractionRequest, results chan<- ContentExtractionResult) {
	for request := range jobs {
		extractedContent, err := f.ExtractContent(request.Content, request.ContentType, request.SourceURL)

		result := ContentExtractionResult{
			Request:          request,
			ExtractedContent: extractedContent,
			Error:            err,
		}

		results <- result
	}
}

// ContentExtractionRequest represents a request for content extraction
type ContentExtractionRequest struct {
	Content     []byte
	ContentType string
	SourceURL   string
	RequestID   string
}

// ContentExtractionResult represents the result of content extraction
type ContentExtractionResult struct {
	Request          ContentExtractionRequest
	ExtractedContent *ExtractedContent
	Error            error
}

// GetSupportedContentTypes returns a list of supported content types
func (f *ExtractorFactory) GetSupportedContentTypes() []string {
	return []string{
		"text/html",
		"application/xhtml+xml",
		"text/plain",
		"application/xml",
		"text/xml",
		"application/rss+xml",
		"application/atom+xml",
	}
}

// IsContentTypeSupported checks if a content type is supported
func (f *ExtractorFactory) IsContentTypeSupported(contentType string) bool {
	supportedTypes := f.GetSupportedContentTypes()
	contentTypeLower := strings.ToLower(contentType)

	for _, supportedType := range supportedTypes {
		if strings.Contains(contentTypeLower, supportedType) {
			return true
		}
	}

	return false
}

// Close closes the factory and cleans up resources
func (f *ExtractorFactory) Close() error {
	f.Logger.Info("extractor factory closed")
	return nil
}
