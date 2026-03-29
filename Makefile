# Makefile for isrv Go application

# Application name
APP_NAME := isrv

# Build directory
BUILD_DIR := build

# Go module name (from go.mod)
MODULE_NAME := github.com/markhc/isrv

# Build info package path
BUILD_INFO_PKG := $(MODULE_NAME)/internal/configuration

# Build variables
BUILD_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
BUILD_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S')
BUILD_GO_VERSION ?= $(shell go version | awk '{print $$3}')
BUILD_PLATFORM ?= $(shell go env GOOS)/$(shell go env GOARCH)

# Linker flags to inject build information
LDFLAGS := -ldflags "\
	-X '$(BUILD_INFO_PKG).BuildVersion=$(BUILD_VERSION)' \
	-X '$(BUILD_INFO_PKG).BuildCommit=$(BUILD_COMMIT)' \
	-X '$(BUILD_INFO_PKG).BuildDate=$(BUILD_DATE)' \
	-X '$(BUILD_INFO_PKG).BuildGoVersion=$(BUILD_GO_VERSION)' \
	-X '$(BUILD_INFO_PKG).BuildPlatform=$(BUILD_PLATFORM)' \
	-s -w -extldflags '-static'"

# Go build flags for static builds
GO_BUILD_FLAGS := -trimpath -a $(LDFLAGS)

# Environment variables for static builds
BUILD_ENV := CGO_ENABLED=0

# Default target
.PHONY: all
all: clean build

# Build the application
.PHONY: build
build: $(BUILD_DIR)/$(APP_NAME)

$(BUILD_DIR)/$(APP_NAME): clean-build
	@echo "Building $(APP_NAME) for $(BUILD_PLATFORM) (static)..."
	@mkdir -p $(BUILD_DIR)
	$(BUILD_ENV) go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/$(APP_NAME) .
	@echo "Built $(APP_NAME) successfully!"

# Build for multiple platforms
.PHONY: build-all
build-all: clean-build
	@echo "Building for multiple platforms (static)..."
	@mkdir -p $(BUILD_DIR)
	
	@echo "Building for Linux/amd64..."
	$(BUILD_ENV) GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 .
	
	@echo "Building for Linux/arm64..."
	$(BUILD_ENV) GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64 .
	
	@echo "Building for macOS/amd64..."
	$(BUILD_ENV) GOOS=darwin GOARCH=amd64 go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/$(APP_NAME)-darwin-amd64 .
	
	@echo "Building for macOS/arm64..."
	$(BUILD_ENV) GOOS=darwin GOARCH=arm64 go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64 .
	
	@echo "Building for Windows/amd64..."
	$(BUILD_ENV) GOOS=windows GOARCH=amd64 go build $(GO_BUILD_FLAGS) -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe .
	
	@echo "All builds completed!"

# Install the application to GOPATH/bin
.PHONY: install
install:
	@echo "Installing $(APP_NAME) (static)..."
	$(BUILD_ENV) go install $(GO_BUILD_FLAGS) .
	@echo "$(APP_NAME) installed successfully!"

# Quick build (same as build but shorter command)
.PHONY: quick
quick: build

# Build and verify static linking
.PHONY: build-verify
build-verify: build verify-static

# Run the application
.PHONY: run
run:
	@echo "Running $(APP_NAME)..."
	go run . $(ARGS)

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	go test -v ./...

# Run tests with coverage
.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run tests with race detection
.PHONY: test-race
test-race:
	@echo "Running tests with race detection..."
	go test -v -race ./...

# Benchmark tests
.PHONY: bench
bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Run linter
.PHONY: lint
lint:
	@echo "Running linter..."
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	golangci-lint run

# Vet code
.PHONY: vet
vet:
	@echo "Vetting code..."
	go vet ./...

# Download and tidy dependencies
.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Update dependencies
.PHONY: deps-update
deps-update:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

# Generate code (if needed)
.PHONY: generate
generate:
	@echo "Generating code..."
	go generate ./...

# Clean build artifacts
.PHONY: clean
clean: clean-build clean-test

.PHONY: clean-build
clean-build:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)

.PHONY: clean-test
clean-test:
	@echo "Cleaning test artifacts..."
	rm -f coverage.out coverage.html

# Show build info
.PHONY: info
info:
	@echo "Build Information:"
	@echo "  App Name: $(APP_NAME)"
	@echo "  Module: $(MODULE_NAME)"
	@echo "  Version: $(BUILD_VERSION)"
	@echo "  Commit: $(BUILD_COMMIT)"
	@echo "  Date: $(BUILD_DATE)"
	@echo "  Go Version: $(BUILD_GO_VERSION)"
	@echo "  Platform: $(BUILD_PLATFORM)"

# Development workflow
.PHONY: dev
dev: clean fmt vet lint test build
	@echo "Development build completed successfully!"

# Release workflow
.PHONY: release
release: clean fmt vet lint test-coverage build-all
	@echo "Release build completed successfully!"

# Help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all          - Clean and build (default)"
	@echo "  build        - Build the application (static binary)"
	@echo "  build-all    - Build for multiple platforms (static binaries)"
	@echo "  build-verify - Build and verify static linking"
	@echo "  verify-static- Verify that built binary is statically linked"
	@echo "  quick        - Quick build (alias for build)"
	@echo "  install      - Install to GOPATH/bin"
	@echo "  run          - Run the application (use ARGS=... for arguments)"
	@echo "  test         - Run tests"
	@echo "  test-coverage- Run tests with coverage report"
	@echo "  test-race    - Run tests with race detection"
	@echo "  bench        - Run benchmark tests"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  vet          - Vet code"
	@echo "  deps         - Download and tidy dependencies"
	@echo "  deps-update  - Update dependencies"
	@echo "  generate     - Generate code"
	@echo "  clean        - Clean build and test artifacts"
	@echo "  info         - Show build information"
	@echo "  dev          - Development workflow (fmt, vet, lint, test, build)"
	@echo "  release      - Release workflow (fmt, vet, lint, test-coverage, build-all)"
	@echo "  help         - Show this help"
	@echo ""
	@echo "All builds create statically linked binaries for maximum portability."