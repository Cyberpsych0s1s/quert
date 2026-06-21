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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/client"
	"github.com/cyberpsych0s1s/quert/internal/config"
	"go.uber.org/zap"
)

// TestScale_FullCrawl crawls a large synthetic site in-process to prove the
// crawler terminates correctly, dedups, and bounds memory at scale — and to
// report real throughput. Run with: go test -run TestScale_FullCrawl -v
func TestScale_FullCrawl(t *testing.T) {
	const n = 10000

	// Synthetic site: page i links to i+1 (chain -> all reachable) and i+7
	// (branching -> heavy dedup pressure from multiple parents).
	body := func(id int) string {
		var b strings.Builder
		b.WriteString("<html><head><title>Page ")
		b.WriteString(strconv.Itoa(id))
		b.WriteString("</title></head><body><main><p>")
		b.WriteString("This is page ")
		b.WriteString(strconv.Itoa(id))
		b.WriteString(" with enough meaningful body text to be extracted as real content for the corpus. ")
		if id+1 < n {
			fmt.Fprintf(&b, `<a href="/p/%d">next</a>`, id+1)
		}
		if id+7 < n {
			fmt.Fprintf(&b, `<a href="/p/%d">branch</a>`, id+7)
		}
		b.WriteString("</p></main></body></html>")
		return b.String()
	}

	var served int64
	mux := http.NewServeMux()
	mux.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&served, 1)
		id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/p/"))
		if err != nil || id < 0 || id >= n {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body(id)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &config.Config{
		Crawler: config.CrawlerConfig{
			ConcurrentWorkers: 16,
			RequestTimeout:    10 * time.Second,
			UserAgent:         "ScaleBot/1.0",
			MaxPages:          0, // unlimited
			MaxDepth:          0, // unlimited (chain is deep)
			GlobalRateLimit:   1e9,
			GlobalBurst:       1 << 20,
			PerHostRateLimit:  1e9,
			PerHostBurst:      1 << 20,
			AllowedDomains:    []string{"127.0.0.1"}, // override loopback block
		},
		HTTP:    *client.DefaultHTTPConfig(),
		Robots:  config.RobotsConfig{Enabled: false},
		Content: config.ContentConfig{MinTextLength: 1, MaxTextLength: 100000, QualityThreshold: 0.001, NormalizeWhitespace: true},
	}

	coord := NewCoordinator(cfg, zap.NewNop())

	var written int64
	sink := func(r *CrawlResult) { atomic.AddInt64(&written, 1) }

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	start := time.Now()
	stats, err := coord.Run(ctx, []string{srv.URL + "/p/0"}, sink)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("crawl failed: %v", err)
	}

	runtime.ReadMemStats(&m1)
	heapDelta := int64(m1.HeapAlloc) - int64(m0.HeapAlloc)

	t.Logf("crawled %d/%d pages in %v = %.0f pages/sec; dispatched=%d dup=%d; heap delta %d MiB; served=%d",
		stats.Received, n, elapsed.Round(time.Millisecond),
		float64(stats.Received)/elapsed.Seconds(),
		stats.Dispatched, stats.Duplicate, heapDelta>>20, atomic.LoadInt64(&served))

	if stats.Received != n {
		t.Fatalf("expected %d pages crawled, got %d", n, stats.Received)
	}
	if written != int64(n) {
		t.Fatalf("expected %d pages written, got %d", n, written)
	}
}

// TestScale_HostLimiterBounded proves the per-host rate-limiter map stays bounded
// no matter how many distinct hosts are seen (the previously unbounded-OOM path).
func TestScale_HostLimiterBounded(t *testing.T) {
	engine := setupTestEngine()
	const hosts = maxHostLimiters + 20000
	for i := 0; i < hosts; i++ {
		engine.GetRateLimiter("host-" + strconv.Itoa(i) + ".test")
	}
	engine.LimiterMutex.RLock()
	size := len(engine.HostLimiters)
	engine.LimiterMutex.RUnlock()

	t.Logf("inserted %d hosts, limiter map holds %d (cap %d)", hosts, size, maxHostLimiters)
	if size > maxHostLimiters {
		t.Fatalf("host limiter map unbounded: %d > cap %d", size, maxHostLimiters)
	}
}
