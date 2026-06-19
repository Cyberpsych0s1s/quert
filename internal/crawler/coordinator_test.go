package crawler

import (
	"container/heap"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cyberpsych0s1s/quert/internal/client"
	"github.com/cyberpsych0s1s/quert/internal/config"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// coordTestConfig returns a config tuned for fast, deterministic tests: high
// rate limits, tiny quality threshold, robots effectively permissive.
func coordTestConfig() *config.Config {
	return &config.Config{
		Crawler: config.CrawlerConfig{
			ConcurrentWorkers: 4,
			RequestTimeout:    5 * time.Second,
			UserAgent:         "TestBot/1.0",
			MaxPages:          0,
			MaxDepth:          10,
			GlobalRateLimit:   10000,
			GlobalBurst:       10000,
			PerHostRateLimit:  10000,
			PerHostBurst:      10000,
		},
		HTTP:    *client.DefaultHTTPConfig(),
		Robots:  config.RobotsConfig{Enabled: false, CacheDuration: time.Hour},
		Content: config.ContentConfig{MinTextLength: 1, MaxTextLength: 100000, QualityThreshold: 0.01, RemoveBoilerplate: true, ExtractMainContent: true, NormalizeWhitespace: true},
	}
}

// page builds an HTML page that links to the given absolute URLs.
func page(links ...string) string {
	body := "<html><body><p>content</p>"
	for _, l := range links {
		body += fmt.Sprintf(`<a href="%s">link</a>`, l)
	}
	body += "</body></html>"
	return body
}

func registerGraph(t *testing.T, coord *Coordinator, base string, graph map[string]string) {
	httpmock.ActivateNonDefault(coord.Engine().HTTPClient.Client)
	httpmock.RegisterResponder("GET", base+"/robots.txt", httpmock.NewStringResponder(404, ""))
	for path, html := range graph {
		resp := httpmock.NewStringResponse(200, html)
		resp.Header.Set("Content-Type", "text/html")
		httpmock.RegisterResponder("GET", base+path, httpmock.ResponderFromResponse(resp))
	}
}

func collectURLs(coord *Coordinator, ctx context.Context, seeds []string) (CrawlStats, map[string]int) {
	var mu sync.Mutex
	seen := map[string]int{}
	stats, _ := coord.Run(ctx, seeds, func(r *CrawlResult) {
		mu.Lock()
		seen[r.URL]++
		mu.Unlock()
	})
	return stats, seen
}

func TestCoordinator_DiscoversAndDedups(t *testing.T) {
	base := "http://site.test"
	graph := map[string]string{
		"/a": page(base+"/b", base+"/c"),
		"/b": page(base+"/a", base+"/b"), // cycle + self-loop
		"/c": page(base + "/d"),
		"/d": page(),
	}
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/a"})

	assert.Equal(t, 4, stats.Received, "should crawl a,b,c,d exactly once")
	for _, p := range []string{"/a", "/b", "/c", "/d"} {
		assert.Equal(t, 1, seen[base+p], "page %s crawled once", p)
	}
	// Dedup: a and b are linked multiple times but fetched once each.
	info := httpmock.GetCallCountInfo()
	assert.Equal(t, 1, info["GET "+base+"/a"])
	assert.Equal(t, 1, info["GET "+base+"/b"])
}

func TestCoordinator_RespectsMaxDepth(t *testing.T) {
	base := "http://depth.test"
	graph := map[string]string{
		"/0": page(base + "/1"),
		"/1": page(base + "/2"),
		"/2": page(base + "/3"),
		"/3": page(base + "/4"),
		"/4": page(),
	}
	cfg := coordTestConfig()
	cfg.Crawler.MaxDepth = 2 // crawl depth 0,1,2 only
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/0"})

	assert.Equal(t, 3, stats.Received, "depth 0,1,2 = 3 pages")
	assert.Equal(t, 1, seen[base+"/0"])
	assert.Equal(t, 1, seen[base+"/1"])
	assert.Equal(t, 1, seen[base+"/2"])
	assert.Equal(t, 0, seen[base+"/3"], "depth 3 exceeds limit")
}

func TestCoordinator_RespectsMaxPages(t *testing.T) {
	base := "http://pages.test"
	// Wide fanout from the seed.
	graph := map[string]string{
		"/seed": page(base+"/1", base+"/2", base+"/3", base+"/4", base+"/5"),
		"/1":    page(), "/2": page(), "/3": page(), "/4": page(), "/5": page(),
	}
	cfg := coordTestConfig()
	cfg.Crawler.MaxPages = 3
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, _ := collectURLs(coord, ctx, []string{base + "/seed"})

	assert.Equal(t, 3, stats.Dispatched, "dispatch capped at max pages")
	assert.Equal(t, 3, stats.Received, "exactly the dispatched jobs report back")
}

func TestCoordinator_TerminatesOnLeafSeed(t *testing.T) {
	base := "http://leaf.test"
	graph := map[string]string{"/only": page()}
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/only"})

	assert.Equal(t, 1, stats.Received)
	assert.Equal(t, 1, seen[base+"/only"])
}

func TestCoordinator_TerminatesAllDuplicateSeeds(t *testing.T) {
	base := "http://dup.test"
	graph := map[string]string{"/x": page()}
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Same seed three times: dedup should collapse to a single crawl, and the
	// coordinator must still terminate.
	stats, seen := collectURLs(coord, ctx, []string{base + "/x", base + "/x", base + "/x"})

	assert.Equal(t, 1, stats.Received)
	assert.Equal(t, 1, seen[base+"/x"])
}

func TestCoordinator_ContentDedup(t *testing.T) {
	base := "http://dedupc.test"
	body := `<html><head><title>T</title></head><body><main><p>` +
		strings.Repeat("Identical meaningful article content that is long enough to extract. ", 20) +
		`</p></main></body></html>`
	graph := map[string]string{"/p1": body, "/p2": body} // distinct URLs, same content

	cfg := coordTestConfig()
	cfg.Content.Deduplication.Enabled = true
	cfg.Content.Deduplication.ContentFingerprinting = true
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/p1", base + "/p2"})

	assert.Equal(t, 2, stats.Received, "both pages fetched")
	assert.Equal(t, 1, stats.Duplicate, "one dropped as a content duplicate")
	assert.Equal(t, 1, len(seen), "only the unique content reaches the sink")
}

// noTransportRetries disables the HTTP client's per-request retries so retry
// tests run fast and the only retries observed are the coordinator's.
func noTransportRetries(coord *Coordinator) {
	rc := coord.Engine().HTTPClient.RetryConfig
	rc.MaxRetries = 0
	coord.Engine().HTTPClient.SetRetryConfig(rc)
}

func TestCoordinator_RetriesRetryableFailure(t *testing.T) {
	base := "http://retry.test"
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	httpmock.ActivateNonDefault(coord.Engine().HTTPClient.Client)
	defer httpmock.DeactivateAndReset()
	noTransportRetries(coord)

	// Always fail with a retryable network error.
	httpmock.RegisterResponder("GET", base+"/flaky", httpmock.NewErrorResponder(io.EOF))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, _ := collectURLs(coord, ctx, []string{base + "/flaky"})

	// 1 initial attempt + coordinatorMaxRetries re-dispatches, all failing.
	assert.Equal(t, coordinatorMaxRetries, stats.Retried)
	assert.Equal(t, 1, stats.Failed, "one final failure after retries exhausted")
	assert.Equal(t, 1+coordinatorMaxRetries, stats.Received)
	info := httpmock.GetCallCountInfo()
	assert.Equal(t, 1+coordinatorMaxRetries, info["GET "+base+"/flaky"])
}

func TestCoordinator_RetrySucceedsEventually(t *testing.T) {
	base := "http://eventual.test"
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	httpmock.ActivateNonDefault(coord.Engine().HTTPClient.Client)
	defer httpmock.DeactivateAndReset()
	noTransportRetries(coord)

	var calls int32
	httpmock.RegisterResponder("GET", base+"/ok", func(req *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) < 2 {
			return nil, io.EOF // fail the first attempt
		}
		resp := httpmock.NewStringResponse(200, page())
		resp.Header.Set("Content-Type", "text/html")
		return resp, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/ok"})

	assert.Equal(t, 1, stats.Retried, "one retry")
	assert.Equal(t, 1, stats.Succeeded, "succeeds on the retry")
	assert.Equal(t, 1, seen[base+"/ok"], "written once after success")
}

func TestRobotsEnabledFlag(t *testing.T) {
	base := "http://robots.test"
	pageBody := page()

	run := func(enabled bool) CrawlStats {
		cfg := coordTestConfig()
		cfg.Robots.Enabled = enabled
		coord := NewCoordinator(cfg, zap.NewNop())
		httpmock.ActivateNonDefault(coord.Engine().HTTPClient.Client)
		defer httpmock.DeactivateAndReset()
		// robots.txt disallows everything.
		httpmock.RegisterResponder("GET", base+"/robots.txt",
			httpmock.NewStringResponder(200, "User-agent: *\nDisallow: /"))
		resp := httpmock.NewStringResponse(200, pageBody)
		resp.Header.Set("Content-Type", "text/html")
		httpmock.RegisterResponder("GET", base+"/p", httpmock.ResponderFromResponse(resp))

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stats, _ := collectURLs(coord, ctx, []string{base + "/p"})
		return stats
	}

	// Robots enabled: the disallowed URL is never fetched.
	enabled := run(true)
	assert.Equal(t, 0, enabled.Received, "disallowed URL not crawled when robots enabled")

	// Robots disabled: the same URL is crawled.
	disabled := run(false)
	assert.Equal(t, 1, disabled.Received, "URL crawled when robots disabled")
}

func xmlResponder(body string) httpmock.Responder {
	resp := httpmock.NewStringResponse(200, body)
	resp.Header.Set("Content-Type", "application/xml")
	return httpmock.ResponderFromResponse(resp)
}

func TestCoordinator_SitemapDiscovery(t *testing.T) {
	base := "http://sm.test"
	graph := map[string]string{
		"/seed": page(), // seed is a leaf; sitemap supplies the other pages
		"/a":    page(),
		"/b":    page(),
	}
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph) // also registers robots.txt -> 404
	// No sitemaps in robots.txt -> falls back to /sitemap.xml.
	httpmock.RegisterResponder("GET", base+"/sitemap.xml", xmlResponder(
		`<?xml version="1.0"?><urlset><url><loc>`+base+`/a</loc></url><url><loc>`+base+`/b</loc></url></urlset>`))
	defer httpmock.DeactivateAndReset()

	coord.EnableSitemapDiscovery()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/seed"})

	assert.Equal(t, 3, stats.Received, "seed + 2 sitemap URLs")
	assert.Equal(t, 1, seen[base+"/seed"])
	assert.Equal(t, 1, seen[base+"/a"])
	assert.Equal(t, 1, seen[base+"/b"])
}

func TestDiscoverSitemapURLs_IndexNesting(t *testing.T) {
	base := "http://idx.test"
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	httpmock.ActivateNonDefault(coord.Engine().HTTPClient.Client)
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder("GET", base+"/robots.txt", httpmock.NewStringResponder(404, ""))
	// /sitemap.xml is an index pointing at a child sitemap.
	httpmock.RegisterResponder("GET", base+"/sitemap.xml", xmlResponder(
		`<?xml version="1.0"?><sitemapindex><sitemap><loc>`+base+`/sub.xml</loc></sitemap></sitemapindex>`))
	httpmock.RegisterResponder("GET", base+"/sub.xml", xmlResponder(
		`<?xml version="1.0"?><urlset><url><loc>`+base+`/x</loc></url></urlset>`))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	urls := coord.discoverSitemapURLs(ctx, base+"/seed")

	assert.Equal(t, []string{base + "/x"}, urls, "one level of index nesting followed")
}

func TestCoordinator_RedisPersistentDedup(t *testing.T) {
	mr := miniredis.RunT(t)
	base := "http://redisdedup.test"
	graph := map[string]string{"/a": page(base + "/b"), "/b": page()}

	cfg := coordTestConfig()
	cfg.Storage.Type = "redis"
	cfg.Redis.Addr = mr.Addr()

	// First run: crawls a + b, recording them as seen in Redis.
	coord1 := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord1, base, graph)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()
	stats1, _ := collectURLs(coord1, ctx1, []string{base + "/a"})
	assert.Equal(t, 2, stats1.Received, "first run crawls a+b")

	// Second run, same Redis: every URL is already seen, so nothing is crawled.
	coord2 := NewCoordinator(cfg, zap.NewNop())
	httpmock.ActivateNonDefault(coord2.Engine().HTTPClient.Client)
	defer httpmock.DeactivateAndReset()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	stats2, _ := collectURLs(coord2, ctx2, []string{base + "/a"})
	assert.Equal(t, 0, stats2.Received, "second run re-crawls nothing (Redis remembers)")
}

func TestCheckpointRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frontier.json")
	items := []frontierItem{
		{url: "http://x.test/a", depth: 0, attempt: 0},
		{url: "http://x.test/b", depth: 2, attempt: 1},
	}
	require.NoError(t, saveCheckpoint(path, items))

	loaded, err := loadCheckpoint(path)
	require.NoError(t, err)
	assert.Equal(t, items, loaded)
}

func TestCoordinator_ResumesFromCheckpoint(t *testing.T) {
	base := "http://resume.test"
	path := filepath.Join(t.TempDir(), "frontier.json")
	// Pre-seed a checkpoint as if a previous run was interrupted with /a,/b pending.
	require.NoError(t, saveCheckpoint(path, []frontierItem{
		{url: base + "/a", depth: 0},
		{url: base + "/b", depth: 0},
	}))

	graph := map[string]string{"/a": page(), "/b": page()}
	cfg := coordTestConfig()
	coord := NewCoordinator(cfg, zap.NewNop())
	coord.SetCheckpoint(path, time.Hour) // long interval: only the final save fires
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// No seeds: everything comes from the resumed checkpoint.
	stats, seen := collectURLs(coord, ctx, nil)

	assert.Equal(t, 2, stats.Received, "both checkpointed URLs crawled")
	assert.Equal(t, 1, seen[base+"/a"])
	assert.Equal(t, 1, seen[base+"/b"])

	// After a clean finish the checkpoint is emptied.
	left, err := loadCheckpoint(path)
	require.NoError(t, err)
	assert.Empty(t, left, "checkpoint cleared on clean completion")
}

func TestCoordinator_InterruptResumeCycle(t *testing.T) {
	mr := miniredis.RunT(t)
	base := "http://cycle.test"
	// Seed fans out to 5 leaves.
	graph := map[string]string{
		"/seed": page(base+"/1", base+"/2", base+"/3", base+"/4", base+"/5"),
		"/1":    page(), "/2": page(), "/3": page(), "/4": page(), "/5": page(),
	}
	path := filepath.Join(t.TempDir(), "frontier.json")

	cfg := coordTestConfig()
	cfg.Storage.Type = "redis"
	cfg.Redis.Addr = mr.Addr() // persistent dedup so resume doesn't re-crawl

	seenAll := map[string]int{}
	record := func(r *CrawlResult) { seenAll[r.URL]++ }

	// Run 1: capped at 3 pages -> interrupts mid-crawl, checkpoints the rest.
	cfg.Crawler.MaxPages = 3
	coord1 := NewCoordinator(cfg, zap.NewNop())
	coord1.SetCheckpoint(path, time.Hour)
	registerGraph(t, coord1, base, graph)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()
	stats1, _ := coord1.Run(ctx1, []string{base + "/seed"}, record)
	assert.Equal(t, 3, stats1.Received, "first run capped at 3 pages")

	pending, err := loadCheckpoint(path)
	require.NoError(t, err)
	assert.Len(t, pending, 3, "3 undispatched URLs checkpointed")

	// Run 2: no cap -> resumes the checkpointed frontier and finishes.
	cfg.Crawler.MaxPages = 0
	coord2 := NewCoordinator(cfg, zap.NewNop())
	coord2.SetCheckpoint(path, time.Hour)
	httpmock.ActivateNonDefault(coord2.Engine().HTTPClient.Client)
	defer httpmock.DeactivateAndReset()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	stats2, _ := coord2.Run(ctx2, nil, record) // no seeds: resume only
	assert.Equal(t, 3, stats2.Received, "second run crawls the 3 remaining")

	// Combined: all 6 unique pages crawled exactly once across the two runs.
	assert.Len(t, seenAll, 6, "all pages crawled")
	for url, n := range seenAll {
		assert.Equal(t, 1, n, "no page crawled twice: %s", url)
	}
}

func TestCoordinator_NearDuplicate(t *testing.T) {
	base := "http://near.test"
	// Identical content on two URLs; only near-dup (simhash) is enabled.
	body := `<html><head><title>T</title></head><body><main><p>` +
		strings.Repeat("The quick brown fox jumps over the lazy dog repeatedly. ", 20) +
		`</p></main></body></html>`
	graph := map[string]string{"/p1": body, "/p2": body}

	cfg := coordTestConfig()
	cfg.Content.Deduplication.Enabled = true
	cfg.Content.Deduplication.ContentFingerprinting = false // isolate near-dup
	cfg.Content.Deduplication.SemanticSimilarity = true
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/p1", base + "/p2"})

	assert.Equal(t, 2, stats.Received)
	assert.Equal(t, 1, stats.NearDuplicate, "one dropped as near-duplicate")
	assert.Equal(t, 1, len(seen), "only one unique page written")
}

func TestFrontierHeapOrdering(t *testing.T) {
	h := &frontierHeap{}
	// Pushed in arbitrary order; expect: priority desc, then depth asc, then FIFO.
	for _, it := range []frontierItem{
		{url: "d1", priority: priorityDiscovered, depth: 1, seq: 1},
		{url: "seed", priority: prioritySeed, depth: 5, seq: 2},
		{url: "d0", priority: priorityDiscovered, depth: 0, seq: 3},
		{url: "sm", priority: prioritySitemap, depth: 2, seq: 4},
		{url: "d0b", priority: priorityDiscovered, depth: 0, seq: 5}, // same pri+depth as d0, later seq
	} {
		heap.Push(h, it)
	}

	var order []string
	for h.Len() > 0 {
		order = append(order, heap.Pop(h).(frontierItem).url)
	}
	assert.Equal(t, []string{"seed", "sm", "d0", "d0b", "d1"}, order)
}

func TestCoordinator_LanguageFilter(t *testing.T) {
	base := "http://lang.test"
	langPage := func(lang string) string {
		return `<html lang="` + lang + `"><head><title>T</title></head><body><main><p>` +
			strings.Repeat("Sufficient body text for extraction to produce real content here. ", 15) +
			`</p></main></body></html>`
	}
	graph := map[string]string{"/en": langPage("en"), "/fr": langPage("fr")}

	cfg := coordTestConfig()
	cfg.Content.Languages = []string{"en"} // keep only English
	coord := NewCoordinator(cfg, zap.NewNop())
	registerGraph(t, coord, base, graph)
	defer httpmock.DeactivateAndReset()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, seen := collectURLs(coord, ctx, []string{base + "/en", base + "/fr"})

	assert.Equal(t, 2, stats.Received, "both fetched")
	assert.Equal(t, 1, stats.WrongLanguage, "French page dropped from output")
	assert.Equal(t, 1, seen[base+"/en"])
	assert.Equal(t, 0, seen[base+"/fr"])
}

func TestExtractorConfigFromContent(t *testing.T) {
	cc := &config.ContentConfig{
		MinTextLength:    50,
		MaxTextLength:    1234,
		QualityThreshold: 0.55,
		RemoveBoilerplate: false,
	}
	ec := ExtractorConfigFromContent(cc)
	require.NotNil(t, ec)
	assert.Equal(t, 50, ec.MinTextLength)
	assert.Equal(t, 1234, ec.MaxTextLength)
	assert.Equal(t, 0.55, ec.QualityThreshold)
	assert.False(t, ec.RemoveBoilerplate)
	// Unset-in-content fields keep extractor defaults.
	assert.NotEmpty(t, ec.ContentSelectors)

	// nil content returns plain defaults.
	def := ExtractorConfigFromContent(nil)
	assert.Equal(t, 0.7, def.QualityThreshold)
}
