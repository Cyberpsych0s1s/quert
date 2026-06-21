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

package robots

import (
	"context"
	"testing"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/client"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNewParser(t *testing.T) {
	logger := zap.NewNop()
	httpCfg := client.DefaultHTTPConfig()
	httpClient := client.NewHTTPClient(httpCfg, logger)

	config := Config{
		UserAgent: "TestBot",
		CacheTTL:  time.Hour,
	}

	p := NewParser(config, httpClient)
	assert.NotNil(t, p)
	assert.Equal(t, "TestBot", p.UserAgent)
	assert.Equal(t, time.Hour, p.CacheTTL)
}

func TestParser_IsAllowed(t *testing.T) {
	logger := zap.NewNop()
	httpCfg := client.DefaultHTTPConfig()
	httpClient := client.NewHTTPClient(httpCfg, logger)

	// Activate httpmock
	httpmock.ActivateNonDefault(httpClient.Client)
	defer httpmock.DeactivateAndReset()

	config := Config{
		UserAgent: "TestBot",
	}
	p := NewParser(config, httpClient)
	ctx := context.Background()

	tests := []struct {
		name           string
		url            string
		robotsBody     string
		statusCode     int
		fetchError     bool
		expectedResult bool
		expectErr      bool
	}{
		{
			name:           "Allowed by default",
			url:            "http://example.com/page",
			robotsBody:     "User-agent: *\nAllow: /",
			statusCode:     200,
			expectedResult: true,
			expectErr:      false,
		},
		{
			name:           "Disallowed explicitly",
			url:            "http://example.com/admin",
			robotsBody:     "User-agent: *\nDisallow: /admin",
			statusCode:     200,
			expectedResult: false,
			expectErr:      false,
		},
		{
			name:           "Allowed specific user agent",
			url:            "http://example.com/private",
			robotsBody:     "User-agent: TestBot\nAllow: /private\nUser-agent: *\nDisallow: /",
			statusCode:     200,
			expectedResult: true,
			expectErr:      false,
		},
		{
			name:           "No robots.txt (404)",
			url:            "http://example.com/page",
			robotsBody:     "",
			statusCode:     404,
			expectedResult: true,
			expectErr:      false,
		},
		{
			name:           "Network Error (Fail Closed)",
			url:            "http://example.com/page",
			robotsBody:     "",
			statusCode:     0,
			fetchError:     true,
			expectedResult: false, // Should not matter as err is returned
			expectErr:      true,  // Expect error now!
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responder := httpmock.NewStringResponder(tt.statusCode, tt.robotsBody)
			if tt.fetchError {
				responder = httpmock.NewErrorResponder(assert.AnError)
			}
			httpmock.RegisterResponder("GET", "http://example.com/robots.txt", responder)

			// Clear cache to force fetch
			p.ClearCache()

			result, err := p.IsAllowed(ctx, tt.url)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result.Allowed)
			}
		})
	}
}

func TestParser_CrawlDelay(t *testing.T) {
	logger := zap.NewNop()
	httpCfg := client.DefaultHTTPConfig()
	httpClient := client.NewHTTPClient(httpCfg, logger)

	httpmock.ActivateNonDefault(httpClient.Client)
	defer httpmock.DeactivateAndReset()

	config := Config{UserAgent: "TestBot"}
	p := NewParser(config, httpClient)

	robotsBody := "User-agent: *\nCrawl-delay: 5"
	httpmock.RegisterResponder("GET", "http://example.com/robots.txt", httpmock.NewStringResponder(200, robotsBody))

	result, err := p.IsAllowed(context.Background(), "http://example.com/page")
	assert.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.CrawlDelay)
}
