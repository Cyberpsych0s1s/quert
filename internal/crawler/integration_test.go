package crawler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/config"
	"go.uber.org/zap"
)

// TestCrawlerWithLocalServer tests the crawler against a local Flask server
// Run this test after starting your Flask server on localhost
func TestCrawlerWithLocalServer(t *testing.T) {
	// Configuration for testing against local server
	serverPort := "5000" // Default Flask port, change if needed
	baseURL := fmt.Sprintf("http://localhost:%s", serverPort)

	// Check if server is running before starting test
	if !isServerRunning(baseURL) {
		t.Skip("Local Flask server not running on " + baseURL + ". Start your Flask server and run the test again.")
	}

	// Create test configurations
	crawlerConfig := &config.CrawlerConfig{
		MaxPages:          10,
		MaxDepth:          2,
		ConcurrentWorkers: 2,
		RequestTimeout:    10 * time.Second,
		UserAgent:         "Quert-Test-Crawler/1.0",
		SeedURLs:          []string{baseURL, baseURL + "/about", baseURL + "/contact"},
	}

	httpConfig := &config.HTTPConfig{
		MaxIdleConnections:        50,
		MaxIdleConnectionsPerHost: 10,
		IdleConnectionTimeout:     30 * time.Second,
		DisableKeepAlives:         false,
		Timeout:                   10 * time.Second,
		DialTimeout:               5 * time.Second,
		TlsHandshakeTimeout:       5 * time.Second,
		ResponseHeaderTimeout:     5 * time.Second,
		DisableCompression:        false,
		AcceptEncoding:            "gzip, deflate",
	}

	robotsConfig := &config.RobotsConfig{
		Enabled:            true,
		CacheDuration:      5 * time.Minute,
		UserAgent:          "Quert-Test-Crawler/1.0",
		CrawlDelayOverride: false,
		RespectCrawlDelay:  true,
	}

	logger, _ := zap.NewDevelopment()

	engine := NewCrawlerEngine(crawlerConfig, httpConfig, robotsConfig, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("Failed to start crawler engine: %v", err)
	}

	results := make([]*CrawlResult, 0)
	var wg sync.WaitGroup

	// Start result collection goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for result := range engine.GetResults() {
			results = append(results, result)
			t.Logf("Crawled: %s - Status: %d - Success: %t - Text Length: %d - Links: %d",
				result.URL,
				result.StatusCode,
				result.Success,
				len(result.ExtractedText),
				len(result.Links))

			if result.Error != nil {
				t.Logf("  Error: %v", result.Error)
			}
		}
	}()

	// Submit crawl jobs
	for _, seedURL := range crawlerConfig.SeedURLs {
		job := &CrawlJob{
			URL:       seedURL,
			Priority:  1,
			Depth:     0,
			Context:   ctx,
			Headers:   make(map[string]string),
			RequestID: fmt.Sprintf("test-%d", time.Now().UnixNano()),
		}

		if err := engine.SubmitJob(job); err != nil {
			t.Logf("Failed to submit job for %s: %v", seedURL, err)
		} else {
			t.Logf("Successfully submitted job for: %s", seedURL)
		}
	}

	time.Sleep(5 * time.Second)

	// Stop the engine. This closes the Results channel.
	if err := engine.Stop(); err != nil {
		t.Errorf("Error stopping engine: %v", err)
	}

	wg.Wait()

	// Print final metrics
	metrics := engine.GetMetrics()
	if metrics != nil {
		t.Logf("Final Metrics:")
		t.Logf("  Total Jobs: %d", metrics.TotalJobs)
		t.Logf("  Successful Jobs: %d", metrics.SuccessfulJobs)
		t.Logf("  Failed Jobs: %d", metrics.FailedJobs)
		t.Logf("  Jobs Per Second: %.2f", metrics.JobsPerSecond)
		t.Logf("  Average Latency: %v", metrics.AverageLatency)
		t.Logf("  Error Rate: %.2f%%", metrics.ErrorRate)
		t.Logf("  Uptime: %v", metrics.Uptime)
	}

	// Print worker statistics
	workerStats := engine.GetWorkerStats()
	for workerID, stats := range workerStats {
		t.Logf("Worker %d Stats:", workerID)
		t.Logf("  Jobs Processed: %d", stats.JobsProcessed)
		t.Logf("  Jobs Successful: %d", stats.JobsSuccessful)
		t.Logf("  Jobs Failed: %d", stats.JobsFailed)
		t.Logf("  Average Time: %v", stats.AverageTime)
	}

	// Analyze results
	t.Logf("Total results collected: %d", len(results))

	successCount := 0
	failureCount := 0
	for _, result := range results {
		if result.Success {
			successCount++
		} else {
			failureCount++
		}
	}

	t.Logf("Successful crawls: %d", successCount)
	t.Logf("Failed crawls: %d", failureCount)

	// Basic assertions
	if len(results) == 0 {
		t.Error("No results were collected - crawler may not be working properly")
	}

	if metrics != nil && metrics.TotalJobs == 0 {
		t.Error("No jobs were processed")
	}

	// Test should have at least some successful results
	if successCount == 0 && failureCount > 0 {
		t.Error("All crawl attempts failed - check your Flask server and robots.txt")
	}
}

// TestCrawlerWithSpecificPort allows testing with a custom port
func TestCrawlerWithSpecificPort(t *testing.T) {
	// You can set this to whatever port your Flask server is running on
	testPort := "8080" // Change this to match your Flask server port
	baseURL := fmt.Sprintf("http://localhost:%s", testPort)

	if !isServerRunning(baseURL) {
		t.Skipf("Local server not running on %s. Start your Flask server on port %s and run the test again.", baseURL, testPort)
	}

	// Simple test configuration
	crawlerConfig := &config.CrawlerConfig{
		MaxPages:          5,
		MaxDepth:          1,
		ConcurrentWorkers: 1,
		RequestTimeout:    15 * time.Second,
		UserAgent:         "Quert-Test-Crawler/1.0",
	}

	httpConfig := &config.HTTPConfig{
		MaxIdleConnections:    10,
		Timeout:               15 * time.Second,
		DialTimeout:           5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}

	logger, _ := zap.NewDevelopment()
	engine := NewCrawlerEngine(crawlerConfig, httpConfig, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Start engine
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("Failed to start engine: %v", err)
	}
	defer engine.Stop()

	// Submit a single job
	job := &CrawlJob{
		URL:       baseURL,
		Priority:  1,
		Depth:     0,
		Context:   ctx,
		RequestID: "test-single",
	}

	if err := engine.SubmitJob(job); err != nil {
		t.Fatalf("Failed to submit job: %v", err)
	}

	// Collect and analyze results
	var result *CrawlResult
	select {
	case result = <-engine.GetResults():
		t.Logf("Successfully crawled %s", result.URL)
		t.Logf("Status Code: %d", result.StatusCode)
		t.Logf("Content Type: %s", result.ContentType)
		t.Logf("Response Time: %v", result.ResponseTime)
		t.Logf("Extracted Text Length: %d", len(result.ExtractedText))
		t.Logf("Links Found: %d", len(result.Links))

		if result.Success {
			t.Logf("✅ Crawl successful!")
			if len(result.ExtractedText) > 0 {
				t.Logf("Sample text: %.200s...", result.ExtractedText)
			}
			if len(result.Links) > 0 {
				t.Logf("Found links: %v", result.Links[:min(len(result.Links), 5)])
			}
		} else {
			t.Logf("❌ Crawl failed: %v", result.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for crawl result")
	}
}

// Helper function to check if server is running
func isServerRunning(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500 // Accept any non-server-error status
}
