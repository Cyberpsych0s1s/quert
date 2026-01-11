package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Almahr1/quert/internal/client"
	"github.com/Almahr1/quert/internal/config"
	"github.com/Almahr1/quert/internal/crawler"
	"github.com/Almahr1/quert/internal/extractor"
	"github.com/Almahr1/quert/internal/frontier"
	"github.com/Almahr1/quert/internal/robots"
	"go.uber.org/zap"
)

// CrawlerDemo demonstrates comprehensive crawler usage
type CrawlerDemo struct {
	logger           *zap.Logger
	engine           *crawler.CrawlerEngine
	urlProcessor     *frontier.URLProcessor
	robotsParser     *robots.Parser
	httpClient       *client.HTTPClient
	extractorFactory *extractor.ExtractorFactory

	// Statistics
	totalPages   int64
	successCount int64
	failureCount int64
	startTime    time.Time

	// Synchronization
	mu sync.RWMutex
}

func main() {
	// Initialize logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting Quert Comprehensive Crawler Example")

	// Create demo instance
	demo := &CrawlerDemo{
		logger:    logger,
		startTime: time.Now(),
	}

	// Setup crawler components
	if err := demo.setupComponents(); err != nil {
		logger.Fatal("Failed to setup crawler components", zap.Error(err))
	}

	// Setup shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start crawler in background
	go func() {
		if err := demo.runCrawler(ctx); err != nil {
			logger.Error("Crawler execution failed", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	logger.Info("Shutdown signal received, stopping crawler...")
	cancel()

	// Shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := demo.shutdown(shutdownCtx); err != nil {
		logger.Error("Shutdown failed", zap.Error(err))
	}

	demo.printFinalStats()
	logger.Info("Crawler example completed successfully")
}

// setupComponents initializes all crawler components
func (d *CrawlerDemo) setupComponents() error {
	// Create crawler configuration
	crawlerConfig := &config.CrawlerConfig{
		MaxPages:          100,
		MaxDepth:          1,
		ConcurrentWorkers: 100,
		RequestTimeout:    30 * time.Second,
		UserAgent:         "Quert-ComprehensiveExample/1.0 (+https://github.com/Almahr1/quert)",
		SeedURLs: []string{
			"https://httpbin.org/html",
			"https://example.com",
			"https://httpbin.org/json",
			"https://httpbin.org/xml",
			"https://reddit.com/r/gaming", // Throw in some spice
		},
		IncludePatterns:  []string{".*"},
		ExcludePatterns:  []string{"\\.pdf$", "\\.jpg$", "\\.png$"},
		AllowedDomains:   []string{},
		BlockedDomains:   []string{"localhost", "127.0.0.1"},
		GlobalRateLimit:  5.0,
		GlobalBurst:      5,
		PerHostRateLimit: 1.0,
		PerHostBurst:     3,
	}

	// HTTP client configuration
	httpConfig := &config.HTTPConfig{
		MaxIdleConnections:        100,
		MaxIdleConnectionsPerHost: 10,
		IdleConnectionTimeout:     60 * time.Second,
		DisableKeepAlives:         false,
		Timeout:                   30 * time.Second,
		DialTimeout:               10 * time.Second,
		TlsHandshakeTimeout:       15 * time.Second,
		ResponseHeaderTimeout:     15 * time.Second,
		DisableCompression:        false,
		AcceptEncoding:            "gzip, deflate",
	}

	// Robots.txt configuration
	robotsConfig := &config.RobotsConfig{
		Enabled:            true,
		CacheDuration:      24 * time.Hour,
		UserAgent:          "*",
		CrawlDelayOverride: false,
		RespectCrawlDelay:  true,
	}

	d.urlProcessor = frontier.NewURLProcessor()

	d.httpClient = client.NewHTTPClient(httpConfig, d.logger)

	// Initialize robots.txt parser
	robotsParserConfig := robots.Config{
		UserAgent:   crawlerConfig.UserAgent,
		CacheTTL:    robotsConfig.CacheDuration,
		HTTPTimeout: crawlerConfig.RequestTimeout,
		MaxSize:     500 * 1024, // 500KB max robots.txt
	}
	d.robotsParser = robots.NewParser(robotsParserConfig, d.httpClient)

	// Initialize content extractor
	extractorConfig := extractor.GetDefaultExtractorConfig()
	extractorConfig.ExtractLinks = true
	extractorConfig.ExtractImages = true
	extractorConfig.ExtractMetadata = true
	extractorConfig.CalculateQuality = true
	extractorConfig.QualityThreshold = 0.5
	d.extractorFactory = extractor.NewExtractorFactory(extractorConfig, d.logger)

	d.engine = crawler.NewCrawlerEngine(crawlerConfig, httpConfig, robotsConfig, d.logger)
	d.engine.SetMetricsCallback(d.metricsCallback)

	d.logger.Info("All components initialized successfully")
	return nil
}

func (d *CrawlerDemo) runCrawler(ctx context.Context) error {
	if err := d.engine.Start(ctx); err != nil {
		return fmt.Errorf("failed to start crawler engine: %w", err)
	}

	go d.processResults(ctx)

	if err := d.submitSeedURLs(ctx); err != nil {
		d.logger.Error("Failed to submit seed URLs", zap.Error(err))
	}

	// Let crawler run for a specific duration or until context cancellation
	select {
	case <-ctx.Done():
		d.logger.Info("Context cancelled, stopping crawler")
	case <-time.After(1 * time.Minute):
		d.logger.Info("Crawling duration completed")
	}

	return nil
}

func (d *CrawlerDemo) submitSeedURLs(ctx context.Context) error {
	// Get crawler config
	config := d.engine.Config

	for i, rawURL := range config.SeedURLs {
		// Process URL through frontier
		urlInfo, err := d.urlProcessor.Process(rawURL)
		if err != nil {
			d.logger.Warn("Failed to process seed URL",
				zap.String("url", rawURL),
				zap.Error(err))
			continue
		}

		// Create crawl job
		job := &crawler.CrawlJob{
			URL:      urlInfo.URL,
			URLInfo:  urlInfo,
			Priority: 1, // High priority for seed URLs
			Depth:    0,
			Headers: map[string]string{
				"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			},
			RequestID:   fmt.Sprintf("seed-%d-%d", i, time.Now().UnixNano()),
			SubmittedAt: time.Now(),
			Context:     ctx,
		}

		if err := d.engine.SubmitJob(job); err != nil {
			d.logger.Error("Failed to submit crawl job",
				zap.String("url", rawURL),
				zap.Error(err))
		} else {
			d.logger.Info("Submitted crawl job",
				zap.String("url", rawURL),
				zap.String("request_id", job.RequestID))
		}
	}

	return nil
}

func (d *CrawlerDemo) processResults(ctx context.Context) {
	results := d.engine.GetResults()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Result processor stopping due to context cancellation")
			return
		case result, ok := <-results:
			if !ok {
				d.logger.Info("Results channel closed, stopping result processor")
				return
			}
			d.handleCrawlResult(result)
		}
	}
}

// handleCrawlResult processes individual crawl results
func (d *CrawlerDemo) handleCrawlResult(result *crawler.CrawlResult) {
	if result == nil {
		return
	}

	d.mu.Lock()
	d.totalPages++
	d.mu.Unlock()

	if result.Success && result.ExtractedContent != nil {
		d.handleSuccessfulResult(result)
	} else {
		d.handleFailedResult(result)
	}
}

func (d *CrawlerDemo) handleSuccessfulResult(result *crawler.CrawlResult) {
	d.mu.Lock()
	d.successCount++
	d.mu.Unlock()

	content := result.ExtractedContent

	// Log extraction results
	d.logger.Info("Successfully crawled and extracted content",
		zap.String("url", result.URL),
		zap.String("title", content.Title),
		zap.String("content_type", result.ContentType),
		zap.Int("status_code", result.StatusCode),
		zap.Duration("response_time", result.ResponseTime),
		zap.Int("content_length", len(result.Body)),
		zap.Int("clean_text_length", len(content.CleanText)),
		zap.Int("word_count", content.Metadata.WordCount),
		zap.Int("sentence_count", content.Metadata.SentenceCount),
		zap.Int("paragraph_count", content.Metadata.ParagraphCount),
		zap.Int("link_count", len(content.Links)),
		zap.Int("image_count", len(content.Images)),
		zap.Float64("quality_score", content.QualityScore),
		zap.String("language", content.Metadata.Language),
		zap.String("author", content.Metadata.Author))

	d.analyzeContent(result)
	d.processExtractedLinks(result)
}

// analyzeContent demonstrates advanced content analysis
func (d *CrawlerDemo) analyzeContent(result *crawler.CrawlResult) {
	content := result.ExtractedContent

	// Content quality analysis
	if content.QualityScore < 0.5 {
		d.logger.Warn("Low quality content detected",
			zap.String("url", result.URL),
			zap.Float64("quality_score", content.QualityScore))
	}

	// Language analysis
	if content.Metadata.Language != "en" {
		d.logger.Info("Non-English content detected",
			zap.String("url", result.URL),
			zap.String("language", content.Metadata.Language))
	}

	// Content structure analysis
	if content.Metadata.WordCount < 50 {
		d.logger.Debug("Short content detected",
			zap.String("url", result.URL),
			zap.Int("word_count", content.Metadata.WordCount))
	}

	// Print content preview
	if len(content.CleanText) > 200 {
		d.logger.Debug("Content preview",
			zap.String("url", result.URL),
			zap.String("preview", content.CleanText[:200]+"..."))
	}

	// Analyze extracted links
	internalLinks := 0
	externalLinks := 0
	for _, link := range content.Links {
		if link.Internal {
			internalLinks++
		} else {
			externalLinks++
		}
	}

	if len(content.Links) > 0 {
		d.logger.Debug("Link analysis",
			zap.String("url", result.URL),
			zap.Int("total_links", len(content.Links)),
			zap.Int("internal_links", internalLinks),
			zap.Int("external_links", externalLinks))
	}
}

func (d *CrawlerDemo) processExtractedLinks(result *crawler.CrawlResult) {
	// This is where you would typically add extracted links back to the frontier
	// for continued crawling. For this demo, we'll just analyze them.

	content := result.ExtractedContent
	if len(content.Links) == 0 {
		return
	}

	d.logger.Debug("Processing extracted links",
		zap.String("source_url", result.URL),
		zap.Int("link_count", len(content.Links)))

	// Example: Process first few links for demonstration
	maxLinksToProcess := 3
	for i, link := range content.Links {
		if i >= maxLinksToProcess {
			break
		}

		if urlInfo, err := d.urlProcessor.Process(link.URL); err == nil {
			d.logger.Debug("Discovered link",
				zap.String("source_url", result.URL),
				zap.String("link_url", urlInfo.URL),
				zap.String("link_text", link.Text),
				zap.Bool("internal", link.Internal),
				zap.String("domain", urlInfo.Domain))
		}
	}
}

func (d *CrawlerDemo) handleFailedResult(result *crawler.CrawlResult) {
	d.mu.Lock()
	d.failureCount++
	d.mu.Unlock()

	d.logger.Error("Crawl job failed",
		zap.String("url", result.URL),
		zap.Int("status_code", result.StatusCode),
		zap.Duration("response_time", result.ResponseTime),
		zap.Bool("retryable", result.Retryable),
		zap.Error(result.Error))
}

// metricsCallback handles periodic metrics reporting
func (d *CrawlerDemo) metricsCallback(metrics *crawler.CrawlerMetrics) {
	d.logger.Info("Crawler metrics update",
		zap.Int64("total_jobs", metrics.TotalJobs),
		zap.Int64("successful_jobs", metrics.SuccessfulJobs),
		zap.Int64("failed_jobs", metrics.FailedJobs),
		zap.Int64("timed-out_jobs", metrics.TimedOutJobs),
		zap.Float64("jobs_per_second", metrics.JobsPerSecond),
		zap.Duration("average_latency", metrics.AverageLatency),
		zap.Int("active_workers", metrics.ActiveWorkers),
		zap.Int("queue_depth", metrics.QueueDepth),
		zap.Duration("uptime", metrics.Uptime),
		zap.Float64("error_rate", metrics.ErrorRate))
}

func (d *CrawlerDemo) shutdown(ctx context.Context) error {
	d.logger.Info("Starting graceful shutdown...")

	// Stop crawler engine
	if err := d.engine.Stop(); err != nil {
		d.logger.Error("Error stopping crawler engine", zap.Error(err))
	}

	// Close HTTP client
	if err := d.httpClient.Close(); err != nil {
		d.logger.Error("Error closing HTTP client", zap.Error(err))
	}

	// Close robots parser
	if err := d.robotsParser.Close(); err != nil {
		d.logger.Error("Error closing robots parser", zap.Error(err))
	}

	// Close extractor factory
	if err := d.extractorFactory.Close(); err != nil {
		d.logger.Error("Error closing extractor factory", zap.Error(err))
	}

	d.logger.Info("Graceful shutdown completed")
	return nil
}

func (d *CrawlerDemo) printFinalStats() {
	d.mu.RLock()
	defer d.mu.RUnlock()

	duration := time.Since(d.startTime)

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("QUERT COMPREHENSIVE CRAWLER - FINAL STATISTICS")
	fmt.Println(strings.Repeat("=", 60))

	fmt.Printf("📊 Crawling Statistics:\n")
	fmt.Printf("   Total Runtime:     %v\n", duration.Round(time.Second))
	fmt.Printf("   Total Pages:       %d\n", d.totalPages)
	fmt.Printf("   Successful:        %d\n", d.successCount)
	fmt.Printf("   Failed:            %d\n", d.failureCount)

	if d.totalPages > 0 {
		successRate := float64(d.successCount) / float64(d.totalPages) * 100
		fmt.Printf("   Success Rate:      %.1f%%\n", successRate)

		if duration.Seconds() > 0 {
			pagesPerSecond := float64(d.totalPages) / duration.Seconds()
			fmt.Printf("   Pages/Second:      %.2f\n", pagesPerSecond)
		}
	}

	if metrics := d.engine.GetMetrics(); metrics != nil {
		fmt.Printf("\n🏃 Worker Statistics:\n")
		fmt.Printf("   Active Workers:    %d\n", metrics.ActiveWorkers)
		fmt.Printf("   Average Latency:   %v\n", metrics.AverageLatency.Round(time.Millisecond))
		fmt.Printf("   Queue Depth:       %d\n", metrics.QueueDepth)
		fmt.Printf("   Error Rate:        %.1f%%\n", metrics.ErrorRate)
	}

	if stats := d.urlProcessor.GetStatistics(); stats != nil {
		fmt.Printf("\n🔗 URL Processing:\n")
		fmt.Printf("   Processed URLs:    %d\n", stats["processed_count"])
		fmt.Printf("   Valid URLs:        %d\n", stats["valid_count"])
		fmt.Printf("   Duplicates:        %d\n", stats["duplicate_count"])
		fmt.Printf("   Invalid URLs:      %d\n", stats["invalid_count"])
	}

	if robotsStats := d.robotsParser.GetCacheStats(); robotsStats.TotalEntries > 0 {
		fmt.Printf("\n🤖 Robots.txt Cache:\n")
		fmt.Printf("   Cached Entries:    %d\n", robotsStats.TotalEntries)
		fmt.Printf("   Expired Entries:   %d\n", robotsStats.ExpiredEntries)
		fmt.Printf("   Average Age:       %v\n", robotsStats.AverageAge.Round(time.Second))
	}

	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("✅ Crawler demonstration completed successfully!")
	fmt.Println(strings.Repeat("=", 60))
}
