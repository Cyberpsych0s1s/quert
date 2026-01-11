package crawler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Almahr1/quert/internal/client"
	"github.com/Almahr1/quert/internal/config"
	"github.com/Almahr1/quert/internal/extractor"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func setupTestEngine() *CrawlerEngine {
	logger := zap.NewNop()
	cfg := &config.CrawlerConfig{
		ConcurrentWorkers: 2,
		RequestTimeout:    time.Second,
		UserAgent:         "TestBot",
	}
	httpCfg := client.DefaultHTTPConfig()
	robotsCfg := &config.RobotsConfig{Enabled: false} // Disable robots for basic worker tests

	engine := NewCrawlerEngine(cfg, httpCfg, robotsCfg, logger)
	extractCfg := extractor.GetDefaultExtractorConfig()
	extractCfg.QualityThreshold = 0.0 // Accept everything
	engine.ExtractorFactory = extractor.NewExtractorFactory(extractCfg, logger)

	return engine
}

func TestCrawlerEngine_StartStop(t *testing.T) {
	engine := setupTestEngine()
	ctx := context.Background()

	// Start
	err := engine.Start(ctx)
	assert.NoError(t, err)
	assert.True(t, engine.IsRunning())

	// Stop
	err = engine.Stop()
	assert.NoError(t, err)
	assert.False(t, engine.IsRunning())
}

func TestCrawlerEngine_SubmitJob(t *testing.T) {
	engine := setupTestEngine()
	ctx := context.Background()
	engine.Start(ctx)
	defer engine.Stop()

	// Mock robots.txt to be allowed (since engine checks it on submit)
	httpmock.ActivateNonDefault(engine.HTTPClient.Client)
	defer httpmock.DeactivateAndReset()
	httpmock.RegisterResponder("GET", "http://example.com/robots.txt", httpmock.NewStringResponder(404, ""))

	job := &CrawlJob{
		URL: "http://example.com/page",
	}

	err := engine.SubmitJob(job)
	assert.NoError(t, err)

	metrics := engine.GetMetrics()

	assert.Equal(t, int64(1), metrics.TotalJobs)
}

func TestCrawlerEngine_WorkerProcessing(t *testing.T) {
	engine := setupTestEngine()
	ctx := context.Background()

	httpmock.ActivateNonDefault(engine.HTTPClient.Client)
	defer httpmock.DeactivateAndReset()

	// Mock robots (404 = allow)
	httpmock.RegisterResponder("GET", "http://example.com/robots.txt", httpmock.NewStringResponder(404, ""))

	// Mock Page Content WITH Content-Type header
	httpmock.RegisterResponder("GET", "http://example.com/page",
		func(req *http.Request) (*http.Response, error) {
			resp := httpmock.NewStringResponse(200, `<html><body><h1>Hello World</h1><a href="/link">Link</a></body></html>`)
			resp.Header.Set("Content-Type", "text/html")
			return resp, nil
		},
	)

	engine.Start(ctx)

	// Submit Job
	job := &CrawlJob{URL: "http://example.com/page"}
	err := engine.SubmitJob(job)
	assert.NoError(t, err)

	// Wait for result (poll results channel)
	select {
	case result := <-engine.GetResults():
		assert.True(t, result.Success)
		assert.Equal(t, "http://example.com/page", result.URL)
		assert.Equal(t, 200, result.StatusCode)
		assert.Contains(t, result.ExtractedText, "Hello World")
		assert.Equal(t, 1, len(result.Links))
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for crawl result")
	}

	engine.Stop()
}

func TestCrawlerEngine_RateLimiter(t *testing.T) {
	engine := setupTestEngine()
	limiter := engine.GetRateLimiter("example.com")
	assert.NotNil(t, limiter)

	// Check burst settings from config
	assert.Equal(t, float64(5), float64(limiter.Burst())) // Default burst
}

func TestCrawlerEngine_ContextCancellation(t *testing.T) {
	engine := setupTestEngine()
	ctx, cancel := context.WithCancel(context.Background())

	engine.Start(ctx)
	assert.True(t, engine.IsRunning())

	cancel() // Cancel context

	// Engine should eventually stop or handle cancellation gracefully
	// We verify manually calling Stop still works
	err := engine.Stop()
	assert.NoError(t, err)
	assert.False(t, engine.IsRunning())
}
