# Quert

[![Go Reference](https://pkg.go.dev/badge/github.com/cyberpsych0s1s/quert.svg)](https://pkg.go.dev/github.com/cyberpsych0s1s/quert)
[![Go Report Card](https://goreportcard.com/badge/github.com/cyberpsych0s1s/quert)](https://goreportcard.com/report/github.com/cyberpsych0s1s/quert)
[![Go Version](https://img.shields.io/github/go-mod/go-version/cyberpsych0s1s/quert)](go.mod)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/cyberpsych0s1s/quert/actions/workflows/ci.yml/badge.svg)](https://github.com/cyberpsych0s1s/quert/actions/workflows/ci.yml)
[![devlog](https://almahri.dev/devlogs/quert/badge.svg)](https://almahri.dev/devlogs/quert/)

A concurrent web crawler in Go for collecting LLM-training data, ethically. Crawls breadth-first, respects robots.txt, rate-limits politely, deduplicates content, and emits clean UTF-8 text as JSONL.

A web crawler built in Go to collect LLM-training data, ethically. It crawls breadth-first and respects robots.txt, rate-limits, it deduplicates content and emits UTF-8 text as JSONL.

> **Status:** In Development. The core features have been completed and tested. See [Project Status](#project-status).

## Features

- **Crawl**: breadth first from seed URLs, is bounded by max depth/pages, priority frontier (seeds → sitemaps → discovered).
- **robots.txt**: disallow rules + enforced crawl-delay; sitemap seeding (`-sitemap`).
- **Rate limiting**:  global + per-host token buckets.
- **Extraction**: HTML, XHTML, XML/RSS/Atom, plain text; main-content selection, boilerplate removal, metadata, quality scoring.
- **Clean text**: charset detection (no mojibake), rune-safe truncation, `<script>`/`<style>` stripped.
- **Dedup**: exact (content hash) + near-duplicate (simhash).
- **Language filtering**, **retries**, **resumable crawls** (`-state`, pair with Redis), **observability** (`-metrics`: JSON + pprof).
- **JSONL output**: one self-describing JSON object per page.

## Requirements

- Go 1.25+ (see `go.mod`).
- Optional: Redis for persistent/resumable dedup.

## Install

```bash
git clone https://github.com/cyberpsych0s1s/quert.git
cd quert
go build -o bin/crawler ./cmd/crawler
```

## Usage

```bash
# Single page
./bin/crawler -seed "https://example.com" -output out.jsonl

# Bounded crawl
./bin/crawler -seed "https://example.com" -max-pages 500 -max-depth 3 -output out.jsonl

# Sitemap-seeded, resumable, with metrics
./bin/crawler -seed "https://example.com" -sitemap -state crawl.state -metrics :6060 -output out.jsonl
```

Seeds may also come from `crawler.seed_urls` in config; `-seed` overrides. Run `-help` for all flags. Logs go to stderr, so stdout stays clean JSONL.

### Output

One JSON object per line:

```json
{"url":"https://example.com/page","status_code":200,"title":"Page Title","language":"en","quality_score":0.83,"word_count":742,"link_count":31,"text":"Clean extracted text…","crawled_at":"2026-06-18T12:00:00Z"}
```

## Library

```go
cfg, _ := config.LoadConfig("", nil)
stats, err := quert.CrawlToJSONL(ctx, cfg, []string{"https://example.com"}, os.Stdout, nil)
```

Custom sink: `quert.Crawl(ctx, cfg, seeds, sinkFn, logger)`. Refer to the full api in [Go docs](https://pkg.go.dev/github.com/cyberpsych0s1s/quert).

## Configuration

Layered: defaults → YAML → env (`CRAWLER_*`) → flags. Sections in `config.yaml`: `crawler`, `http`, `content` (incl. `deduplication`), `robots`, `frontier`, `storage`/`redis`. Sample `config.yaml` included.

## Ethics

Crawls politely: robots.txt respected, global + per-host rate limits, descriptive `User-Agent` on every request. Disable robots handling only for hosts you own or are permitted to crawl.

## Testing

```bash
go test ./...          # all
go test -race ./...    # race detector
```

Covers crawl loop, extraction, robots, Redis-backed resume, and a 10k-page in-process scale test.

## Project Status

**Tested**: full pipeline end-to-end; 10k-page crawl with flat/bounded memory; checkpoint + Redis resume survive restart.

**Improving**: throughput/memory under multi-day real-network crawls; extraction is heuristic, not yet readability-class.

**Planned**: distributed crawling, higher-quality extraction, more output sinks (object storage, columnar).

## License

Quert uses the Apache 2.0 License. See [`LICENSE`](LICENSE).


