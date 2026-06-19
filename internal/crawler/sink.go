package crawler

import (
	"encoding/json"
	"io"
	"time"
)

// Sink consumes crawl results for output. Implementations decide the on-disk or
// over-the-wire format. Write is called once per result; Close flushes and
// releases any resources. A Sink need not be safe for concurrent use — the
// coordinator drives it from a single goroutine.
type Sink interface {
	Write(*CrawlResult) error
	Close() error
}

// OutputRecord is the serialized form of a crawled page, shaped for downstream
// LLM-data pipelines.
type OutputRecord struct {
	URL        string  `json:"url"`
	StatusCode int     `json:"status_code"`
	Title      string  `json:"title,omitempty"`
	Language   string  `json:"language,omitempty"`
	Quality    float64 `json:"quality_score"`
	WordCount  int     `json:"word_count"`
	LinkCount  int     `json:"link_count"`
	Text       string  `json:"text"`
	CrawledAt  string  `json:"crawled_at"`
}

// ResultToRecord converts a crawl result into an output record. It reports
// ok=false for failed crawls or results without extracted content (e.g. pages
// rejected by the quality gate), which callers should not write.
func ResultToRecord(r *CrawlResult) (OutputRecord, bool) {
	if r == nil || r.Error != nil || r.ExtractedContent == nil {
		return OutputRecord{}, false
	}
	c := r.ExtractedContent
	return OutputRecord{
		URL:        r.URL,
		StatusCode: r.StatusCode,
		Title:      c.Title,
		Language:   c.Metadata.Language,
		Quality:    c.QualityScore,
		WordCount:  c.Metadata.WordCount,
		LinkCount:  len(c.Links),
		Text:       c.CleanText,
		CrawledAt:  r.CompletedAt.UTC().Format(time.RFC3339),
	}, true
}

// JSONLSink writes one JSON object per line (JSONL) to an io.Writer. Results
// without extractable content are skipped. It does not close the underlying
// writer — the caller owns its lifecycle.
type JSONLSink struct {
	enc     *json.Encoder
	written int64
}

// NewJSONLSink returns a JSONLSink writing to w.
func NewJSONLSink(w io.Writer) *JSONLSink {
	return &JSONLSink{enc: json.NewEncoder(w)}
}

// Write encodes a result as one JSONL line. Results with no extractable content
// are silently skipped (returns nil).
func (s *JSONLSink) Write(r *CrawlResult) error {
	rec, ok := ResultToRecord(r)
	if !ok {
		return nil
	}
	if err := s.enc.Encode(rec); err != nil {
		return err
	}
	s.written++
	return nil
}

// Written reports how many records have been written so far.
func (s *JSONLSink) Written() int64 { return s.written }

// Close is a no-op; json.Encoder writes through to the underlying writer with no
// internal buffering, and the writer's lifecycle belongs to the caller.
func (s *JSONLSink) Close() error { return nil }
