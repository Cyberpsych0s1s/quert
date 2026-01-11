package frontier

import (
	"context"
	"testing"

	"github.com/Almahr1/quert/internal/storage/memory"
	"github.com/stretchr/testify/assert"
)

func TestURLNormalizer_Normalize(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		expected string
		wantErr  bool
	}{
		{
			name:     "Basic Normalization",
			rawURL:   "HTTP://Example.com/",
			expected: "http://example.com/",
			wantErr:  false,
		},
		{
			name:     "Remove Default Port HTTP",
			rawURL:   "http://example.com:80/path",
			expected: "http://example.com/path",
			wantErr:  false,
		},
		{
			name:     "Remove Default Port HTTPS",
			rawURL:   "https://example.com:443/path",
			expected: "https://example.com/path",
			wantErr:  false,
		},
		{
			name:     "Keep Non-Default Port",
			rawURL:   "http://example.com:8080/path",
			expected: "http://example.com:8080/path",
			wantErr:  false,
		},
		{
			name:     "Remove Fragment",
			rawURL:   "http://example.com/path#fragment",
			expected: "http://example.com/path",
			wantErr:  false,
		},
		{
			name:     "Sort Query Params",
			rawURL:   "http://example.com/path?b=2&a=1",
			expected: "http://example.com/path?a=1&b=2",
			wantErr:  false,
		},
		{
			name:     "Remove Empty Params",
			rawURL:   "http://example.com/path?a=1&b=&c=3",
			expected: "http://example.com/path?a=1&c=3",
			wantErr:  false,
		},
		{
			name:     "UTM Parameters Removal",
			rawURL:   "http://example.com/path?utm_source=google&q=test",
			expected: "http://example.com/path?q=test",
			wantErr:  false,
		},
		{
			name:     "Path Cleaning",
			rawURL:   "http://example.com/a/./b/../c",
			expected: "http://example.com/a/c",
			wantErr:  false,
		},
		{
			name:     "Invalid URL",
			rawURL:   "://invalid",
			expected: "",
			wantErr:  true,
		},
	}

	n := NewURLNormalizer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := n.Normalize(tt.rawURL)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestURLValidator_IsValid(t *testing.T) {
	v := NewURLValidator()
	// Customize validator for testing
	v.AllowedDomains = []string{"example.com", "test.com"}
	v.BlockedDomains = []string{"malicious.com"}
	v.MaxURLLength = 100

	tests := []struct {
		name    string
		urlInfo *URLInfo
		want    bool
	}{
		{
			name: "Valid URL",
			urlInfo: &URLInfo{
				URL: "http://example.com/page",
			},
			want: true,
		},
		{
			name: "Blocked Domain",
			urlInfo: &URLInfo{
				URL: "http://malicious.com/page",
			},
			want: false,
		},
		{
			name: "Not Allowed Domain",
			urlInfo: &URLInfo{
				URL: "http://other.com/page",
			},
			want: false,
		},
		{
			name: "Blocked Extension",
			urlInfo: &URLInfo{
				URL: "http://example.com/image.jpg",
			},
			want: false,
		},
		{
			name: "URL Too Long",
			urlInfo: &URLInfo{
				URL: "http://example.com/" + string(make([]byte, 100)),
			},
			want: false,
		},
		{
			name:    "Nil URLInfo",
			urlInfo: nil,
			want:    false,
		},
		{
			name: "Empty URL",
			urlInfo: &URLInfo{
				URL: "",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := v.IsValid(tt.urlInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestURLDeduplicator(t *testing.T) {
	// Initialize with explicit memory store
	store := memory.New()
	d := NewURLDeduplicator(store)
	ctx := context.Background()

	url := "http://example.com/page1"

	// First check
	isDup, err := d.IsDuplicate(ctx, url)
	assert.NoError(t, err)
	assert.False(t, isDup, "First check should not be duplicate")

	// Add URL
	err = d.AddURL(ctx, url)
	assert.NoError(t, err)

	// Second check
	isDup, err = d.IsDuplicate(ctx, url)
	assert.NoError(t, err)
	assert.True(t, isDup, "Second check should be duplicate")

	// Check different URL
	isDup, err = d.IsDuplicate(ctx, "http://example.com/page2")
	assert.NoError(t, err)
	assert.False(t, isDup, "Different URL should not be duplicate")
}

func TestURLProcessor_Process(t *testing.T) {
	p := NewURLProcessor()
	ctx := context.Background()

	tests := []struct {
		name      string
		rawURL    string
		wantURL   string
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Process Valid URL",
			rawURL:    "HTTP://Example.COM/Page?Q=Test",
			wantURL:   "http://example.com/Page?Q=Test",
			expectErr: false,
		},
		{
			name:      "Process Duplicate URL",
			rawURL:    "HTTP://Example.COM/Page?Q=Test", // Same as above after normalization
			wantURL:   "",
			expectErr: true,
			errMsg:    "URL already processed",
		},
		{
			name:      "Process Invalid URL",
			rawURL:    "://invalid",
			wantURL:   "",
			expectErr: true,
			errMsg:    "failed to normalize URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := p.Process(ctx, tt.rawURL)

			if tt.expectErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				assert.Nil(t, info)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, info)
				assert.Equal(t, tt.wantURL, info.NormalizedURL)
				assert.Equal(t, tt.rawURL, info.OriginalURL)
			}
		})
	}

	stats := p.GetStatistics()
	assert.Equal(t, uint64(3), stats["processed_count"])
	assert.Equal(t, uint64(1), stats["valid_count"])     // Only first one valid
	assert.Equal(t, uint64(1), stats["duplicate_count"]) // Second one dup
	assert.Equal(t, uint64(1), stats["invalid_count"])   // Third one invalid
}

func TestSimhash(t *testing.T) {
	content1 := "This is a test content for simhash calculation."
	content2 := "This is a test content for simhash calculation." // Identical
	content3 := "This is a DIFFERENT content for simhash calculation."

	hash1 := CalculateSimhash(content1)
	hash2 := CalculateSimhash(content2)
	hash3 := CalculateSimhash(content3)

	assert.Equal(t, hash1, hash2, "Identical content should have same simhash")
	assert.NotEqual(t, hash1, hash3, "Different content should have different simhash")

	dist12 := HammingDistance(hash1, hash2)
	assert.Equal(t, 0, dist12, "Distance between identical hashes should be 0")

	dist13 := HammingDistance(hash1, hash3)
	assert.True(t, dist13 > 0, "Distance between different hashes should be > 0")
	assert.True(t, dist13 < 64, "Distance should be less than 64")
}

func TestRemoveDefaultPort(t *testing.T) {
	tests := []struct {
		host     string
		scheme   string
		expected string
	}{
		{"example.com:80", "http", "example.com"},
		{"example.com:443", "https", "example.com"},
		{"example.com:8080", "http", "example.com:8080"},
		{"example.com", "http", "example.com"},
		{"127.0.0.1:80", "http", "127.0.0.1"},
		{"[::1]:80", "http", "[::1]"}, // IPv6 test
	}

	for _, tt := range tests {
		got := RemoveDefaultPort(tt.host, tt.scheme)
		assert.Equal(t, tt.expected, got, "Host: %s Scheme: %s", tt.host, tt.scheme)
	}
}

func TestExtractDomainHelpers(t *testing.T) {
	// ExtractDomain
	d, err := ExtractDomain("http://sub.example.com:8080/path")
	assert.NoError(t, err)
	assert.Equal(t, "sub.example.com", d)

	// ExtractHostFromURL
	h, err := ExtractHostFromURL("http://sub.example.com:8080/path")
	assert.NoError(t, err)
	assert.Equal(t, "sub.example.com:8080", h)

	// ExtractSubdomain
	s, err := ExtractSubdomain("http://blog.site.com")
	assert.NoError(t, err)
	assert.Equal(t, "blog", s)

	// ExtractTLD
	tld, err := ExtractTLD("http://example.co.uk")
	assert.NoError(t, err)
	assert.Equal(t, "uk", tld) // Simple extraction logic test
}
