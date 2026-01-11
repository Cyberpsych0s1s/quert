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
	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	crawlerConfig := &config.CrawlerConfig{
		MaxPages:          10,
		MaxDepth:          2,
		ConcurrentWorkers: 3,
		RequestTimeout:    15 * time.Second,
		UserAgent:         "Quert-Example/1.0 (+https://github.com/Almahr1/quert)",
		SeedURLs: []string{
			"https://httpbin.org/html",
			"https://example.com",
		},
	}

	httpConfig := &config.HTTPConfig{
		MaxIdleConnections:        50,
		MaxIdleConnectionsPerHost: 5,
		IdleConnectionTimeout:     30 * time.Second,
		DisableKeepAlives:         false,
		Timeout:                   15 * time.Second,
		DialTimeout:               5 * time.Second,
		TlsHandshakeTimeout:       10 * time.Second,
		ResponseHeaderTimeout:     10 * time.Second,
		DisableCompression:        false,
	}

	engine := crawler.NewCrawlerEngine(crawlerConfig, httpConfig, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("Starting crawler engine")
	if err := engine.Start(ctx); err != nil {
		logger.Fatal("Failed to start crawler engine", zap.Error(err))
	}

	go processResults(engine, logger)

	for _, url := range crawlerConfig.SeedURLs {
		job := &crawler.CrawlJob{
			URL:         url,
			Priority:    1,
			Depth:       0,
			Headers:     map[string]string{},
			RequestID:   fmt.Sprintf("job-%d", time.Now().UnixNano()),
			SubmittedAt: time.Now(),
			Context:     ctx, // Use the main context for jobs
		}

		logger.Info("Submitting crawl job", zap.String("url", url))
		if err := engine.SubmitJob(job); err != nil {
			logger.Error("Failed to submit job", zap.String("url", url), zap.Error(err))
		}
	}

	logger.Info("Crawler is running. Waiting for context to complete...")
	<-ctx.Done()
	logger.Info("Context finished. Shutting down.")

	if err := engine.Stop(); err != nil {
		logger.Error("Error stopping crawler", zap.Error(err))
	}

	// Print final metrics
	metrics := engine.GetMetrics()
	if metrics != nil {
		logger.Info("Final crawler metrics",
			zap.Int64("total_jobs", metrics.TotalJobs),
			zap.Int64("successful_jobs", metrics.SuccessfulJobs),
			zap.Int64("failed_jobs", metrics.FailedJobs),
			zap.Int64("timed_out_jobs", metrics.TimedOutJobs),
			zap.Float64("jobs_per_second", metrics.JobsPerSecond),
			zap.Duration("uptime", metrics.Uptime))
	}

	logger.Info("Crawler example completed")
}

// processResults processes crawl results and demonstrates content extraction features
func processResults(engine *crawler.CrawlerEngine, logger *zap.Logger) {
	results := engine.GetResults()

	for result := range results {
		if result == nil {
			continue
		}

		if result.Success && result.ExtractedContent != nil {
			content := result.ExtractedContent

			logger.Info("Successfully extracted content",
				zap.String("url", result.URL),
				zap.String("title", content.Title),
				zap.Int("word_count", content.Metadata.WordCount),
				zap.Int("link_count", len(content.Links)),
				zap.Int("image_count", len(content.Images)),
				zap.Float64("quality_score", content.QualityScore),
				zap.String("language", content.Metadata.Language),
				zap.String("author", content.Metadata.Author))

			// Print first 200 characters of clean text
			if len(content.CleanText) > 200 {
				logger.Info("Content preview",
					zap.String("url", result.URL),
					zap.String("preview", content.CleanText[:200]+"..."))
			} else {
				logger.Info("Content preview",
					zap.String("url", result.URL),
					zap.String("preview", content.CleanText))
			}

			// Print extracted links
			if len(content.Links) > 0 {
				logger.Info("Extracted links", zap.String("url", result.URL))
				for i, link := range content.Links {
					if i >= 5 {
						logger.Info(fmt.Sprintf("... and %d more links", len(content.Links)-5))
						break
					}
					logger.Info("Link found",
						zap.String("link_url", link.URL),
						zap.String("link_text", link.Text),
						zap.Bool("internal", link.Internal))
				}
			}

		} else if result.Error != nil {
			logger.Error("Crawl job failed",
				zap.String("url", result.URL),
				zap.Error(result.Error),
				zap.Bool("retryable", result.Retryable))
		}
	}
}
