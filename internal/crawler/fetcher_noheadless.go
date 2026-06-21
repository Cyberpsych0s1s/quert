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

//go:build !headless

package crawler

import "errors"

// newHeadlessFetcher reports that headless rendering is not compiled in. The real
// implementation lives in fetcher_headless.go behind the `headless` build tag;
// building without that tag keeps the binary free of any browser-driver
// dependency (the single-binary default).
func newHeadlessFetcher(_ *CrawlerEngine) (Fetcher, error) {
	return nil, errors.New("javascript rendering requires building with -tags headless")
}
