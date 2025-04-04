.PHONY: build clean test lint run docker-build docker-push test-unit test-integration test-e2e test-all coverage

BINARY=phpcloud
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# Build
build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/phpcloud

# Clean build artifacts
clean:
	rm -rf bin/
	go clean
	go clean -testcache

# Run tests
test: test-unit

# Run unit tests only (fast, no external deps)
test-unit:
	go test -v -short ./internal/...

# Run integration tests (uses testcontainers)
test-integration:
	go test -v -run Integration ./internal/... -timeout 30m

# Run e2e tests (full system tests with containers)
test-e2e:
	go test -v -run E2E ./internal/... -timeout 30m

# Run all tests
test-all: test-unit test-integration test-e2e

# Run tests with coverage
coverage:
	go test -short -coverprofile=coverage.out ./internal/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run tests with race detector
test-race:
	go test -race -short ./internal/...

# Run benchmarks
bench:
	go test -bench=. -benchmem ./internal/...

# Run linting
lint:
	golangci-lint run ./...

# Download dependencies
deps:
	go mod download
	go mod tidy

# Verify dependencies
verify:
	go mod verify

# Run locally
run: build
	./bin/$(BINARY) --config ./phpcloud.yaml

# Development mode with hot reload
dev:
	air -c .air.toml

# Build Docker image
docker-build:
	docker build -t phpcloud:$(VERSION) -t phpcloud:latest .

# Push Docker image
docker-push: docker-build
	docker push phpcloud:$(VERSION)
	docker push phpcloud:latest

# Run tests in Docker
docker-test:
	docker run --rm -v $(PWD):/app -w /app golang:1.26-alpine \
		sh -c "apk add --no-cache git make && go test -short ./internal/..."

# Install tools
tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install gotest.tools/gotestsum@latest

# Setup for development
setup: tools deps

# CI/CD targets for GitHub Actions
ci-lint: lint

ci-test: test-unit test-race

ci-integration: test-integration

ci-e2e: test-e2e

ci-build: build

ci-docker: docker-build

# All-in-one CI
ci: ci-lint ci-test ci-build

.DEFAULT_GOAL := build
