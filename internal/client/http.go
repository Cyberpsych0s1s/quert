package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/config"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

type HTTPClient struct {
	Client      *http.Client
	Config      *config.HTTPConfig
	Logger      *zap.Logger
	Middleware  []Middleware
	RetryConfig RetryConfig
}

type RetryConfig struct {
	MaxRetries      int
	BackoffStrategy BackoffStrategy
	RetryableErrors []error
	RetryableStatus []int
}

type BackoffStrategy interface {
	NextDelay(attempt int) time.Duration
}

type ExponentialBackoff struct {
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Multiplier float64
	Jitter     bool
}

type LinearBackoff struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

type Middleware interface {
	RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error)
}

type LoggingMiddleware struct {
	Logger *zap.Logger
}

type UserAgentMiddleware struct {
	UserAgent string
}

type TimeoutMiddleware struct {
	Timeout time.Duration
}

type RateLimitMiddleware struct {
	GlobalLimiter *rate.Limiter
	HostLimiters  map[string]*rate.Limiter
	Mu            sync.RWMutex
	RPS           float64
	Burst         int
	PerHost       bool
}

type HTTPMetricsCollector interface {
	RecordRequest(method string, url string, statusCode int, duration time.Duration, size int64)
	RecordError(method string, url string, err error)
}

type MetricsMiddleware struct {
	Collector HTTPMetricsCollector
}

type Response struct {
	*http.Response
	URL           string
	StatusCode    int
	ContentLength int64
	Duration      time.Duration
	Attempts      int
}

func NewHTTPClient(cfg *config.HTTPConfig, logger *zap.Logger) *HTTPClient {
	client := BuildHTTPClient(cfg)
	retryConfig := RetryConfig{
		MaxRetries: 3,
		BackoffStrategy: &ExponentialBackoff{
			BaseDelay:  500 * time.Millisecond,
			MaxDelay:   30 * time.Second,
			Multiplier: 2.0,
			Jitter:     true,
		},
		RetryableStatus: []int{429, 500, 502, 503, 504},
		RetryableErrors: []error{},
	}
	middleware := []Middleware{
		NewLoggingMiddleware(logger),
		NewUserAgentMiddleware("WebCrawler/1.0"),
	}

	return &HTTPClient{
		Client:      client,
		Config:      cfg,
		Logger:      logger,
		Middleware:  middleware,
		RetryConfig: retryConfig,
	}
}

func NewHTTPClientWithMiddleware(cfg *config.HTTPConfig, logger *zap.Logger, middleware ...Middleware) *HTTPClient {
	client := NewHTTPClient(cfg, logger)
	client.Middleware = append(client.Middleware, middleware...)
	return client
}

func (c *HTTPClient) Get(ctx context.Context, url string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("GET Request Failed: %w", err)
	}
	return c.Do(ctx, req)
}

func (c *HTTPClient) Post(ctx context.Context, url string, contentType string, body interface{}) (*Response, error) {
	var reqBody io.Reader

	if body != nil {
		switch v := body.(type) {
		case string:
			reqBody = strings.NewReader(v)
		case []byte:
			reqBody = bytes.NewReader(v)
		case io.Reader:
			reqBody = v
		default:
			// Assume it's a struct to be JSON marshaled
			jsonData, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			reqBody = bytes.NewReader(jsonData)
			if contentType == "" {
				contentType = "application/json"
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("POST request creation failed: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return c.Do(ctx, req)
}

func (c *HTTPClient) Head(ctx context.Context, url string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, fmt.Errorf("HEAD request creation failed: %w", err)
	}
	return c.Do(ctx, req)
}

func (c *HTTPClient) Do(ctx context.Context, req *http.Request) (*Response, error) {
	start := time.Now()
	var lastErr error
	var resp *http.Response
	attempts := 0

	transport := ChainMiddleware(c.Middleware, c.Client.Transport)

	for attempt := 0; attempt <= c.RetryConfig.MaxRetries; attempt++ {
		reqClone := req.Clone(ctx)
		attempts = attempt + 1

		if attempt > 0 {
			delay := c.RetryConfig.BackoffStrategy.NextDelay(attempt)
			c.Logger.Debug("retrying request",
				zap.String("url", req.URL.String()),
				zap.Int("attempt", attempt),
				zap.Duration("delay", delay),
			)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, lastErr = transport.RoundTrip(reqClone)

		if lastErr == nil {
			if !IsRetryableStatus(resp.StatusCode, c.RetryConfig.RetryableStatus) {
				break
			}
			resp.Body.Close()
			lastErr = fmt.Errorf("received retryable status code: %d", resp.StatusCode)
			continue
		}

		if !IsRetryableError(lastErr, c.RetryConfig.RetryableErrors) {
			break
		}

		c.Logger.Warn("request failed, will retry",
			zap.String("url", req.URL.String()),
			zap.Int("attempt", attempt),
			zap.Error(lastErr),
		)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("request failed after %d attempts: %w", c.RetryConfig.MaxRetries+1, lastErr)
	}

	duration := time.Since(start)
	response := &Response{
		Response:      resp,
		URL:           req.URL.String(),
		StatusCode:    resp.StatusCode,
		ContentLength: resp.ContentLength,
		Duration:      duration,
		Attempts:      attempts,
	}

	return response, nil
}

func (c *HTTPClient) AddMiddleware(middleware ...Middleware) {
	c.Middleware = append(c.Middleware, middleware...)
}

func (c *HTTPClient) SetRetryConfig(config RetryConfig) {
	c.RetryConfig = config
}

func (c *HTTPClient) Close() error {
	c.Client.CloseIdleConnections()
	c.Logger.Info("HTTP client closed")
	return nil
}

func (e *ExponentialBackoff) NextDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	delay := float64(e.BaseDelay) * math.Pow(e.Multiplier, float64(attempt-1))

	if e.Jitter {
		jitter := delay * 0.1 * rand.Float64()
		delay += jitter
	}

	result := time.Duration(delay)
	result = min(result, e.MaxDelay)

	return result
}

func (l *LinearBackoff) NextDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	delay := time.Duration(attempt) * l.BaseDelay

	delay = min(delay, l.MaxDelay)

	return delay
}

func (m *LoggingMiddleware) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	start := time.Now()

	m.Logger.Debug("HTTP request",
		zap.String("method", req.Method),
		zap.String("url", req.URL.String()),
		zap.String("user_agent", req.Header.Get("User-Agent")),
	)

	resp, err := next.RoundTrip(req)
	duration := time.Since(start)

	if err != nil {
		m.Logger.Error("HTTP request failed",
			zap.String("method", req.Method),
			zap.String("url", req.URL.String()),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
	} else {
		m.Logger.Debug("HTTP response",
			zap.String("method", req.Method),
			zap.String("url", req.URL.String()),
			zap.Int("status", resp.StatusCode),
			zap.Int64("content_length", resp.ContentLength),
			zap.Duration("duration", duration),
		)
	}

	return resp, err
}

func (m *UserAgentMiddleware) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", m.UserAgent)
	}
	return next.RoundTrip(req)
}

func (m *TimeoutMiddleware) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), m.Timeout)
	defer cancel()

	reqWithTimeout := req.WithContext(ctx)
	return next.RoundTrip(reqWithTimeout)
}

func (m *RateLimitMiddleware) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	ctx := req.Context()

	if m.GlobalLimiter != nil {
		if err := m.GlobalLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("global rate limit exceeded: %w", err)
		}
	}

	if m.PerHost {
		host := req.URL.Hostname()
		limiter := m.getLimit(host)
		if err := limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("host rate limit exceeded for %s: %w", host, err)
		}
	}

	return next.RoundTrip(req)
}

func (m *RateLimitMiddleware) getLimit(host string) *rate.Limiter {
	m.Mu.RLock()
	limiter, exists := m.HostLimiters[host]
	m.Mu.RUnlock()

	if exists {
		return limiter
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	if limiter, exists := m.HostLimiters[host]; exists {
		return limiter
	}

	limiter = rate.NewLimiter(rate.Limit(m.RPS), m.Burst)
	m.HostLimiters[host] = limiter
	return limiter
}

func (m *MetricsMiddleware) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	start := time.Now()

	resp, err := next.RoundTrip(req)
	duration := time.Since(start)

	if m.Collector != nil {
		if err != nil {
			m.Collector.RecordError(req.Method, req.URL.String(), err)
		} else {
			contentLength := resp.ContentLength
			if contentLength == -1 {
				contentLength = 0
			}
			m.Collector.RecordRequest(req.Method, req.URL.String(), resp.StatusCode, duration, contentLength)
		}
	}

	return resp, err
}

func NewLoggingMiddleware(logger *zap.Logger) *LoggingMiddleware {
	return &LoggingMiddleware{
		Logger: logger,
	}
}

func NewUserAgentMiddleware(userAgent string) *UserAgentMiddleware {
	return &UserAgentMiddleware{
		UserAgent: userAgent,
	}
}

func NewTimeoutMiddleware(timeout time.Duration) *TimeoutMiddleware {
	return &TimeoutMiddleware{
		Timeout: timeout,
	}
}

func NewRateLimitMiddleware(rps float64, burst int, perHost bool) *RateLimitMiddleware {
	m := &RateLimitMiddleware{
		HostLimiters: make(map[string]*rate.Limiter),
		RPS:          rps,
		Burst:        burst,
		PerHost:      perHost,
	}

	if !perHost && rps > 0 {
		m.GlobalLimiter = rate.NewLimiter(rate.Limit(rps), burst)
	}

	return m
}

func NewMetricsMiddleware(collector HTTPMetricsCollector) *MetricsMiddleware {
	return &MetricsMiddleware{
		Collector: collector,
	}
}

func IsRetryableError(err error, retryableErrors []error) bool {
	if err == nil {
		return false
	}

	// Check against explicitly configured retryable errors
	if slices.Contains(retryableErrors, err) {
		return true
	}

	// Check for common sentinel errors
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Checking for "timeout" errors
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Check for temporary DNS errors (e.g., server failure, timeout).
	// Note: "No such host" errors are permanent (Temporary() is false) and will NOT be retried.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.Temporary() {
		return true
	}

	return false
}

var defaultRetryableStatusCodes = map[int]struct{}{
	429: {}, // Too Many Requests
	500: {}, // Internal Server Error
	502: {}, // Bad Gateway
	503: {}, // Service Unavailable
	504: {}, // Gateway Timeout
}

func IsRetryableStatus(statusCode int, retryableStatus []int) bool {
	// Use pre-built map if using default retryable status codes
	if len(retryableStatus) == 5 {
		isDefault := true
		for _, code := range retryableStatus {
			if _, exists := defaultRetryableStatusCodes[code]; !exists {
				isDefault = false
				break
			}
		}
		if isDefault {
			_, exists := defaultRetryableStatusCodes[statusCode]
			return exists
		}
	}

	// Fallback to creating map for custom retryable status codes
	set := make(map[int]struct{}, len(retryableStatus))
	for _, code := range retryableStatus {
		set[code] = struct{}{}
	}

	_, exists := set[statusCode]
	return exists
}

func BuildHTTPClient(cfg *config.HTTPConfig) *http.Client {
	dialer := &net.Dialer{
		Timeout: cfg.DialTimeout,
		Control: func(network string, address string, c syscall.RawConn) error {
			return nil
		},
	}

	transport := &http.Transport{
		MaxIdleConns:          cfg.MaxIdleConnections,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnectionsPerHost,
		IdleConnTimeout:       cfg.IdleConnectionTimeout,
		DisableKeepAlives:     cfg.DisableKeepAlives,
		DisableCompression:    cfg.DisableCompression,
		TLSHandshakeTimeout:   cfg.TlsHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		DialContext:           dialer.DialContext,
	}

	client := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}

	return client
}

func ChainMiddleware(middleware []Middleware, base http.RoundTripper) http.RoundTripper {
	if len(middleware) == 0 {
		return base
	}

	// Create a middleware chain by wrapping each middleware around the next
	// We build from the inside out (base -> middleware[n-1] -> ... -> middleware[0])
	result := &middlewareChain{
		Middleware: middleware[len(middleware)-1],
		Next:       base,
	}

	// Wrap each middleware around the previous result
	for i := len(middleware) - 2; i >= 0; i-- {
		result = &middlewareChain{
			Middleware: middleware[i],
			Next:       result,
		}
	}

	return result
}

type middlewareChain struct {
	Middleware Middleware
	Next       http.RoundTripper
}

func (m *middlewareChain) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.Middleware.RoundTrip(req, m.Next)
}

func DefaultHTTPConfig() *config.HTTPConfig {
	return &config.HTTPConfig{
		MaxIdleConnections:        1000,
		MaxIdleConnectionsPerHost: 100,
		IdleConnectionTimeout:     90 * time.Second,
		DisableKeepAlives:         false,
		Timeout:                   30 * time.Second,
		DialTimeout:               5 * time.Second,
		TlsHandshakeTimeout:       10 * time.Second,
		ResponseHeaderTimeout:     10 * time.Second,
		DisableCompression:        false,
		AcceptEncoding:            "gzip, deflate",
	}
}
