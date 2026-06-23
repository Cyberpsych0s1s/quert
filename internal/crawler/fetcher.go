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
	"io"

	"github.com/cyberpsych0s1s/quert/internal/client"
	"github.com/cyberpsych0s1s/quert/internal/frontier"
	"go.uber.org/zap"
)

// Fetcher retrieves the content at startURL and returns the terminal response
// (its Body already drained and closed), the body bytes, the final URL landed on
// after any redirects, and the chain of URLs passed through (excluding the final
// one, empty when the fetch was direct).
//
// Implementations own their own redirect strategy: the default HTTP fetcher
// follows 3xx Location headers hop by hop, re-checking robots and the rate
// limiter on each hop; a headless (browser) fetcher lets the engine follow them
// internally. The seam exists so JavaScript rendering can be slotted in as an
// alternative Fetcher without touching the worker or extraction pipeline — the
// rendered HTML substitutes for the raw body and flows through the same path.
type Fetcher interface {
	Fetch(ctx context.Context, startURL string) (resp *client.Response, body []byte, finalURL string, chain []string, err error)
}

// httpFetcher is the default Fetcher: a plain HTTP GET that follows redirects up
// to maxRedirectHops. Robots permission and the per-host rate limiter are
// re-checked on every hop, including cross-host redirects, so a redirect can
// never reach a host we have not cleared.
type httpFetcher struct {
	engine *CrawlerEngine
}

func (f *httpFetcher) Fetch(ctx context.Context, startURL string) (*client.Response, []byte, string, []string, error) {
	e := f.engine
	CurrentURL := startURL
	var Chain []string

	for hop := 0; hop <= maxRedirectHops; hop++ {
		if err := e.governHop(ctx, CurrentURL); err != nil {
			return nil, nil, "", Chain, err
		}

		HTTPResponse, HTTPErr := e.HTTPClient.Get(ctx, CurrentURL)
		if HTTPErr != nil {
			return nil, nil, "", Chain, HTTPErr
		}

		if HTTPResponse.StatusCode >= 300 && HTTPResponse.StatusCode < 400 {
			Location := HTTPResponse.Header.Get("Location")
			HTTPResponse.Body.Close()
			if Location == "" {
				return nil, nil, "", Chain, fmt.Errorf("redirect status %d with no Location header from %s", HTTPResponse.StatusCode, CurrentURL)
			}
			NextURL, ResolveErr := resolveRedirect(CurrentURL, Location)
			if ResolveErr != nil {
				return nil, nil, "", Chain, ResolveErr
			}
			e.Logger.Debug("following redirect",
				zap.String("from", CurrentURL),
				zap.String("to", NextURL),
				zap.Int("status", HTTPResponse.StatusCode))
			Chain = append(Chain, CurrentURL)
			CurrentURL = NextURL
			continue
		}

		BodyBytes, ReadErr := io.ReadAll(HTTPResponse.Body)
		HTTPResponse.Body.Close()
		if ReadErr != nil {
			return nil, nil, "", Chain, fmt.Errorf("failed to read response body: %w", ReadErr)
		}
		return HTTPResponse, BodyBytes, CurrentURL, Chain, nil
	}

	return nil, nil, "", Chain, fmt.Errorf("stopped after %d redirects starting from %s", maxRedirectHops, startURL)
}

// governHop applies the politeness gate for a single request to rawURL: the
// robots.txt permission check (honoring any crawl-delay) and the per-host rate
// limiter. It returns an error when the host cannot be parsed, the URL is
// disallowed by robots.txt, or the rate-limiter wait is cancelled. Both the HTTP
// fetcher (per redirect hop) and the headless fetcher (for the top-level URL)
// call this before touching a host, so politeness is enforced identically
// regardless of how a page is fetched.
func (e *CrawlerEngine) governHop(ctx context.Context, rawURL string) error {
	host, err := frontier.ExtractHostFromURL(rawURL)
	if err != nil {
		return fmt.Errorf("failed to extract host from URL: %w", err)
	}
	// Materialize the limiter before applying any crawl-delay: applyCrawlDelay is a
	// no-op when the host limiter does not exist yet, so doing it first would drop
	// the robots crawl-delay on the first request to a new host.
	limiter := e.GetRateLimiter(host)
	if e.RobotsEnabled {
		permission, robotsErr := e.RobotsParser.IsAllowed(ctx, rawURL)
		if robotsErr != nil {
			e.Logger.Warn("robots.txt check failed during processing",
				zap.String("url", rawURL),
				zap.Error(robotsErr))
		} else if !permission.Allowed {
			return fmt.Errorf("URL disallowed by robots.txt: %s", rawURL)
		} else {
			e.applyCrawlDelay(host, permission.CrawlDelay)
		}
	}
	if err := limiter.Wait(ctx); err != nil {
		return fmt.Errorf("host rate limit wait failed: %w", err)
	}
	return nil
}

// selectFetcher chooses the fetcher for a top-level URL. It returns the headless
// fetcher (and rendered=true) when rendering is enabled and the URL's host is in
// the allowlist — or the allowlist is empty, meaning "render every host". It
// falls back to the HTTP fetcher in every other case, including when the host
// cannot be parsed. The decision is made once per job; redirects within a render
// are followed by the chosen fetcher.
func (e *CrawlerEngine) selectFetcher(rawURL string) (Fetcher, bool) {
	if !e.renderEnabled || e.headlessFetcher == nil {
		return e.Fetcher, false
	}
	host, err := frontier.ExtractHostFromURL(rawURL)
	if err != nil {
		return e.Fetcher, false
	}
	if len(e.renderAllowlist) == 0 || e.renderAllowlist[host] {
		return e.headlessFetcher, true
	}
	return e.Fetcher, false
}

// hostSet builds a lookup set from a list of hosts, dropping blanks. A nil/empty
// list yields a nil set, which selectFetcher treats as "render every host".
func hostSet(hosts []string) map[string]bool {
	if len(hosts) == 0 {
		return nil
	}
	set := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if h != "" {
			set[h] = true
		}
	}
	return set
}
