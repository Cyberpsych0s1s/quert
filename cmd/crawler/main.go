// Copyright 2026 Omar Almahri and the Quert contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command crawler is the Quert command-line web crawler.
//
// It crawls from seed URLs, following discovered links breadth-first (bounded by
// max depth and max pages), respecting robots.txt and per-host rate limits, and
// writes each extracted page as one JSON object per line (JSONL) to the output.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof handlers on http.DefaultServeMux
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cyberpsych0s1s/quert/internal/config"
	"github.com/cyberpsych0s1s/quert/internal/crawler"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Build metadata, injected via -ldflags by the Makefile.
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to config file (default: ./config.yaml if present)")
		seedCSV    = flag.String("seed", "", "comma-separated seed URLs (overrides config seed_urls)")
		workers    = flag.Int("workers", 0, "number of concurrent workers (0 = use config)")
		maxPages   = flag.Int("max-pages", 0, "maximum pages to crawl (0 = use config)")
		outputPath = flag.String("output", "", "JSONL output file (default: stdout)")
		timeout    = flag.Duration("timeout", 0, "overall crawl timeout, e.g. 2m (0 = no limit)")
		sitemap    = flag.Bool("sitemap", false, "seed the frontier from each host's sitemaps (robots.txt / sitemap.xml)")
		statePath  = flag.String("state", "", "checkpoint file for resumable crawls (saves pending frontier, resumes on restart)")
		metrics    = flag.String("metrics", "", "address for metrics + pprof HTTP server, e.g. :6060 (empty = off)")
		jsRender   = flag.Bool("js", false, "render pages with a headless browser (requires building with -tags headless)")
		verbose    = flag.Bool("v", false, "verbose (debug) logging")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("quert %s (commit %s, built %s)\n", version, commit, buildTime)
		return
	}

	logger := newLogger(*verbose)
	defer func() { _ = logger.Sync() }()

	cfg, err := config.LoadConfig(*configPath, nil)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	// Apply CLI overrides on top of the loaded config.
	if *seedCSV != "" {
		cfg.Crawler.SeedURLs = splitCSV(*seedCSV)
	}
	if *workers > 0 {
		cfg.Crawler.ConcurrentWorkers = *workers
	}
	if *maxPages > 0 {
		cfg.Crawler.MaxPages = *maxPages
	}
	if *jsRender {
		cfg.Features.JavaScriptRendering = true
	}

	seeds := cfg.Crawler.SeedURLs
	if len(seeds) == 0 {
		logger.Fatal("no seed URLs: pass -seed or set crawler.seed_urls in the config")
	}

	// Output sink: a file when -output is given, otherwise stdout. When resuming
	// (-state set) the output is opened in append mode so a restart adds to,
	// rather than truncates, the JSONL collected so far.
	var out io.Writer = os.Stdout
	if *outputPath != "" {
		flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if *statePath != "" {
			flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(*outputPath, flags, 0o644)
		if err != nil {
			logger.Fatal("failed to open output file", zap.Error(err))
		}
		defer func() { _ = f.Close() }()
		out = f
	}

	// Root context: cancelled by SIGINT/SIGTERM and (optionally) a deadline.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutdown signal received, stopping gracefully")
		cancel()
	}()

	coord := crawler.NewCoordinator(cfg, logger)
	if *sitemap {
		coord.EnableSitemapDiscovery()
	}
	if *statePath != "" {
		coord.SetCheckpoint(*statePath, 0)
	}
	if *metrics != "" {
		startMetricsServer(*metrics, coord, logger)
	}

	// JSONL output sink. The coordinator invokes the sink from a single consumer
	// goroutine, so it need not be safe for concurrent use.
	jsonl := crawler.NewJSONLSink(out)
	sink := func(result *crawler.CrawlResult) {
		if err := jsonl.Write(result); err != nil {
			logger.Warn("failed to write output record",
				zap.String("url", result.URL), zap.Error(err))
		}
	}

	logger.Info("starting crawl", zap.Int("seeds", len(seeds)),
		zap.Int("max_depth", cfg.Crawler.MaxDepth), zap.Int("max_pages", cfg.Crawler.MaxPages))

	stats, err := coord.Run(ctx, seeds, sink)
	if err != nil {
		logger.Fatal("crawl failed", zap.Error(err))
	}

	logger.Info("crawl complete",
		zap.Int("dispatched", stats.Dispatched),
		zap.Int("results", stats.Received),
		zap.Int("succeeded", stats.Succeeded),
		zap.Int("duplicate", stats.Duplicate),
		zap.Int("failed", stats.Failed),
		zap.Int64("written", jsonl.Written()))
}

// startMetricsServer serves live crawler metrics as JSON at /metrics and Go
// runtime profiles at /debug/pprof on addr, in a background goroutine.
func startMetricsServer(addr string, coord *crawler.Coordinator, logger *zap.Logger) {
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coord.Engine().GetMetrics())
	})
	logger.Info("metrics server listening", zap.String("addr", addr))
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			logger.Warn("metrics server stopped", zap.Error(err))
		}
	}()
}

// newLogger builds a human-readable console logger to stderr so it never mixes
// with JSONL output on stdout.
func newLogger(verbose bool) *zap.Logger {
	level := zapcore.InfoLevel
	if verbose {
		level = zapcore.DebugLevel
	}
	encCfg := zap.NewDevelopmentEncoderConfig()
	encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encCfg),
		zapcore.Lock(os.Stderr),
		level,
	)
	return zap.New(core)
}

// splitCSV splits a comma-separated list, trimming whitespace and dropping
// empty entries.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
