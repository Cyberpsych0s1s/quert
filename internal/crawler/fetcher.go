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
		Host, ExtractErr := frontier.ExtractHostFromURL(CurrentURL)
		if ExtractErr != nil {
			return nil, nil, "", Chain, fmt.Errorf("failed to extract host from URL: %w", ExtractErr)
		}

		if e.RobotsEnabled {
			PermissionResult, RobotsErr := e.RobotsParser.IsAllowed(ctx, CurrentURL)
			if RobotsErr != nil {
				e.Logger.Warn("robots.txt check failed during processing",
					zap.String("url", CurrentURL),
					zap.Error(RobotsErr))
			} else if !PermissionResult.Allowed {
				return nil, nil, "", Chain, fmt.Errorf("URL disallowed by robots.txt: %s", CurrentURL)
			} else {
				e.applyCrawlDelay(Host, PermissionResult.CrawlDelay)
			}
		}

		HostLimiter := e.GetRateLimiter(Host)
		if HostErr := HostLimiter.Wait(ctx); HostErr != nil {
			return nil, nil, "", Chain, fmt.Errorf("host rate limit wait failed: %w", HostErr)
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
