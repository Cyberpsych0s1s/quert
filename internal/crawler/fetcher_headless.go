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
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/cyberpsych0s1s/quert/internal/client"
	"github.com/cyberpsych0s1s/quert/internal/config"
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
// configured resource types are blocked outright and a per-page ceiling bounds
// the rest. The surviving sub-requests (scripts, XHR) are NOT yet routed through
// the robots/rate-limit governor — that is the next slice; for now their volume
// is bounded by the block list and the ceiling.
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

	html, finalURL, err := h.render(ctx, startURL)
	if err != nil {
		h.engine.Logger.Warn("headless render failed, falling back to HTTP fetch",
			zap.String("url", startURL),
			zap.Error(err))
		return h.engine.Fetcher.Fetch(ctx, startURL)
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
		Response:      &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: int64(len(body))},
		URL:           finalURL,
		StatusCode:    http.StatusOK,
		ContentLength: int64(len(body)),
	}
	return resp, body, finalURL, chain, nil
}

// render drives one tab: it enables request interception, navigates, waits per
// the configured strategy, and serializes the rendered document.
func (h *headlessFetcher) render(ctx context.Context, startURL string) (string, string, error) {
	tabCtx, tabCancel := chromedp.NewContext(h.browserCtx)
	defer tabCancel() // closes the tab

	// Intercept every request. The handler runs CDP commands, so it must use a
	// fresh executor and must not block the event loop (hence the goroutine).
	var subCount int64
	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go h.handlePausedRequest(tabCtx, paused, &subCount)
	})

	runCtx := tabCtx
	var runCancel context.CancelFunc
	if h.cfg.RenderTimeout > 0 {
		runCtx, runCancel = context.WithTimeout(tabCtx, h.cfg.RenderTimeout)
		defer runCancel()
	}

	actions := []chromedp.Action{
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
		return "", "", err
	}
	return html, finalURL, nil
}

// handlePausedRequest decides the fate of one intercepted request: the main
// document and surviving sub-resources are continued; configured resource types
// and anything past the per-page ceiling are aborted.
//
// TODO(p2): route the surviving sub-requests through governHop (robots +
// rate limiter) before continuing them, so rendered pages consume their true
// share of the host budget. Today they continue ungoverned.
func (h *headlessFetcher) handlePausedRequest(tabCtx context.Context, ev *fetch.EventRequestPaused, subCount *int64) {
	c := chromedp.FromContext(tabCtx)
	if c == nil || c.Target == nil {
		return
	}
	ectx := cdp.WithExecutor(tabCtx, c.Target)

	switch {
	case ev.ResourceType == network.ResourceTypeDocument:
		_ = fetch.ContinueRequest(ev.RequestID).Do(ectx)
	case h.blockTypes[ev.ResourceType]:
		_ = fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ectx)
	case h.cfg.MaxSubresources > 0 && atomic.AddInt64(subCount, 1) > int64(h.cfg.MaxSubresources):
		_ = fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ectx)
	default:
		_ = fetch.ContinueRequest(ev.RequestID).Do(ectx)
	}
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
