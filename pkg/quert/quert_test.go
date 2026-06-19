package quert_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/config"
	"github.com/cyberpsych0s1s/quert/pkg/quert"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCrawlToJSONL exercises the public batteries-included API end-to-end against
// a real local server (the API builds its own HTTP client, so httpmock can't
// intercept it).
func TestCrawlToJSONL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound) // permissive
	})
	rich := "<html><head><title>Page</title></head><body><main><p>" +
		strings.Repeat("Meaningful article content for extraction. ", 30) +
		`</p><a href="/a">a</a></main></body></html>`
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(rich))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(rich))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg, err := config.LoadConfig("", nil)
	require.NoError(t, err)
	cfg.Crawler.MaxPages = 5
	cfg.Crawler.MaxDepth = 2
	cfg.Content.QualityThreshold = 0.001               // accept ~everything for the test
	cfg.Crawler.AllowedDomains = []string{"127.0.0.1"} // override the default loopback block

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stats, err := quert.CrawlToJSONL(ctx, cfg, []string{srv.URL + "/"}, &buf, nil)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, stats.Received, 1, "crawled at least the seed")
	assert.Contains(t, buf.String(), srv.URL, "JSONL output contains crawled URLs")
}
