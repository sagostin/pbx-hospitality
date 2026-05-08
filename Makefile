.PHONY: build test lint clean docker-build docker-run

# Binary names
MAIN_BINARY=bicom-hospitality
SITE_CONNECTOR_BINARY=site-connector

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOLINT=$(GOCMD) run golang.org/x/tools/cmd/golangci-lint@latest

# Build flags
LDFLAGS=-ldflags="-w -s"

# Directories
BUILD_DIR=./bin

all: test build

build: build-main build-connector

build-main:
	@echo "Building $(MAIN_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(MAIN_BINARY) ./cmd/bicom-hospitality

build-connector:
	@echo "Building $(SITE_CONNECTOR_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(SITE_CONNECTOR_BINARY) ./cmd/site-connector

test:
	@echo "Running tests..."
	$(GOTEST) -v -race -cover ./...

test-quick:
	@echo "Running tests (quick)..."
	$(GOTEST) ./...

lint:
	@echo "Running linter..."
	$(GOLINT) run ./...

tidy:
	@echo "Tidying modules..."
	$(GOMOD) tidy

verify:
	@echo "Verifying dependencies..."
	$(GOMOD) verify

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	$(GOCMD) clean

docker-build:
	@echo "Building Docker image..."
	docker build -t pbx-hospitality:latest .

docker-build-no-cache:
	@echo "Building Docker image (no cache)..."
	docker build --no-cache -t pbx-hospitality:latest .

docker-run:
	@echo "Running Docker container..."
	docker run -p 8080:8080 --env-file .env pbx-hospitality:latest

docker-compose-up:
	@echo "Starting services with docker-compose..."
	docker-compose up -d

docker-compose-down:
	@echo "Stopping services with docker-compose..."
	docker-compose down

help:
	@echo "Available targets:"
	@echo "  build              - Build both binaries"
	@echo "  build-main         - Build main service binary"
	@echo "  build-connector    - Build site-connector binary"
	@echo "  test               - Run tests with verbose output"
	@echo "  test-quick         - Run tests quickly"
	@echo "  lint               - Run linter"
	@echo "  tidy               - Tidy go modules"
	@echo "  verify             - Verify dependencies"
	@echo "  clean              - Clean build artifacts"
	@echo "  docker-build       - Build Docker image"
	@echo "  docker-run         - Run Docker container"
	@echo "  docker-compose-up  - Start services with docker-compose"
	@echo "  docker-compose-down - Stop services with docker-compose"
	@echo "  help               - Show this help message"