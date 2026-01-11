package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Almahr1/quert/internal/client"
	"github.com/Almahr1/quert/internal/config"
	"github.com/Almahr1/quert/internal/extractor"
	"github.com/Almahr1/quert/internal/frontier"
	"github.com/Almahr1/quert/internal/robots"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// CrawlJob represents a single crawling task
type CrawlJob struct {
	URL         string
	URLInfo     *frontier.URLInfo
	Priority    int
	Depth       int
	Headers     map[string]string
	RequestID   string
	SubmittedAt time.Time
	Context     context.Context
}

// CrawlResult represents the result of a crawling operation
type CrawlResult struct {
	Job              *CrawlJob
	URL              string
	StatusCode       int
	Headers          http.Header
	Body             []byte
	ContentType      string
	ContentLength    int64
	ResponseTime     time.Duration
	Error            error
	Links            []string
	ExtractedText    string
	ExtractedContent *extractor.ExtractedContent
	CompletedAt      time.Time
	Success          bool
	Retryable        bool
}

// WorkerStats holds statistics for a worker
type WorkerStats struct {
	WorkerID       int
	JobsProcessed  int64
	JobsSuccessful int64
	JobsFailed     int64
	JobsTimedOut   int64
	TotalTime      time.Duration
	AverageTime    time.Duration
	LastJobTime    time.Time
	IsActive       bool
	CurrentJob     *CrawlJob
}

// CrawlerEngine manages the worker pool and job distribution
type CrawlerEngine struct {
	// Configuration
	Config     *config.CrawlerConfig
	HTTPConfig *config.HTTPConfig
	Logger     *zap.Logger

	// Worker pool management
	Workers     int
	Jobs        chan *CrawlJob
	Results     chan *CrawlResult // Workers -> Processor
	UserResults chan *CrawlResult // Processor -> User
	WorkerStats map[int]*WorkerStats
	StatsMutex  sync.RWMutex

	// HTTP and external dependencies
	HTTPClient       *client.HTTPClient
	RobotsParser     *robots.Parser
	ExtractorFactory *extractor.ExtractorFactory

	// Rate limiting
	GlobalLimiter *rate.Limiter
	HostLimiters  map[string]*rate.Limiter
	LimiterMutex  sync.RWMutex

	// Lifecycle management
	Ctx          context.Context
	Cancel       context.CancelFunc
	Wg           sync.WaitGroup
	WorkerWg     sync.WaitGroup
	Running      bool
	RunningMutex sync.RWMutex

	// Metrics and monitoring
	TotalJobs       int64
	SuccessfulJobs  int64
	TimedOutJobs    int64
	FailedJobs      int64
	StartTime       time.Time
	MetricsCallback func(*CrawlerMetrics)
}

// CrawlerMetrics holds overall crawler performance metrics
type CrawlerMetrics struct {
	TotalJobs      int64
	SuccessfulJobs int64
	TimedOutJobs   int64
	FailedJobs     int64
	JobsPerSecond  float64
	AverageLatency time.Duration
	ActiveWorkers  int
	QueueDepth     int
	Uptime         time.Duration
	ErrorRate      float64
}

// WorkerConfig holds configuration for individual workers
type WorkerConfig struct {
	ID              int
	RequestTimeout  time.Duration
	RetryAttempts   int
	BackoffStrategy string
}

func NewCrawlerEngine(cfg *config.CrawlerConfig, httpCfg *config.HTTPConfig, robotsCfg *config.RobotsConfig, logger *zap.Logger) *CrawlerEngine {
	if cfg == nil {
		panic("crawler config cannot be nil")
	}
	if httpCfg == nil {
		panic("http config cannot be nil")
	}
	if logger == nil {
		panic("logger cannot be nil")
	}

	// Set default values if configuration is incomplete
	crawlerConfig := *cfg // Copy to avoid modifying original

	// Set default/optimal worker count (2-4x CPU cores) * you could set this higher if you have more processing power by default *
	if crawlerConfig.ConcurrentWorkers <= 0 {
		cpuCount := runtime.NumCPU()
		crawlerConfig.ConcurrentWorkers = cpuCount * 3 // 3x CPU cores as default
		if crawlerConfig.ConcurrentWorkers > 50 {
			crawlerConfig.ConcurrentWorkers = 50
		}
		logger.Info("using default worker count", zap.Int("workers", crawlerConfig.ConcurrentWorkers), zap.Int("cpu_cores", cpuCount))
	}

	if crawlerConfig.RequestTimeout <= 0 {
		crawlerConfig.RequestTimeout = 30 * time.Second
		logger.Info("using default request timeout", zap.Duration("timeout", crawlerConfig.RequestTimeout))
	}

	if crawlerConfig.UserAgent == "" {
		crawlerConfig.UserAgent = "Quert/1.0 (+https://github.com/Almahr1/quert)"
		logger.Info("using default user agent", zap.String("user_agent", crawlerConfig.UserAgent))
	}

	if crawlerConfig.MaxPages <= 0 {
		crawlerConfig.MaxPages = 10000 // Reasonable default
		logger.Info("using default max pages", zap.Int("max_pages", crawlerConfig.MaxPages))
	}

	if crawlerConfig.MaxDepth <= 0 {
		crawlerConfig.MaxDepth = 5 // Reasonable default depth
		logger.Info("using default max depth", zap.Int("max_depth", crawlerConfig.MaxDepth))
	}

	// Create buffered channels with appropriate buffer sizes
	// Calculate buffer sizes based on worker count with safety bounds
	jobsBufferSize := crawlerConfig.ConcurrentWorkers * 10
	if jobsBufferSize < 50 {
		jobsBufferSize = 50 // Minimum for small deployments
	}
	if jobsBufferSize > 1000 {
		jobsBufferSize = 1000 // Maximum to prevent excessive memory usage
	}

	resultsBufferSize := crawlerConfig.ConcurrentWorkers * 5
	if resultsBufferSize < 25 {
		resultsBufferSize = 25 // Minimum for small deployments
	}
	if resultsBufferSize > 500 {
		resultsBufferSize = 500 // Maximum to prevent excessive memory usage
	}

	logger.Info("creating buffered channels",
		zap.Int("jobs_buffer", jobsBufferSize),
		zap.Int("results_buffer", resultsBufferSize),
		zap.Int("workers", crawlerConfig.ConcurrentWorkers))

	jobsChannel := make(chan *CrawlJob, jobsBufferSize)
	resultsChannel := make(chan *CrawlResult, resultsBufferSize)
	userResultsChannel := make(chan *CrawlResult, resultsBufferSize)

	httpClient := client.NewHTTPClient(httpCfg, logger)

	// Create robots.txt parser instance with configurable settings
	var robotsConfig robots.Config
	if robotsCfg != nil {
		// Map user-provided robots configuration to robots.Config format
		userAgent := robotsCfg.UserAgent
		if userAgent == "" {
			userAgent = crawlerConfig.UserAgent // Fallback to crawler user agent
		}

		cacheDuration := robotsCfg.CacheDuration
		if cacheDuration <= 0 {
			cacheDuration = 24 * time.Hour // Default if not specified
		}

		robotsConfig = robots.Config{
			UserAgent:   userAgent,
			CacheTTL:    cacheDuration,
			HTTPTimeout: crawlerConfig.RequestTimeout, // Always use crawler timeout
			MaxSize:     500 * 1024,                   // Standard 500KB limit
		}
		logger.Info("using provided robots.txt configuration",
			zap.String("user_agent", robotsConfig.UserAgent),
			zap.Duration("cache_ttl", robotsConfig.CacheTTL),
			zap.Bool("enabled", robotsCfg.Enabled),
			zap.Bool("respect_crawl_delay", robotsCfg.RespectCrawlDelay))
	} else {
		// Create sensible defaults derived from crawler config
		robotsConfig = robots.Config{
			UserAgent:   crawlerConfig.UserAgent,      // Reuse crawler's user agent
			CacheTTL:    24 * time.Hour,               // Standard 24-hour cache
			HTTPTimeout: crawlerConfig.RequestTimeout, // Match crawler timeout
			MaxSize:     500 * 1024,                   // 500KB max robots.txt size
		}
		logger.Info("using default robots.txt configuration derived from crawler config",
			zap.String("user_agent", robotsConfig.UserAgent),
			zap.Duration("cache_ttl", robotsConfig.CacheTTL),
			zap.Duration("http_timeout", robotsConfig.HTTPTimeout))
	}

	robotsParser := robots.NewParser(robotsConfig, httpClient)

	// Create content extractor factory with default configuration
	extractorConfig := extractor.GetDefaultExtractorConfig()
	extractorFactory := extractor.NewExtractorFactory(extractorConfig, logger)

	// Set up rate limiters (global and per-host maps)
	// Use configurable rate limits with sensible defaults
	GlobalRateLimit := crawlerConfig.GlobalRateLimit
	if GlobalRateLimit <= 0 {
		GlobalRateLimit = 5.0 // Default 5 req/sec if not configured
	}
	GlobalBurst := crawlerConfig.GlobalBurst
	if GlobalBurst <= 0 {
		GlobalBurst = 10 // Default burst of 10 if not configured
	}

	GlobalLimiter := rate.NewLimiter(rate.Limit(GlobalRateLimit), GlobalBurst)

	// Per-host rate limiters map (will be populated dynamically)
	HostLimiters := make(map[string]*rate.Limiter)

	logger.Info("initialized rate limiters",
		zap.Float64("global_rate_limit", float64(GlobalLimiter.Limit())),
		zap.Int("global_burst", GlobalLimiter.Burst()),
		zap.Float64("per_host_rate_limit", crawlerConfig.PerHostRateLimit),
		zap.Int("per_host_burst", crawlerConfig.PerHostBurst))

	// Initialize worker statistics tracking
	workerStats := make(map[int]*WorkerStats)
	for i := 0; i < crawlerConfig.ConcurrentWorkers; i++ {
		workerStats[i] = &WorkerStats{
			WorkerID:       i,
			JobsProcessed:  0,
			JobsSuccessful: 0,
			JobsTimedOut:   0,
			JobsFailed:     0,
			TotalTime:      0,
			AverageTime:    0,
			LastJobTime:    time.Time{},
			IsActive:       false,
			CurrentJob:     nil,
		}
	}

	// Set up context for graceful shutdown
	// Note: Context will be provided when Start() is called
	// Here we just initialize the engine structure

	// Return configured CrawlerEngine instance
	engine := &CrawlerEngine{
		// Configuration
		Config:     &crawlerConfig,
		HTTPConfig: httpCfg,
		Logger:     logger,

		// Worker pool management
		Workers:     crawlerConfig.ConcurrentWorkers,
		Jobs:        jobsChannel,
		Results:     resultsChannel,
		UserResults: userResultsChannel,
		WorkerStats: workerStats,
		StatsMutex:  sync.RWMutex{},

		// HTTP and external dependencies
		HTTPClient:       httpClient,
		RobotsParser:     robotsParser,
		ExtractorFactory: extractorFactory,

		// Rate limiting
		GlobalLimiter: GlobalLimiter,
		HostLimiters:  HostLimiters,
		LimiterMutex:  sync.RWMutex{},

		// Lifecycle management
		Ctx:          nil, // Will be set in Start()
		Cancel:       nil, // Will be set in Start()
		Wg:           sync.WaitGroup{},
		Running:      false,
		RunningMutex: sync.RWMutex{},

		// Metrics and monitoring
		TotalJobs:       0,
		SuccessfulJobs:  0,
		FailedJobs:      0,
		StartTime:       time.Time{}, // Will be set in Start()
		MetricsCallback: nil,
	}

	logger.Info("crawler engine initialized successfully",
		zap.Int("workers", crawlerConfig.ConcurrentWorkers),
		zap.Int("jobs_buffer", jobsBufferSize),
		zap.Int("results_buffer", resultsBufferSize))

	return engine
}

// Start begins the crawler engine and spawns worker goroutines
func (CrawlerEngine *CrawlerEngine) Start(Context context.Context) error {
	// Check if engine is already running (thread-safe check)
	if CrawlerEngine.IsRunning() {
		return fmt.Errorf("crawler engine is already running")
	}

	// Set running state to true with mutex protection
	CrawlerEngine.RunningMutex.Lock()
	CrawlerEngine.Running = true
	CrawlerEngine.RunningMutex.Unlock()

	// Store provided context and create cancellable child context
	CrawlerEngine.Ctx, CrawlerEngine.Cancel = context.WithCancel(Context)

	// Record start time for uptime tracking
	CrawlerEngine.StartTime = time.Now()

	// Spawn configured number of worker goroutines
	for WorkerID := 0; WorkerID < CrawlerEngine.Workers; WorkerID++ {
		CrawlerEngine.WorkerWg.Add(1)
		go CrawlerEngine.Worker(CrawlerEngine.Ctx, WorkerID)
	}

	// Start result processor goroutine
	CrawlerEngine.Wg.Add(1)
	go CrawlerEngine.ResultProcessor(CrawlerEngine.Ctx)

	// Start metrics collection goroutine (if callback provided)
	if CrawlerEngine.MetricsCallback != nil {
		CrawlerEngine.Wg.Add(1)
		go CrawlerEngine.MetricsCollector(CrawlerEngine.Ctx)
	}

	// Start rate limiter cleanup goroutine
	CrawlerEngine.Wg.Add(1)
	go CrawlerEngine.cleanupRateLimiters(CrawlerEngine.Ctx)

	// Log successful startup with worker count
	CrawlerEngine.Logger.Info("crawler engine started successfully",
		zap.Int("workers", CrawlerEngine.Workers),
		zap.Int("jobs_buffer_size", cap(CrawlerEngine.Jobs)),
		zap.Int("results_buffer_size", cap(CrawlerEngine.Results)))

	// Return nil on success, error on failure
	return nil
}

func (CrawlerEngine *CrawlerEngine) Stop() error {
	if !CrawlerEngine.IsRunning() {
		return fmt.Errorf("crawler engine is not running")
	}

	CrawlerEngine.RunningMutex.Lock()
	CrawlerEngine.Running = false
	CrawlerEngine.RunningMutex.Unlock()

	CrawlerEngine.Logger.Info("stopping crawler engine gracefully")

	if CrawlerEngine.Cancel != nil {
		CrawlerEngine.Cancel()
	}

	remainingJobs := len(CrawlerEngine.Jobs)
	close(CrawlerEngine.Jobs)

	workerDone := make(chan struct{})
	go func() {
		CrawlerEngine.WorkerWg.Wait()
		close(workerDone)
	}()

	select {
	case <-workerDone:
		CrawlerEngine.Logger.Info("all workers finished")
	case <-time.After(15 * time.Second):
		CrawlerEngine.Logger.Warn("timeout waiting for workers")
	}

	close(CrawlerEngine.Results)

	waitDone := make(chan struct{})
	go func() {
		CrawlerEngine.Wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		CrawlerEngine.Logger.Info("result processor and background tasks finished")
	case <-time.After(15 * time.Second):
		CrawlerEngine.Logger.Warn("timeout waiting for background tasks")
	}

	CrawlerEngine.LimiterMutex.Lock()
	for Host := range CrawlerEngine.HostLimiters {
		delete(CrawlerEngine.HostLimiters, Host)
	}
	CrawlerEngine.LimiterMutex.Unlock()

	Metrics := CrawlerEngine.GetMetrics()
	if Metrics != nil {
		CrawlerEngine.Logger.Info("crawler engine stopped",
			zap.Int("jobs_left_in_queue", remainingJobs),
			zap.Int64("total_jobs", Metrics.TotalJobs),
			zap.Int64("successful_jobs", Metrics.SuccessfulJobs),
			zap.Int64("timed_out_jobs", Metrics.TimedOutJobs),
			zap.Int64("failed_jobs", Metrics.FailedJobs),
			zap.Duration("uptime", Metrics.Uptime))
	}

	return nil
}

func (CrawlerEngine *CrawlerEngine) SubmitJob(Job *CrawlJob) error {
	if Job == nil {
		return fmt.Errorf("job cannot be nil")
	}
	if Job.URL == "" {
		return fmt.Errorf("job URL cannot be empty")
	}
	if Job.Context == nil {
		Job.Context = context.Background()
	}

	if !CrawlerEngine.IsRunning() {
		return fmt.Errorf("crawler engine is not running")
	}

	Job.SubmittedAt = time.Now()

	if Job.RequestID == "" {
		Job.RequestID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Int63())
	}

	HostFromURL, ExtractErr := frontier.ExtractHostFromURL(Job.URL)
	if ExtractErr != nil {
		return fmt.Errorf("failed to extract host from URL: %w", ExtractErr)
	}

	PermissionResult, RobotsErr := CrawlerEngine.RobotsParser.IsAllowed(Job.Context, Job.URL)
	if RobotsErr != nil {
		CrawlerEngine.Logger.Warn("robots.txt check failed, allowing by default",
			zap.String("url", Job.URL),
			zap.Error(RobotsErr))
	} else if !PermissionResult.Allowed {
		return fmt.Errorf("URL disallowed by robots.txt: %s", Job.URL)
	}

	HostLimiter := CrawlerEngine.GetRateLimiter(HostFromURL)
	if GlobalErr := CrawlerEngine.GlobalLimiter.Wait(Job.Context); GlobalErr != nil {
		return fmt.Errorf("global rate limit wait failed: %w", GlobalErr)
	}
	if HostErr := HostLimiter.Wait(Job.Context); HostErr != nil {
		return fmt.Errorf("host rate limit wait failed: %w", HostErr)
	}

	select {
	case CrawlerEngine.Jobs <- Job:
	case <-Job.Context.Done():
		return Job.Context.Err()
	}

	atomic.AddInt64(&CrawlerEngine.TotalJobs, 1)

	CrawlerEngine.Logger.Debug("job submitted to queue",
		zap.String("url", Job.URL),
		zap.String("request_id", Job.RequestID),
		zap.Int("priority", Job.Priority),
		zap.Int("depth", Job.Depth))

	return nil
}

// GetResults returns a channel for receiving crawl results
func (CrawlerEngine *CrawlerEngine) GetResults() <-chan *CrawlResult {
	if CrawlerEngine == nil {
		return nil
	}
	return CrawlerEngine.UserResults
}

// GetMetrics returns current crawler performance metrics
func (CrawlerEngine *CrawlerEngine) GetMetrics() *CrawlerMetrics {
	CrawlerEngine.StatsMutex.RLock()
	defer CrawlerEngine.StatsMutex.RUnlock()

	if CrawlerEngine.StartTime.IsZero() {
		return &CrawlerMetrics{
			TotalJobs:      0,
			SuccessfulJobs: 0,
			FailedJobs:     0,
			JobsPerSecond:  0,
			AverageLatency: 0,
			ActiveWorkers:  0,
			QueueDepth:     len(CrawlerEngine.Jobs),
			Uptime:         0,
			ErrorRate:      0,
		}
	}

	TotalJobsSnapshot := atomic.LoadInt64(&CrawlerEngine.TotalJobs)
	SuccessfulJobsSnapshot := atomic.LoadInt64(&CrawlerEngine.SuccessfulJobs)
	TimedOutJobsSnapshot := atomic.LoadInt64(&CrawlerEngine.TimedOutJobs)
	FailedJobsSnapshot := atomic.LoadInt64(&CrawlerEngine.FailedJobs)

	Uptime := time.Since(CrawlerEngine.StartTime)
	UptimeSeconds := Uptime.Seconds()

	var JobsPerSecond float64
	if UptimeSeconds > 0 {
		JobsPerSecond = float64(TotalJobsSnapshot) / UptimeSeconds
	}

	var TotalLatency time.Duration
	var LatencyCount int64
	ActiveWorkerCount := 0

	for _, WorkerStats := range CrawlerEngine.WorkerStats {
		if WorkerStats.IsActive {
			ActiveWorkerCount++
		}
		if WorkerStats.JobsProcessed > 0 {
			TotalLatency += WorkerStats.AverageTime * time.Duration(WorkerStats.JobsProcessed)
			LatencyCount += WorkerStats.JobsProcessed
		}
	}

	var AverageLatency time.Duration
	if LatencyCount > 0 {
		AverageLatency = TotalLatency / time.Duration(LatencyCount)
	}

	QueueDepth := len(CrawlerEngine.Jobs)

	var ErrorRate float64
	if TotalJobsSnapshot > 0 {
		ErrorRate = float64(FailedJobsSnapshot) / float64(TotalJobsSnapshot) * 100
	}

	Metrics := &CrawlerMetrics{
		TotalJobs:      TotalJobsSnapshot,
		SuccessfulJobs: SuccessfulJobsSnapshot,
		TimedOutJobs:   TimedOutJobsSnapshot,
		FailedJobs:     FailedJobsSnapshot,
		JobsPerSecond:  JobsPerSecond,
		AverageLatency: AverageLatency,
		ActiveWorkers:  ActiveWorkerCount,
		QueueDepth:     QueueDepth,
		Uptime:         Uptime,
		ErrorRate:      ErrorRate,
	}

	return Metrics
}

// GetWorkerStats returns statistics for all workers
func (CrawlerEngine *CrawlerEngine) GetWorkerStats() map[int]*WorkerStats {
	CrawlerEngine.StatsMutex.RLock()
	defer CrawlerEngine.StatsMutex.RUnlock()

	// Create copy of worker stats map to avoid data races
	StatsCopy := make(map[int]*WorkerStats)

	for WorkerID, OriginalStats := range CrawlerEngine.WorkerStats {
		// Create a deep copy of the worker stats
		StatsCopy[WorkerID] = &WorkerStats{
			WorkerID:       OriginalStats.WorkerID,
			JobsProcessed:  OriginalStats.JobsProcessed,
			JobsSuccessful: OriginalStats.JobsSuccessful,
			JobsFailed:     OriginalStats.JobsFailed,
			TotalTime:      OriginalStats.TotalTime,
			LastJobTime:    OriginalStats.LastJobTime,
			IsActive:       OriginalStats.IsActive,
			CurrentJob:     OriginalStats.CurrentJob, // Note: this is a pointer copy
		}

		// Calculate average times for each worker
		if OriginalStats.JobsProcessed > 0 {
			StatsCopy[WorkerID].AverageTime = OriginalStats.TotalTime / time.Duration(OriginalStats.JobsProcessed)
		} else {
			StatsCopy[WorkerID].AverageTime = 0
		}

		// Update active status based on current job presence
		StatsCopy[WorkerID].IsActive = OriginalStats.CurrentJob != nil
	}

	return StatsCopy
}

// SetMetricsCallback sets a callback function for metrics reporting
func (CrawlerEngine *CrawlerEngine) SetMetricsCallback(Callback func(*CrawlerMetrics)) {
	CrawlerEngine.MetricsCallback = Callback

	// If engine is running, restart metrics goroutine with new callback
	// Note: For simplicity, we'll let the existing metrics goroutine pick up the new callback
	// In a more sophisticated implementation, we might restart the goroutine
	if CrawlerEngine.IsRunning() && Callback != nil {
		CrawlerEngine.Logger.Info("metrics callback updated while engine is running")
	}
}

// Worker is the main worker goroutine function
func (CrawlerEngine *CrawlerEngine) Worker(Context context.Context, WorkerID int) {
	defer CrawlerEngine.WorkerWg.Done()

	CrawlerEngine.StatsMutex.Lock()
	if CrawlerEngine.WorkerStats[WorkerID] != nil {
		CrawlerEngine.WorkerStats[WorkerID].IsActive = true
	}
	CrawlerEngine.StatsMutex.Unlock()

	CrawlerEngine.Logger.Info("worker started",
		zap.Int("worker_id", WorkerID))

	defer func() {
		CrawlerEngine.StatsMutex.Lock()
		if CrawlerEngine.WorkerStats[WorkerID] != nil {
			CrawlerEngine.WorkerStats[WorkerID].IsActive = false
			CrawlerEngine.WorkerStats[WorkerID].CurrentJob = nil
		}
		CrawlerEngine.StatsMutex.Unlock()

		CrawlerEngine.Logger.Info("worker stopped",
			zap.Int("worker_id", WorkerID))
	}()

	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C // drain
	}

	for {
		select {
		case <-Context.Done():
			CrawlerEngine.Logger.Debug("worker received shutdown signal",
				zap.Int("worker_id", WorkerID))
			return

		case Job, ChannelOpen := <-CrawlerEngine.Jobs:
			if !ChannelOpen {
				CrawlerEngine.Logger.Debug("jobs channel closed, worker shutting down",
					zap.Int("worker_id", WorkerID))
				return
			}

			CrawlerEngine.StatsMutex.Lock()
			if CrawlerEngine.WorkerStats[WorkerID] != nil {
				CrawlerEngine.WorkerStats[WorkerID].CurrentJob = Job
			}
			CrawlerEngine.StatsMutex.Unlock()

			Result := CrawlerEngine.ProcessJob(Context, Job, WorkerID)

			if Result == nil {
				CrawlerEngine.StatsMutex.Lock()
				if CrawlerEngine.WorkerStats[WorkerID] != nil {
					CrawlerEngine.WorkerStats[WorkerID].CurrentJob = nil
				}
				CrawlerEngine.StatsMutex.Unlock()
				continue
			}

			CrawlerEngine.UpdateWorkerStats(WorkerID, Job, Result)

			timer.Reset(5 * time.Second)
			select {
			case CrawlerEngine.Results <- Result:
				if !timer.Stop() {
					<-timer.C
				}
			case <-Context.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
				CrawlerEngine.Logger.Warn("timeout sending result to results channel",
					zap.Int("worker_id", WorkerID),
					zap.String("url", Job.URL))
			}

			CrawlerEngine.StatsMutex.Lock()
			if CrawlerEngine.WorkerStats[WorkerID] != nil {
				CrawlerEngine.WorkerStats[WorkerID].CurrentJob = nil
			}
			CrawlerEngine.StatsMutex.Unlock()
		}
	}
}

// ProcessJob handles the actual crawling of a single URL
func (CrawlerEngine *CrawlerEngine) ProcessJob(Context context.Context, Job *CrawlJob, WorkerID int) *CrawlResult {
	StartTime := time.Now()

	CrawlerEngine.Logger.Debug("processing job",
		zap.Int("worker_id", WorkerID),
		zap.String("url", Job.URL),
		zap.String("request_id", Job.RequestID))

	Result := &CrawlResult{
		Job:         Job,
		URL:         Job.URL,
		CompletedAt: time.Now(),
		Success:     false,
		Retryable:   false,
	}

	PermissionResult, RobotsErr := CrawlerEngine.RobotsParser.IsAllowed(Context, Job.URL)
	if RobotsErr != nil {
		CrawlerEngine.Logger.Warn("robots.txt check failed during processing",
			zap.String("url", Job.URL),
			zap.Error(RobotsErr))
	} else if !PermissionResult.Allowed {
		Result.Error = fmt.Errorf("URL disallowed by robots.txt: %s", Job.URL)
		Result.Retryable = false
		Result.ResponseTime = time.Since(StartTime)
		return Result
	}

	HostFromURL, ExtractErr := frontier.ExtractHostFromURL(Job.URL)
	if ExtractErr != nil {
		Result.Error = fmt.Errorf("failed to extract host from URL: %w", ExtractErr)
		Result.Retryable = false
		Result.ResponseTime = time.Since(StartTime)
		return Result
	}

	HostLimiter := CrawlerEngine.GetRateLimiter(HostFromURL)
	if HostErr := HostLimiter.Wait(Context); HostErr != nil {
		Result.Error = fmt.Errorf("host rate limit wait failed: %w", HostErr)
		Result.Retryable = true
		Result.ResponseTime = time.Since(StartTime)
		return Result
	}

	HTTPResponse, HTTPErr := CrawlerEngine.HTTPClient.Get(Context, Job.URL)
	if HTTPErr != nil {
		if errors.Is(HTTPErr, context.DeadlineExceeded) || errors.Is(HTTPErr, context.Canceled) {
			atomic.AddInt64(&CrawlerEngine.TimedOutJobs, 1)
			return nil
		}
		Result.Error = fmt.Errorf("HTTP request failed: %w", HTTPErr)
		Result.Retryable = CrawlerEngine.IsRetryableHTTPError(HTTPErr)
		Result.ResponseTime = time.Since(StartTime)
		return Result
	}
	defer HTTPResponse.Body.Close()

	Result.StatusCode = HTTPResponse.StatusCode
	Result.Headers = HTTPResponse.Header
	Result.ContentType = HTTPResponse.Header.Get("Content-Type")
	Result.ContentLength = HTTPResponse.ContentLength
	Result.ResponseTime = time.Since(StartTime)

	BodyBytes, ReadErr := io.ReadAll(HTTPResponse.Body)
	if ReadErr != nil {
		Result.Error = fmt.Errorf("failed to read response body: %w", ReadErr)
		Result.Retryable = true
		return Result
	}
	Result.Body = BodyBytes

	if HTTPResponse.StatusCode < 200 || HTTPResponse.StatusCode >= 300 {
		Result.Error = fmt.Errorf("HTTP error status: %d", HTTPResponse.StatusCode)
		Result.Retryable = CrawlerEngine.IsRetryableHTTPStatus(HTTPResponse.StatusCode)
		return Result
	}

	ExtractedContent, ExtractionErr := CrawlerEngine.ExtractorFactory.ExtractContent(BodyBytes, Result.ContentType, Job.URL)
	if ExtractionErr != nil {
		CrawlerEngine.Logger.Warn("content extraction failed, continuing without extracted content",
			zap.String("url", Job.URL),
			zap.Error(ExtractionErr))

		Result.Links = []string{}
		Result.ExtractedText = ""
		Result.ExtractedContent = nil
	} else {
		Result.ExtractedContent = ExtractedContent
		Result.ExtractedText = ExtractedContent.CleanText

		ExtractedLinks := make([]string, len(ExtractedContent.Links))
		for i, link := range ExtractedContent.Links {
			ExtractedLinks[i] = link.URL
		}
		Result.Links = ExtractedLinks
	}

	Result.Success = true
	Result.Retryable = false

	logFields := []zap.Field{
		zap.Int("worker_id", WorkerID),
		zap.String("url", Job.URL),
		zap.Int("status_code", Result.StatusCode),
		zap.Duration("response_time", Result.ResponseTime),
		zap.Int("body_size", len(Result.Body)),
		zap.Int("extracted_links", len(Result.Links)),
	}

	if Result.ExtractedContent != nil {
		logFields = append(logFields,
			zap.String("title", Result.ExtractedContent.Title),
			zap.Int("word_count", Result.ExtractedContent.Metadata.WordCount),
			zap.Float64("quality_score", Result.ExtractedContent.QualityScore),
			zap.Int("text_length", len(Result.ExtractedContent.CleanText)))
	}

	CrawlerEngine.Logger.Debug("job processed successfully", logFields...)

	return Result
}

// ResultProcessor handles crawl results and manages output
func (CrawlerEngine *CrawlerEngine) ResultProcessor(Context context.Context) {
	defer CrawlerEngine.Wg.Done()

	defer close(CrawlerEngine.UserResults)

	CrawlerEngine.Logger.Info("result processor started")

	defer func() {
		CrawlerEngine.Logger.Info("result processor stopped")
	}()

	drainTimer := time.NewTimer(0)
	if !drainTimer.Stop() {
		<-drainTimer.C
	}

	// Start result processing loop with context cancellation
	for {
		select {
		case <-Context.Done():
			CrawlerEngine.Logger.Info("result processor received shutdown signal, draining remaining results")

			drainTimer.Reset(10 * time.Second)

			for {
				select {
				case Result, ChannelOpen := <-CrawlerEngine.Results:
					if !ChannelOpen {
						CrawlerEngine.Logger.Info("results channel closed, result processor exiting")
						return
					}
					CrawlerEngine.handleResultRouting(Result)
				case <-drainTimer.C:
					CrawlerEngine.Logger.Warn("timeout draining results, forcing shutdown")
					return
				}
			}

		case Result, ChannelOpen := <-CrawlerEngine.Results:
			// Listen for results from results channel
			if !ChannelOpen {
				CrawlerEngine.Logger.Info("results channel closed, result processor exiting")
				return
			}

			CrawlerEngine.handleResultRouting(Result)
		}
	}
}

// handleResultRouting processes the result internally and forwards it to the user
func (CrawlerEngine *CrawlerEngine) handleResultRouting(Result *CrawlResult) {
	// 1. Internal Processing (Stats, logging, retry logic, etc.)
	CrawlerEngine.ProcessResult(Result)

	// 2. Forward to User
	// Non-blocking send to prevent stalling the engine if the user (or test) isn't reading fast enough
	select {
	case CrawlerEngine.UserResults <- Result:
		// Sent successfully
	default:
		// Buffer full, drop result to keep engine running
		// In a production system, you might want to increase buffer size or warn
		CrawlerEngine.Logger.Warn("user results channel full, dropping result",
			zap.String("url", Result.URL))
	}
}

// ProcessResult handles individual crawl results
func (CrawlerEngine *CrawlerEngine) ProcessResult(Result *CrawlResult) {
	// Update global statistics (success/failure counters)
	if Result.Success {
		atomic.AddInt64(&CrawlerEngine.SuccessfulJobs, 1)
		// Log result processing with URL and status
		CrawlerEngine.Logger.Debug("result processed successfully",
			zap.String("url", Result.URL),
			zap.Int("status_code", Result.StatusCode),
			zap.Duration("response_time", Result.ResponseTime),
			zap.Int("extracted_links", len(Result.Links)))

		// Handle successful results (store data, extract links)
		// TODO: In the future, this would integrate with storage layer
		// For now, we just log the successful processing

	} else {
		atomic.AddInt64(&CrawlerEngine.FailedJobs, 1)
		// Handle failed results (retry logic, error logging)
		CrawlerEngine.Logger.Warn("result processing failed",
			zap.String("url", Result.URL),
			zap.Int("status_code", Result.StatusCode),
			zap.Bool("retryable", Result.Retryable),
			zap.Error(Result.Error))

		// TODO: Implement retry logic here
		// If Result.Retryable is true, could resubmit job with exponential backoff
	}

	// Send results to external processors if configured
	// TODO: This would be where we integrate with external result processors
	// such as storage systems, message queues, etc.

	// Clean up resources associated with completed jobs
	// For now, results are handled by garbage collection
	// In a production system, we might need explicit cleanup
}

// MetricsCollector periodically collects and reports metrics
func (CrawlerEngine *CrawlerEngine) MetricsCollector(Context context.Context) {
	// Decrement wait group when function exits
	defer CrawlerEngine.Wg.Done()

	// Create ticker for periodic metrics collection (default: 30s)
	Ticker := time.NewTicker(30 * time.Second)
	defer Ticker.Stop()

	CrawlerEngine.Logger.Info("metrics collector started")

	// Log metrics collector shutdown
	defer func() {
		CrawlerEngine.Logger.Info("metrics collector stopped")
	}()

	// Start metrics collection loop with context cancellation
	for {
		select {
		case <-Context.Done():
			// Handle context cancellation gracefully
			CrawlerEngine.Logger.Debug("metrics collector received shutdown signal")
			// Stop ticker and clean up resources (handled by defer)
			return

		case <-Ticker.C:
			// On each tick, calculate current metrics using GetMetrics
			CurrentMetrics := CrawlerEngine.GetMetrics()
			if CurrentMetrics == nil {
				continue
			}

			// Call metrics callback function if configured
			if CrawlerEngine.MetricsCallback != nil {
				go func(Metrics *CrawlerMetrics) {
					// Run callback in separate goroutine to avoid blocking
					defer func() {
						if RecoverErr := recover(); RecoverErr != nil {
							CrawlerEngine.Logger.Error("metrics callback panic",
								zap.Any("error", RecoverErr))
						}
					}()
					CrawlerEngine.MetricsCallback(Metrics)
				}(CurrentMetrics)
			}

			// Log key metrics at INFO level for monitoring
			CrawlerEngine.Logger.Info("crawler metrics",
				zap.Int64("total_jobs", CurrentMetrics.TotalJobs),
				zap.Int64("successful_jobs", CurrentMetrics.SuccessfulJobs),
				zap.Int64("failed_jobs", CurrentMetrics.FailedJobs),
				zap.Float64("jobs_per_second", CurrentMetrics.JobsPerSecond),
				zap.Duration("average_latency", CurrentMetrics.AverageLatency),
				zap.Int("active_workers", CurrentMetrics.ActiveWorkers),
				zap.Int("queue_depth", CurrentMetrics.QueueDepth),
				zap.Duration("uptime", CurrentMetrics.Uptime),
				zap.Float64("error_rate", CurrentMetrics.ErrorRate))
		}
	}
}

// GetRateLimiter gets or creates a rate limiter for a specific host
func (CrawlerEngine *CrawlerEngine) GetRateLimiter(Host string) *rate.Limiter {
	// Acquire read lock to check if limiter exists
	CrawlerEngine.LimiterMutex.RLock()
	if Limiter, exists := CrawlerEngine.HostLimiters[Host]; exists {
		CrawlerEngine.LimiterMutex.RUnlock()
		return Limiter
	}
	CrawlerEngine.LimiterMutex.RUnlock()

	// Upgrade to write lock if limiter doesn't exist
	CrawlerEngine.LimiterMutex.Lock()
	defer CrawlerEngine.LimiterMutex.Unlock()

	// Double-check pattern to avoid race condition
	if Limiter, exists := CrawlerEngine.HostLimiters[Host]; exists {
		return Limiter
	}

	// Create new rate limiter with host-specific configuration
	// Use configurable per-host rate limits
	PerHostRateLimit := CrawlerEngine.Config.PerHostRateLimit
	if PerHostRateLimit <= 0 {
		PerHostRateLimit = 3.0 // Default 3 req/sec per host if not configured
	}
	PerHostBurst := CrawlerEngine.Config.PerHostBurst
	if PerHostBurst <= 0 {
		PerHostBurst = 5 // Default burst of 5 per host if not configured
	}

	// TODO: Handle rate limit configuration from robots.txt crawl-delay
	// Check if we should respect crawl-delay from robots.txt
	// This would override the configured rate limit for this specific host
	HostLimiter := rate.NewLimiter(rate.Limit(PerHostRateLimit), PerHostBurst)

	// Store limiter in map for future use
	CrawlerEngine.HostLimiters[Host] = HostLimiter

	CrawlerEngine.Logger.Debug("created new rate limiter for host",
		zap.String("host", Host),
		zap.Float64("rate_limit", float64(HostLimiter.Limit())),
		zap.Int("burst", HostLimiter.Burst()),
		zap.Float64("configured_per_host_rate", PerHostRateLimit),
		zap.Int("configured_per_host_burst", PerHostBurst))

	return HostLimiter
}

// IsRetryableHTTPError determines if an HTTP error should trigger a retry
func (CrawlerEngine *CrawlerEngine) IsRetryableHTTPError(Error error) bool {
	if Error == nil {
		return false
	}

	// Use the HTTP client's retry logic
	return client.IsRetryableError(Error, CrawlerEngine.HTTPClient.RetryConfig.RetryableErrors)
}

// IsRetryableHTTPStatus determines if an HTTP status code should trigger a retry
func (CrawlerEngine *CrawlerEngine) IsRetryableHTTPStatus(StatusCode int) bool {
	// Use the HTTP client's retry logic
	return client.IsRetryableStatus(StatusCode, CrawlerEngine.HTTPClient.RetryConfig.RetryableStatus)
}

// cleanupRateLimiters removes unused rate limiters to prevent memory leaks
func (CrawlerEngine *CrawlerEngine) cleanupRateLimiters(Context context.Context) {
	// Decrement wait group when function exits
	defer CrawlerEngine.Wg.Done()

	// Create ticker for periodic cleanup (default: 10 minutes)
	Ticker := time.NewTicker(10 * time.Minute)
	defer Ticker.Stop()

	CrawlerEngine.Logger.Info("starting rate limiter cleanup goroutine", zap.Duration("interval", 10*time.Minute))

	// Start cleanup loop with context cancellation
	for {
		select {
		case <-Context.Done():
			// Handle context cancellation gracefully
			CrawlerEngine.Logger.Info("rate limiter cleanup goroutine stopping due to context cancellation")
			return

		case <-Ticker.C:
			// On each tick, iterate through rate limiters
			CrawlerEngine.LimiterMutex.Lock()

			InitialCount := len(CrawlerEngine.HostLimiters)
			RemovedCount := 0

			// Remove limiters that haven't been used recently
			// Simple approach: remove limiters with no recent activity
			// More sophisticated: track last use time and remove old ones
			for Host, Limiter := range CrawlerEngine.HostLimiters {
				// Check if limiter has available tokens (indicating no recent heavy use)
				// This is a simple heuristic - if burst is fully available, likely unused
				if Limiter.Tokens() >= float64(Limiter.Burst()) {
					delete(CrawlerEngine.HostLimiters, Host)
					RemovedCount++
				}
			}

			CrawlerEngine.LimiterMutex.Unlock()

			// Log cleanup statistics (removed/remaining limiters)
			if RemovedCount > 0 {
				CrawlerEngine.Logger.Info("cleaned up unused rate limiters",
					zap.Int("removed", RemovedCount),
					zap.Int("remaining", InitialCount-RemovedCount),
					zap.Int("initial", InitialCount))
			}
		}
	}
}

// IsRunning safely checks if the crawler engine is running
func (CrawlerEngine *CrawlerEngine) IsRunning() bool {
	// Acquire read lock for thread safety
	CrawlerEngine.RunningMutex.RLock()
	defer CrawlerEngine.RunningMutex.RUnlock()

	// Read running status
	// Release lock and return status (handled by defer)
	return CrawlerEngine.Running
}

// UpdateWorkerStats safely updates statistics for a worker
func (CrawlerEngine *CrawlerEngine) UpdateWorkerStats(WorkerID int, Job *CrawlJob, Result *CrawlResult) {
	// Acquire write lock for thread safety
	CrawlerEngine.StatsMutex.Lock()
	defer CrawlerEngine.StatsMutex.Unlock()

	// Get or create worker stats for WorkerID
	CurrentWorkerStats, exists := CrawlerEngine.WorkerStats[WorkerID]
	if !exists {
		// This shouldn't happen if engine is properly initialized, but handle gracefully
		CurrentWorkerStats = &WorkerStats{
			WorkerID:       WorkerID,
			JobsProcessed:  0,
			JobsSuccessful: 0,
			JobsFailed:     0,
			TotalTime:      0,
			AverageTime:    0,
			LastJobTime:    time.Time{},
			IsActive:       false,
			CurrentJob:     nil,
		}
		CrawlerEngine.WorkerStats[WorkerID] = CurrentWorkerStats
	}

	// Increment job counters based on result success/failure
	CurrentWorkerStats.JobsProcessed++
	if Result.Success {
		CurrentWorkerStats.JobsSuccessful++
	} else {
		CurrentWorkerStats.JobsFailed++
	}

	// Update timing statistics with job duration
	JobDuration := Result.ResponseTime
	if JobDuration > 0 {
		CurrentWorkerStats.TotalTime += JobDuration

		// Calculate running average of job processing time
		CurrentWorkerStats.AverageTime = CurrentWorkerStats.TotalTime / time.Duration(CurrentWorkerStats.JobsProcessed)
	}

	// Update last job time and current job reference
	CurrentWorkerStats.LastJobTime = time.Now()
	CurrentWorkerStats.CurrentJob = nil // Job is completed

	// Release lock after updates complete (handled by defer)
}
