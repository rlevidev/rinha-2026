# Contributing to Rinha de Backend 2026

## Development Setup

### Prerequisites
- Go 1.26+ with `GOEXPERIMENT=simd` enabled
- CPU with AVX2 support (required for SIMD kernels)
- Docker (for testing production configuration)
- Linux with `CAP_SYS_NICE` and `CAP_IPC_LOCK` capabilities (required for 
  `SCHED_FIFO` real-time priority and `mlockall` memory locking outside Docker)
- Make (optional, for convenience commands)

### Environment Setup
```bash
# Enable Go experiments
export GOEXPERIMENT=simd

# Clone repository
git clone https://github.com/rlevidev/rinha-2026.git
cd rinha-2026

# Download dependencies
go mod download
```

## Running Tests

### Unit Tests
```bash
# Run all tests with race detector
go test -race ./...

# Run tests for specific package
go test -race ./internal/fraud
go test -race ./internal/index
```

### Benchmarks
```bash
# Run benchmarks
go test -bench=. ./...

# Run benchmarks with memory allocation profiling
go test -bench=. -benchmem ./...
```

### Integration Tests
```bash
# Build and run minimal cluster for integration testing
make dev-up

# Run test requests against local cluster
make test-request

# Tear down test environment
make down
```

## Makefile Commands

The project includes a Makefile for common development tasks:

```bash
# Build all binaries
make build

# Run unit tests
make test

# Run tests with race detector
make test-race

# Run benchmarks
make bench

# Format code
make fmt

# Run linters
make lint

# Start development environment (LB + 2 API workers)
make dev-up

# Stop development environment
make down

# Send test request to local service
make test-request

# View service logs
make logs
```

## Code Style

### Go Specific
- Use `gofmt` and `goimports` for formatting (configured via pre-commit hooks)
- Follow [Effective Go](https://golang.org/doc/effective_go.html) guidelines
- Prefer composition over inheritance
- Use interfaces for dependency injection
- Handle errors explicitly with context wrapping
- Avoid global state where possible

### Performance Considerations
- Minimize heap allocations in hot paths
- Use `sync.Pool` for reusable objects when allocation is unavoidable
- Prefer arrays/slices over maps for fixed-size lookup tables
- Use `unsafe` and `uintptr` only when necessary and with careful consideration
- Profile before optimizing - use `pprof` to identify bottlenecks

### Security Considerations
- Validate all inputs at boundaries
- Use constant-time comparisons for sensitive data
- Avoid dynamic code generation (`reflect`, `unsafe.Pointer` for arbitrary memory access)
- Prefer standard library crypto implementations
- Never hardcode secrets or credentials

## Debugging Tips

### Profiling
```bash
# CPU profiling
go tool pprof http://localhost:6060/debug/pprof/profile

# Memory profiling  
go tool pprof http://localhost:6060/debug/pprof/heap

# Block profiling
go tool pprof http://localhost:6060/debug/pprof/block

# Mutex profiling
go tool pprof http://localhost:6060/debug/pprof/mutex
```

### Debugging Low-Latency Issues
1. Check for GC pauses: `GODEBUG=gctrace=1 ./server ...`
2. Monitor syscall rates: `strace -c -p <pid>`
3. Check CPU affinity: `taskset -p <pid>`
4. Verify memory locking: `cat /proc/<pid>/status | grep VmLck`
5. Look for priority inversion: `chrt -p <pid>`

### Common Issues
- **"fatal: CPU sem AVX2"**: Ensure your CPU supports AVX2 instructions
- **Page fault latency spikes**: Verify `mlockall` succeeded (`cat /proc/<pid>/status`)
- **High syscall usage**: Check if `SO_BUSY_POLL` is enabled (`ss -i`)
- **GC pauses despite GOGC=off**: Ensure no background goroutines are allocating

## Adding New Features

### Adding New Feature Vectors
1. Update `fraud.Request` struct with new field
2. Modify `fraud.ParseRequest` to parse the new field
3. Update `fraud.Vectorize` to encode the new feature
4. Add unit tests for the new vectorization logic
5. Update normalization constants if needed

### Adding New Search Partitions
1. Determine new feature bits for partitioning
2. Update `index.NPartitions` constant
3. Modify server routing logic in `handleRequest`
4. Update build_index command to generate new partitions
5. Adjust fallback routing logic for missing partitions

### Performance Optimization Process
1. Profile to identify bottleneck (`pprof`)
2. Implement optimization targeting the bottleneck
3. Verify correctness with existing tests
4. Benchmark improvement using representative workload
5. Check for regressions in related areas
6. Update documentation if API or behavior changed

## Releasing

### Building Production Images
```bash
# Build multi-architecture images (if needed)
docker buildx build --platform linux/amd64 -t rinha-2026:latest .

# Build for current platform only
docker build -t rinha-2026:latest .
```


## Troubleshooting

### Build Failures
- **"missing go.sum entry"**: Run `go mod tidy`
- **SIMD build errors**: Ensure Go 1.26+ with `GOEXPERIMENT=simd` is set
- **Missing AVX2 instructions**: Verify CPU support with `lscpu | grep avx2`

### Runtime Issues
- **Service fails to start**: Check logs for `mlockall` or `sched_setscheduler` permission issues
- **High latency**: Verify real-time priority is set (`chrt -p <pid>` should show RT priority)
- **Connection refused**: Ensure LB and API workers are running and sockets exist in `/sockets`
- **Memory issues**: Check memory usage with `docker stats` or `ps aux --sort=-rss`

### Performance Issues
- **Throughput lower than expected**: Check CPU affinity with `taskset -p <pid>`
- **Latency spikes**: Look for GC activity despite `GOGC=off` (background threads)
- **Search quality degradation**: Verify index files are loaded correctly (check logs on startup)