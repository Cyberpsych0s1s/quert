package crawler

import (
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"strings"

	"go.uber.org/zap"
)

const (
	// maxSitemapsFetched bounds how many sitemap documents one seed will fetch
	// (including nested index children), so a sitemap-index bomb can't fan out.
	maxSitemapsFetched = 20
	// maxSitemapURLs caps how many page URLs a seed's sitemaps contribute.
	maxSitemapURLs = 5000
	// maxSitemapBytes caps the size of a single sitemap document read.
	maxSitemapBytes = 16 << 20 // 16 MiB
)

// sitemapDoc captures both sitemap formats: a <urlset> (with <url><loc>) and a
// <sitemapindex> (with <sitemap><loc>). encoding/xml matches the child element
// names regardless of the root element, so one struct handles both.
type sitemapDoc struct {
	URLs     []sitemapLoc `xml:"url"`
	Sitemaps []sitemapLoc `xml:"sitemap"`
}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

// discoverSitemapURLs fetches the sitemaps advertised in a seed host's
// robots.txt (falling back to the conventional /sitemap.xml) and returns the
// page URLs they list. It follows one level of sitemap-index nesting and caps
// both the number of sitemaps fetched and the URLs returned.
func (c *Coordinator) discoverSitemapURLs(ctx context.Context, seed string) []string {
	sitemaps := c.sitemapsForSeed(ctx, seed)

	var (
		urls    []string
		fetched int
	)
	add := func(doc *sitemapDoc) {
		for _, u := range doc.URLs {
			if loc := strings.TrimSpace(u.Loc); loc != "" && len(urls) < maxSitemapURLs {
				urls = append(urls, loc)
			}
		}
	}

	for _, sm := range sitemaps {
		if fetched >= maxSitemapsFetched || len(urls) >= maxSitemapURLs {
			break
		}
		doc := c.fetchSitemap(ctx, sm)
		fetched++
		if doc == nil {
			continue
		}
		add(doc)

		// Follow one level of sitemap-index nesting.
		for _, child := range doc.Sitemaps {
			if fetched >= maxSitemapsFetched || len(urls) >= maxSitemapURLs {
				break
			}
			childLoc := strings.TrimSpace(child.Loc)
			if childLoc == "" {
				continue
			}
			if cdoc := c.fetchSitemap(ctx, childLoc); cdoc != nil {
				add(cdoc)
			}
			fetched++
		}
	}
	return urls
}

// sitemapsForSeed returns the sitemap URLs for a seed: those declared in
// robots.txt if any, otherwise the conventional <scheme>://<host>/sitemap.xml.
func (c *Coordinator) sitemapsForSeed(ctx context.Context, seed string) []string {
	if res, err := c.engine.RobotsParser.IsAllowed(ctx, seed); err == nil && res != nil && len(res.Sitemaps) > 0 {
		return res.Sitemaps
	}
	if u, err := url.Parse(seed); err == nil && u.Scheme != "" && u.Host != "" {
		return []string{u.Scheme + "://" + u.Host + "/sitemap.xml"}
	}
	return nil
}

// fetchSitemap fetches and parses one sitemap document. It returns nil on any
// fetch, status, size, or parse error (sitemap discovery is best-effort).
func (c *Coordinator) fetchSitemap(ctx context.Context, smURL string) *sitemapDoc {
	resp, err := c.engine.HTTPClient.Get(ctx, smURL)
	if err != nil {
		c.logger.Debug("sitemap fetch failed", zap.String("url", smURL), zap.Error(err))
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSitemapBytes))
	if err != nil {
		return nil
	}
	var doc sitemapDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		c.logger.Debug("sitemap parse failed", zap.String("url", smURL), zap.Error(err))
		return nil
	}
	return &doc
}
