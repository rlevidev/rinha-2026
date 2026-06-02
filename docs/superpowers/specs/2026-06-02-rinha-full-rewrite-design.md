# Rinha 2026 — Full Rewrite Design (after Vinicius Piassa)

## Context

Current score: -6000 (p99=2002ms, 12.180 http_errors, detection score cut triggered at >15% failure rate).
Top-7 reference (Vinicius Piassa): final score +6000 (p99=0.45ms, 0 http_errors, 0 FP/FN).

The rewrite adopts the same proven architecture: epoll-based load balancer that passes client fds
via SCM_RIGHTS (no byte proxying), single-thread epoll API server with SCHED_FIFO real-time
priority, and IVF k-NN with AVX2 SIMD via `simd/archsimd`. Code is rewritten from scratch in
this project's style, with MIT attribution to the original author.

## Architecture

```
client → :9999 (TCP, epoll) ─┐
                              │ round-robin SCM_RIGHTS
                              ▼
              api1.sock (UDS) ──▶ server 1 (epoll single-thread)
              api2.sock (UDS) ──▶ server 2 (epoll single-thread)
                              │ owns the client fd end-to-end
                              ▼
                    epoll recv → HTTP frame → parse → vectorize
                              → tag → IVF k-NN (simd/archsimd) → reply
```

## Module layout

```
cmd/
  lb/            # epoll listener, SCM_RIGHTS sender, round-robin
  server/        # epoll worker, client fd receiver, fraud pipeline
  build_index/   # offline k-means → bbox pack → binary writer

internal/
  fraud/         # zero-alloc JSON parser, vectorizer, tag, mcc lookup
  index/         # IVF on-disk format, mmap reader, SIMD search kernel
  netx/          # SCM_RIGHTS fd-passing, epoll busy-poll ioctl

index/           # 12 partition binary files (committed, built offline)
```

## LB (cmd/lb)

Single-thread epoll loop on TCP :9999. For each accepted client fd:
1. round-robin pick backend from pre-connected Unix socket array
2. `sendmsg(SCM_RIGHTS)` sends the fd to the chosen backend
3. close local copy

No byte proxying, no HTTP inspection, no response generation.

Socket options:
- `SO_REUSEADDR`, `SO_REUSEPORT`
- `TCP_DEFER_ACCEPT` (wake only on data)
- `SO_BUSY_POLL 50µs`, `SO_PREFER_BUSY_POLL`, `SO_BUSY_POLL_BUDGET 8`
- `TCP_FASTOPEN` accept-queue depth 256
- `SOCK_CLOEXEC` on every fd
- `O_NONBLOCK` on listen fd
- `accept4` drains until EAGAIN

Pre-warm: 32 self-loopback requests at startup to a kernel path (docker-proxy, accept,
SCM_RIGHTS, API recv).

GC off (`SetGCPercent(-1)`), `SetMemoryLimit(6MB)`, `GOMAXPROCS(1)`.

## Server (cmd/server)

Epoll single-thread loop on two event sources:

1. **Control fd** (Unix socket from LB): receive client fds via `RecvFDs` (SCM_RIGHTS),
   apply `TCP_NODELAY` + `TCP_QUICKACK`, register with epoll.
2. **Client fds**: non-blocking `recvfrom` (via `RawSyscall6` for zero scheduler overhead),
   HTTP frame, parse, vectorize, search, reply.

Per-fd state: `[4096]byte` buffer + `pos int`. No per-request allocations.

OS tuning:
- `runtime.LockOSThread()` + `SCHED_FIFO` priority 10 (gate: `NO_FIFO=1` env var)
- `debug.SetGCPercent(-1)` + `debug.SetMemoryLimit(160MB)`
- `unix.Mlockall(MCL_CURRENT | MCL_FUTURE)` (best-effort)
- `unix.Prctl(PR_SET_TIMERSLACK, 1)`
- `unix.SetEpollBusyPoll` via EPIOCSPARAMS

HTTP framing: search `\r\n\r\n` in buffer, scan for `content-length:` ASCII case-fold,
read body, call pipeline, send response, shift pipeline data. One request per read cycle.

## Fraud pipeline (internal/fraud)

### Parser

Hand-written JSON state machine, zero allocations per request. `psr` struct tracks
byte position and captures the raw text ranges for `known_merchants[]` and `merchant.id`
(needed for the `unknown_merchant` computation after the entire object is parsed).

Fast number parsing: accumulate integer mantissa, divide by `pow10[fracDigits]`. Falls
back to `strconv.ParseFloat` for exponents or mantissa overflow (>18 digits).

ISO-8601 parse: `days_from_civil` formula, optional fractional seconds + `Z` or `±HH:MM`
timezone.

Fields extracted:
- `Amount float64`, `CustomerAvg float64`, `MerchantAvg float64`, `KmHome float64`,
  `KmLast float64`, `TS int64`, `LastTS int64`, `Installments int32`, `TxCount24h int32`,
  `MCC [4]byte`, `IsOnline bool`, `CardPresent bool`, `HasLastTx bool`, `KnownMerchant bool`

### MCC lookup

Hardcoded 9-entry table (no runtime JSON parse):

```
5411→1500, 5812→3000, 5912→2000, 5944→4500, 7801→8000,
7802→7500, 7995→8500, 4511→3500, 5311→2500
```
Default: 5000.

### Vectorize (14+2 dims int16, SCALE=10000)

```
0:  clamp(amount     / 10000)
1:  clamp(installments / 12)
2:  clamp((amount/customer_avg) / 10)
3:  hour(ts) / 23
4:  weekday(ts) / 6
5:  clamp(minutes_since_last / 1440) OR -SCALE (null last_tx)
6:  clamp(km_from_last / 1000) OR -SCALE (null last_tx)
7:  clamp(km_home / 1000)
8:  clamp(tx_count_24h / 20)
9:  SCALE if is_online else 0
10: SCALE if card_present else 0
11: SCALE if unknown_merchant else 0
12: mccRisk(mcc)
13: clamp(merchant_avg / 10000)
14: 0
15: 0
```

### Tag (4-bit partition key)

```
bit 0: has_last_tx   (v[5] >= 0)
bit 1: unknown_merchant (v[11] > 0)
bit 2: is_online     (v[9] > 0)
bit 3: card_present  (v[10] > 0)
```

Fallback: if `indices[tag] == nil`, clear card bit, retry; if still nil, clear online bit.

The online+card combination is absent in the training corpus, so 4 of 16 tags are empty.
12 partition files exist.

## Index format (internal/index)

### On-disk

```
header:         [8]magic | [4]n_clusters | [4]n_vectors | zero-pad to 64
cluster_offsets: (K+1)*4, padded to 64
bbox_min:       K*16*2 int16, padded to 64
bbox_max:       K*16*2 int16, padded to 64
pair_arr[0..6]: n * int32 each (packed lo|hi<<16 for 2 dims), padded to 64
labels:         n * uint8
tail_pad:       64 zero bytes
```

K = `clamp(len(refs)/300, 64, 2048)`.

### Reader (mmap)

- `Open()` mmaps the file (`MAP_PRIVATE|MAP_POPULATE`), `Mlock`s, `MADV_WILLNEED`,
  `MADV_HUGEPAGE`, wires up zero-copy section views.
- `buildBPSOA()` reshapes the per-cluster bbox arrays into 8-cluster pair SoA groups
  for phase 1 SIMD. Phantom clusters get `INT16_MAX/MIN` so they never win.

### Search phases

Phase 0: broadcast query pairs into 8-lane SIMD registers.

Phase 1 (bbox lower bound, 8 clusters/SIMD iter):
```
for each group of 8 clusters:
  load bpsoaMin, bpsoaMax for pair 0
  gap = max(bmin - q, 0) | max(q - bmax, 0)
  acc = DotProductPairs(gap)    // squared gap for each cluster
  for pairs 1..6 add DotProductPairs
  store → l1..l8 per group
  packed[c] = (lb<<CidBits) | c
```

Phase 2 (greedy probe, max 12 probes):
```
for probe in 0..11:
  best = min(packed[0..K])
  if best == tombstone: break
  if lb(best) >= worst_top5: break
  scanCluster(bestC)
  tombstone packed[bestC]
```

Phase 3 (exact cluster scan, 8 vecs/SIMD iter):
```
load query pairs
for each batch of 8:
  gate: if (accA+accB)[lane] >= worst: skip lane
  accum chain A = pairs{3,0,2,6}, B = pairs{5,1,4}
  final = A+B
  for each vector in batch not gated: insert into top-5
```

Phase 4 (repair):
- If `fraud_count ∈ [1,4]` (ambiguous), set `maxProbes = K` (full sweep)
- lb-prune still gates; unambiguous verdicts (0 or 5 top-5 fraud) return early

ScanCluster uses 2-stage early termination:
- Pair order: {3,5}, {0,1}, {2,4,6}
- Stage 1: sum pairs 3+5 (high-variance dims). If every lane ≥ worst_key, skip batch.
- Stage 2: sum pairs 0+1 (medium-variance). If every lane ≥ worst_key, skip batch.
- Stage 3: sum the remaining {2,4,6}. Full L2 only for surviving lanes.

### Build offline (cmd/build_index)

1. Read `references.json.gz` (auto-detect gzip by magic bytes)
2. Parse corpus into `[]Ref{V[16]float32, Label uint8}`
3. Filter by tag
4. K-means (k = clamp(len/300, 64, 2048), 20 iters, evenly-spaced init, parallel)
5. Counting sort by cluster
6. Sort within cluster by distance to centroid
7. BBox pack (quantize int16, compute min/max per cluster, stripe pairs)
8. Write binary

## Containerization

```dockerfile
FROM golang:1.26.3 AS build
ENV GOEXPERIMENT=simd CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN go build -trimpath -ldflags="-s -w" -o /out/lb ./cmd/lb

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
COPY --from=build /out/lb /lb
COPY index/ /index/
```

Compose services:
- `api1`: cpuset 0, 0.475 CPU, 171MB, command `/server /sockets/api1.sock /index`
- `api2`: cpuset 1, 0.475 CPU, 171MB, command `/server /sockets/api2.sock /index`
- `lb`: cpuset 2,3, 0.05 CPU, 8MB, command `/lb 9999 /sockets/api1.sock /sockets/api2.sock`
- Volume `sockets`: tmpfs 4MB mode=0777
- ulimits: `memlock=-1`, `nofile=65535`

## Verification

- Unit tests: parse, vectorize, tag, index open, index search
- Match test: compare IVF search verdict against brute-force full-sweep on 1000 random queries
- Integration: real unix socket API server → curl /ready and /fraud-score
- Benchmark: `BenchmarkParseVectorize` (0 allocs/op), `BenchmarkSearch` (0 allocs/op)
- Smoke: same image composes, responds on :9999

## Phases

| Phase | Deliverable | Tests |
|---|---|---|
| F1 | Module scaffold, delete legacy code | `go vet ./...` passes |
| F2 | `internal/fraud` parse + vectorize + tag | unit tests pass |
| F3 | `internal/index` on-disk format + mmap reader + brute-force search | brute-force match test |
| F4 | `cmd/build_index` → generate 12 partition bin files | index files exist |
| F5 | IVF search phases 1-2-3 (scalar) | match test vs brute-force |
| F6 | SIMD intrinsics via `archsimd` | benchmarks 0 allocs, match test |
| F7 | `cmd/server` epoll loop + SCM_RIGHTS + tuning | smoke test passes |
| F8 | `cmd/lb` epoll listener + SCM_RIGHTS + self-warm | integration smoke |
| F9 | Dockerfile, compose, image build | docker compose smoke, benchmarks |
| F10 | Push, CI, Rinha previa | score improves from -6000 |

## Risks and mitigations

- `GOEXPERIMENT=simd` is experimental: pin Go 1.26.3, gate SIMD behind `HasAVX2()`
- `SCHED_FIFO` needs `CAP_SYS_NICE`: gated behind `NO_FIFO=1`
- `mlockall` may fail under tight memory: best-effort, errors ignored
- K-means divergence from exact search: `TestSearchMatchesBruteForce` sample validation
- Parser regression: existing example payloads all parse identically to current output
- Deadline (2026-06-05): 10 phases, each independently testable; fallback = submit current + incremental fixes if F1-F4 delay

## Attribution

Architecture inspired by and code rewritten from
[github.com/vinicius-piassa/rinha-backend-2026-go](https://github.com/vinicius-piassa/rinha-backend-2026-go).
MIT license attribution in `info.json` and `README.md`.
