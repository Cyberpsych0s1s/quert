// Package config provides comprehensive configuration management for the web crawler
// Supports multiple configuration sources: YAML, JSON, environment variables, and command line flags

// TODO: Remove BindEnvVariables, and its call in LoadConfig
// currently it works because manual binding likely is taking precedence.
// but this is very confusing, and is just bad practice.
// will fix after math midterm (REMIND ME!!!)

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config represents the complete application configuration
type Config struct {
	Crawler        CrawlerConfig    `mapstructure:"crawler" yaml:"crawler" json:"crawler"`
	RateLimit      RateLimitConfig  `mapstructure:"rate_limit" yaml:"rate_limit" json:"rate_limit"`
	Content        ContentConfig    `mapstructure:"content" yaml:"content" json:"content"`
	Storage        StorageConfig    `mapstructure:"storage" yaml:"storage" json:"storage"`
	Monitoring     MonitoringConfig `mapstructure:"monitoring" yaml:"monitoring" json:"monitoring"`
	HTTP           HTTPConfig       `mapstructure:"http" yaml:"http" json:"http"`
	Frontier       FrontierConfig   `mapstructure:"frontier" yaml:"frontier" json:"frontier"`
	Robots         RobotsConfig     `mapstructure:"robots" yaml:"robots" json:"robots"`
	Redis          RedisConfig      `mapstructure:"redis" yaml:"redis" json:"redis"`
	Security       SecurityConfig   `mapstructure:"security" yaml:"security" json:"security"`
	Features       FeatureConfig    `mapstructure:"features" yaml:"features" json:"features"`
	ConfigFileUsed string           `json:"-" yaml:"-"`
}

// CrawlerConfig holds basic crawler settings
type CrawlerConfig struct {
	MaxPages          int           `mapstructure:"max_pages" yaml:"max_pages" json:"max_pages"`
	MaxDepth          int           `mapstructure:"max_depth" yaml:"max_depth" json:"max_depth"`
	ConcurrentWorkers int           `mapstructure:"concurrent_workers" yaml:"concurrent_workers" json:"concurrent_workers"`
	RequestTimeout    time.Duration `mapstructure:"request_timeout" yaml:"request_timeout" json:"request_timeout"`
	UserAgent         string        `mapstructure:"user_agent" yaml:"user_agent" json:"user_agent"`
	SeedURLs          []string      `mapstructure:"seed_urls" yaml:"seed_urls" json:"seed_urls"`
	IncludePatterns   []string      `mapstructure:"include_patterns" yaml:"include_patterns" json:"include_patterns"`
	ExcludePatterns   []string      `mapstructure:"exclude_patterns" yaml:"exclude_patterns" json:"exclude_patterns"`
	AllowedDomains    []string      `mapstructure:"allowed_domains" yaml:"allowed_domains" json:"allowed_domains"`
	BlockedDomains    []string      `mapstructure:"blocked_domains" yaml:"blocked_domains" json:"blocked_domains"`
	GlobalRateLimit   float64       `mapstructure:"global_rate_limit" yaml:"global_rate_limit" json:"global_rate_limit"`
	GlobalBurst       int           `mapstructure:"global_burst" yaml:"global_burst" json:"global_burst"`
	PerHostRateLimit  float64       `mapstructure:"per_host_rate_limit" yaml:"per_host_rate_limit" json:"per_host_rate_limit"`
	PerHostBurst      int           `mapstructure:"per_host_burst" yaml:"per_host_burst" json:"per_host_burst"`
}

// RateLimitConfig holds rate limiting settings
type RateLimitConfig struct {
	RequestsPerSecond float64       `mapstructure:"requests_per_second" yaml:"requests_per_second" json:"requests_per_second"`
	Burst             int           `mapstructure:"burst" yaml:"burst" json:"burst"`
	PerHostLimit      bool          `mapstructure:"per_host_limit" yaml:"per_host_limit" json:"per_host_limit"`
	DefaultDelay      time.Duration `mapstructure:"default_delay" yaml:"default_delay" json:"default_delay"`
	Adaptive          bool          `mapstructure:"adaptive" yaml:"adaptive" json:"adaptive"`
	RespectRetryAfter bool          `mapstructure:"respect_retry_after" yaml:"respect_retry_after" json:"respect_retry_after"`
	FailureThreshold  int           `mapstructure:"failure_threshold" yaml:"failure_threshold" json:"failure_threshold"`
	RecoveryTimeout   time.Duration `mapstructure:"recovery_timeout" yaml:"recovery_timeout" json:"recovery_timeout"`
	MaxRequests       int           `mapstructure:"max_requests" yaml:"max_requests" json:"max_requests"`
}

// ContentConfig holds content processing settings
type ContentConfig struct {
	MinTextLength       int         `mapstructure:"min_text_length" yaml:"min_text_length" json:"min_text_length"`
	MaxTextLength       int         `mapstructure:"max_text_length" yaml:"max_text_length" json:"max_text_length"`
	Languages           []string    `mapstructure:"languages" yaml:"languages" json:"languages"`
	QualityThreshold    float64     `mapstructure:"quality_threshold" yaml:"quality_threshold" json:"quality_threshold"`
	RemoveBoilerplate   bool        `mapstructure:"remove_boilerplate" yaml:"remove_boilerplate" json:"remove_boilerplate"`
	ExtractMainContent  bool        `mapstructure:"extract_main_content" yaml:"extract_main_content" json:"extract_main_content"`
	PreserveFormatting  bool        `mapstructure:"preserve_formatting" yaml:"preserve_formatting" json:"preserve_formatting"`
	NormalizeWhitespace bool        `mapstructure:"normalize_whitespace" yaml:"normalize_whitespace" json:"normalize_whitespace"`
	Deduplication       DedupConfig `mapstructure:"deduplication" yaml:"deduplication" json:"deduplication"`
}

// DedupConfig holds deduplication settings
type DedupConfig struct {
	Enabled               bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	UrlFingerprinting     bool    `mapstructure:"url_fingerprinting" yaml:"url_fingerprinting" json:"url_fingerprinting"`
	ContentFingerprinting bool    `mapstructure:"content_fingerprinting" yaml:"content_fingerprinting" json:"content_fingerprinting"`
	SemanticSimilarity    bool    `mapstructure:"semantic_similarity" yaml:"semantic_similarity" json:"semantic_similarity"`
	SimilarityThreshold   float64 `mapstructure:"similarity_threshold" yaml:"similarity_threshold" json:"similarity_threshold"`
}

// StorageConfig holds storage backend settings
type StorageConfig struct {
	Type                  string        `mapstructure:"type" yaml:"type" json:"type"` // Storage backend type (e.g., "file", "postgres", "redis")
	Path                  string        `mapstructure:"path" yaml:"path" json:"path"`
	BatchSize             int           `mapstructure:"batch_size" yaml:"batch_size" json:"batch_size"`
	Compression           bool          `mapstructure:"compression" yaml:"compression" json:"compression"`
	FileFormat            string        `mapstructure:"file_format" yaml:"file_format" json:"file_format"`
	ConnectionString      string        `mapstructure:"connection_string" yaml:"connection_string" json:"connection_string"`
	MaxConnections        int           `mapstructure:"max_connections" yaml:"max_connections" json:"max_connections"`
	MaxIdleConnections    int           `mapstructure:"max_idle_connections" yaml:"max_idle_connections" json:"max_idle_connections"`
	ConnectionMaxLifetime time.Duration `mapstructure:"connection_max_lifetime" yaml:"connection_max_lifetime" json:"connection_max_lifetime"`
	BadgerPath            string        `mapstructure:"badger_path" yaml:"badger_path" json:"badger_path"`
	BadgerInMemory        bool          `mapstructure:"badger_in_memory" yaml:"badger_in_memory" json:"badger_in_memory"`
	BadgerSyncWrites      bool          `mapstructure:"badger_sync_writes" yaml:"badger_sync_writes" json:"badger_sync_writes"`
	S3Endpoint            string        `mapstructure:"s3_endpoint" yaml:"s3_endpoint" json:"s3_endpoint"`
	S3Bucket              string        `mapstructure:"s3_bucket" yaml:"s3_bucket" json:"s3_bucket"`
	S3Region              string        `mapstructure:"s3_region" yaml:"s3_region" json:"s3_region"`
	S3AccessKey           string        `mapstructure:"s3_access_key" yaml:"s3_access_key" json:"s3_access_key"`
	S3SecretKey           string        `mapstructure:"s3_secret_key" yaml:"s3_secret_key" json:"s3_secret_key"`
	S3UseSSL              bool          `mapstructure:"s3_use_ssl" yaml:"s3_use_ssl" json:"s3_use_ssl"`
}

// MonitoringConfig holds monitoring and observability settings
type MonitoringConfig struct {
	LogLevel          string  `mapstructure:"log_level" yaml:"log_level" json:"log_level"`
	LogFormat         string  `mapstructure:"log_format" yaml:"log_format" json:"log_format"`
	LogFile           string  `mapstructure:"log_file" yaml:"log_file" json:"log_file"`
	LogRotation       bool    `mapstructure:"log_rotation" yaml:"log_rotation" json:"log_rotation"`
	LogMaxSize        string  `mapstructure:"log_max_size" yaml:"log_max_size" json:"log_max_size"`
	LogMaxBackups     int     `mapstructure:"log_max_backups" yaml:"log_max_backups" json:"log_max_backups"`
	MetricsEnabled    bool    `mapstructure:"metrics_enabled" yaml:"metrics_enabled" json:"metrics_enabled"`
	MetricsPort       int     `mapstructure:"metrics_port" yaml:"metrics_port" json:"metrics_port"`
	MetricsPath       string  `mapstructure:"metrics_path" yaml:"metrics_path" json:"metrics_path"`
	HealthPort        int     `mapstructure:"health_port" yaml:"health_port" json:"health_port"`
	HealthPath        string  `mapstructure:"health_path" yaml:"health_path" json:"health_path"`
	EnableProfiling   bool    `mapstructure:"enable_profiling" yaml:"enable_profiling" json:"enable_profiling"`
	ProfilingPort     int     `mapstructure:"profiling_port" yaml:"profiling_port" json:"profiling_port"`
	TracingEnabled    bool    `mapstructure:"tracing_enabled" yaml:"tracing_enabled" json:"tracing_enabled"`
	JaegerEndpoint    string  `mapstructure:"jaeger_endpoint" yaml:"jaeger_endpoint" json:"jaeger_endpoint"`
	ServiceName       string  `mapstructure:"service_name" yaml:"service_name" json:"service_name"`
	TracingSampleRate float64 `mapstructure:"tracing_sample_rate" yaml:"tracing_sample_rate" json:"tracing_sample_rate"`
}

// HTTPConfig holds HTTP client settings
type HTTPConfig struct {
	MaxIdleConnections        int           `mapstructure:"max_idle_connections" yaml:"max_idle_connections" json:"max_idle_connections"`
	MaxIdleConnectionsPerHost int           `mapstructure:"max_idle_connections_per_host" yaml:"max_idle_connections_per_host" json:"max_idle_connections_per_host"`
	IdleConnectionTimeout     time.Duration `mapstructure:"idle_connection_timeout" yaml:"idle_connection_timeout" json:"idle_connection_timeout"`
	DisableKeepAlives         bool          `mapstructure:"disable_keep_alives" yaml:"disable_keep_alives" json:"disable_keep_alives"`
	Timeout                   time.Duration `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	DialTimeout               time.Duration `mapstructure:"dial_timeout" yaml:"dial_timeout" json:"dial_timeout"`
	TlsHandshakeTimeout       time.Duration `mapstructure:"tls_handshake_timeout" yaml:"tls_handshake_timeout" json:"tls_handshake_timeout"`
	ResponseHeaderTimeout     time.Duration `mapstructure:"response_header_timeout" yaml:"response_header_timeout" json:"response_header_timeout"`
	DisableCompression        bool          `mapstructure:"disable_compression" yaml:"disable_compression" json:"disable_compression"`
	AcceptEncoding            string        `mapstructure:"accept_encoding" yaml:"accept_encoding" json:"accept_encoding"`
}

// FrontierConfig holds URL frontier settings
type FrontierConfig struct {
	QueueCapacity      int           `mapstructure:"queue_capacity" yaml:"queue_capacity" json:"queue_capacity"`
	PriorityQueues     int           `mapstructure:"priority_queues" yaml:"priority_queues" json:"priority_queues"`
	PolitenessQueues   int           `mapstructure:"politeness_queues" yaml:"politeness_queues" json:"politeness_queues"`
	CheckpointInterval time.Duration `mapstructure:"checkpoint_interval" yaml:"checkpoint_interval" json:"checkpoint_interval"`
	PersistentState    bool          `mapstructure:"persistent_state" yaml:"persistent_state" json:"persistent_state"`
	UrlNormalization   bool          `mapstructure:"url_normalization" yaml:"url_normalization" json:"url_normalization"`
	Canonicalization   bool          `mapstructure:"canonicalization" yaml:"canonicalization" json:"canonicalization"`
	RemoveFragments    bool          `mapstructure:"remove_fragments" yaml:"remove_fragments" json:"remove_fragments"`
	SortQueryParams    bool          `mapstructure:"sort_query_params" yaml:"sort_query_params" json:"sort_query_params"`
}

// RobotsConfig holds robots.txt settings
type RobotsConfig struct {
	Enabled            bool          `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	CacheDuration      time.Duration `mapstructure:"cache_duration" yaml:"cache_duration" json:"cache_duration"`
	UserAgent          string        `mapstructure:"user_agent" yaml:"user_agent" json:"user_agent"`
	CrawlDelayOverride bool          `mapstructure:"crawl_delay_override" yaml:"crawl_delay_override" json:"crawl_delay_override"`
	RespectCrawlDelay  bool          `mapstructure:"respect_crawl_delay" yaml:"respect_crawl_delay" json:"respect_crawl_delay"`
}

// RedisConfig holds Redis settings
type RedisConfig struct {
	Addr               string        `mapstructure:"addr" yaml:"addr" json:"addr"`
	Password           string        `mapstructure:"password" yaml:"password" json:"password"`
	Db                 int           `mapstructure:"db" yaml:"db" json:"db"`
	PoolSize           int           `mapstructure:"pool_size" yaml:"pool_size" json:"pool_size"`
	MinIdleConnections int           `mapstructure:"min_idle_connections" yaml:"min_idle_connections" json:"min_idle_connections"`
	MaxRetries         int           `mapstructure:"max_retries" yaml:"max_retries" json:"max_retries"`
	RetryDelay         time.Duration `mapstructure:"retry_delay" yaml:"retry_delay" json:"retry_delay"`
}

// SecurityConfig holds security settings
type SecurityConfig struct {
	TlsInsecureSkipVerify bool     `mapstructure:"tls_insecure_skip_verify" yaml:"tls_insecure_skip_verify" json:"tls_insecure_skip_verify"`
	TlsMinVersion         string   `mapstructure:"tls_min_version" yaml:"tls_min_version" json:"tls_min_version"`
	AuthEnabled           bool     `mapstructure:"auth_enabled" yaml:"auth_enabled" json:"auth_enabled"`
	AuthType              string   `mapstructure:"auth_type" yaml:"auth_type" json:"auth_type"`
	AuthUsername          string   `mapstructure:"auth_username" yaml:"auth_username" json:"auth_username"`
	AuthPassword          string   `mapstructure:"auth_password" yaml:"auth_password" json:"auth_password"`
	AuthToken             string   `mapstructure:"auth_token" yaml:"auth_token" json:"auth_token"`
	AllowedIPs            []string `mapstructure:"allowed_ips" yaml:"allowed_ips" json:"allowed_ips"`
	BlockedIPs            []string `mapstructure:"blocked_ips" yaml:"blocked_ips" json:"blocked_ips"`
}

// FeatureConfig holds feature flags
type FeatureConfig struct {
	JavaScriptRendering     bool `mapstructure:"javascript_rendering" yaml:"javascript_rendering" json:"javascript_rendering"`
	SemanticAnalysis        bool `mapstructure:"semantic_analysis" yaml:"semantic_analysis" json:"semantic_analysis"`
	ContentClassification   bool `mapstructure:"content_classification" yaml:"content_classification" json:"content_classification"`
	MultiLanguageProcessing bool `mapstructure:"multi_language_processing" yaml:"multi_language_processing" json:"multi_language_processing"`
	RealTimeStreaming       bool `mapstructure:"real_time_streaming" yaml:"real_time_streaming" json:"real_time_streaming"`
}

func LoadConfig(configPath string, flags *pflag.FlagSet) (*Config, error) {
	v := viper.New()

	SetDefaults(v)

	if configPath != "" {
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("specified config file does not exist: %s", configPath)
		}
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")

		if home, err := os.UserHomeDir(); err == nil {
			v.AddConfigPath(filepath.Join(home, ".crawler"))
		}
		v.AddConfigPath("/etc/crawler")
	}

	v.SetEnvPrefix("CRAWLER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	BindEnvVariables(v)

	ConfigFileUsed := ""
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	} else {
		ConfigFileUsed = v.ConfigFileUsed()
	}

	if flags != nil {
		if err := v.BindPFlags(flags); err != nil {
			return nil, fmt.Errorf("failed to bind command flags: %w", err)
		}
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	config.ConfigFileUsed = ConfigFileUsed

	if err := ValidateConfig(&config); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return &config, nil
}

// bindEnvVariables explicitly binds environment variables for better support
func BindEnvVariables(v *viper.Viper) {
	// Common environment variable mappings
	envMappings := map[string]string{
		"crawler.user_agent":             "CRAWLER_USER_AGENT",
		"rate_limit.requests_per_second": "CRAWLER_RATE_LIMIT_REQUESTS_PER_SECOND",
		"storage.type":                   "CRAWLER_STORAGE_TYPE",
		"storage.path":                   "CRAWLER_STORAGE_PATH",
		"storage.connection_string":      "CRAWLER_STORAGE_CONNECTION_STRING",
		"storage.s3_access_key":          "CRAWLER_S3_ACCESS_KEY",
		"storage.s3_secret_key":          "CRAWLER_S3_SECRET_KEY",
		"storage.s3_bucket":              "CRAWLER_S3_BUCKET",
		"storage.s3_region":              "CRAWLER_S3_REGION",
		"monitoring.log_level":           "CRAWLER_LOG_LEVEL",
		"monitoring.log_file":            "CRAWLER_LOG_FILE",
		"redis.addr":                     "CRAWLER_REDIS_ADDR",
		"redis.password":                 "CRAWLER_REDIS_PASSWORD",
		"security.auth_username":         "CRAWLER_AUTH_USERNAME",
		"security.auth_password":         "CRAWLER_AUTH_PASSWORD",
		"security.auth_token":            "CRAWLER_AUTH_TOKEN",
	}

	for key, env := range envMappings {
		v.BindEnv(key, env)
	}
}

// setDefaults sets all default configuration values
func SetDefaults(v *viper.Viper) {
	// Crawler defaults
	v.SetDefault("crawler.max_pages", 10000)
	v.SetDefault("crawler.max_depth", 5)
	v.SetDefault("crawler.concurrent_workers", 10)
	v.SetDefault("crawler.request_timeout", "30s")
	v.SetDefault("crawler.user_agent", "LLMCrawler/1.0 (+https://github.com/Almahr1/quert)")
	v.SetDefault("crawler.global_rate_limit", 5.0)
	v.SetDefault("crawler.global_burst", 10)
	v.SetDefault("crawler.per_host_rate_limit", 3.0)
	v.SetDefault("crawler.per_host_burst", 5)

	// Rate limiting defaults
	v.SetDefault("rate_limit.requests_per_second", 2.0)
	v.SetDefault("rate_limit.burst", 10)
	v.SetDefault("rate_limit.per_host_limit", true)
	v.SetDefault("rate_limit.default_delay", "1s")
	v.SetDefault("rate_limit.adaptive", true)
	v.SetDefault("rate_limit.respect_retry_after", true)
	v.SetDefault("rate_limit.failure_threshold", 5)
	v.SetDefault("rate_limit.recovery_timeout", "30s")
	v.SetDefault("rate_limit.max_requests", 3)

	// Content defaults
	v.SetDefault("content.min_text_length", 100)
	v.SetDefault("content.max_text_length", 100000)
	v.SetDefault("content.languages", []string{"en"})
	v.SetDefault("content.quality_threshold", 0.7)
	v.SetDefault("content.remove_boilerplate", true)
	v.SetDefault("content.extract_main_content", true)
	v.SetDefault("content.preserve_formatting", false)
	v.SetDefault("content.normalize_whitespace", true)
	v.SetDefault("content.deduplication.enabled", true)
	v.SetDefault("content.deduplication.url_fingerprinting", true)
	v.SetDefault("content.deduplication.content_fingerprinting", true)
	v.SetDefault("content.deduplication.semantic_similarity", true)
	v.SetDefault("content.deduplication.similarity_threshold", 0.85)

	// Storage defaults
	v.SetDefault("storage.type", "file")
	v.SetDefault("storage.path", "./data")
	v.SetDefault("storage.batch_size", 1000)
	v.SetDefault("storage.compression", true)
	v.SetDefault("storage.file_format", "jsonl")
	v.SetDefault("storage.max_connections", 25)
	v.SetDefault("storage.max_idle_connections", 5)
	v.SetDefault("storage.connection_max_lifetime", "300s")

	// Monitoring defaults
	v.SetDefault("monitoring.log_level", "info")
	v.SetDefault("monitoring.log_format", "json")
	v.SetDefault("monitoring.log_file", "./logs/crawler.log")
	v.SetDefault("monitoring.log_rotation", true)
	v.SetDefault("monitoring.log_max_size", "100MB")
	v.SetDefault("monitoring.log_max_backups", 10)
	v.SetDefault("monitoring.metrics_enabled", true)
	v.SetDefault("monitoring.metrics_port", 8080)
	v.SetDefault("monitoring.metrics_path", "/metrics")
	v.SetDefault("monitoring.health_port", 8080)
	v.SetDefault("monitoring.health_path", "/health")
	v.SetDefault("monitoring.enable_profiling", false)
	v.SetDefault("monitoring.profiling_port", 6060)
	v.SetDefault("monitoring.tracing_enabled", false)
	v.SetDefault("monitoring.service_name", "web-crawler")
	v.SetDefault("monitoring.tracing_sample_rate", 0.1)

	// HTTP defaults
	v.SetDefault("http.max_idle_connections", 1000)
	v.SetDefault("http.max_idle_connections_per_host", 100)
	v.SetDefault("http.idle_connection_timeout", "90s")
	v.SetDefault("http.disable_keep_alives", false)
	v.SetDefault("http.timeout", "30s")
	v.SetDefault("http.dial_timeout", "5s")
	v.SetDefault("http.tls_handshake_timeout", "10s")
	v.SetDefault("http.response_header_timeout", "10s")
	v.SetDefault("http.disable_compression", false)
	v.SetDefault("http.accept_encoding", "gzip, deflate")

	// Frontier defaults
	v.SetDefault("frontier.queue_capacity", 100000)
	v.SetDefault("frontier.priority_queues", 10)
	v.SetDefault("frontier.politeness_queues", 1000)
	v.SetDefault("frontier.checkpoint_interval", "60s")
	v.SetDefault("frontier.persistent_state", true)
	v.SetDefault("frontier.url_normalization", true)
	v.SetDefault("frontier.canonicalization", true)
	v.SetDefault("frontier.remove_fragments", true)
	v.SetDefault("frontier.sort_query_params", true)

	// Robots defaults
	v.SetDefault("robots.enabled", true)
	v.SetDefault("robots.cache_duration", "24h")
	v.SetDefault("robots.user_agent", "*")
	v.SetDefault("robots.crawl_delay_override", false)
	v.SetDefault("robots.respect_crawl_delay", true)

	// Redis defaults
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.pool_size", 10)
	v.SetDefault("redis.min_idle_connections", 5)
	v.SetDefault("redis.max_retries", 3)
	v.SetDefault("redis.retry_delay", "1s")

	// Security defaults
	v.SetDefault("security.tls_insecure_skip_verify", false)
	v.SetDefault("security.tls_min_version", "1.2")
	v.SetDefault("security.auth_enabled", false)
	v.SetDefault("security.auth_type", "basic")

	// Feature flags defaults
	v.SetDefault("features.javascript_rendering", false)
	v.SetDefault("features.semantic_analysis", true)
	v.SetDefault("features.content_classification", true)
	v.SetDefault("features.multi_language_processing", true)
	v.SetDefault("features.real_time_streaming", false)
}

// validateConfig performs comprehensive validation of the configuration
func ValidateConfig(config *Config) error {
	// Validate crawler settings
	if config.Crawler.MaxPages <= 0 {
		return fmt.Errorf("crawler.max_pages must be positive, got %d", config.Crawler.MaxPages)
	}
	if config.Crawler.MaxDepth < 0 {
		return fmt.Errorf("crawler.max_depth must be non-negative, got %d", config.Crawler.MaxDepth)
	}
	if config.Crawler.ConcurrentWorkers <= 0 {
		return fmt.Errorf("crawler.concurrent_workers must be positive, got %d", config.Crawler.ConcurrentWorkers)
	}
	if config.Crawler.RequestTimeout <= 0 {
		return fmt.Errorf("crawler.request_timeout must be positive, got %v", config.Crawler.RequestTimeout)
	}
	if config.Crawler.UserAgent == "" {
		return fmt.Errorf("crawler.user_agent cannot be empty")
	}

	// Validate rate limiting settings
	if config.RateLimit.RequestsPerSecond <= 0 {
		return fmt.Errorf("rate_limit.requests_per_second must be positive, got %f", config.RateLimit.RequestsPerSecond)
	}
	if config.RateLimit.Burst <= 0 {
		return fmt.Errorf("rate_limit.burst must be positive, got %d", config.RateLimit.Burst)
	}
	if config.RateLimit.FailureThreshold <= 0 {
		return fmt.Errorf("rate_limit.failure_threshold must be positive, got %d", config.RateLimit.FailureThreshold)
	}

	// Validate content settings
	if config.Content.MinTextLength < 0 {
		return fmt.Errorf("content.min_text_length must be non-negative, got %d", config.Content.MinTextLength)
	}
	if config.Content.MaxTextLength <= config.Content.MinTextLength {
		return fmt.Errorf("content.max_text_length (%d) must be greater than min_text_length (%d)",
			config.Content.MaxTextLength, config.Content.MinTextLength)
	}
	if config.Content.QualityThreshold < 0 || config.Content.QualityThreshold > 1 {
		return fmt.Errorf("content.quality_threshold must be between 0 and 1, got %f", config.Content.QualityThreshold)
	}
	if config.Content.Deduplication.SimilarityThreshold < 0 || config.Content.Deduplication.SimilarityThreshold > 1 {
		return fmt.Errorf("content.deduplication.similarity_threshold must be between 0 and 1, got %f",
			config.Content.Deduplication.SimilarityThreshold)
	}

	// Validate storage settings
	validStorageTypes := map[string]bool{"file": true, "postgres": true, "badger": true, "s3": true}
	if !validStorageTypes[config.Storage.Type] {
		return fmt.Errorf("invalid storage.type: %s. Valid options: file, postgres, badger, s3", config.Storage.Type)
	}
	if config.Storage.BatchSize <= 0 {
		return fmt.Errorf("storage.batch_size must be positive, got %d", config.Storage.BatchSize)
	}

	// Validate file storage specific settings
	if config.Storage.Type == "file" {
		if config.Storage.Path == "" {
			return fmt.Errorf("storage.path is required for file storage")
		}
		validFormats := map[string]bool{"json": true, "jsonl": true, "parquet": true}
		if !validFormats[config.Storage.FileFormat] {
			return fmt.Errorf("invalid storage.file_format: %s. Valid options: json, jsonl, parquet", config.Storage.FileFormat)
		}
	}

	// Validate database storage settings
	if config.Storage.Type == "postgres" && config.Storage.ConnectionString == "" {
		return fmt.Errorf("storage.connection_string is required for postgres storage")
	}

	// Validate S3 storage settings
	if config.Storage.Type == "s3" {
		if config.Storage.S3Bucket == "" {
			return fmt.Errorf("storage.s3_bucket is required for S3 storage")
		}
		if config.Storage.S3AccessKey == "" || config.Storage.S3SecretKey == "" {
			return fmt.Errorf("storage.s3_access_key and s3_secret_key are required for S3 storage")
		}
	}

	// Validate monitoring settings
	validLogLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLogLevels[config.Monitoring.LogLevel] {
		return fmt.Errorf("invalid monitoring.log_level: %s. Valid options: debug, info, warn, error", config.Monitoring.LogLevel)
	}
	validLogFormats := map[string]bool{"json": true, "text": true}
	if !validLogFormats[config.Monitoring.LogFormat] {
		return fmt.Errorf("invalid monitoring.log_format: %s. Valid options: json, text", config.Monitoring.LogFormat)
	}
	if config.Monitoring.MetricsPort <= 0 || config.Monitoring.MetricsPort > 65535 {
		return fmt.Errorf("monitoring.metrics_port must be a valid port number, got %d", config.Monitoring.MetricsPort)
	}
	if config.Monitoring.TracingSampleRate < 0 || config.Monitoring.TracingSampleRate > 1 {
		return fmt.Errorf("monitoring.tracing_sample_rate must be between 0 and 1, got %f", config.Monitoring.TracingSampleRate)
	}

	// Validate HTTP settings
	if config.HTTP.MaxIdleConnections <= 0 {
		return fmt.Errorf("http.max_idle_connections must be positive, got %d", config.HTTP.MaxIdleConnections)
	}
	if config.HTTP.MaxIdleConnectionsPerHost <= 0 {
		return fmt.Errorf("http.max_idle_connections_per_host must be positive, got %d", config.HTTP.MaxIdleConnectionsPerHost)
	}
	if config.HTTP.MaxIdleConnectionsPerHost > config.HTTP.MaxIdleConnections {
		return fmt.Errorf("http.max_idle_connections_per_host (%d) cannot exceed max_idle_connections (%d)",
			config.HTTP.MaxIdleConnectionsPerHost, config.HTTP.MaxIdleConnections)
	}

	// Validate frontier settings
	if config.Frontier.QueueCapacity <= 0 {
		return fmt.Errorf("frontier.queue_capacity must be positive, got %d", config.Frontier.QueueCapacity)
	}
	if config.Frontier.PriorityQueues <= 0 {
		return fmt.Errorf("frontier.priority_queues must be positive, got %d", config.Frontier.PriorityQueues)
	}
	if config.Frontier.PolitenessQueues <= 0 {
		return fmt.Errorf("frontier.politeness_queues must be positive, got %d", config.Frontier.PolitenessQueues)
	}
	if config.Frontier.CheckpointInterval <= 0 {
		return fmt.Errorf("frontier.checkpoint_interval must be positive, got %v", config.Frontier.CheckpointInterval)
	}

	// Validate robots settings
	if config.Robots.CacheDuration <= 0 {
		return fmt.Errorf("robots.cache_duration must be positive, got %v", config.Robots.CacheDuration)
	}

	// Validate Redis settings if Redis is used for frontier persistence
	if config.Frontier.PersistentState && config.Storage.Type == "redis" {
		if config.Redis.Addr == "" {
			return fmt.Errorf("redis.addr is required when using Redis for frontier persistence")
		}
		if config.Redis.PoolSize <= 0 {
			return fmt.Errorf("redis.pool_size must be positive, got %d", config.Redis.PoolSize)
		}
	}

	// Validate security settings
	validTLSVersions := map[string]bool{"1.0": true, "1.1": true, "1.2": true, "1.3": true}
	if !validTLSVersions[config.Security.TlsMinVersion] {
		return fmt.Errorf("invalid security.tls_min_version: %s. Valid options: 1.0, 1.1, 1.2, 1.3", config.Security.TlsMinVersion)
	}

	if config.Security.AuthEnabled {
		validAuthTypes := map[string]bool{"basic": true, "bearer": true, "api_key": true}
		if !validAuthTypes[config.Security.AuthType] {
			return fmt.Errorf("invalid security.auth_type: %s. Valid options: basic, bearer, api_key", config.Security.AuthType)
		}

		if config.Security.AuthType == "basic" && (config.Security.AuthUsername == "" || config.Security.AuthPassword == "") {
			return fmt.Errorf("security.auth_username and auth_password are required for basic auth")
		}
		if (config.Security.AuthType == "bearer" || config.Security.AuthType == "api_key") && config.Security.AuthToken == "" {
			return fmt.Errorf("security.auth_token is required for %s auth", config.Security.AuthType)
		}
	}

	// Validate language codes in content configuration
	validLanguageCodes := map[string]bool{
		"en": true, "es": true, "fr": true, "de": true, "it": true, "pt": true,
		"ru": true, "zh": true, "ja": true, "ko": true, "ar": true, "hi": true,
	}
	for _, lang := range config.Content.Languages {
		if !validLanguageCodes[lang] {
			return fmt.Errorf("unsupported language code: %s. Supported codes: %v", lang, GetKeys(validLanguageCodes))
		}
	}

	// Validate seed URLs format
	for i, url := range config.Crawler.SeedURLs {
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return fmt.Errorf("crawler.seed_urls[%d] must be a valid HTTP/HTTPS URL, got: %s", i, url)
		}
	}

	// Cross-validation: ensure consistent worker and queue settings
	if config.Crawler.ConcurrentWorkers > config.Frontier.QueueCapacity {
		return fmt.Errorf("crawler.concurrent_workers (%d) should not exceed frontier.queue_capacity (%d) to avoid starvation",
			config.Crawler.ConcurrentWorkers, config.Frontier.QueueCapacity)
	}

	// Create required directories
	if err := CreateDirectories(config); err != nil {
		return fmt.Errorf("failed to create required directories: %w", err)
	}

	return nil
}

// getKeys returns the keys of a map[string]bool as a slice
func GetKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// createDirectories creates required directories based on configuration
func CreateDirectories(config *Config) error {
	var dirsToCreate []string

	// Add storage path if using file storage
	if config.Storage.Type == "file" && config.Storage.Path != "" {
		dirsToCreate = append(dirsToCreate, config.Storage.Path)
	}

	// Add BadgerDB path if using BadgerDB
	if config.Storage.Type == "badger" && config.Storage.BadgerPath != "" {
		dirsToCreate = append(dirsToCreate, config.Storage.BadgerPath)
	}

	// Add log file directory
	if config.Monitoring.LogFile != "" {
		logDir := filepath.Dir(config.Monitoring.LogFile)
		if logDir != "." {
			dirsToCreate = append(dirsToCreate, logDir)
		}
	}

	// Create directories
	for _, dir := range dirsToCreate {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// GetLogger creates a configured logger based on monitoring settings
func (c *Config) GetLogger() (*zap.Logger, error) {
	var zapConfig zap.Config

	if c.Monitoring.LogFormat == "json" {
		zapConfig = zap.NewProductionConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
	}

	// Set log level
	level := zap.InfoLevel
	switch c.Monitoring.LogLevel {
	case "debug":
		level = zap.DebugLevel
	case "info":
		level = zap.InfoLevel
	case "warn":
		level = zap.WarnLevel
	case "error":
		level = zap.ErrorLevel
	}
	zapConfig.Level = zap.NewAtomicLevelAt(level)

	// Set output paths
	if c.Monitoring.LogFile != "" {
		zapConfig.OutputPaths = []string{"stdout", c.Monitoring.LogFile}
	}

	return zapConfig.Build()
}

// String returns a string representation of the configuration (with sensitive data redacted)
func (c *Config) String() string {
	// Create a copy and redact sensitive information
	configCopy := *c
	configCopy.Storage.ConnectionString = RedactConnectionString(configCopy.Storage.ConnectionString)
	configCopy.Storage.S3AccessKey = Redact(configCopy.Storage.S3AccessKey)
	configCopy.Storage.S3SecretKey = Redact(configCopy.Storage.S3SecretKey)
	configCopy.Redis.Password = Redact(configCopy.Redis.Password)
	configCopy.Security.AuthPassword = Redact(configCopy.Security.AuthPassword)
	configCopy.Security.AuthToken = Redact(configCopy.Security.AuthToken)

	return fmt.Sprintf("%+v", configCopy)
}

// redact replaces sensitive values with asterisks
func Redact(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + strings.Repeat("*", len(value)-4) + value[len(value)-2:]
}

// redactConnectionString redacts password from database connection string
func RedactConnectionString(connStr string) string {
	if connStr == "" {
		return ""
	}
	// Simple password redaction for PostgreSQL connection strings
	if strings.Contains(connStr, "password=") {
		parts := strings.Split(connStr, " ")
		for i, part := range parts {
			if strings.HasPrefix(part, "password=") {
				parts[i] = "password=****"
				break
			}
		}
		return strings.Join(parts, " ")
	}
	return connStr
}

// GetConfigFileUsed returns the path of the configuration file that was used to load the config
func (c *Config) GetConfigFileUsed() string {
	return c.ConfigFileUsed
}
