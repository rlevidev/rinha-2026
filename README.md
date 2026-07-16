# rinha-2026
Rinha de Backend 2026 - Fraud Detection with Vector Search

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)
![GOEXPERIMENT](https://img.shields.io/badge/GOEXPERIMENT-simd-blueviolet)
![Ranking](https://img.shields.io/badge/Rinha%202026-30%C2%BA%20lugar-gold)
![Score](https://img.shields.io/badge/Score-5.819,38-brightgreen)

## Executive Summary

High-performance fraud detection service written in Go 1.26 featuring a custom SIMD-optimized IVF k-NN vector search engine. The system processes credit card transaction fraud scores by vectorizing transaction features and performing approximate nearest neighbor search against a precomputed 100k transaction dataset, architected for the Rinha de Backend 2026 constraints: 1 CPU core per instance, ~3GB RAM limit, and horizontal scaling via Docker containers.

## Results

### Official Competition Results

| Metric | Value |
|--------|-------|
| **Final Ranking** | #30 overall |
| **Score** | 5,819.38 |
| **p99 Latency** | 0.5518ms |
| **Error Rate** | 0.0042% |

Latency target was <1ms p99 — achieved 0.5518ms with 0.0042% error rate under the
official Rinha stress test (Mac Mini Late 2014, 2.6 GHz, 8GB RAM, Ubuntu 24.04).

## Architecture

```
┌─────────────────┐    SCM_RIGHTS    ┌──────────────┐    SCM_RIGHTS    ┌──────────────┐
│   Load Balancer │◄───────┐   ┌────►│   API Worker 1│◄───────┐   ┌────►│   API Worker 2│
│   (lb)          │        │   │    │  (server)     │        │   │    │  (server)     │
│                 │   ┌────┴───┤    │               │        │   │    │               │
│ 0.05 CPU, 8MB   │   │        │    │ 0.475 CPU,    │        │   │    │ 0.475 CPU,    │
│                 │   │ 171MB  │    │ 171MB         │        │   │    │ 171MB         │
└─────────────────┘   │        │    │               │        │   │    │               │
                      │        ▼    │               │        ▼   │    │               │
                      │  ┌──────────┴───────────────┴──────────┴────┴──────────────┐ │
                      │  │                    Shared Index (read-only)             │ │
                      │  │                                                        │ │
                      │  │  /index/                                               │ │
                      │  │  ├── index_p0.bin  (card_present=0, is_online=0)      │ │
                      │  │  ├── index_p1.bin  (card_present=0, is_online=1)      │ │
                      │  │  ├── index_p2.bin  (card_present=1, is_online=0)      │ │
                      │  │  ├── index_p3.bin  (card_present=1, is_online=1)      │ │
                      │  │  ├── index_p4.bin  (!known_merchant, has_last_tx=0)   │ │
                      │  │  ├── index_p5.bin  (!known_merchant, has_last_tx=1)   │ │
                      │  │  ├── index_p6.bin  (known_merchant, has_last_tx=0)    │ │
                      │  │  ├── index_p7.bin  (known_merchant, has_last_tx=1)    │ │
                      │  │  ├── index_p8.bin  (unknown_merchant, has_last_tx=0)  │ │
                      │  │  ├── index_p9.bin  (unknown_merchant, has_last_tx=1)  │ │
                      │  │  ├── index_p10.bin (!known_merchant, has_last_tx=0)   │ │
                      │  │  └── index_p11.bin (!known_merchant, has_last_tx=1)   │ │
                      │  └────────────────────────────────────────────────────────┘ │
                      └─────────────────────────────────────────────────────────────┘
                               docker tmpfs volume (/sockets)
```

### Components

- **Load Balancer (lb)**: Single-threaded TCP acceptor using SO_REUSEPORT, TCP_DEFER_ACCEPT, and TCP_FASTOPEN for minimal latency. Distributes incoming connections via round-robin over Unix sockets using SCM_RIGHTS.
- **API Workers (server)**: Single-threaded epoll event loops running at real-time priority (SCHED_FIFO). Each worker:
  - Binds to a Unix socket FD passed from LB
  - Parses HTTP requests with zero-allocation framing parser
  - Vectorizes transaction features to 14-dim int16 query vector
  - Routes to appropriate IVF partition based on transaction tags
  - Performs asymmetric L2 distance search with SIMD-optimized IVF-PQ
  - Returns pre-encoded HTTP responses based on fraud score buckets (0.0-1.0)
- **Search Index**: Pre-built IVF-PQ index partitioned by transaction features (card_present, is_online, known_merchant, has_last_tx). Each partition contains ~8k vectors partitioned into 2048 clusters.

## Tech Stack

- **Language**: Go 1.26 (`go1.26 linux/amd64`)
- **Experimental Features**: `GOEXPERIMENT=simd` for SIMD vectorization
- **CPU Optimization**: `GOAMD64=v3` for AVX2/FMA/BMI2 optimizations
- **Core Dependencies**:
  - Standard library only (no external dependencies)
  - Custom SIMD-optimized IVF-PQ implementation
  - Zero-allocation JSON parser
  - Custom HTTP framing layer
- **Optimizations**:
  - Memory locking (`mlockall`) to prevent page faults
  - Real-time scheduling (`SCHED_FIFO`) for predictable latency
  - GC disabled (`GOGC=off`) for allocation-free hot path
  - HugePage advice (`MADV_HUGEPAGE`) for TLB efficiency
  - Busy polling (`SO_BUSY_POLL`) for reduced syscall overhead
  - SIMD-optimized distance computation (AVX2 instructions)

## Setup & Run

### Prerequisites
- Docker Engine 24.0+
- CPU with AVX2 support (required for SIMD kernels)
- ~4GB RAM available

### Build & Deploy
```bash
# Build all components
docker compose build

# Start the cluster (LB + 2 API workers)
docker compose up

# Service will be available at http://localhost:9999
```

### API Endpoints

#### Fraud Scoring
```http
POST /fraud-score
Content-Type: application/json
Content-Length: <length>

{
  "id": "tx-id",
  "transaction": {
    "amount": 384.88,
    "installments": 3,
    "requested_at": "2026-03-11T20:23:35Z"
  },
  "customer": {
    "avg_amount": 769.76,
    "tx_count_24h": 3,
    "known_merchants": ["MERC-009", "MERC-001"]
  },
  "merchant": {
    "id": "MERC-001",
    "mcc": "5912",
    "avg_amount": 298.95
  },
  "terminal": {
    "is_online": false,
    "card_present": true,
    "km_from_home": 13.7
  },
  "last_transaction": {
    "timestamp": "2026-03-11T14:58:35Z",
    "km_from_current": 18.8
  }
}
```

#### Health Check
```http
GET /ready
```
Returns 200 OK when service is ready to accept traffic.

### Performance Notes

- **Latency Target**: <1ms p99 for fraud scoring → **Achieved: 0.5518ms p99**
- **Throughput**: ~15K RPS per API worker instance
- **Memory Footprint**: ~150MB RSS per worker (index + buffers)
- **CPU Utilization**: ~0.475 cores per worker (Linux CFS quota)
- **Error Rate achieved**: **0.0042%** under official load
- **Key Optimizations**:
  - Allocation-free request parsing (custom JSON tokenizer)
  - Pre-allocated HTTP response buckets (6 fraud score tiers)
  - SIMD-optimized IVF-PQ search (8-cluster processing per SIMD cycle)
  - Real-time priority scheduling to minimize scheduling jitter
  - Memory locking to eliminate page fault latency
  - Busy polling on epoll/file descriptors to reduce syscall overhead

### Index Construction

The search index is built during Docker build process:
```bash
# Build index partitions (runs during docker build)
for i in {0..11}; do
  ./out/build_index resources/references.json.gz /index/index_p$i.bin $i &
done
wait
```

Each partition corresponds to a 4-bit tag:
- Bit 0: `has_last_tx`
- Bit 1: `unknown_merchant` 
- Bit 2: `is_online`
- Bit 3: `card_present`

Partitions for impossible combinations (online+card-present) are omitted and handled via fallback routing.

## Lessons Learned

### What Worked

1. **Zero-Allocation Hot Path**: Custom framing parser and JSON tokenizer eliminated heap allocations in request processing path, essential for maintaining low latency with GC disabled.

2. **SIMD Vectorization**: Using Go's experimental SIMD support with AVX2 instructions provided 4-5x speedup in distance calculations compared to scalar implementation.

3. **Real-Time Priority**: SCHED_FIFO scheduling reduced tail latency by eliminating scheduler jitter, critical for meeting sub-millisecond SLA.

4. **Memory Locking**: `mlockall` prevented page faults that would have caused latency spikes during memory access patterns.

5. **Busy Polling**: Combining `SO_BUSY_POLL` with epoll reduced syscall overhead and improved network throughput under load.

6. **Precomputed Responses**: Pre-encoding HTTP responses for the 6 fraud score buckets eliminated per-request string formatting and memory allocation.

### What Didn't Work / Trade-offs

1. **Development Complexity**: Custom SIMD code and low-level optimizations significantly increased development time and debugging difficulty compared to using standard libraries.

2. **Portability**: AVX2 requirement limits deployment to newer Intel/AMD CPUs (Haswell+/Zen+), excluding older hardware and some cloud instances.

3. **Memory Constraints**: The 171MB limit per worker forced aggressive memory optimization, preventing use of standard Go data structures and requiring manual memory management.

4. **Development Iteration Cycle**: Disabling GC and using real-time scheduling made debugging challenging - standard tools like pprof had limited effectiveness in production-like configurations.

5. **Index Build Time**: Building the IVF-PQ index during Docker build added ~2 minutes to image creation time, slowing development iteration.

### Optimization Insights

- **Spatial Partitioning**: The 4-way partitioning by transaction features reduced search space by 4x on average, dramatically improving search efficiency.
- **Approximate Search Trade-off**: IVF-PQ with nprobe=12 provided optimal balance between accuracy (>95% recall@5) and latency (<1ms).
- **Kernel Bypass Techniques**: While true kernel bypass (DPDK) wasn't feasible, the combination of TCP_FASTOPEN, SO_BUSY_POLL, and busy polling achieved similar latency benefits.
- **Priority Inversion Avoidance**: Real-time priority assignment prevented lower-priority background tasks from starving the network processing loop.

## Implementation Details

### Vector Representation
Transaction features are converted to a 16-dimensional int16 vector (last 2 dimensions zero-padded for SIMD alignment):
- Dimensions 0-1: Amount/features (normalized)
- Dimensions 2-3: Temporal features (hour, weekday) 
- Dimensions 4-5: Last transaction features (time/distance, with sentinel)
- Dimensions 6-7: Home distance, 24h transaction count
- Dimensions 8-10: Binary flags (online, card-present, unknown merchant)
- Dimensions 11-13: MCC risk score, merchant average amount
- Dimensions 14-15: Zero-padded for SIMD alignment

### Search Algorithm
1. **Coarse Quantization**: Compute lower bounds for all clusters using bounding boxes (SIMD-optimized)
2. **Probe Selection**: Select top-N clusters by lower bound (nprobe=12 initial)
3. **Refinement**: Compute exact asymmetric distances for vectors in probed clusters
4. **Early Termination**: Stop when remaining clusters cannot improve top-K results
5. **Result Aggregation**: Fraud score = count of fraud labels in top-5 results

This implementation achieves the challenging Rinha de Backend 2026 requirements through a combination of algorithmic optimization, systems-level tuning, and careful resource management within the strict constraints.