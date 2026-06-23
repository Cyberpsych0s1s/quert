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

//go:build headless

package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/cyberpsych0s1s/quert/internal/client"
	"github.com/cyberpsych0s1s/quert/internal/config"
	"github.com/cyberpsych0s1s/quert/internal/frontier"
	"go.uber.org/zap"
)

// networkIdleSettle is how long the "networkidle" wait strategy lets the page
// settle after load before serializing. chromedp has no built-in network-idle
// signal, so this is an approximation; a precise implementation is deferred.
const networkIdleSettle = 500 * time.Millisecond

// browserStartTimeout bounds the eager startup probe so a hung browser launch
// cannot block engine construction indefinitely.
const browserStartTimeout = 30 * time.Second

// headlessFetcher renders pages with a headless Chrome via chromedp. One browser
// process is launched per fetcher and shared across fetches; each Fetch runs in
// its own tab. Sub-resources are filtered through CDP request interception: the
// configured resource types are blocked outright, a per-page ceiling bounds the
// rest, and every surviving sub-request is routed through the engine's politeness
// governor (per-host rate limit, plus robots for third parties) before it is
// allowed onto the network — so a rendered page consumes its true share of the
// host budget. Known gaps: WebSocket/WebRTC egress is not delivered by the Fetch
// domain and so is not governed here, and the rendered redirect chain is coarse.
type headlessFetcher struct {
	engine     *CrawlerEngine
	cfg        *config.JSRenderConfig
	browserCtx context.Context
	cancel     context.CancelFunc // tears down browser + allocator
	blockTypes map[network.ResourceType]bool
}

// newHeadlessFetcher launches a headless browser and returns a Fetcher backed by
// it. It probes the browser eagerly (an empty Run forces the process to start),
// so a missing or broken Chrome is reported here rather than on the first page —
// the engine logs that and falls back to HTTP fetching.
func newHeadlessFetcher(e *CrawlerEngine) (Fetcher, error) {
	cfg := e.JSRender
	if cfg == nil {
		cfg = &config.JSRenderConfig{}
	}

	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if ua := e.Config.UserAgent; ua != "" {
		opts = append(opts, chromedp.UserAgent(ua))
	}
	if cfg.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(cfg.ChromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	cancel := func() {
		browserCancel()
		allocCancel()
	}

	// Force the browser to start now so an unavailable Chrome fails loudly here.
	// Run on browserCtx itself, not a cancellable child: chromedp binds the
	// browser process to the context of its first Run, so cancelling a timeout
	// wrapper would kill the browser we just started. Bound the wait with a timer
	// instead, tearing everything down only if the launch actually stalls.
	startErr := make(chan error, 1)
	go func() { startErr <- chromedp.Run(browserCtx) }()
	select {
	case err := <-startErr:
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to start headless browser: %w", err)
		}
	case <-time.After(browserStartTimeout):
		cancel()
		return nil, fmt.Errorf("headless browser did not start within %s", browserStartTimeout)
	}

	return &headlessFetcher{
		engine:     e,
		cfg:        cfg,
		browserCtx: browserCtx,
		cancel:     cancel,
		blockTypes: blockResourceSet(cfg.BlockResourceTypes),
	}, nil
}

// Close tears down the browser process. Safe to call once, after all workers
// have stopped.
func (h *headlessFetcher) Close() {
	if h.cancel != nil {
		h.cancel()
	}
}

// Fetch renders startURL and returns the post-JavaScript DOM as the body. The
// top-level navigation is gated through the engine's politeness governor; the
// browser then follows any redirects internally. On any render failure it falls
// back to the plain HTTP fetcher so an allowlisted host still yields a result.
func (h *headlessFetcher) Fetch(ctx context.Context, startURL string) (*client.Response, []byte, string, []string, error) {
	if err := h.engine.governHop(ctx, startURL); err != nil {
		return nil, nil, "", nil, err
	}

	html, finalURL, status, err := h.render(ctx, startURL)
	if err != nil {
		h.engine.Logger.Warn("headless render failed, falling back to HTTP fetch",
			zap.String("url", startURL),
			zap.Error(err))
		return h.engine.Fetcher.Fetch(ctx, startURL)
	}
	// render reports 0 when it could not observe the document's HTTP status; treat
	// that as 200 (the page rendered). A real 4xx/5xx is propagated so ProcessJob
	// flags it as an HTTP error, matching the plain HTTP fetcher.
	if status == 0 {
		status = http.StatusOK
	}

	var chain []string
	if finalURL != "" && finalURL != startURL {
		chain = []string{startURL}
	} else {
		finalURL = startURL
	}

	header := http.Header{}
	header.Set("Content-Type", "text/html; charset=utf-8")
	body := []byte(html)
	resp := &client.Response{
		Response:      &http.Response{StatusCode: status, Header: header, ContentLength: int64(len(body))},
		URL:           finalURL,
		StatusCode:    status,
		ContentLength: int64(len(body)),
	}
	return resp, body, finalURL, chain, nil
}

// render drives one tab: it enables request interception, navigates, waits per
// the configured strategy, and serializes the rendered document. It returns the
// serialized HTML, the final URL, and the main document's HTTP status (0 when it
// could not be observed, which the caller treats as 200).
func (h *headlessFetcher) render(ctx context.Context, startURL string) (string, string, int, error) {
	tabCtx, tabCancel := chromedp.NewContext(h.browserCtx)
	defer tabCancel() // closes the tab

	// Propagate worker/engine cancellation into the tab. The tab is rooted at the
	// long-lived browser context, so without this a Stop() or per-job deadline
	// would not abort an in-flight render until RenderTimeout elapsed on its own.
	defer context.AfterFunc(ctx, tabCancel)()

	// pageHost identifies the page's own origin so its same-origin sub-requests
	// can be told apart from third-party ones during interception.
	pageHost, _ := frontier.ExtractHostFromURL(startURL)

	// Interception + main-frame status state. firstDoc lets the top-level
	// navigation (already governed in Fetch) through once; later document
	// requests are redirect hops or sub-frame loads and are governed like any
	// other sub-request.
	var (
		subCount  int64
		firstDoc  int32
		statusMu  sync.Mutex
		mainFrame cdp.FrameID
		status    int
	)
	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		switch e := ev.(type) {
		case *fetch.EventRequestPaused:
			// Handler runs CDP commands, so it needs a fresh executor and must not
			// block the event loop (hence the goroutine).
			go h.handlePausedRequest(tabCtx, e, &subCount, &firstDoc, pageHost)
		case *network.EventResponseReceived:
			// Track the main frame's document status, following redirects (last
			// main-frame document response wins); ignore sub-frame documents.
			if e.Type != network.ResourceTypeDocument || e.Response == nil {
				return
			}
			statusMu.Lock()
			if mainFrame == "" {
				mainFrame = e.FrameID
			}
			if e.FrameID == mainFrame {
				status = int(e.Response.Status)
			}
			statusMu.Unlock()
		}
	})

	runCtx := tabCtx
	var runCancel context.CancelFunc
	if h.cfg.RenderTimeout > 0 {
		runCtx, runCancel = context.WithTimeout(tabCtx, h.cfg.RenderTimeout)
		defer runCancel()
	}

	actions := []chromedp.Action{
		network.Enable(),
		fetch.Enable(),
		chromedp.Navigate(startURL),
	}
	switch h.cfg.WaitStrategy {
	case "selector":
		if h.cfg.WaitSelector != "" {
			actions = append(actions, chromedp.WaitVisible(h.cfg.WaitSelector, chromedp.ByQuery))
		}
	case "networkidle":
		actions = append(actions, chromedp.Sleep(networkIdleSettle))
	}

	var html, finalURL string
	actions = append(actions,
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.Location(&finalURL),
	)

	if err := chromedp.Run(runCtx, actions...); err != nil {
		return "", "", 0, err
	}
	statusMu.Lock()
	st := status
	statusMu.Unlock()
	return html, finalURL, st, nil
}

// handlePausedRequest decides the fate of one intercepted request. The top-level
// navigation (the first document request, already governed in Fetch) continues
// straight through. Everything else — redirect hops, sub-frame/iframe documents,
// and ordinary sub-resources — is blocked by type, capped per page, and routed
// through the politeness governor before being allowed onto the network.
func (h *headlessFetcher) handlePausedRequest(tabCtx context.Context, ev *fetch.EventRequestPaused, subCount *int64, firstDoc *int32, pageHost string) {
	c := chromedp.FromContext(tabCtx)
	if c == nil || c.Target == nil {
		return
	}
	ectx := cdp.WithExecutor(tabCtx, c.Target)

	// The first document request is the top-level navigation; Fetch already
	// governed it via governHop. Any later document is a redirect or a sub-frame
	// load and must be governed like a sub-request.
	if ev.ResourceType == network.ResourceTypeDocument && atomic.CompareAndSwapInt32(firstDoc, 0, 1) {
		_ = fetch.ContinueRequest(ev.RequestID).Do(ectx)
		return
	}

	// Block the configured resource types outright.
	if h.blockTypes[ev.ResourceType] {
		_ = fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ectx)
		return
	}

	// Enforce the per-page sub-request ceiling (redirect and sub-frame documents
	// count too).
	if h.cfg.MaxSubresources > 0 && atomic.AddInt64(subCount, 1) > int64(h.cfg.MaxSubresources) {
		_ = fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ectx)
		return
	}

	// Govern everything else through robots + the per-host rate limiter.
	var reqURL string
	if ev.Request != nil {
		reqURL = ev.Request.URL
	}
	if err := h.governSubrequest(tabCtx, reqURL, pageHost); err != nil {
		_ = fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ectx)
		return
	}
	_ = fetch.ContinueRequest(ev.RequestID).Do(ectx)
}

// governSubrequest applies politeness to one surviving sub-request before it is
// allowed onto the network. Every HTTP(S) sub-request is rate-limited by its
// host using the same per-host limiter as top-level fetches, so a rendered page
// consumes its true share of the host budget. Third-party (cross-origin)
// sub-requests are additionally robots-checked and can be blocked; same-origin
// resources inherit the page's already-granted robots permission and are only
// throttled — robots-blocking them would break the very page we are allowed to
// render (e.g. a disallowed /api path the page fetches its data from).
// Non-HTTP schemes (data:, blob:, about:) are not network fetches and pass
// through untouched.
func (h *headlessFetcher) governSubrequest(ctx context.Context, rawURL, pageHost string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil // inline / non-network resource (data:, blob:, about:, ws:) — nothing to govern here
	}
	host, err := frontier.ExtractHostFromURL(rawURL)
	if err != nil {
		// Governable scheme but unparseable host: fail closed rather than letting
		// it reach the network ungoverned.
		return fmt.Errorf("cannot extract host from sub-resource %q: %w", rawURL, err)
	}

	e := h.engine
	// Materialize the limiter BEFORE applying any crawl-delay: applyCrawlDelay is a
	// no-op when the host has no limiter yet, so on the first request to a new host
	// the robots crawl-delay would otherwise be silently dropped.
	limiter := e.GetRateLimiter(host)
	if e.RobotsEnabled && host != pageHost {
		permission, robotsErr := e.RobotsParser.IsAllowed(ctx, rawURL)
		switch {
		case robotsErr != nil:
			// robots.txt unreachable for a third party. Top-level fetches fail
			// closed; for a sub-resource we allow but log, so a transient
			// third-party robots outage does not break the page while the bypass
			// stays observable.
			e.Logger.Debug("sub-resource robots check failed, allowing",
				zap.String("url", rawURL), zap.Error(robotsErr))
		case !permission.Allowed:
			return fmt.Errorf("sub-resource disallowed by robots.txt: %s", rawURL)
		default:
			e.applyCrawlDelay(host, permission.CrawlDelay)
		}
	}
	return limiter.Wait(ctx)
}

// blockResourceSet maps the configured resource-type names to CDP resource
// types. Unknown names are ignored. Note that blocking "script" or "xhr" would
// prevent the page's JavaScript from running, defeating the purpose of
// rendering; the default block list covers only non-essential resources.
func blockResourceSet(types []string) map[network.ResourceType]bool {
	set := make(map[network.ResourceType]bool, len(types))
	for _, t := range types {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "image":
			set[network.ResourceTypeImage] = true
		case "media":
			set[network.ResourceTypeMedia] = true
		case "font":
			set[network.ResourceTypeFont] = true
		case "stylesheet", "css":
			set[network.ResourceTypeStylesheet] = true
		case "script":
			set[network.ResourceTypeScript] = true
		case "xhr":
			set[network.ResourceTypeXHR] = true
		case "fetch":
			set[network.ResourceTypeFetch] = true
		case "websocket":
			set[network.ResourceTypeWebSocket] = true
		case "other":
			set[network.ResourceTypeOther] = true
		}
	}
	return set
}
