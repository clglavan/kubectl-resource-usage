# Makefile for kubectl-resource-usage

# Project settings
BINARY_NAME := kubectl-resource_usage
PROJECT_NAME := kubectl-resource-usage
VERSION ?= v0.1.0
COMMIT_HASH := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go settings
GO_VERSION := $(shell go version | cut -d' ' -f3)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH) -X main.buildTime=$(BUILD_TIME)

# Build directories
BUILD_DIR := build
DIST_DIR := dist

# Supported platforms
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

.PHONY: all
all: clean build

.PHONY: clean
clean:
	@echo "🧹 Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR) $(DIST_DIR)

.PHONY: deps
deps:
	@echo "📦 Downloading dependencies..."
	@go mod download
	@go mod tidy

.PHONY: lint
lint:
	@echo "🔍 Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "⚠️  golangci-lint not installed, skipping..."; \
	fi

.PHONY: build
build: deps
	@echo "🔨 Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) .

.PHONY: build-all
build-all: deps
	@echo "🔨 Building for all platforms..."
	@mkdir -p $(DIST_DIR)
	@$(foreach platform,$(PLATFORMS), \
		$(call build_platform,$(platform)) \
	)

define build_platform
	$(eval OS := $(word 1,$(subst /, ,$(1))))
	$(eval ARCH := $(word 2,$(subst /, ,$(1))))
	$(eval BINARY := $(if $(filter windows,$(OS)),$(BINARY_NAME).exe,$(BINARY_NAME)))
	$(eval ARCHIVE := $(PROJECT_NAME)-$(OS)-$(ARCH).tar.gz)
	
	@echo "  📦 Building $(OS)/$(ARCH)..."
	@GOOS=$(OS) GOARCH=$(ARCH) go build \
		-ldflags "$(LDFLAGS)" \
		-o $(DIST_DIR)/$(BINARY) .
	
	@echo "  📮 Creating archive $(ARCHIVE)..."
	@cd $(DIST_DIR) && tar -czf $(ARCHIVE) $(BINARY) ../LICENSE ../README.md
	@rm $(DIST_DIR)/$(BINARY)
	
	@echo "  🔒 Generating checksum..."
	@cd $(DIST_DIR) && sha256sum $(ARCHIVE) > $(ARCHIVE).sha256
endef

.PHONY: release
release: clean lint build-all
	@echo "🚀 Release artifacts created in $(DIST_DIR)/"
	@ls -la $(DIST_DIR)/

.PHONY: checksums
checksums:
	@echo "🔒 Generating checksums for all archives..."
	@cd $(DIST_DIR) && for file in *.tar.gz; do \
		sha256sum $$file > $$file.sha256; \
	done

.PHONY: install
install: build
	@echo "📥 Installing $(BINARY_NAME) to /usr/local/bin..."
	@sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "✅ Installed successfully!"

.PHONY: uninstall
uninstall:
	@echo "🗑️  Uninstalling $(BINARY_NAME)..."
	@sudo rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "✅ Uninstalled successfully!"

.PHONY: dev
dev: build
	@echo "🚀 Running development version..."
	@./$(BUILD_DIR)/$(BINARY_NAME) --help

.PHONY: docker
docker:
	@echo "🐳 Building Docker image..."
	@docker build -t $(PROJECT_NAME):$(VERSION) .
	@docker tag $(PROJECT_NAME):$(VERSION) $(PROJECT_NAME):latest

.PHONY: help
help:
	@echo "📖 Available targets:"
	@echo "  all         - Clean and build"
	@echo "  clean       - Clean build artifacts"
	@echo "  deps        - Download dependencies"
	@echo "  lint        - Run linter"
	@echo "  build       - Build for current platform"
	@echo "  build-all   - Build for all supported platforms"
	@echo "  release     - Full release build (clean, lint, build-all)"
	@echo "  checksums   - Generate SHA256 checksums"
	@echo "  install     - Install binary to /usr/local/bin"
	@echo "  uninstall   - Remove binary from /usr/local/bin"
	@echo "  dev         - Build and show help (for development)"
	@echo "  docker      - Build Docker image"
	@echo "  help        - Show this help"
	@echo ""
	@echo "🔧 Variables:"
	@echo "  VERSION     - Release version (default: $(VERSION))"
	@echo ""
	@echo "🏗️  Build info:"
	@echo "  Go version: $(GO_VERSION)"
	@echo "  Commit:     $(COMMIT_HASH)"
	@echo "  Build time: $(BUILD_TIME)"
