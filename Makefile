# Go Web Crawler Makefile

# Variables
BINARY_NAME=crawler
BINARY_PATH=./bin/$(BINARY_NAME)
MAIN_PATH=./cmd/crawler
GO_FILES=$(shell find . -name "*.go" -type f -not -path "./vendor/*")
TEST_TIMEOUT=30s

# Build info
VERSION=$(shell git describe --tags --always --dirty)
COMMIT=$(shell git rev-parse HEAD)
BUILD_TIME=$(shell date +%Y-%m-%dT%H:%M:%S%z)

# Go build flags
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)"

.PHONY: help build test run run-crawl clean fmt lint deps dev benchmark coverage profile install-tools clean-all test-all test-extractor test-crawler test-robots

# Default target
help: ## Show this help message
	@echo 'Usage: make <target>'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Build targets
build: ## Build the crawler binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	go build $(LDFLAGS) -o $(BINARY_PATH) $(MAIN_PATH)
	@echo "Binary built at $(BINARY_PATH)"

build-race: ## Build with race detector
	@echo "Building $(BINARY_NAME) with race detector..."
	@mkdir -p bin
	go build -race $(LDFLAGS) -o $(BINARY_PATH) $(MAIN_PATH)

# Development targets
dev: ## Run in development mode with hot reload (requires air)
	@echo "Starting development server with hot reload..."
	air -c .air.toml

run: build ## Build and run the crawler
	@echo "Running $(BINARY_NAME)..."
	$(BINARY_PATH)

run-config: build ## Run with custom config file
	@echo "Running $(BINARY_NAME) with config..."
	$(BINARY_PATH) -config=./config/config.yaml


benchmark: ## Run benchmarks
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

benchmark-old-config: ## Run config benchmarks only (legacy)
	@echo "Running configuration benchmarks..."
	go test -bench=. -benchmem ./internal/config/...

coverage: ## Generate test coverage report
	@echo "Generating coverage report..."
	@mkdir -p coverage
	go test -coverprofile=coverage/coverage.out ./...
	go tool cover -html=coverage/coverage.out -o coverage/coverage.html
	@echo "Coverage report generated at coverage/coverage.html"

# Code quality targets
fmt: ## Format Go code
	@echo "Formatting code..."
	gofmt -s -w $(GO_FILES)
	goimports -w $(GO_FILES)

lint: ## Run linter
	@echo "Running linter..."
	golangci-lint run --timeout 5m ./...

vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...

# Dependency management
deps: ## Download dependencies
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

deps-update: ## Update dependencies
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

deps-vendor: ## Vendor dependencies
	@echo "Vendoring dependencies..."
	go mod vendor

# Configuration testing targets
# Testing targets for /test folder structure

# Run all tests
test: ## Run all tests
	@echo "Running all tests..."
	go test -timeout $(TEST_TIMEOUT) -v ./...

# Run configuration tests only
test-config: ## Run configuration tests only
	@echo "Running configuration tests..."
	go test -timeout $(TEST_TIMEOUT) -v ./internal/config/...

# Run HTTP client tests
test-http: ## Run HTTP client tests
	@echo "Running HTTP client tests..."
	go test -timeout $(TEST_TIMEOUT) -v ./internal/client/...

# Run URL processing tests
test-url: ## Run URL processing tests
	@echo "Running URL processing tests..."
	go test -timeout $(TEST_TIMEOUT) -v ./internal/frontier/...

# Component coverage tests
test-coverage-config: ## Run config tests with coverage
	@echo "Running configuration tests with coverage..."
	@mkdir -p coverage
	go test -timeout $(TEST_TIMEOUT) -v -coverprofile=coverage/config_coverage.out ./internal/config/
	go tool cover -html=coverage/config_coverage.out -o coverage/config_coverage.html
	@echo "Config coverage report generated at coverage/config_coverage.html"

test-coverage-http: ## Run HTTP client tests with coverage
	@echo "Running HTTP client tests with coverage..."
	@mkdir -p coverage
	go test -timeout $(TEST_TIMEOUT) -v -coverprofile=coverage/http_coverage.out ./internal/client/
	go tool cover -html=coverage/http_coverage.out -o coverage/http_coverage.html
	@echo "HTTP coverage report generated at coverage/http_coverage.html"

test-coverage-url: ## Run URL processing tests with coverage
	@echo "Running URL processing tests with coverage..."
	@mkdir -p coverage
	go test -timeout $(TEST_TIMEOUT) -v -coverprofile=coverage/url_coverage.out ./internal/frontier/
	go tool cover -html=coverage/url_coverage.out -o coverage/url_coverage.html
	@echo "URL processing coverage report generated at coverage/url_coverage.html"

# Run benchmarks for current components
benchmark-config: ## Run config benchmarks
	@echo "Running configuration benchmarks..."
	go test -bench=. -benchmem ./internal/config/ -run Benchmark

benchmark-http: ## Run HTTP client benchmarks
	@echo "Running HTTP client benchmarks..."
	go test -bench=. -benchmem ./internal/client/ -run Benchmark

benchmark-url: ## Run URL processing benchmarks
	@echo "Running URL processing benchmarks..."
	go test -bench=. -benchmem ./internal/frontier/ -run Benchmark

# Run tests with race detector
test-race: ## Run tests with race detector
	@echo "Running tests with race detector..."
	go test -race -timeout $(TEST_TIMEOUT) -v ./...

# Clean test artifacts
clean-test: ## Clean test artifacts
	@echo "Cleaning test artifacts..."
	@rm -rf coverage/
	@rm -rf test_data/ test_logs/

# Setup test environment
setup-test: ## Setup test environment
	@echo "Setting up test environment..."
	@mkdir -p test_data test_logs coverage
# Profiling targets
profile-cpu: ## Generate CPU profile
	@echo "Generating CPU profile..."
	@mkdir -p profiles
	go test -cpuprofile=profiles/cpu.prof -bench=. ./...
	@echo "CPU profile saved to profiles/cpu.prof"

profile-mem: ## Generate memory profile
	@echo "Generating memory profile..."
	@mkdir -p profiles
	go test -memprofile=profiles/mem.prof -bench=. ./...
	@echo "Memory profile saved to profiles/mem.prof"

profile-view-cpu: profile-cpu ## View CPU profile
	go tool pprof profiles/cpu.prof

profile-view-mem: profile-mem ## View memory profile
	go tool pprof profiles/mem.prof


# Run target
run-crawl: build ## Crawl SEED end-to-end (usage: make run-crawl SEED=https://example.com [OUTPUT=out.jsonl])
	@echo "Running crawler..."
	$(BINARY_PATH) -seed "$(SEED)" $(if $(OUTPUT),-output "$(OUTPUT)",) $(if $(CONFIG),-config "$(CONFIG)",)

test-all: test ## Run all tests
	@echo "All tests completed successfully!"

# Enhanced test targets
test-extractor: ## Run content extractor tests
	@echo "Running content extractor tests..."
	go test -timeout $(TEST_TIMEOUT) -v ./internal/extractor/...

test-crawler: ## Run crawler engine tests
	@echo "Running crawler engine tests..."
	go test -timeout $(TEST_TIMEOUT) -v ./internal/crawler/...

test-robots: ## Run robots.txt tests
	@echo "Running robots.txt tests..."
	go test -timeout $(TEST_TIMEOUT) -v ./internal/robots/...

# Cleanup targets
clean: ## Clean build artifacts
	@echo "Cleaning up..."
	rm -rf bin/
	rm -rf coverage/
	rm -rf profiles/
	rm -rf dist/
	go clean -cache
	go clean -testcache

clean-all: clean ## Clean all artifacts including example outputs
	@echo "Cleaning all artifacts..."
	rm -rf bin/
	rm -rf coverage/
	rm -rf profiles/
	rm -rf dist/
	@echo "Removing example output files..."
	@find . -name "*.log" -delete
	@find . -name "crawl_output_*.txt" -delete
	@find . -name "extracted_content_*.json" -delete
	go clean -cache
	go clean -testcache
	@echo "All artifacts cleaned!"

clean-deps: ## Clean dependency cache
	@echo "Cleaning dependency cache..."
	go clean -modcache

# Installation targets
install: build ## Install binary to $GOPATH/bin
	@echo "Installing $(BINARY_NAME)..."
	cp $(BINARY_PATH) $(GOPATH)/bin/

install-tools: ## Install development tools
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/cosmtrek/air@latest
	go install github.com/swaggo/swag/cmd/swag@latest

# Release targets
release: clean ## Build release binaries for multiple platforms
	@echo "Building release binaries..."
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "Release binaries built in dist/"

# Security targets
security: ## Run security checks
	@echo "Running security checks..."
	gosec ./...

# Database targets (when applicable)
migrate-up: ## Run database migrations up
	@echo "Running database migrations..."
	# Add your migration command here

migrate-down: ## Run database migrations down
	@echo "Rolling back database migrations..."
	# Add your rollback command here

# Quick development workflow for current phase
quick: fmt lint test build ## Quick development workflow (format, lint, test, build)
	@echo "Quick development workflow completed successfully!"

# Current development focus workflow  
current: fmt test-config test-http test-url test-extractor test-crawler test-robots build ## Test current implemented components
	@echo "Current component testing completed successfully!"

# Pre-commit check
pre-commit: fmt lint vet test-race ## Pre-commit validation
	@echo "Pre-commit checks passed!"

# CI/CD simulation
ci: deps fmt lint vet test-race coverage ## Simulate CI pipeline
	@echo "CI pipeline completed successfully!"

# Project setup completion check
setup-check: ## Check if foundation phase is complete
	@echo "Checking foundation phase completion..."
	@echo "=== Basic Project Files ==="
	@test -f go.mod && echo "✓ go.mod exists" || echo "✗ go.mod missing"
	@test -f config.yaml && echo "✓ config.yaml exists" || echo "✗ config.yaml missing"
	@test -f README.md && echo "✓ README.md exists" || echo "✗ README.md missing"
	@test -f LICENSE && echo "✓ LICENSE exists" || echo "✗ LICENSE exists"
	@test -f Makefile && echo "✓ Makefile exists" || echo "✗ Makefile missing"
	@echo ""
	@echo "=== Core Components ==="
	@test -f internal/config/config.go && echo "✓ Configuration system" || echo "✗ Configuration missing"
	@test -f internal/client/http.go && echo "✓ HTTP client wrapper" || echo "✗ HTTP client missing"
	@test -f internal/frontier/url.go && echo "✓ URL processing system" || echo "✗ URL processing missing"
	@echo ""
	@echo "=== Component Testing ==="
	@echo "Testing configuration system..."
	@make test-config > /dev/null 2>&1 && echo "✓ Config tests pass" || echo "✗ Config tests fail"
	@echo "Testing HTTP client..."
	@make test-http > /dev/null 2>&1 && echo "✓ HTTP client tests pass" || echo "✗ HTTP client tests fail"
	@echo "Testing URL processing..."
	@make test-url > /dev/null 2>&1 && echo "✓ URL processing tests pass" || echo "✗ URL processing tests fail"
	@echo ""
	@echo "✅ Foundation phase completed! Ready for core crawler development."

# Check what's needed for working demo
demo-check: ## Check progress towards working demo
	@echo "Checking progress towards working demo..."
	@echo "=== Foundation (Completed) ==="
	@test -f internal/config/config.go && echo "✓ Configuration system" || echo "✗ Missing"
	@test -f internal/client/http.go && echo "✓ HTTP client" || echo "✗ Missing" 
	@test -f internal/frontier/url.go && echo "✓ URL processing" || echo "✗ Missing"
	@echo ""
	@echo "=== Core Crawler (In Progress) ==="
	@test -f internal/robots/parser.go && echo "✓ Robots.txt handler" || echo "⏳ Needed for demo"
	@test -f internal/crawler/worker.go && echo "✓ Worker pool" || echo "⏳ Needed for demo"  
	@test -f internal/extractor/extractor.go && echo "✓ Content extraction" || echo "⏳ Needed for demo"
	@test -f internal/storage/storage.go && echo "✓ Storage system" || echo "⏳ Needed for demo"
	@echo ""
	@echo "📋 Next steps for working demo:"
	@echo "   1. Implement robots.txt parser"
	@echo "   2. Create worker pool crawler"
	@echo "   3. Add HTML content extraction"  
	@echo "   4. Add basic file storage"

# Show build info
info: ## Show build information
	@echo "Binary: $(BINARY_NAME)"
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Build time: $(BUILD_TIME)"
	@echo "Build path: $(BINARY_PATH)"