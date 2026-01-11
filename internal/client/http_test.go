package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Almahr1/quert/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type testPayload struct {
	Message string `json:"message"`
	ID      int    `json:"id"`
}

func CreateTestConfig() *config.HTTPConfig {
	return &config.HTTPConfig{
		MaxIdleConnections:        100,
		MaxIdleConnectionsPerHost: 10,
		IdleConnectionTimeout:     30 * time.Second,
		DisableKeepAlives:         false,
		Timeout:                   5 * time.Second,
		DialTimeout:               2 * time.Second,
		TlsHandshakeTimeout:       3 * time.Second,
		ResponseHeaderTimeout:     3 * time.Second,
		DisableCompression:        false,
	}
}

func CreateTestLogger() *zap.Logger {
	return zaptest.NewLogger(&testing.T{})
}

func TestNewHTTPClient(t *testing.T) {
	cfg := CreateTestConfig()
	logger := CreateTestLogger()

	client := NewHTTPClient(cfg, logger)

	assert.NotNil(t, client)
	assert.NotNil(t, client.Client)
	assert.NotNil(t, client.Logger)
	assert.Equal(t, cfg, client.Config)
	assert.Equal(t, 3, client.RetryConfig.MaxRetries)
	assert.Len(t, client.Middleware, 2) // LoggingMiddleware and UserAgentMiddleware
}

func TestNewHTTPClientWithMiddleware(t *testing.T) {
	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	timeoutMiddleware := NewTimeoutMiddleware(1 * time.Second)

	client := NewHTTPClientWithMiddleware(cfg, logger, timeoutMiddleware)

	assert.NotNil(t, client)
	assert.Len(t, client.Middleware, 3) // Original 2 + 1 custom
}

func TestHTTPClient_Get(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		statusCode   int
		expectError  bool
		expectedBody string
	}{
		{
			name:         "successful GET request",
			responseBody: "Hello, World!",
			statusCode:   200,
			expectError:  false,
			expectedBody: "Hello, World!",
		},
		{
			name:         "GET with 404 status",
			responseBody: "Not Found",
			statusCode:   404,
			expectError:  false,
			expectedBody: "Not Found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			cfg := CreateTestConfig()
			logger := CreateTestLogger()
			client := NewHTTPClient(cfg, logger)

			ctx := context.Background()
			resp, err := client.Get(ctx, server.URL)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, tt.statusCode, resp.StatusCode)
			assert.Equal(t, server.URL, resp.URL)
			assert.Greater(t, resp.Duration, time.Duration(0))

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedBody, string(body))
		})
	}
}

func TestHTTPClient_Head(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodHead, r.Method)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", "123")
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	ctx := context.Background()
	resp, err := client.Head(ctx, server.URL)

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
	assert.Equal(t, "123", resp.Header.Get("Content-Length"))
}

func TestHTTPClient_Post(t *testing.T) {
	tests := []struct {
		name        string
		body        interface{}
		contentType string
		expected    string
	}{
		{
			name:        "POST with string body",
			body:        "test string",
			contentType: "text/plain",
			expected:    "test string",
		},
		{
			name:        "POST with byte slice body",
			body:        []byte("test bytes"),
			contentType: "application/octet-stream",
			expected:    "test bytes",
		},
		{
			name:        "POST with struct body (JSON)",
			body:        testPayload{Message: "hello", ID: 42},
			contentType: "",
			expected:    `{"message":"hello","id":42}`,
		},
		{
			name:        "POST with struct body and explicit JSON content type",
			body:        testPayload{Message: "world", ID: 24},
			contentType: "application/json",
			expected:    `{"message":"world","id":24}`,
		},
		{
			name:        "POST with io.Reader body",
			body:        strings.NewReader("reader content"),
			contentType: "text/plain",
			expected:    "reader content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)

				if tt.contentType != "" {
					assert.Equal(t, tt.contentType, r.Header.Get("Content-Type"))
				} else {
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				}

				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.Equal(t, tt.expected, string(body))

				w.WriteHeader(200)
				w.Write([]byte("OK"))
			}))
			defer server.Close()

			cfg := CreateTestConfig()
			logger := CreateTestLogger()
			client := NewHTTPClient(cfg, logger)

			ctx := context.Background()
			resp, err := client.Post(ctx, server.URL, tt.contentType, tt.body)

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, 200, resp.StatusCode)
		})
	}
}

func TestHTTPClient_Post_InvalidJSON(t *testing.T) {
	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	invalidBody := make(chan int)

	ctx := context.Background()
	_, err := client.Post(ctx, "http://example.com", "", invalidBody)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal request body")
}

func TestHTTPClient_RetryLogic_RetryableStatus(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount <= 2 {
			// Fail first 2 attempts with retryable status
			w.WriteHeader(503)
			w.Write([]byte("Service Unavailable"))
		} else {
			// Succeed on 3rd attempt
			w.WriteHeader(200)
			w.Write([]byte("Success"))
		}
	}))
	defer server.Close()

	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	ctx := context.Background()
	resp, err := client.Get(ctx, server.URL)

	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 3, attemptCount) // Should have made 3 attempts

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "Success", string(body))
}

func TestHTTPClient_RetryLogic_Exhaustion(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.WriteHeader(503)
		w.Write([]byte("Always fail"))
	}))
	defer server.Close()

	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	ctx := context.Background()
	_, err := client.Get(ctx, server.URL)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request failed after")
	assert.Equal(t, 4, attemptCount) // Should make MaxRetries + 1 attempts (3 + 1)
}

func TestExponentialBackoff_NextDelay(t *testing.T) {
	backoff := &ExponentialBackoff{
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   1 * time.Second,
		Multiplier: 2.0,
		Jitter:     false, // Disable jitter for predictable testing
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 0},
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, 1 * time.Second}, // Capped at MaxDelay
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			delay := backoff.NextDelay(tt.attempt)
			assert.Equal(t, tt.expected, delay)
		})
	}
}

func TestExponentialBackoff_WithJitter(t *testing.T) {
	backoff := &ExponentialBackoff{
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   1 * time.Second,
		Multiplier: 2.0,
		Jitter:     true,
	}

	delay1 := backoff.NextDelay(2)
	delay2 := backoff.NextDelay(2)

	// With jitter enabled, delays should vary slightly
	baseExpected := 200 * time.Millisecond
	assert.True(t, delay1 >= baseExpected)
	assert.True(t, delay1 <= baseExpected+20*time.Millisecond) // Max 10% jitter

	// Two calls should produce different results (with very high probability)
	// Note: This test might occasionally fail due to randomness, but probability is very low
	if delay1 == delay2 {
		t.Logf("Warning: Jitter produced same delay twice: %v", delay1)
	}
}

func TestLinearBackoff_NextDelay(t *testing.T) {
	backoff := &LinearBackoff{
		BaseDelay: 100 * time.Millisecond,
		MaxDelay:  500 * time.Millisecond,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 0},
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 300 * time.Millisecond},
		{4, 400 * time.Millisecond},
		{5, 500 * time.Millisecond},
		{6, 500 * time.Millisecond}, // Capped at MaxDelay
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			delay := backoff.NextDelay(tt.attempt)
			assert.Equal(t, tt.expected, delay)
		})
	}
}

func TestLoggingMiddleware_RoundTrip(t *testing.T) {
	logger := CreateTestLogger()
	middleware := NewLoggingMiddleware(logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	// Create a simple transport to test with
	transport := http.DefaultTransport

	resp, err := middleware.RoundTrip(req, transport)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestUserAgentMiddleware_RoundTrip(t *testing.T) {
	userAgent := "TestCrawler/1.0"
	middleware := NewUserAgentMiddleware(userAgent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, userAgent, r.Header.Get("User-Agent"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	transport := http.DefaultTransport

	_, err = middleware.RoundTrip(req, transport)
	require.NoError(t, err)
}

func TestUserAgentMiddleware_PreservesExistingUserAgent(t *testing.T) {
	existingUA := "ExistingUA/2.0"
	middleware := NewUserAgentMiddleware("TestCrawler/1.0")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, existingUA, r.Header.Get("User-Agent"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", existingUA)

	transport := http.DefaultTransport

	_, err = middleware.RoundTrip(req, transport)
	require.NoError(t, err)
}

func TestTimeoutMiddleware_RoundTrip(t *testing.T) {
	timeout := 100 * time.Millisecond
	middleware := NewTimeoutMiddleware(timeout)

	// Create a server that delays longer than our timeout
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	transport := http.DefaultTransport

	start := time.Now()
	_, err = middleware.RoundTrip(req, transport)
	duration := time.Since(start)

	assert.Error(t, err)
	assert.True(t, duration < 150*time.Millisecond) // Should timeout before server responds
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "timeout error",
			// Create a mock net.Error that returns Timeout() = true
			err:      &net.DNSError{IsTimeout: true},
			expected: true,
		},
		{
			name:     "DNS error",
			err:      &net.DNSError{IsTemporary: true},
			expected: true,
		},
		{
			name:     "connection refused",
			err:      syscall.ECONNREFUSED, // Use actual syscall error
			expected: true,
		},
		{
			name:     "connection reset",
			err:      syscall.ECONNRESET, // Use actual syscall error
			expected: true,
		},
		{
			name:     "EOF error",
			err:      io.EOF, // Use actual io.EOF
			expected: true,
		},
		{
			name:     "context canceled",
			err:      context.Canceled, // Use actual context.Canceled
			expected: true,
		},
		{
			name:     "deadline exceeded",
			err:      context.DeadlineExceeded, // Use actual context error
			expected: true,
		},
		{
			name:     "non-retryable error",
			err:      fmt.Errorf("invalid request format"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryableError(tt.err, []error{})
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRetryableStatus(t *testing.T) {
	retryableStatus := []int{429, 500, 502, 503, 504}

	tests := []struct {
		statusCode int
		expected   bool
	}{
		{200, false},
		{404, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
		{400, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.statusCode), func(t *testing.T) {
			result := IsRetryableStatus(tt.statusCode, retryableStatus)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestChainMiddleware(t *testing.T) {
	logger := CreateTestLogger()
	middleware1 := NewLoggingMiddleware(logger)
	middleware2 := NewUserAgentMiddleware("TestUA/1.0")

	baseTransport := http.DefaultTransport
	chained := ChainMiddleware([]Middleware{middleware1, middleware2}, baseTransport)

	assert.NotNil(t, chained)

	// Test that chained middleware works
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "TestUA/1.0", r.Header.Get("User-Agent"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := chained.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestChainMiddleware_EmptyMiddleware(t *testing.T) {
	baseTransport := http.DefaultTransport
	chained := ChainMiddleware([]Middleware{}, baseTransport)

	assert.Equal(t, baseTransport, chained)
}

func TestHTTPClient_AddMiddleware(t *testing.T) {
	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	initialCount := len(client.Middleware)

	timeoutMiddleware := NewTimeoutMiddleware(1 * time.Second)
	client.AddMiddleware(timeoutMiddleware)

	assert.Equal(t, initialCount+1, len(client.Middleware))
}

func TestHTTPClient_SetRetryConfig(t *testing.T) {
	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	newConfig := RetryConfig{
		MaxRetries: 5,
		BackoffStrategy: &LinearBackoff{
			BaseDelay: 200 * time.Millisecond,
			MaxDelay:  2 * time.Second,
		},
		RetryableStatus: []int{429, 500},
	}

	client.SetRetryConfig(newConfig)

	assert.Equal(t, 5, client.RetryConfig.MaxRetries)
	assert.Equal(t, []int{429, 500}, client.RetryConfig.RetryableStatus)
}

func TestHTTPClient_Close(t *testing.T) {
	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	err := client.Close()
	assert.NoError(t, err)
}

func TestHTTPClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := CreateTestConfig()
	logger := CreateTestLogger()
	client := NewHTTPClient(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Get(ctx, server.URL)
	assert.Error(t, err)
	assert.True(t, err == context.DeadlineExceeded || strings.Contains(err.Error(), "context deadline exceeded"))
}

func BenchmarkHTTPClient_Get(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	cfg := CreateTestConfig()
	logger := zap.NewNop()
	client := NewHTTPClient(cfg, logger)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(ctx, server.URL)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkExponentialBackoff_NextDelay(b *testing.B) {
	backoff := &ExponentialBackoff{
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   1 * time.Second,
		Multiplier: 2.0,
		Jitter:     true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		backoff.NextDelay(i % 10)
	}
}
