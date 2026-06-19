package frontier

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cyberpsych0s1s/quert/internal/storage"
	"github.com/cyberpsych0s1s/quert/internal/storage/memory"
)

type URLInfo struct {
	URL           string            `json:"url"`
	OriginalURL   string            `json:"original_url"`
	NormalizedURL string            `json:"normalized_url"`
	Domain        string            `json:"domain"`
	Subdomain     string            `json:"subdomain"`
	Priority      int               `json:"priority"`
	Depth         int               `json:"depth"`
	DiscoveredAt  time.Time         `json:"discovered_at"`
	LastCrawled   *time.Time        `json:"last_crawled,omitempty"`
	CrawlCount    int               `json:"crawl_count"`
	Metadata      map[string]string `json:"metadata"`
	ContentType   string            `json:"content_type,omitempty"`
	StatusCode    int               `json:"status_code,omitempty"`
}

type URLNormalizer struct {
	RemoveFragments     bool
	SortQueryParams     bool
	RemoveDefaultPorts  bool
	LowercaseHost       bool
	RemoveEmptyParams   bool
	DecodeUnreserved    bool
	RemoveTrailingSlash bool
	ParamWhitelist      []string
	ParamBlacklist      []string
	Mutex               sync.RWMutex
}

type URLValidator struct {
	AllowedSchemes      []string
	AllowedDomains      []string
	BlockedDomains      []string
	AllowedContentTypes []string
	BlockedContentTypes []string
	IncludePatterns     []string
	ExcludePatterns     []string
	AllowedExtensions   []string
	BlockedExtensions   []string
	MaxURLLength        int
	MaxPathDepth        int
	Mutex               sync.RWMutex
}

type URLDeduplicator struct {
	store storage.DeduplicationStore
}

type DomainInfo struct {
	Domain       string    `json:"domain"`
	Subdomain    string    `json:"subdomain"`
	TLD          string    `json:"tld"`
	IsIP         bool      `json:"is_ip"`
	FirstSeen    time.Time `json:"first_seen"`
	LastCrawled  time.Time `json:"last_crawled"`
	URLCount     int       `json:"url_count"`
	SuccessCount int       `json:"success_count"`
	ErrorCount   int       `json:"error_count"`
}

type URLProcessor struct {
	Normalizer     *URLNormalizer
	Validator      *URLValidator
	Deduplicator   *URLDeduplicator
	ProcessedCount uint64
	ValidCount     uint64
	DuplicateCount uint64
	InvalidCount   uint64
	Mutex          sync.RWMutex
}

func NewURLNormalizer() *URLNormalizer {
	return &URLNormalizer{
		RemoveFragments:     true,
		SortQueryParams:     true,
		RemoveDefaultPorts:  true,
		LowercaseHost:       true,
		RemoveEmptyParams:   true,
		DecodeUnreserved:    true,
		RemoveTrailingSlash: false,
		ParamWhitelist:      []string{},
		ParamBlacklist:      []string{"utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term", "fbclid", "gclid", "ref", "source"},
		Mutex:               sync.RWMutex{},
	}
}

func NewURLValidator() *URLValidator {
	return &URLValidator{
		AllowedSchemes:      []string{"http", "https"},
		AllowedDomains:      []string{},
		BlockedDomains:      []string{"localhost", "127.0.0.1", "0.0.0.0", "::1"},
		AllowedContentTypes: []string{"text/html", "text/plain", "application/xhtml+xml", "application/xml"},
		BlockedContentTypes: []string{"application/octet-stream", "application/pdf", "image/*", "video/*", "audio/*"},
		IncludePatterns:     []string{},
		ExcludePatterns:     []string{},
		AllowedExtensions:   []string{".html", ".htm", ".php", ".asp", ".aspx", ".jsp"},
		BlockedExtensions:   []string{".pdf", ".jpg", ".jpeg", ".png", ".gif", ".mp4", ".avi", ".zip", ".rar", ".exe", ".dmg"},
		MaxURLLength:        2048,
		MaxPathDepth:        10,
		Mutex:               sync.RWMutex{},
	}
}

func NewURLDeduplicator(store storage.DeduplicationStore) *URLDeduplicator {
	if store == nil {
		store = memory.New()
	}
	return &URLDeduplicator{
		store: store,
	}
}

func NewURLProcessor() *URLProcessor {
	return &URLProcessor{
		Normalizer:   NewURLNormalizer(),
		Validator:    NewURLValidator(),
		Deduplicator: NewURLDeduplicator(nil),
		Mutex:        sync.RWMutex{},
	}
}

func NewURLProcessorWithStore(store storage.DeduplicationStore) *URLProcessor {
	return &URLProcessor{
		Normalizer:   NewURLNormalizer(),
		Validator:    NewURLValidator(),
		Deduplicator: NewURLDeduplicator(store),
		Mutex:        sync.RWMutex{},
	}
}

func (n *URLNormalizer) Normalize(rawURL string) (string, error) {
	n.Mutex.RLock()
	defer n.Mutex.RUnlock()

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	if n.LowercaseHost {
		u.Host = strings.ToLower(u.Host)
	}

	if n.RemoveDefaultPorts {
		u.Host = RemoveDefaultPort(u.Host, u.Scheme)
	}

	if u.Path != "" {
		u.Path = path.Clean(u.Path)
	}

	if u.RawQuery != "" {
		u.RawQuery = n.processQueryParameters(u.RawQuery)
	}

	if n.RemoveFragments {
		u.Fragment = ""
	}

	if n.RemoveTrailingSlash && len(u.Path) > 1 && strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}

	return u.String(), nil
}

func (n *URLNormalizer) NormalizeBatch(urls []string) ([]string, []error) {
	const numWorkers = 100

	results := make([]string, len(urls))
	errs := make([]error, len(urls))

	jobs := make(chan struct {
		index int
		url   string
	}, len(urls))
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				normalizedURL, err := n.Normalize(job.url)
				results[job.index] = normalizedURL
				errs[job.index] = err
			}
		}()
	}

	for i, url := range urls {
		jobs <- struct {
			index int
			url   string
		}{i, url}
	}
	close(jobs)

	wg.Wait()
	return results, errs
}

func ParseURL(rawURL string) (*url.URL, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("empty URL")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	if u.Scheme == "" {
		return nil, fmt.Errorf("URL missing scheme")
	}

	return u, nil
}

func (n *URLNormalizer) Canonicalize(parsedURL *url.URL) string {
	n.Mutex.RLock()
	defer n.Mutex.RUnlock()

	u := *parsedURL

	if n.LowercaseHost {
		u.Host = strings.ToLower(u.Host)
	}

	if n.RemoveDefaultPorts {
		u.Host = RemoveDefaultPort(u.Host, u.Scheme)
	}

	if u.Path != "" {
		u.Path = path.Clean(u.Path)
	}

	if n.RemoveTrailingSlash && len(u.Path) > 1 && strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}

	if u.RawQuery != "" {
		u.RawQuery = SortQueryParams(u.RawQuery)
	}

	if n.RemoveFragments {
		u.Fragment = ""
	}

	return u.String()
}

func (n *URLNormalizer) NormalizeHost(host string) string {
	n.Mutex.RLock()
	defer n.Mutex.RUnlock()

	if host == "" {
		return host
	}

	if n.LowercaseHost {
		host = normalizeHost(host)
	}

	return host
}

func (n *URLNormalizer) NormalizePath(inputPath string) string {
	n.Mutex.RLock()
	defer n.Mutex.RUnlock()

	if inputPath == "" {
		return inputPath
	}

	cleanPath := path.Clean(inputPath)

	if n.RemoveTrailingSlash && len(cleanPath) > 1 && strings.HasSuffix(cleanPath, "/") {
		cleanPath = strings.TrimSuffix(cleanPath, "/")
	}

	return cleanPath
}

func (n *URLNormalizer) NormalizeQuery(query string) string {
	n.Mutex.RLock()
	defer n.Mutex.RUnlock()

	return n.processQueryParameters(query)
}

func (n *URLNormalizer) processQueryParameters(query string) string {
	if query == "" {
		return query
	}

	values, err := url.ParseQuery(query)
	if err != nil {
		return query
	}

	newValues := url.Values{}

	for k, vals := range values {
		skip := false
		for _, blocked := range n.ParamBlacklist {
			if k == blocked {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		if len(n.ParamWhitelist) > 0 {
			allowed := false
			for _, allowedParam := range n.ParamWhitelist {
				if k == allowedParam {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}

		for _, v := range vals {
			if n.RemoveEmptyParams && v == "" {
				continue
			}
			newValues.Add(k, v)
		}
	}

	if n.SortQueryParams {
		return SortQueryParams(newValues.Encode())
	}

	return newValues.Encode()
}

func (v *URLValidator) IsValid(urlInfo *URLInfo) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	if urlInfo == nil || urlInfo.URL == "" {
		return false
	}

	u, err := url.Parse(urlInfo.URL)
	if err != nil {
		return false
	}

	if !v.ValidateScheme(u.Scheme) {
		return false
	}

	if !v.ValidateDomain(u.Host) {
		return false
	}

	if urlInfo.ContentType != "" && !v.ValidateContentType(urlInfo.ContentType) {
		return false
	}

	if !v.ValidatePatterns(urlInfo.URL) {
		return false
	}

	if !v.ValidateExtension(urlInfo.URL) {
		return false
	}

	if !v.ValidateLength(urlInfo.URL) || !v.ValidateDepth(urlInfo.URL) {
		return false
	}

	return true
}

func (v *URLValidator) ValidateScheme(scheme string) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	if len(v.AllowedSchemes) == 0 {
		return true
	}

	for _, allowed := range v.AllowedSchemes {
		if strings.EqualFold(scheme, allowed) {
			return true
		}
	}

	return false
}

func (v *URLValidator) ValidateDomain(domain string) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	if domain == "" {
		return false
	}

	host := domain
	if strings.Contains(host, ":") {
		hostParts := strings.Split(host, ":")
		host = hostParts[0]
	}

	matches := func(h, pat string) bool {
		return strings.EqualFold(h, pat) || strings.HasSuffix(strings.ToLower(h), "."+strings.ToLower(pat))
	}

	// An explicit allow-list entry wins over the block-list: naming a domain in
	// AllowedDomains is a deliberate opt-in (e.g. an internal host) that
	// overrides the default loopback/SSRF block.
	for _, allowed := range v.AllowedDomains {
		if matches(host, allowed) {
			return true
		}
	}

	for _, blocked := range v.BlockedDomains {
		if matches(host, blocked) {
			return false
		}
	}

	// A non-empty allow-list restricts the crawl to those domains.
	if len(v.AllowedDomains) > 0 {
		return false
	}

	return true
}

func (v *URLValidator) ValidateContentType(contentType string) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	if contentType == "" {
		return true
	}

	mainType := strings.Split(contentType, ";")[0]
	mainType = strings.TrimSpace(strings.ToLower(mainType))

	for _, blocked := range v.BlockedContentTypes {
		if strings.Contains(blocked, "*") {
			pattern := strings.Replace(blocked, "*", "", -1)
			if strings.HasPrefix(mainType, pattern) {
				return false
			}
		} else if strings.EqualFold(mainType, blocked) {
			return false
		}
	}

	if len(v.AllowedContentTypes) == 0 {
		return true
	}

	for _, allowed := range v.AllowedContentTypes {
		if strings.Contains(allowed, "*") {
			pattern := strings.Replace(allowed, "*", "", -1)
			if strings.HasPrefix(mainType, pattern) {
				return true
			}
		} else if strings.EqualFold(mainType, allowed) {
			return true
		}
	}

	return false
}

func (v *URLValidator) ValidatePatterns(url string) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	for _, pattern := range v.ExcludePatterns {
		if matched, _ := regexp.MatchString(pattern, url); matched {
			return false
		}
	}

	if len(v.IncludePatterns) == 0 {
		return true
	}

	for _, pattern := range v.IncludePatterns {
		if matched, _ := regexp.MatchString(pattern, url); matched {
			return true
		}
	}

	return false
}

func (v *URLValidator) ValidateExtension(rawURL string) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	ext := ExtractFileExtension(u.Path)
	if ext == "" {
		return true
	}

	for _, blocked := range v.BlockedExtensions {
		if strings.EqualFold(ext, blocked) {
			return false
		}
	}

	if len(v.AllowedExtensions) == 0 {
		return true
	}

	for _, allowed := range v.AllowedExtensions {
		if strings.EqualFold(ext, allowed) {
			return true
		}
	}

	return false
}

func (v *URLValidator) ValidateLength(rawURL string) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	if v.MaxURLLength <= 0 {
		return true
	}

	return len(rawURL) <= v.MaxURLLength
}

func (v *URLValidator) ValidateDepth(rawURL string) bool {
	v.Mutex.RLock()
	defer v.Mutex.RUnlock()

	if v.MaxPathDepth <= 0 {
		return true
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	depth := CountPathSegments(u.Path)
	return depth <= v.MaxPathDepth
}

func (d *URLDeduplicator) IsDuplicate(ctx context.Context, url string) (bool, error) {
	return d.store.IsSeen(ctx, url)
}

func (d *URLDeduplicator) IsContentDuplicate(ctx context.Context, contentHash string) (bool, string, error) {
	url, err := d.store.GetOriginalURL(ctx, "content", contentHash)
	if err == storage.ErrNotFound {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, url, nil
}

func (d *URLDeduplicator) IsSimilar(ctx context.Context, simhash uint64) (bool, string, error) {
	hashStr := strconv.FormatUint(simhash, 10)
	url, err := d.store.GetOriginalURL(ctx, "simhash", hashStr)
	if err == storage.ErrNotFound {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, url, nil
}

func (d *URLDeduplicator) IsSemanticallyDuplicate(ctx context.Context, embeddingHash string) (bool, string, error) {
	url, err := d.store.GetOriginalURL(ctx, "semantic", embeddingHash)
	if err == storage.ErrNotFound {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, url, nil
}

func (d *URLDeduplicator) AddURL(ctx context.Context, url string) error {
	return d.store.MarkSeen(ctx, url)
}

func (d *URLDeduplicator) AddContentHash(ctx context.Context, hash, url string) error {
	return d.store.StoreHash(ctx, "content", hash, url)
}

func (d *URLDeduplicator) AddSimhash(ctx context.Context, hash uint64, url string) error {
	hashStr := strconv.FormatUint(hash, 10)
	return d.store.StoreHash(ctx, "simhash", hashStr, url)
}

func (d *URLDeduplicator) AddSemanticHash(ctx context.Context, hash, url string) error {
	return d.store.StoreHash(ctx, "semantic", hash, url)
}

func ExtractHostFromURL(rawURL string) (string, error) {
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	host := u.Host
	if host == "" {
		return "", fmt.Errorf("no host found in URL")
	}

	return host, nil
}

func ExtractDomain(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	host := u.Host
	if host == "" {
		return "", fmt.Errorf("URL has no host")
	}

	if strings.Contains(host, ":") {
		hostParts := strings.Split(host, ":")
		host = hostParts[0]
	}

	return host, nil
}

func ExtractSubdomain(rawURL string) (string, error) {
	domain, err := ExtractDomain(rawURL)
	if err != nil {
		return "", err
	}

	parts := strings.Split(domain, ".")
	if len(parts) < 3 {
		return "", nil
	}

	subdomainParts := parts[:len(parts)-2]
	return strings.Join(subdomainParts, "."), nil
}

func ExtractTLD(rawURL string) (string, error) {
	domain, err := ExtractDomain(rawURL)
	if err != nil {
		return "", err
	}

	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid domain format")
	}

	return parts[len(parts)-1], nil
}

func GetDomainInfo(rawURL string) (*DomainInfo, error) {
	domain, err := ExtractDomain(rawURL)
	if err != nil {
		return nil, err
	}

	subdomain, _ := ExtractSubdomain(rawURL)
	tld, _ := ExtractTLD(rawURL)
	isIP := IsIPAddress(domain)

	return &DomainInfo{
		Domain:       domain,
		Subdomain:    subdomain,
		TLD:          tld,
		IsIP:         isIP,
		FirstSeen:    time.Now(),
		LastCrawled:  time.Time{},
		URLCount:     0,
		SuccessCount: 0,
		ErrorCount:   0,
	}, nil
}

func IsValidDomain(domain string) bool {
	if domain == "" {
		return false
	}

	domainRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?))*$`)
	return domainRegex.MatchString(domain)
}

func IsIPAddress(host string) bool {
	if host == "" {
		return false
	}

	ip := net.ParseIP(host)
	return ip != nil
}

func (p *URLProcessor) Process(ctx context.Context, rawURL string) (*URLInfo, error) {
	p.Mutex.Lock()
	defer p.Mutex.Unlock()

	p.ProcessedCount++

	normalizedURL, err := p.Normalizer.Normalize(rawURL)
	if err != nil {
		p.InvalidCount++
		return nil, fmt.Errorf("failed to normalize URL: %w", err)
	}

	isDup, err := p.Deduplicator.IsDuplicate(ctx, normalizedURL)
	if err != nil {
		return nil, fmt.Errorf("deduplication check failed: %w", err)
	}
	if isDup {
		p.DuplicateCount++
		return nil, fmt.Errorf("URL already processed: %s", normalizedURL)
	}

	domain, _ := ExtractDomain(normalizedURL)
	subdomain, _ := ExtractSubdomain(normalizedURL)

	urlInfo := &URLInfo{
		URL:           normalizedURL,
		OriginalURL:   rawURL,
		NormalizedURL: normalizedURL,
		Domain:        domain,
		Subdomain:     subdomain,
		Priority:      0,
		Depth:         0,
		DiscoveredAt:  time.Now(),
		LastCrawled:   nil,
		CrawlCount:    0,
		Metadata:      make(map[string]string),
	}

	if !p.Validator.IsValid(urlInfo) {
		p.InvalidCount++
		return nil, fmt.Errorf("URL validation failed: %s", normalizedURL)
	}

	if err := p.Deduplicator.AddURL(ctx, normalizedURL); err != nil {
		return nil, fmt.Errorf("failed to mark URL as processed: %w", err)
	}
	p.ValidCount++

	return urlInfo, nil
}

func (p *URLProcessor) ProcessBatch(ctx context.Context, urls []string) ([]*URLInfo, []error) {
	const numWorkers = 50

	results := make([]*URLInfo, len(urls))
	errs := make([]error, len(urls))

	jobs := make(chan struct {
		index int
		url   string
	}, len(urls))
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				select {
				case <-ctx.Done():
					errs[job.index] = ctx.Err()
					continue
				default:
				}

				urlInfo, err := p.Process(ctx, job.url)
				results[job.index] = urlInfo
				errs[job.index] = err
			}
		}()
	}

	for i, url := range urls {
		jobs <- struct {
			index int
			url   string
		}{i, url}
	}
	close(jobs)

	wg.Wait()
	return results, errs
}

func (p *URLProcessor) GetStatistics() map[string]uint64 {
	p.Mutex.RLock()
	defer p.Mutex.RUnlock()

	return map[string]uint64{
		"processed_count": p.ProcessedCount,
		"valid_count":     p.ValidCount,
		"duplicate_count": p.DuplicateCount,
		"invalid_count":   p.InvalidCount,
	}
}

func (p *URLProcessor) Reset() {
	p.Mutex.Lock()
	defer p.Mutex.Unlock()

	p.ProcessedCount = 0
	p.ValidCount = 0
	p.DuplicateCount = 0
	p.InvalidCount = 0

	p.Deduplicator = NewURLDeduplicator(nil)
}

func (p *URLProcessor) UpdateConfiguration(normalizer *URLNormalizer, validator *URLValidator) {
	p.Mutex.Lock()
	defer p.Mutex.Unlock()

	if normalizer != nil {
		p.Normalizer = normalizer
	}
	if validator != nil {
		p.Validator = validator
	}
}

func CalculateContentHash(content []byte) string {
	hash := sha256.Sum256(content)
	return fmt.Sprintf("%x", hash)
}

func CalculateSimhash(content string) uint64 {
	if content == "" {
		return 0
	}

	var b strings.Builder
	b.Grow(len(content))
	lastSpace := true
	for _, r := range content {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		} else {
			b.WriteRune(unicode.ToLower(r))
			lastSpace = false
		}
	}
	normalized := b.String()

	n := 3
	if len(normalized) < n {
		// Fallback for tiny strings
		return hashShingle(normalized)
	}

	var features [64]int

	// Note: We use string slicing [i:i+n], which shares memory in Go (cheap)
	// We do NOT create a slice []string of all shingles.
	for i := 0; i <= len(normalized)-n; i++ {
		shingle := normalized[i : i+n]
		hash := hashShingle(shingle)

		for j := 0; j < 64; j++ {
			bit := (hash >> j) & 1
			if bit == 1 {
				features[j]++
			} else {
				features[j]--
			}
		}
	}

	var simhash uint64
	for i := 0; i < 64; i++ {
		if features[i] > 0 {
			simhash |= (1 << i)
		}
	}

	return simhash
}

// Helper: FNV-1a hash
func hashShingle(shingle string) uint64 {
	const (
		fnvOffsetBasis uint64 = 14695981039346656037
		fnvPrime       uint64 = 1099511628211
	)
	hash := fnvOffsetBasis
	for i := 0; i < len(shingle); i++ {
		hash ^= uint64(shingle[i])
		hash *= fnvPrime
	}
	return hash
}

func HammingDistance(hash1, hash2 uint64) int {
	xor := hash1 ^ hash2

	count := 0
	for xor != 0 {
		count += int(xor & 1)
		xor >>= 1
	}

	return count
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func RemoveDefaultPort(host, scheme string) string {
	// Fast path if no port present
	if !strings.Contains(host, ":") {
		return host
	}

	h, p, err := net.SplitHostPort(host)
	if err != nil {
		// Fallback if parsing fails (e.g. might be a literal IPv6 without brackets? unlikely for valid URL)
		return host
	}

	// Check if this is an IPv6 literal (contains colons) and needs brackets restored
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		h = "[" + h + "]"
	}

	if (scheme == "http" && p == "80") || (scheme == "https" && p == "443") {
		return h
	}
	return host
}

func SortQueryParams(query string) string {
	if query == "" {
		return ""
	}

	values, err := url.ParseQuery(query)
	if err != nil {
		return query
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sortedValues := url.Values{}
	for _, k := range keys {
		paramValues := values[k]
		sort.Strings(paramValues)
		for _, v := range paramValues {
			sortedValues.Add(k, v)
		}
	}

	return sortedValues.Encode()
}

func ResolvePath(inputPath string) string {
	if inputPath == "" {
		return "/"
	}

	resolved := path.Clean(inputPath)

	if !strings.HasPrefix(resolved, "/") {
		resolved = "/" + resolved
	}

	return resolved
}

func ExtractFileExtension(urlPath string) string {
	lastSlash := strings.LastIndex(urlPath, "/")
	filename := urlPath
	if lastSlash != -1 {
		filename = urlPath[lastSlash+1:]
	}

	lastDot := strings.LastIndex(filename, ".")
	if lastDot == -1 || lastDot == 0 {
		return ""
	}

	return strings.ToLower(filename[lastDot:])
}

func CountPathSegments(path string) int {
	if path == "" || path == "/" {
		return 0
	}

	path = strings.Trim(path, "/")
	if path == "" {
		return 0
	}

	return len(strings.Split(path, "/"))
}
