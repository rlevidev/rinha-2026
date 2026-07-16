.PHONY: build test test-race bench fmt lint dev-up down test-request

# Build all binaries
build:
	@echo "Building all binaries..."
	mkdir -p bin
	GOEXPERIMENT=simd GOAMD64=v3 go build -trimpath -ldflags="-s -w" -o bin/build_index ./cmd/build_index
	GOEXPERIMENT=simd GOAMD64=v3 go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server
	GOEXPERIMENT=simd GOAMD64=v3 go build -trimpath -ldflags="-s -w" -o bin/lb ./cmd/lb
	@echo "Build complete!"

# Run unit tests
test:
	@echo "Running unit tests..."
	go test ./...

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	go test -race ./...

# Run benchmarks
bench:
	@echo "Running benchmarks..."
	go test -bench=. ./...

# Run benchmarks with memory allocation profiling
benchmem:
	@echo "Running benchmarks with memory profiling..."
	go test -bench=. -benchmem ./...

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	goimports -w .

# Run linters
lint:
	@echo "Running linters..."
	golangci-lint run

# Start development environment (LB + 2 API workers)
dev-up:
	@echo "Starting development environment..."
	docker compose up

# Stop development environment
down:
	@echo "Stopping development environment..."
	docker compose down

# Send test request to local service
test-request:
	@echo "Sending test request to local service..."
	curl -X POST http://localhost:9999/fraud-score \
	  -H "Content-Type: application/json" \
	  -d '{"id":"tx-id","transaction":{"amount":384.88,"installments":3,"requested_at":"2026-03-11T20:23:35Z"},"customer":{"avg_amount":769.76,"tx_count_24h":3,"known_merchants":["MERC-009","MERC-001"]},"merchant":{"id":"MERC-001","mcc":"5912","avg_amount":298.95},"terminal":{"is_online":false,"card_present":true,"km_from_home":13.7},"last_transaction":{"timestamp":"2026-03-11T14:58:35Z","km_from_current":18.8}}'

# Default target
help:
	@echo "Available targets:"
	@echo "  make build       - Build all binaries"
	@echo "  make test        - Run unit tests"
	@echo "  make test-race   - Run tests with race detector"
	@echo "  make bench       - Run benchmarks"
	@echo "  make benchmem    - Run benchmarks with memory profiling"
	@echo "  make fmt         - Format code"
	@echo "  make lint        - Run linters"
	@echo "  make dev-up      - Start development environment (LB + 2 API workers)"
	@echo "  make down        - Stop development environment"
	@echo "  make test-request- Send test request to local service"
	@echo "  make help        - Show this help"