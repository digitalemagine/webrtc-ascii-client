# WebRTC ASCII Video Client Makefile

.PHONY: help build termplay webrtc-recv release run clean test fmt vet lint lint-fix lint-install install deps tidy demo version check-deps

# Variables
BINARY_NAME=webrtc-ascii-client
MAIN_PATH=./cmd/webrtc-ascii-client
# termplay is pure Go (no cgo, no FFmpeg libraries needed)
TERMPLAY_PATH=./cmd/termplay
# webrtc-recv is pure Go too
RECV_PATH=./cmd/webrtc-recv
BUILD_DIR=./bin

# Version information
# Only the version is passed in; commit and commit time come from the build
# information the Go toolchain embeds (see internal/version).
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
VERSION_PKG=github.com/digitalemagine/webrtc-ascii-client/internal/version

# Build flags
VERSION_LDFLAGS=-X $(VERSION_PKG).Version=$(VERSION)
LDFLAGS=-ldflags "$(VERSION_LDFLAGS)"
# Release builds drop the symbol table and DWARF (-s -w): about 35% smaller.
RELEASE_LDFLAGS=-ldflags "-s -w $(VERSION_LDFLAGS)"

# Linting
GOLANGCI_LINT_VERSION=v2.15.0
LINT_CACHE_DIR=.cache

# Default target
help: ## Show this help message
	@echo "WebRTC ASCII Video Client"
	@echo "========================="
	@echo ""
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# Build targets
build: termplay webrtc-recv ## Build the binaries (webrtc-ascii-client, termplay, webrtc-recv)
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(BINARY_NAME)"

termplay: ## Build only termplay (pure Go)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/termplay $(TERMPLAY_PATH)
	@echo "Built: $(BUILD_DIR)/termplay"

webrtc-recv: ## Build only webrtc-recv (pure Go)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/webrtc-recv $(RECV_PATH)
	@echo "Built: $(BUILD_DIR)/webrtc-recv"

release: ## Build stripped binaries for distribution
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -trimpath $(RELEASE_LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	CGO_ENABLED=0 go build -trimpath $(RELEASE_LDFLAGS) -o $(BUILD_DIR)/termplay $(TERMPLAY_PATH)
	CGO_ENABLED=0 go build -trimpath $(RELEASE_LDFLAGS) -o $(BUILD_DIR)/webrtc-recv $(RECV_PATH)
	@echo "Built: $(BUILD_DIR)/$(BINARY_NAME), $(BUILD_DIR)/termplay, $(BUILD_DIR)/webrtc-recv (stripped)"

install: ## Install stripped binaries to $GOPATH/bin
	CGO_ENABLED=1 go install -trimpath $(RELEASE_LDFLAGS) $(MAIN_PATH)
	CGO_ENABLED=0 go install -trimpath $(RELEASE_LDFLAGS) $(TERMPLAY_PATH)
	CGO_ENABLED=0 go install -trimpath $(RELEASE_LDFLAGS) $(RECV_PATH)

# Development targets
run: build ## Build and run the application
	@$(BUILD_DIR)/$(BINARY_NAME)

demo: build ## Run the demo mode
	@$(BUILD_DIR)/$(BINARY_NAME) -demo

test: ## Run tests
	go test -v ./...

fmt: ## Format code
	go fmt ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (v2)
	@command -v golangci-lint >/dev/null 2>&1 || (echo "Error: golangci-lint not found. Run 'make lint-install' first." && exit 1)
	@lint_go=$$(golangci-lint version | sed -n 's/.*built with \(go[0-9.]*\).*/\1/p'); \
	module_go=$$(sed -n 's/^go //p' go.mod | head -n1); \
	if [ -n "$$lint_go" ] && [ -n "$$module_go" ] && [ "$$(printf '%s\n%s\n' "$${lint_go#go}" "$$module_go" | sort -V | head -n1)" != "$$module_go" ]; then \
		echo "Error: golangci-lint was built with $$lint_go but this module requires go$$module_go+."; \
		echo "Run 'make lint-install' to rebuild the pinned version with a newer Go toolchain."; \
		exit 1; \
	fi
	@mkdir -p $(LINT_CACHE_DIR)/go-build $(LINT_CACHE_DIR)/golangci-lint
	GOCACHE=$(PWD)/$(LINT_CACHE_DIR)/go-build GOLANGCI_LINT_CACHE=$(PWD)/$(LINT_CACHE_DIR)/golangci-lint golangci-lint run --config .golangci.yml ./...

lint-fix: ## Run golangci-lint with auto-fixes
	@command -v golangci-lint >/dev/null 2>&1 || (echo "Error: golangci-lint not found. Run 'make lint-install' first." && exit 1)
	@lint_go=$$(golangci-lint version | sed -n 's/.*built with \(go[0-9.]*\).*/\1/p'); \
	module_go=$$(sed -n 's/^go //p' go.mod | head -n1); \
	if [ -n "$$lint_go" ] && [ -n "$$module_go" ] && [ "$$(printf '%s\n%s\n' "$${lint_go#go}" "$$module_go" | sort -V | head -n1)" != "$$module_go" ]; then \
		echo "Error: golangci-lint was built with $$lint_go but this module requires go$$module_go+."; \
		echo "Run 'make lint-install' to rebuild the pinned version with a newer Go toolchain."; \
		exit 1; \
	fi
	@mkdir -p $(LINT_CACHE_DIR)/go-build $(LINT_CACHE_DIR)/golangci-lint
	GOCACHE=$(PWD)/$(LINT_CACHE_DIR)/go-build GOLANGCI_LINT_CACHE=$(PWD)/$(LINT_CACHE_DIR)/golangci-lint golangci-lint run --config .golangci.yml --fix ./...

lint-install: ## Install/update golangci-lint (pinned)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Dependency management
deps: ## Download dependencies
	go mod download

tidy: ## Clean up dependencies
	go mod tidy

# Cleanup
clean: ## Clean build artifacts
	rm -rf $(BUILD_DIR)
	go clean

# Utility targets
version: build ## Show version information
	@$(BUILD_DIR)/$(BINARY_NAME) -version

# Check system dependencies
check-deps: ## Check if required system dependencies are available
	@which pkg-config > /dev/null || (echo "Error: pkg-config not found" && exit 1)
	@pkg-config --exists libavcodec libavutil libswscale || (echo "Error: FFmpeg development libraries not found" && exit 1)
	@echo "✓ All system dependencies found"
