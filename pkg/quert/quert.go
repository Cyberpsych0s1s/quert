// Package quert provides a public API for the Quert web crawler.
//
// The simplest entry point is Crawl / CrawlToJSONL: give it a config and seed
// URLs and it runs the full pipeline (discovery, robots, rate limiting, extract,
// dedup) for you. For more control, build a Coordinator with NewCoordinator and
// call Run.
package quert

import (
	"context"
	"io"

	"github.com/cyberpsych0s1s/quert/internal/config"
	"github.com/cyberpsych0s1s/quert/internal/crawler"
	"github.com/cyberpsych0s1s/quert/internal/extractor"
	"go.uber.org/zap"
)

// Re-export all configuration types
type (
	Config           = config.Config
	CrawlerConfig    = config.CrawlerConfig
	RateLimitConfig  = config.RateLimitConfig
	ContentConfig    = config.ContentConfig
	DedupConfig      = config.DedupConfig
	StorageConfig    = config.StorageConfig
	MonitoringConfig = config.MonitoringConfig
	HTTPConfig       = config.HTTPConfig
	FrontierConfig   = config.FrontierConfig
	RobotsConfig     = config.RobotsConfig
	RedisConfig      = config.RedisConfig
	SecurityConfig   = config.SecurityConfig
	FeatureConfig    = config.FeatureConfig
)

// Re-export crawler types
type (
	CrawlerEngine  = crawler.CrawlerEngine
	CrawlJob       = crawler.CrawlJob
	CrawlResult    = crawler.CrawlResult
	CrawlerMetrics = crawler.CrawlerMetrics
	Coordinator    = crawler.Coordinator
	CrawlStats     = crawler.CrawlStats
	ResultSink     = crawler.ResultSink
	Sink           = crawler.Sink
	JSONLSink      = crawler.JSONLSink
	OutputRecord   = crawler.OutputRecord
)

// Re-export extractor types
type (
	ExtractedContent = extractor.ExtractedContent
	ExtractedLink    = extractor.ExtractedLink
	ExtractedImage   = extractor.ExtractedImage
	ContentMetadata  = extractor.ContentMetadata
	ExtractorFactory = extractor.ExtractorFactory
	ExtractorConfig  = extractor.ExtractorConfig
)

// Re-export constructor functions
var (
	NewCrawlerEngine          = crawler.NewCrawlerEngine
	NewCoordinator            = crawler.NewCoordinator
	NewExtractorFactory       = extractor.NewExtractorFactory
	NewJSONLSink              = crawler.NewJSONLSink
	GetDefaultExtractorConfig = extractor.GetDefaultExtractorConfig
)

// Re-export configuration functions
var (
	LoadConfig = config.LoadConfig
)

// Crawl runs a complete crawl from the given seed URLs using cfg, invoking sink
// for each result, and returns summary statistics. It is the batteries-included
// entry point: discovery, robots.txt, rate limiting, extraction, and dedup are
// all handled. Pass a nil logger for silent operation.
func Crawl(ctx context.Context, cfg *Config, seeds []string, sink ResultSink, logger *zap.Logger) (CrawlStats, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	coord := crawler.NewCoordinator(cfg, logger)
	return coord.Run(ctx, seeds, sink)
}

// CrawlToJSONL runs a complete crawl and writes each extracted page to w as one
// JSON object per line (JSONL). Pass a nil logger for silent operation.
func CrawlToJSONL(ctx context.Context, cfg *Config, seeds []string, w io.Writer, logger *zap.Logger) (CrawlStats, error) {
	sink := crawler.NewJSONLSink(w)
	return Crawl(ctx, cfg, seeds, func(r *CrawlResult) { _ = sink.Write(r) }, logger)
}
