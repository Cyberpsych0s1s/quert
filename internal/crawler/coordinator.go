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

package crawler

import (
	"container/heap"
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/config"
	"github.com/cyberpsych0s1s/quert/internal/extractor"
	"github.com/cyberpsych0s1s/quert/internal/frontier"
	"github.com/cyberpsych0s1s/quert/internal/storage"
	"github.com/cyberpsych0s1s/quert/internal/storage/memory"
	redisstore "github.com/cyberpsych0s1s/quert/internal/storage/redis"
	"go.uber.org/zap"
)

// ResultSink consumes each crawl result as it completes. It is called from a
// single goroutine, so implementations need not be safe for concurrent use.
type ResultSink func(*CrawlResult)

// CrawlStats summarizes a completed crawl.
type CrawlStats struct {
	Dispatched    int // jobs submitted to the engine
	Received      int // results received back
	Succeeded     int // results with a successful fetch
	Failed        int // results with an error (final, after retries exhausted)
	Duplicate     int // successful pages dropped as exact content duplicates
	NearDuplicate int // successful pages dropped as near-duplicates (simhash)
	Retried       int // retryable failures re-dispatched
	Dropped       int // discovered URLs dropped because the frontier was full
	WrongLanguage int // pages dropped from output for a non-target language
}

const (
	// simhashNearThreshold is the maximum Hamming distance (out of 64 bits) at
	// which two pages' simhashes are considered near-duplicates.
	simhashNearThreshold = 3
	// maxSimhashes caps the in-memory near-duplicate fingerprint set so a very
	// large crawl can't grow it without bound; beyond this, near-dup detection
	// degrades gracefully (new pages are no longer compared/added).
	maxSimhashes = 200000
)

// coordinatorMaxRetries is how many times a retryable failure is re-dispatched
// by the coordinator. This is on top of the HTTP client's per-request transport
// retries — it gives a failed URL another full attempt later in the crawl.
const coordinatorMaxRetries = 2

// frontierItem is a URL queued for crawling along with its discovery depth and
// retry attempt. retry marks a re-dispatch so it bypasses the page cap and is
// not double-counted against the dispatched total. priority orders dispatch
// (higher first); seq preserves FIFO order among equal-priority, equal-depth
// items.
type frontierItem struct {
	url      string
	depth    int
	attempt  int
	retry    bool
	priority int
	seq      uint64
}

// frontierHeap is a priority queue over frontierItem: higher priority first,
// then shallower depth (breadth-first), then insertion order (FIFO).
type frontierHeap []frontierItem

func (h frontierHeap) Len() int { return len(h) }
func (h frontierHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.priority != b.priority {
		return a.priority > b.priority
	}
	if a.depth != b.depth {
		return a.depth < b.depth
	}
	return a.seq < b.seq
}
func (h frontierHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *frontierHeap) Push(x any)   { *h = append(*h, x.(frontierItem)) }
func (h *frontierHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Priority levels for newly discovered work.
const (
	priorityDiscovered = 0   // links found while crawling
	prioritySitemap    = 50  // URLs from a sitemap
	prioritySeed       = 100 // explicit seed URLs
)

// Coordinator drives the crawl discovery loop on top of a CrawlerEngine. It owns
// the URL frontier (queue + dedup/validation), dispatches URLs to the engine,
// and feeds links discovered in results back into the frontier — bounded by max
// depth and max pages. The engine itself stays a simple job-in/result-out unit;
// the coordinator is the scheduler that turns it into a real crawler.
//
// Concurrency model: a single feeder goroutine pops the queue and calls the
// engine's (rate-limited) SubmitJob; a single consumer goroutine drains results
// and enqueues their child links. A condition variable coordinates termination,
// which is reached when the queue is empty and no job is in flight.
type Coordinator struct {
	engine *CrawlerEngine
	proc   *frontier.URLProcessor
	store  storage.DeduplicationStore
	logger *zap.Logger

	maxPages         int
	maxDepth         int
	maxRetries       int
	maxQueue         int
	dedupContent     bool
	dedupNearContent bool
	discoverSitemaps bool
	allowedLangs     map[string]bool // primary language subtags; empty = allow all

	checkpointPath     string
	checkpointInterval time.Duration

	mu         sync.Mutex
	cond       *sync.Cond
	queue      frontierHeap
	seq        uint64
	inFlight   int
	dispatched int
	done       bool
	stats      CrawlStats

	// simhashes accumulates content fingerprints for near-duplicate detection.
	// Accessed only from the single consumer goroutine.
	simhashes []uint64
}

// NewCoordinator builds a coordinator and its underlying engine from cfg. The
// engine's extractor is configured from cfg.Content (see ExtractorConfigFromContent),
// and the frontier's validator is scoped using cfg.Crawler's domain and pattern
// filters.
func NewCoordinator(cfg *config.Config, logger *zap.Logger) *Coordinator {
	if logger == nil {
		logger = zap.NewNop()
	}
	engine := NewCrawlerEngine(&cfg.Crawler, &cfg.HTTP, &cfg.Robots, &cfg.JSRender, &cfg.Features, logger)
	engine.ExtractorFactory = extractor.NewExtractorFactory(ExtractorConfigFromContent(&cfg.Content), logger)

	store := storeFromConfig(cfg, logger)

	c := &Coordinator{
		engine:           engine,
		proc:             newConfiguredProcessor(&cfg.Crawler, store),
		store:            store,
		logger:           logger,
		maxPages:         cfg.Crawler.MaxPages,
		maxDepth:         cfg.Crawler.MaxDepth,
		maxRetries:       coordinatorMaxRetries,
		maxQueue:         cfg.Frontier.QueueCapacity,
		dedupContent:     cfg.Content.Deduplication.Enabled && cfg.Content.Deduplication.ContentFingerprinting,
		dedupNearContent: cfg.Content.Deduplication.Enabled && cfg.Content.Deduplication.SemanticSimilarity,
		allowedLangs:     buildLangSet(cfg.Content.Languages),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// buildLangSet returns the set of allowed primary language subtags (lowercased,
// e.g. "en" from "en-US"). An empty input means "accept all languages".
func buildLangSet(langs []string) map[string]bool {
	if len(langs) == 0 {
		return nil
	}
	set := make(map[string]bool, len(langs))
	for _, l := range langs {
		if p := primaryLang(l); p != "" {
			set[p] = true
		}
	}
	return set
}

// primaryLang extracts the primary subtag of a language tag: "en-US" -> "en",
// "pt_BR" -> "pt", "EN" -> "en".
func primaryLang(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	for i, r := range t {
		if r == '-' || r == '_' {
			return t[:i]
		}
	}
	return t
}

// storeFromConfig builds the deduplication store: Redis when configured (so the
// "seen" set survives restarts), otherwise in-memory. Falls back to in-memory if
// Redis is unreachable.
func storeFromConfig(cfg *config.Config, logger *zap.Logger) storage.DeduplicationStore {
	if strings.EqualFold(cfg.Storage.Type, "redis") {
		rs, err := redisstore.New(redisstore.Config{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.Db,
			Prefix:   "quert:",
		})
		if err != nil {
			logger.Warn("redis dedup store unavailable, using in-memory", zap.Error(err))
			return memory.New()
		}
		logger.Info("using redis dedup store", zap.String("addr", cfg.Redis.Addr))
		return rs
	}
	return memory.New()
}

// Engine returns the underlying crawler engine (e.g. for metrics access).
func (c *Coordinator) Engine() *CrawlerEngine { return c.engine }

// EnableSitemapDiscovery makes Run seed the frontier from each seed host's
// sitemaps (declared in robots.txt or the conventional /sitemap.xml) in addition
// to the seed URLs themselves.
func (c *Coordinator) EnableSitemapDiscovery() { c.discoverSitemaps = true }

// newConfiguredProcessor builds a URL processor whose validator respects the
// crawler config's domain allow/block lists and include/exclude patterns.
func newConfiguredProcessor(cfg *config.CrawlerConfig, store storage.DeduplicationStore) *frontier.URLProcessor {
	p := frontier.NewURLProcessorWithStore(store)
	if len(cfg.AllowedDomains) > 0 {
		p.Validator.AllowedDomains = cfg.AllowedDomains
	}
	if len(cfg.BlockedDomains) > 0 {
		p.Validator.BlockedDomains = append(p.Validator.BlockedDomains, cfg.BlockedDomains...)
	}
	if len(cfg.IncludePatterns) > 0 {
		p.Validator.IncludePatterns = cfg.IncludePatterns
	}
	if len(cfg.ExcludePatterns) > 0 {
		p.Validator.ExcludePatterns = cfg.ExcludePatterns
	}
	return p
}

// Run starts the engine, seeds the frontier, and crawls until the frontier
// drains, the page cap is hit, or ctx is cancelled. Each result is passed to
// sink as it arrives. It blocks until the crawl finishes and returns summary
// stats.
func (c *Coordinator) Run(ctx context.Context, seeds []string, sink ResultSink) (CrawlStats, error) {
	if err := c.engine.Start(ctx); err != nil {
		return CrawlStats{}, err
	}
	defer func() {
		if c.store != nil {
			_ = c.store.Close()
		}
	}()

	// Wake the feeder promptly on cancellation even if it is parked in cond.Wait.
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		c.done = true
		c.mu.Unlock()
		c.cond.Broadcast()
	}()

	// Resume any pending frontier from a previous run before seeding.
	c.resumeFromCheckpoint(ctx)

	for _, s := range seeds {
		if err := c.enqueue(ctx, s, 0, prioritySeed); err != nil {
			// Seeds are explicit user intent; a rejected seed (out of scope,
			// invalid, or filtered by config domain/pattern rules) is worth
			// surfacing rather than silently dropping.
			c.logger.Warn("seed not queued", zap.String("url", s), zap.Error(err))
		}
		if c.discoverSitemaps {
			smURLs := c.discoverSitemapURLs(ctx, s)
			c.logger.Info("sitemap discovery", zap.String("seed", s), zap.Int("urls", len(smURLs)))
			for _, u := range smURLs {
				_ = c.enqueue(ctx, u, 0, prioritySitemap) // dedup + scope filtering handled by enqueue
			}
		}
	}

	// Periodic checkpointing of the pending frontier, if enabled.
	var cpStop chan struct{}
	if c.checkpointPath != "" {
		cpStop = make(chan struct{})
		go c.checkpointLoop(cpStop)
	}

	consumerDone := make(chan struct{})
	go c.consume(ctx, sink, consumerDone)

	c.feed(ctx) // blocks until the crawl drains or is cancelled

	// Stopping the engine closes its result channel, which ends the consumer.
	if err := c.engine.Stop(); err != nil {
		c.logger.Warn("engine stop error", zap.Error(err))
	}
	<-consumerDone

	// Stop periodic checkpointing and write a final snapshot (empty on a clean
	// finish, so a subsequent run starts fresh; non-empty if interrupted).
	if cpStop != nil {
		close(cpStop)
		if err := saveCheckpoint(c.checkpointPath, c.snapshotQueue()); err != nil {
			c.logger.Warn("final checkpoint save failed", zap.Error(err))
		}
	}

	c.mu.Lock()
	stats := c.stats
	stats.Dispatched = c.dispatched
	c.mu.Unlock()
	return stats, nil
}

// SetCheckpoint enables frontier checkpointing to path: the pending queue is
// saved periodically and on exit, and loaded on the next Run to resume an
// interrupted crawl. A non-positive interval defaults to 30s. For full
// resumability (not re-crawling already-fetched pages), pair this with a
// persistent dedup store (storage.type = "redis").
func (c *Coordinator) SetCheckpoint(path string, interval time.Duration) {
	c.checkpointPath = path
	if interval <= 0 {
		interval = 30 * time.Second
	}
	c.checkpointInterval = interval
}

// resumeFromCheckpoint loads a previously saved frontier and re-queues it. Loaded
// URLs are marked seen so re-discovery does not duplicate them.
func (c *Coordinator) resumeFromCheckpoint(ctx context.Context) {
	if c.checkpointPath == "" {
		return
	}
	items, err := loadCheckpoint(c.checkpointPath)
	if err != nil || len(items) == 0 {
		return
	}
	c.mu.Lock()
	for _, it := range items {
		_ = c.proc.Deduplicator.AddURL(ctx, it.url)
		c.seq++
		it.seq = c.seq
		heap.Push(&c.queue, it)
	}
	c.mu.Unlock()
	c.logger.Info("resumed from checkpoint",
		zap.String("path", c.checkpointPath), zap.Int("pending", len(items)))
}

// checkpointLoop periodically snapshots the pending frontier to disk.
func (c *Coordinator) checkpointLoop(stop chan struct{}) {
	t := time.NewTicker(c.checkpointInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := saveCheckpoint(c.checkpointPath, c.snapshotQueue()); err != nil {
				c.logger.Warn("checkpoint save failed", zap.Error(err))
			}
		}
	}
}

// isNearDuplicate reports whether sh is within simhashNearThreshold Hamming
// distance of any previously seen content fingerprint.
func (c *Coordinator) isNearDuplicate(sh uint64) bool {
	for _, prev := range c.simhashes {
		if frontier.HammingDistance(sh, prev) <= simhashNearThreshold {
			return true
		}
	}
	return false
}

// snapshotQueue returns a copy of the current pending queue.
func (c *Coordinator) snapshotQueue() []frontierItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]frontierItem, len(c.queue))
	copy(out, c.queue)
	return out
}

// errDepthExceeded is returned by enqueue when a URL is beyond the depth limit.
var errDepthExceeded = errors.New("exceeds max depth")

// enqueue normalizes, validates, and dedups a URL, then appends it to the queue
// if it is new, in scope, and within the depth limit. It returns a non-nil
// error describing why a URL was not queued (duplicate, invalid, out of scope,
// or too deep); callers may log or ignore it.
func (c *Coordinator) enqueue(ctx context.Context, rawURL string, depth, priority int) error {
	c.mu.Lock()
	if c.done {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if c.maxDepth > 0 && depth > c.maxDepth {
		return errDepthExceeded
	}

	// Process marks the URL as seen on success, so the same URL never enqueues
	// twice across the whole crawl.
	info, err := c.proc.Process(ctx, rawURL)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return nil
	}
	// Bound frontier memory: drop discovered URLs once the queue is full. This
	// caps memory on huge crawls at the cost of coverage (tracked via Dropped).
	if c.maxQueue > 0 && len(c.queue) >= c.maxQueue {
		c.stats.Dropped++
		return errQueueFull
	}
	c.seq++
	heap.Push(&c.queue, frontierItem{url: info.URL, depth: depth, priority: priority, seq: c.seq})
	c.cond.Broadcast()
	return nil
}

// errQueueFull is returned by enqueue when the frontier is at QueueCapacity.
var errQueueFull = errors.New("frontier queue full")

// feed is the dispatch loop: it pops URLs and submits them to the engine until
// the crawl is complete.
func (c *Coordinator) feed(ctx context.Context) {
	for {
		c.mu.Lock()
		atCap := c.maxPages > 0 && c.dispatched >= c.maxPages
		// Park while there is nothing to dispatch right now (empty queue or at
		// the page cap) but work is still in flight.
		for (len(c.queue) == 0 || atCap) && c.inFlight > 0 && !c.done {
			c.cond.Wait()
			atCap = c.maxPages > 0 && c.dispatched >= c.maxPages
		}
		// Terminate when cancelled, or when nothing is dispatchable and nothing
		// is in flight (queue drained, or capped and in-flight drained).
		if c.done || (c.inFlight == 0 && (len(c.queue) == 0 || atCap)) {
			c.done = true
			c.mu.Unlock()
			c.cond.Broadcast()
			return
		}
		item := heap.Pop(&c.queue).(frontierItem)
		// Retries re-attempt an already-counted page, so they neither advance
		// the page cap nor inflate the dispatched total.
		if !item.retry {
			c.dispatched++
		}
		c.inFlight++
		c.mu.Unlock()

		job := &CrawlJob{URL: item.url, Depth: item.depth, Attempt: item.attempt, Context: ctx}
		if err := c.engine.SubmitJob(job); err != nil {
			c.logger.Warn("failed to submit job", zap.String("url", item.url), zap.Error(err))
			c.mu.Lock()
			c.inFlight--
			c.mu.Unlock()
			c.cond.Broadcast()
		}
	}
}

// consume drains crawl results, enqueues their discovered links, and forwards
// each result to the sink.
func (c *Coordinator) consume(ctx context.Context, sink ResultSink, doneCh chan struct{}) {
	defer close(doneCh)
	results := c.engine.GetResults()
	for {
		select {
		case <-ctx.Done():
			return
		case res, ok := <-results:
			if !ok {
				return
			}
			// Enqueue children BEFORE decrementing in-flight, so the feeder
			// never observes (empty queue && zero in-flight) while child URLs
			// are still pending — which would terminate the crawl prematurely.
			if res != nil && res.Job != nil {
				childDepth := res.Job.Depth + 1
				if c.maxDepth <= 0 || childDepth <= c.maxDepth {
					for _, link := range res.Links {
						_ = c.enqueue(ctx, link, childDepth, priorityDiscovered)
					}
				}
			}

			c.mu.Lock()
			// Re-dispatch retryable failures (dedup-bypassing, since the URL is
			// already marked seen). Append BEFORE decrementing in-flight so the
			// feeder cannot terminate while the retry is pending.
			requeued := false
			if !c.done && res != nil && res.Error != nil && res.Retryable &&
				res.Job != nil && res.Job.Attempt < c.maxRetries {
				c.seq++
				heap.Push(&c.queue, frontierItem{
					url:      res.URL,
					depth:    res.Job.Depth,
					attempt:  res.Job.Attempt + 1,
					retry:    true,
					priority: priorityDiscovered,
					seq:      c.seq,
				})
				requeued = true
			}
			c.inFlight--
			c.stats.Received++
			switch {
			case requeued:
				c.stats.Retried++
			case res != nil && res.Success && res.Error == nil:
				c.stats.Succeeded++
			default:
				c.stats.Failed++
			}
			c.mu.Unlock()
			c.cond.Broadcast()

			if requeued || res == nil {
				continue
			}

			// Cross-page content dedup: drop pages whose cleaned text matches one
			// already emitted (mirror sites, print/canonical variants, session-id
			// URL twins). This filters OUTPUT only — discovery already followed the
			// links above, so dedup never prunes the crawl graph.
			if c.dedupContent && res.ExtractedContent != nil && res.ExtractedContent.CleanText != "" {
				hash := frontier.CalculateContentHash([]byte(res.ExtractedContent.CleanText))
				if dup, _, err := c.proc.Deduplicator.IsContentDuplicate(ctx, hash); err == nil && dup {
					c.mu.Lock()
					c.stats.Duplicate++
					c.mu.Unlock()
					continue
				}
				_ = c.proc.Deduplicator.AddContentHash(ctx, hash, res.URL)
			}

			// Near-duplicate detection: drop pages whose simhash is within a small
			// Hamming distance of one already emitted (templated/boilerplate-heavy
			// pages that differ only trivially). In-memory and consumer-local; the
			// store's exact simhash lookup can't do fuzzy matching.
			if c.dedupNearContent && res.ExtractedContent != nil && res.ExtractedContent.CleanText != "" {
				sh := frontier.CalculateSimhash(res.ExtractedContent.CleanText)
				if c.isNearDuplicate(sh) {
					c.mu.Lock()
					c.stats.NearDuplicate++
					c.mu.Unlock()
					continue
				}
				if len(c.simhashes) < maxSimhashes {
					c.simhashes = append(c.simhashes, sh)
				}
			}

			// Language filtering: keep only pages in a configured target language.
			// Links were already followed above; this filters OUTPUT only. Pages
			// with no detected language are kept (can't prove they're off-target).
			if len(c.allowedLangs) > 0 && res.ExtractedContent != nil {
				if lang := primaryLang(res.ExtractedContent.Metadata.Language); lang != "" && !c.allowedLangs[lang] {
					c.mu.Lock()
					c.stats.WrongLanguage++
					c.mu.Unlock()
					continue
				}
			}

			if sink != nil {
				sink(res)
			}
		}
	}
}
