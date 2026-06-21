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
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/client"
	"github.com/cyberpsych0s1s/quert/internal/frontier"
	"github.com/temoto/robotstxt"
)

type CacheEntry struct {
	Robots    *robotstxt.RobotsData
	FetchedAt time.Time
	ExpiresAt time.Time
}

type Parser struct {
	HTTPClient *client.HTTPClient
	Cache      map[string]*CacheEntry
	CacheMutex sync.RWMutex
	CacheTTL   time.Duration
	UserAgent  string

	// Replaced potentially infinite map with fixed-size sharded locks
	lockShards [256]sync.Mutex
}

type Config struct {
	UserAgent   string
	CacheTTL    time.Duration
	HTTPTimeout time.Duration
	MaxSize     int64
}

type PermissionResult struct {
	Allowed      bool
	CrawlDelay   time.Duration
	DisallowedBy string
	Sitemaps     []string
}

type FetchResult struct {
	Success      bool
	Robots       *robotstxt.RobotsData
	Error        error
	ResponseCode int
	FetchTime    time.Duration
}

func NewParser(Config Config, HTTPClient *client.HTTPClient) *Parser {
	if Config.UserAgent == "" {
		Config.UserAgent = "*"
	}
	if Config.CacheTTL <= 0 {
		Config.CacheTTL = 24 * time.Hour
	}
	if Config.HTTPTimeout <= 0 {
		Config.HTTPTimeout = 30 * time.Second
	}
	if Config.MaxSize <= 0 {
		Config.MaxSize = 500 * 1024
	}

	return &Parser{
		HTTPClient: HTTPClient,
		Cache:      make(map[string]*CacheEntry),
		CacheMutex: sync.RWMutex{},
		CacheTTL:   Config.CacheTTL,
		UserAgent:  Config.UserAgent,
		// lockShards is zero-initialized automatically
	}
}

func (P *Parser) IsAllowed(Ctx context.Context, RawURL string) (*PermissionResult, error) {
	if RawURL == "" {
		return nil, fmt.Errorf("empty URL provided")
	}

	// Fix: Parse URL to get path for robots check
	u, err := url.Parse(RawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Extract host from parsed URL or fallback to helper
	Host := u.Host
	if Host == "" {
		var err error
		Host, err = frontier.ExtractHostFromURL(RawURL)
		if err != nil {
			return nil, fmt.Errorf("failed to extract host from URL %q: %w", RawURL, err)
		}
	}

	Robots, Err := P.GetRobots(Ctx, Host)
	if Err != nil {
		return nil, fmt.Errorf("robots.txt unavailable (fail closed): %w", Err)
	}

	pathQuery := u.Path
	if u.RawQuery != "" {
		pathQuery += "?" + u.RawQuery
	}
	if pathQuery == "" {
		pathQuery = "/"
	}

	Allowed := Robots.TestAgent(pathQuery, P.UserAgent)

	var CrawlDelay time.Duration
	if Group := Robots.FindGroup(P.UserAgent); Group != nil {
		if Group.CrawlDelay > 0 {
			Seconds := int64(Group.CrawlDelay / time.Second)
			CrawlDelay = time.Duration(Seconds) * time.Second
		}
	}

	var Sitemaps []string
	if Robots.Sitemaps != nil {
		Sitemaps = Robots.Sitemaps
	} else {
		Sitemaps = []string{}
	}

	DisallowedBy := ""
	if !Allowed {
		DisallowedBy = "robots.txt disallow rule"
	}

	return &PermissionResult{
		Allowed:      Allowed,
		CrawlDelay:   CrawlDelay,
		DisallowedBy: DisallowedBy,
		Sitemaps:     Sitemaps,
	}, nil
}

func (P *Parser) GetRobots(Ctx context.Context, Host string) (*robotstxt.RobotsData, error) {
	if Host == "" {
		return nil, fmt.Errorf("empty host provided")
	}
	NormalizedHost := Host
	if strings.Contains(Host, ":") {
		if H, _, Err := net.SplitHostPort(Host); Err == nil {
			NormalizedHost = H
		}
	}
	if Robots, Found := P.GetCachedRobots(NormalizedHost); Found {
		return Robots, nil
	}

	// Use sharded lock to prevent concurrent fetches for the same host
	HostMutex := P.GetPerHostMutex(NormalizedHost)
	HostMutex.Lock()
	defer HostMutex.Unlock()

	// Double-check cache after lock
	if Robots, Found := P.GetCachedRobots(NormalizedHost); Found {
		return Robots, nil
	}

	FetchResult, Err := P.FetchRobotsFromServer(Ctx, Host)
	if Err != nil {
		return nil, fmt.Errorf("failed to fetch robots.txt for host %q: %w", Host, Err)
	}

	if FetchResult.Error != nil {
		return nil, fmt.Errorf("robots.txt fetch failed: %w", FetchResult.Error)
	}

	if !FetchResult.Success || FetchResult.Robots == nil {
		PermissiveRobots, _ := robotstxt.FromString("")
		P.SetCachedRobots(NormalizedHost, PermissiveRobots)
		return PermissiveRobots, nil
	}

	P.SetCachedRobots(NormalizedHost, FetchResult.Robots)
	return FetchResult.Robots, nil
}

func (P *Parser) FetchRobotsFromServer(Ctx context.Context, Host string) (*FetchResult, error) {
	Start := time.Now()
	RobotsURL := BuildRobotsURL(Host)
	if RobotsURL == "" {
		return &FetchResult{
			Success:      false,
			Robots:       nil,
			Error:        fmt.Errorf("failed to build robots.txt URL for host %q", Host),
			ResponseCode: 0,
			FetchTime:    time.Since(Start),
		}, nil
	}

	Resp, Err := P.HTTPClient.Get(Ctx, RobotsURL)
	if Err != nil {
		return &FetchResult{
			Success:      false,
			Robots:       nil,
			Error:        fmt.Errorf("HTTP request failed: %w", Err),
			ResponseCode: 0,
			FetchTime:    time.Since(Start),
		}, nil
	}
	defer Resp.Body.Close()
	Result := &FetchResult{
		Success:      false,
		Robots:       nil,
		Error:        nil,
		ResponseCode: Resp.StatusCode,
		FetchTime:    time.Since(Start),
	}

	switch Resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusForbidden, http.StatusUnauthorized:
		PermissiveRobots, _ := robotstxt.FromString("")
		Result.Success = true
		Result.Robots = PermissiveRobots
		return Result, nil
	default:
		Result.Error = fmt.Errorf("unexpected status code: %d", Resp.StatusCode)
		return Result, nil
	}
	LimitedReader := io.LimitReader(Resp.Body, 500*1024)
	Content, Err := io.ReadAll(LimitedReader)
	if Err != nil {
		Result.Error = fmt.Errorf("failed to read response body: %w", Err)
		return Result, nil
	}
	if len(Content) >= 500*1024 {
		Result.Error = fmt.Errorf("robots.txt too large (>500KB)")
		return Result, nil
	}
	Robots, Err := ParseRobotsContent(Content)
	if Err != nil {
		Result.Error = fmt.Errorf("failed to parse robots.txt: %w", Err)
		return Result, nil
	}
	Result.Success = true
	Result.Robots = Robots
	return Result, nil
}

// GetPerHostMutex returns a mutex from the fixed shard set based on host hash.
// This prevents infinite memory growth while ensuring thread safety.
func (P *Parser) GetPerHostMutex(Host string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(Host))
	// Map the hash to one of the 256 mutexes
	idx := h.Sum32() % 256
	return &P.lockShards[idx]
}

func (P *Parser) IsExpired(Entry *CacheEntry) bool {
	if Entry == nil {
		return true
	}
	return time.Now().After(Entry.ExpiresAt)
}

func (P *Parser) GetCachedRobots(Host string) (*robotstxt.RobotsData, bool) {
	P.CacheMutex.RLock()
	defer P.CacheMutex.RUnlock()
	Entry, Exists := P.Cache[Host]
	if !Exists {
		return nil, false
	}
	if P.IsExpired(Entry) {
		return nil, false
	}
	return Entry.Robots, true
}

func (P *Parser) SetCachedRobots(Host string, Robots *robotstxt.RobotsData) {
	P.CacheMutex.Lock()
	defer P.CacheMutex.Unlock()
	Now := time.Now()
	Entry := &CacheEntry{
		Robots:    Robots,
		FetchedAt: Now,
		ExpiresAt: Now.Add(P.CacheTTL),
	}
	P.Cache[Host] = Entry
}

func (P *Parser) ClearCache() {
	P.CacheMutex.Lock()
	defer P.CacheMutex.Unlock()
	P.Cache = make(map[string]*CacheEntry)
}

func (P *Parser) ClearExpired() int {
	P.CacheMutex.Lock()
	defer P.CacheMutex.Unlock()
	RemovedCount := 0
	for Host, Entry := range P.Cache {
		if P.IsExpired(Entry) {
			delete(P.Cache, Host)
			RemovedCount++
		}
	}
	return RemovedCount
}

func (P *Parser) GetCacheStats() CacheStats {
	P.CacheMutex.RLock()
	defer P.CacheMutex.RUnlock()
	TotalEntries := len(P.Cache)
	ExpiredEntries := 0
	var OldestEntry, NewestEntry time.Time
	var TotalAge time.Duration

	if TotalEntries == 0 {
		return CacheStats{
			TotalEntries:   0,
			ExpiredEntries: 0,
			OldestEntry:    time.Time{},
			NewestEntry:    time.Time{},
			CacheHitRate:   0.0,
			AverageAge:     0,
		}
	}
	First := true
	for _, Entry := range P.Cache {
		if P.IsExpired(Entry) {
			ExpiredEntries++
		}

		FetchTime := Entry.FetchedAt
		if First {
			OldestEntry = FetchTime
			NewestEntry = FetchTime
			First = false
		} else {
			if FetchTime.Before(OldestEntry) {
				OldestEntry = FetchTime
			}
			if FetchTime.After(NewestEntry) {
				NewestEntry = FetchTime
			}
		}

		TotalAge += time.Since(FetchTime)
	}

	AverageAge := time.Duration(0)
	if TotalEntries > 0 {
		AverageAge = TotalAge / time.Duration(TotalEntries)
	}
	return CacheStats{
		TotalEntries:   TotalEntries,
		ExpiredEntries: ExpiredEntries,
		OldestEntry:    OldestEntry,
		NewestEntry:    NewestEntry,
		CacheHitRate:   0.0,
		AverageAge:     AverageAge,
	}
}

type CacheStats struct {
	TotalEntries   int
	ExpiredEntries int
	OldestEntry    time.Time
	NewestEntry    time.Time
	CacheHitRate   float64
	AverageAge     time.Duration
}

func BuildRobotsURL(Host string) string {
	Host = strings.TrimSpace(Host)

	if Host == "" {
		return ""
	}

	if !strings.HasPrefix(Host, "http://") && !strings.HasPrefix(Host, "https://") {
		Host = "http://" + Host
	}

	U, Err := frontier.ParseURL(Host)
	if Err != nil {
		if !strings.HasSuffix(Host, "/") {
			Host += "/"
		}
		return Host + "robots.txt"
	}

	U.Path = "/robots.txt"
	U.RawQuery = ""
	U.Fragment = ""

	return U.String()
}

func ParseRobotsContent(Content []byte) (*robotstxt.RobotsData, error) {
	if len(Content) == 0 {
		R, _ := robotstxt.FromString("")
		return R, nil
	}
	R, Err := robotstxt.FromBytes(Content)
	if Err != nil {
		R, _ = robotstxt.FromString("")
		return R, fmt.Errorf("failed to parse robots.txt, returning permissive: %w", Err)
	}

	if R == nil {
		R, _ = robotstxt.FromString("")
		return R, fmt.Errorf("robots.txt parsing returned nil data, returning permissive")
	}

	return R, nil
}

func (P *Parser) Close() error {
	P.ClearCache()
	if P.HTTPClient != nil {
		P.HTTPClient.Close()
	}
	// No need to clean up mutexes as they are now a fixed array
	return nil
}
