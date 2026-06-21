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

import "errors"

// newHeadlessFetcher constructs the headless (browser-backed) Fetcher. It is a
// placeholder until the chromedp-backed implementation lands; for now a build
// tagged `headless` compiles but reports rendering as unavailable, so the engine
// falls back to HTTP fetching exactly as an untagged build does.
//
// The real implementation will drive a headless browser, serialize the rendered
// DOM, and route every sub-request through the engine's robots + rate-limit
// governors. It lives behind this build tag so the default binary carries no
// browser-driver dependency.
func newHeadlessFetcher(_ *CrawlerEngine) (Fetcher, error) {
	return nil, errors.New("headless rendering not yet implemented")
}
