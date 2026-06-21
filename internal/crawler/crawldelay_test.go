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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

func TestApplyCrawlDelay(t *testing.T) {
	engine := setupTestEngine()
	engine.Config.PerHostRateLimit = 3.0 // 3 req/s configured baseline
	engine.Config.PerHostBurst = 5

	// Create the host limiter via the normal path.
	host := "example.com"
	lim := engine.GetRateLimiter(host)
	assert.Equal(t, rate.Limit(3.0), lim.Limit(), "baseline rate from config")

	// A 2s crawl-delay => 0.5 req/s, more restrictive than 3/s: must tighten.
	engine.applyCrawlDelay(host, 2*time.Second)
	assert.Equal(t, rate.Limit(0.5), lim.Limit(), "tightened to crawl-delay rate")
	assert.Equal(t, 1, lim.Burst(), "burst reduced to 1 for spacing")

	// A crawl-delay implying a FASTER rate than config must NOT loosen.
	engine.applyCrawlDelay(host, 100*time.Millisecond) // => 10 req/s
	assert.Equal(t, rate.Limit(0.5), lim.Limit(), "never loosens existing limit")

	// Non-positive delay is a no-op.
	engine.applyCrawlDelay(host, 0)
	assert.Equal(t, rate.Limit(0.5), lim.Limit())

	// Unknown host is a no-op (must not panic).
	engine.applyCrawlDelay("unknown.test", 5*time.Second)
}
