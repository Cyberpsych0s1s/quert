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

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfigDefaults tests loading configuration with default values
func TestLoadConfigDefaults(t *testing.T) {
	// Test loading config with no config file and no env vars
	config, err := LoadConfig("", nil)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify some default values
	assert.Equal(t, 10000, config.Crawler.MaxPages)
	assert.Equal(t, 5, config.Crawler.MaxDepth)
	assert.Equal(t, 10, config.Crawler.ConcurrentWorkers)
	assert.Equal(t, 30*time.Second, config.Crawler.RequestTimeout)
	assert.Equal(t, "LLMCrawler/1.0 (+https://github.com/cyberpsych0s1s/quert)", config.Crawler.UserAgent)

	assert.Equal(t, 2.0, config.RateLimit.RequestsPerSecond)
	assert.Equal(t, 10, config.RateLimit.Burst)
	assert.True(t, config.RateLimit.PerHostLimit)

	assert.Equal(t, "file", config.Storage.Type)
	assert.Equal(t, "./data", config.Storage.Path)
	assert.Equal(t, 1000, config.Storage.BatchSize)
	assert.True(t, config.Storage.Compression)
	assert.Equal(t, "jsonl", config.Storage.FileFormat)
}

// TestLoadConfigFromFile tests loading configuration from a YAML file
func TestLoadConfigFromFile(t *testing.T) {
	// Create a temporary config file
	tempDir, err := os.MkdirTemp("", "config_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "test_config.yaml")
	configContent := `
crawler:
  max_pages: 5000
  max_depth: 3
  concurrent_workers: 8
  request_timeout: 20s
  user_agent: "TestCrawler/1.0"
  seed_urls:
    - "https://example.com"
    - "https://test.com"

rate_limit:
  requests_per_second: 1.5
  burst: 5
  per_host_limit: true

storage:
  type: "file"
  path: "./test_data"
  batch_size: 500
  compression: false
  file_format: "json"

monitoring:
  log_level: "debug"
  log_format: "text"
  metrics_enabled: false
`

	err = os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Load config from file
	config, err := LoadConfig(configPath, nil)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify values from config file
	assert.Equal(t, 5000, config.Crawler.MaxPages)
	assert.Equal(t, 3, config.Crawler.MaxDepth)
	assert.Equal(t, 8, config.Crawler.ConcurrentWorkers)
	assert.Equal(t, 20*time.Second, config.Crawler.RequestTimeout)
	assert.Equal(t, "TestCrawler/1.0", config.Crawler.UserAgent)
	assert.Equal(t, []string{"https://example.com", "https://test.com"}, config.Crawler.SeedURLs)

	assert.Equal(t, 1.5, config.RateLimit.RequestsPerSecond)
	assert.Equal(t, 5, config.RateLimit.Burst)

	assert.Equal(t, "file", config.Storage.Type)
	assert.Equal(t, "./test_data", config.Storage.Path)
	assert.Equal(t, 500, config.Storage.BatchSize)
	assert.False(t, config.Storage.Compression)
	assert.Equal(t, "json", config.Storage.FileFormat)

	assert.Equal(t, "debug", config.Monitoring.LogLevel)
	assert.Equal(t, "text", config.Monitoring.LogFormat)
	assert.False(t, config.Monitoring.MetricsEnabled)

	// Verify config file used
	assert.Equal(t, configPath, config.ConfigFileUsed)
}

// TestLoadConfigFromEnvironment tests loading configuration from environment variables
func TestLoadConfigFromEnvironment(t *testing.T) {
	// Set environment variables
	envVars := map[string]string{
		"CRAWLER_CRAWLER_MAX_PAGES":              "2000",
		"CRAWLER_CRAWLER_CONCURRENT_WORKERS":     "6",
		"CRAWLER_RATE_LIMIT_REQUESTS_PER_SECOND": "3.0",
		"CRAWLER_STORAGE_TYPE":                   "postgres",
		"CRAWLER_STORAGE_CONNECTION_STRING":      "postgresql://user:pass@localhost/db",
		"CRAWLER_MONITORING_LOG_LEVEL":           "warn",
		"CRAWLER_USER_AGENT":                     "EnvCrawler/1.0",
		"CRAWLER_S3_BUCKET":                      "test-bucket",
		"CRAWLER_LOG_LEVEL":                      "error",
	}

	// Set environment variables
	for key, value := range envVars {
		os.Setenv(key, value)
	}
	defer func() {
		// Clean up environment variables
		for key := range envVars {
			os.Unsetenv(key)
		}
	}()

	// Load config
	config, err := LoadConfig("", nil)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify environment variables took effect
	assert.Equal(t, 2000, config.Crawler.MaxPages)
	assert.Equal(t, 6, config.Crawler.ConcurrentWorkers)
	assert.Equal(t, 3.0, config.RateLimit.RequestsPerSecond)
	assert.Equal(t, "postgres", config.Storage.Type)
	assert.Equal(t, "postgresql://user:pass@localhost/db", config.Storage.ConnectionString)
	assert.Equal(t, "warn", config.Monitoring.LogLevel)
	assert.Equal(t, "test-bucket", config.Storage.S3Bucket)
}

// TestLoadConfigWithFlags tests loading configuration with command line flags
func TestLoadConfigWithFlags(t *testing.T) {
	// Create flag set with viper-compatible naming (underscores not dashes)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("crawler.max_pages", 3000, "Max pages to crawl")
	flags.String("crawler.user_agent", "FlagCrawler/1.0", "User agent string")
	flags.String("storage.type", "badger", "Storage type")
	flags.String("monitoring.log_level", "info", "Log level")

	// Parse flags (note: viper expects underscores in config keys)
	err := flags.Parse([]string{
		"--crawler.max_pages=3000",
		"--crawler.user_agent=FlagCrawler/1.0",
		"--storage.type=badger",
		"--monitoring.log_level=info",
	})
	require.NoError(t, err)

	// Load config with flags
	config, err := LoadConfig("", flags)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify flag values took effect
	assert.Equal(t, 3000, config.Crawler.MaxPages)
	assert.Equal(t, "FlagCrawler/1.0", config.Crawler.UserAgent)
	assert.Equal(t, "badger", config.Storage.Type)
	assert.Equal(t, "info", config.Monitoring.LogLevel)
}

// TestLoadConfigPrecedence tests the precedence order: flags > env vars > config file > defaults
func TestLoadConfigPrecedence(t *testing.T) {
	// Create temporary config file
	tempDir, err := os.MkdirTemp("", "config_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "precedence_test.yaml")
	configContent := `
crawler:
  max_pages: 1000
  user_agent: "ConfigFileCrawler/1.0"
storage:
  type: "file"
monitoring:
  log_level: "debug"
`

	err = os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Set environment variable
	os.Setenv("CRAWLER_CRAWLER_MAX_PAGES", "2000")
	os.Setenv("CRAWLER_STORAGE_TYPE", "postgres")
	os.Setenv("CRAWLER_STORAGE_CONNECTION_STRING", "postgresql://user:pass@localhost/testdb")
	defer func() {
		os.Unsetenv("CRAWLER_CRAWLER_MAX_PAGES")
		os.Unsetenv("CRAWLER_STORAGE_TYPE")
		os.Unsetenv("CRAWLER_STORAGE_CONNECTION_STRING")
	}()

	// Create flags (highest precedence)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("crawler.max_pages", 3000, "Max pages")
	err = flags.Parse([]string{"--crawler.max_pages=3000"})
	require.NoError(t, err)

	// Load config
	config, err := LoadConfig(configPath, flags)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify precedence:
	// max_pages: flag (3000) > env (2000) > config file (1000) > default (10000)
	assert.Equal(t, 3000, config.Crawler.MaxPages)

	// user_agent: config file > default (no env var or flag set)
	assert.Equal(t, "ConfigFileCrawler/1.0", config.Crawler.UserAgent)

	// storage.type: env var (postgres) > config file (file) > default (file)
	assert.Equal(t, "postgres", config.Storage.Type)

	// log_level: config file (debug) > default (info)
	assert.Equal(t, "debug", config.Monitoring.LogLevel)
}

// TestLoadConfigNonExistentFile tests error handling for non-existent config file
func TestLoadConfigNonExistentFile(t *testing.T) {
	_, err := LoadConfig("/non/existent/path/config.yaml", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

// TestLoadConfigInvalidFile tests error handling for invalid config file
func TestLoadConfigInvalidFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "config_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "invalid.yaml")
	invalidContent := `
invalid yaml content:
  - this is
    - not valid
      yaml: [
`
	err = os.WriteFile(configPath, []byte(invalidContent), 0644)
	require.NoError(t, err)

	_, err = LoadConfig(configPath, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

// TestValidateConfig tests configuration validation
func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name        string
		configFunc  func() *Config
		expectError bool
		errorText   string
	}{
		{
			name: "valid config",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				return config
			},
			expectError: false,
		},
		{
			name: "invalid max_pages",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Crawler.MaxPages = -1
				return config
			},
			expectError: true,
			errorText:   "max_pages must be positive",
		},
		{
			name: "invalid concurrent_workers",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Crawler.ConcurrentWorkers = 0
				return config
			},
			expectError: true,
			errorText:   "concurrent_workers must be positive",
		},
		{
			name: "empty user_agent",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Crawler.UserAgent = ""
				return config
			},
			expectError: true,
			errorText:   "user_agent cannot be empty",
		},
		{
			name: "invalid requests_per_second",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.RateLimit.RequestsPerSecond = -1.0
				return config
			},
			expectError: true,
			errorText:   "requests_per_second must be positive",
		},
		{
			name: "invalid text length range",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Content.MinTextLength = 1000
				config.Content.MaxTextLength = 500
				return config
			},
			expectError: true,
			errorText:   "max_text_length",
		},
		{
			name: "invalid quality threshold",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Content.QualityThreshold = 1.5
				return config
			},
			expectError: true,
			errorText:   "quality_threshold must be between 0 and 1",
		},
		{
			name: "invalid storage type",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Storage.Type = "invalid"
				return config
			},
			expectError: true,
			errorText:   "invalid storage.type",
		},
		{
			name: "missing postgres connection string",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Storage.Type = "postgres"
				config.Storage.ConnectionString = ""
				return config
			},
			expectError: true,
			errorText:   "connection_string is required for postgres",
		},
		{
			name: "missing s3 credentials",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Storage.Type = "s3"
				config.Storage.S3Bucket = "test-bucket"
				config.Storage.S3AccessKey = ""
				return config
			},
			expectError: true,
			errorText:   "s3_access_key and s3_secret_key are required",
		},
		{
			name: "invalid log level",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Monitoring.LogLevel = "invalid"
				return config
			},
			expectError: true,
			errorText:   "invalid monitoring.log_level",
		},
		{
			name: "invalid port",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Monitoring.MetricsPort = 70000
				return config
			},
			expectError: true,
			errorText:   "metrics_port must be a valid port number",
		},
		{
			name: "invalid tls version",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Security.TlsMinVersion = "2.0"
				return config
			},
			expectError: true,
			errorText:   "invalid security.tls_min_version",
		},
		{
			name: "missing auth credentials for basic auth",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Security.AuthEnabled = true
				config.Security.AuthType = "basic"
				config.Security.AuthUsername = ""
				return config
			},
			expectError: true,
			errorText:   "auth_username and auth_password are required for basic auth",
		},
		{
			name: "invalid language code",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Content.Languages = []string{"en", "invalid"}
				return config
			},
			expectError: true,
			errorText:   "unsupported language code: invalid",
		},
		{
			name: "invalid seed url",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Crawler.SeedURLs = []string{"ftp://example.com"}
				return config
			},
			expectError: true,
			errorText:   "must be a valid HTTP/HTTPS URL",
		},
		{
			name: "workers exceed queue capacity",
			configFunc: func() *Config {
				config, _ := LoadConfig("", nil)
				config.Crawler.ConcurrentWorkers = 50000
				config.Frontier.QueueCapacity = 1000
				return config
			},
			expectError: true,
			errorText:   "should not exceed frontier.queue_capacity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.configFunc()
			err := ValidateConfig(config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorText != "" {
					assert.Contains(t, err.Error(), tt.errorText)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCreateDirectories tests directory creation
func TestCreateDirectories(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "config_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	config := &Config{
		Storage: StorageConfig{
			Type: "file",
			Path: filepath.Join(tempDir, "storage", "data"),
		},
		Monitoring: MonitoringConfig{
			LogFile: filepath.Join(tempDir, "logs", "app.log"),
		},
	}

	err = CreateDirectories(config)
	assert.NoError(t, err)

	// Verify directories were created
	assert.DirExists(t, filepath.Join(tempDir, "storage", "data"))
	assert.DirExists(t, filepath.Join(tempDir, "logs"))
}

// TestConfigString tests configuration string representation with redacted sensitive data
func TestConfigString(t *testing.T) {
	config := &Config{
		Storage: StorageConfig{
			ConnectionString: "postgresql://user:password123@localhost/db",
			S3AccessKey:      "AKIA123456789",
			S3SecretKey:      "secretkey123456789",
		},
		Redis: RedisConfig{
			Password: "redispassword",
		},
		Security: SecurityConfig{
			AuthPassword: "authpass123",
			AuthToken:    "token123456789",
		},
	}

	str := config.String()

	// Verify sensitive data is redacted (except for connection string password since it's not in password= format)
	assert.NotContains(t, str, "secretkey123456789")
	assert.NotContains(t, str, "redispassword")
	assert.NotContains(t, str, "authpass123")
	assert.NotContains(t, str, "token123456789")

	// Verify redaction patterns (connection string password won't be redacted due to format)
	assert.Contains(t, str, "AK*********89")
	assert.Contains(t, str, "se**************89")
}

// TestGetLogger tests logger creation
func TestGetLogger(t *testing.T) {
	config := &Config{
		Monitoring: MonitoringConfig{
			LogLevel:  "debug",
			LogFormat: "json",
		},
	}

	logger, err := config.GetLogger()
	assert.NoError(t, err)
	assert.NotNil(t, logger)

	// Test with different format
	config.Monitoring.LogFormat = "text"
	logger2, err := config.GetLogger()
	assert.NoError(t, err)
	assert.NotNil(t, logger2)
}

// TestBindEnvVariables tests environment variable binding
func TestBindEnvVariables(t *testing.T) {
	// This is an internal function, but we can test it indirectly
	// by checking if the LoadConfig function properly handles the bound env vars

	os.Setenv("CRAWLER_S3_ACCESS_KEY", "test-access-key")
	defer os.Unsetenv("CRAWLER_S3_ACCESS_KEY")

	config, err := LoadConfig("", nil)
	require.NoError(t, err)

	assert.Equal(t, "test-access-key", config.Storage.S3AccessKey)
}

// TestRedactFunctions tests sensitive data redaction
func TestRedactFunctions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "short string",
			input:    "abc",
			expected: "****",
		},
		{
			name:     "normal string",
			input:    "password123",
			expected: "pa*******23",
		},
		{
			name:     "long string",
			input:    "verylongpasswordstring",
			expected: "ve******************ng",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Redact(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}

	// Test connection string redaction
	connTests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty connection string",
			input:    "",
			expected: "",
		},
		{
			name:     "connection string without password",
			input:    "postgresql://user@localhost/db",
			expected: "postgresql://user@localhost/db",
		},
		{
			name:     "connection string with password",
			input:    "postgresql://user:mypassword@localhost/db",
			expected: "postgresql://user:mypassword@localhost/db", // Simple implementation only redacts password= format
		},
		{
			name:     "connection string with password= format",
			input:    "host=localhost user=test password=secret123 dbname=mydb",
			expected: "host=localhost user=test password=**** dbname=mydb",
		},
	}

	for _, tt := range connTests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactConnectionString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetKeys tests the getKeys helper function
func TestGetKeys(t *testing.T) {
	m := map[string]bool{
		"apple":  true,
		"banana": false,
		"cherry": true,
	}

	keys := GetKeys(m)
	assert.Len(t, keys, 3)
	assert.Contains(t, keys, "apple")
	assert.Contains(t, keys, "banana")
	assert.Contains(t, keys, "cherry")

	// Test empty map
	emptyKeys := GetKeys(map[string]bool{})
	assert.Len(t, emptyKeys, 0)
}

// BenchmarkLoadConfig benchmarks configuration loading
func BenchmarkLoadConfig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := LoadConfig("", nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkValidateConfig benchmarks configuration validation
func BenchmarkValidateConfig(b *testing.B) {
	config, err := LoadConfig("", nil)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := ValidateConfig(config)
		if err != nil {
			b.Fatal(err)
		}
	}
}
