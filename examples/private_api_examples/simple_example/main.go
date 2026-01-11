package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Almahr1/quert/internal/config"
	"github.com/Almahr1/quert/internal/crawler"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting Quert Simple Example")

	crawlerConfig := &config.CrawlerConfig{
		MaxPages:          5,
		MaxDepth:          1,
		ConcurrentWorkers: 2,
		RequestTimeout:    30 * time.Second,
		UserAgent:         "Quert-SimpleExample/1.0",
		SeedURLs: []string{
			"https://httpbin.org/html",
			"https://example.com",
		},
		GlobalRateLimit:  1.0,
		GlobalBurst:      2,
		PerHostRateLimit: 1.0,
		PerHostBurst:     1,
	}

	httpConfig := &config.HTTPConfig{
		MaxIdleConnections:        50,
		MaxIdleConnectionsPerHost: 5,
		IdleConnectionTimeout:     30 * time.Second,
		DisableKeepAlives:         false,
		Timeout:                   30 * time.Second,
		DialTimeout:               10 * time.Second,
		TlsHandshakeTimeout:       15 * time.Second,
		ResponseHeaderTimeout:     15 * time.Second,
		DisableCompression:        false,
	}

	engine := crawler.NewCrawlerEngine(crawlerConfig, httpConfig, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger.Info("Starting crawler engine")
	if err := engine.Start(ctx); err != nil {
		logger.Fatal("Failed to start crawler engine", zap.Error(err))
	}

	go processResults(engine, logger)

	// Submit crawl jobs
	for i, url := range crawlerConfig.SeedURLs {
		job := &crawler.CrawlJob{
			URL:         url,
			Priority:    1,
			Depth:       0,
			Headers:     map[string]string{},
			RequestID:   fmt.Sprintf("simple-job-%d", i),
			SubmittedAt: time.Now(),
			Context:     ctx,
		}

		logger.Info("Submitting crawl job", zap.String("url", url))
		if err := engine.SubmitJob(job); err != nil {
			logger.Error("Failed to submit job", zap.String("url", url), zap.Error(err))
		}
	}

	// Wait for crawling to complete
	time.Sleep(30 * time.Second)

	logger.Info("Stopping crawler engine")
	if err := engine.Stop(); err != nil {
		logger.Error("Error stopping crawler", zap.Error(err))
	}

	// Print final metrics
	if metrics := engine.GetMetrics(); metrics != nil {
		logger.Info("Final crawler metrics",
			zap.Int64("total_jobs", metrics.TotalJobs),
			zap.Int64("successful_jobs", metrics.SuccessfulJobs),
			zap.Int64("failed_jobs", metrics.FailedJobs),
			zap.Float64("jobs_per_second", metrics.JobsPerSecond),
			zap.Duration("uptime", metrics.Uptime))
	}

	logger.Info("Simple crawler example completed")
}

func processResults(engine *crawler.CrawlerEngine, logger *zap.Logger) {
	results := engine.GetResults()

	for result := range results {
		if result == nil {
			continue
		}

		if result.Success {
			logger.Info("Successfully crawled page",
				zap.String("url", result.URL),
				zap.Int("status_code", result.StatusCode),
				zap.Duration("response_time", result.ResponseTime),
				zap.Int("content_length", len(result.Body)))

			if result.ExtractedContent != nil {
				content := result.ExtractedContent
				logger.Info("Extracted content",
					zap.String("url", result.URL),
					zap.String("title", content.Title),
					zap.Int("word_count", content.Metadata.WordCount),
					zap.Int("link_count", len(content.Links)),
					zap.Float64("quality_score", content.QualityScore))
			}
		} else {
			logger.Error("Failed to crawl page",
				zap.String("url", result.URL),
				zap.Error(result.Error),
				zap.Bool("retryable", result.Retryable))
		}
	}
}
