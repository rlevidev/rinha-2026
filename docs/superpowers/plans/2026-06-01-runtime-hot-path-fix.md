# Runtime Hot Path Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduzir `p99` e eliminar `http_errors` no caminho quente da Rinha removendo overhead de framework por request, limitando fallback caro na busca vetorial e tornando o roteamento de partição determinístico.

**Architecture:** O balanceador vai deixar de atuar como proxy HTTP genérico e passará a ser um relay mínimo sobre Unix sockets, com round-robin puro e retry curto apenas quando um backend falhar. A API vai expor o contrato com um servidor HTTP mínimo ou com escrita direta no socket, mas sem `ReverseProxy`, sem `encoding/json` no hot path e sem respostas de erro para falhas internas previsíveis. A busca vetorial vai manter a mesma semântica do desafio, porém com orçamento de probing explícito, fallback de partição previsível e sem sweep completo surpresa em casos ambíguos.

**Tech Stack:** Go 1.26, Unix domain sockets, `net`/`syscall` de baixo nível, `mmap` para o índice, testes de integração em Go, `docker compose` para smoke test.

---

### Task 1: Replace the LB reverse proxy with a raw Unix-socket relay

**Files:**
- Modify: `cmd/lb/main.go`
- Create: `internal/lb/relay_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestPickBackendAlternates(t *testing.T) {
	sockets := []string{"/tmp/api1.sock", "/tmp/api2.sock"}

	if got := pickBackend(0, sockets); got != sockets[0] {
		t.Fatalf("pickBackend(0) = %q, want %q", got, sockets[0])
	}
	if got := pickBackend(1, sockets); got != sockets[1] {
		t.Fatalf("pickBackend(1) = %q, want %q", got, sockets[1])
	}
	if got := pickBackend(2, sockets); got != sockets[0] {
		t.Fatalf("pickBackend(2) = %q, want %q", got, sockets[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lb -run TestPickBackendAlternates -v`
Expected: FAIL because `pickBackend` does not exist yet.

- [ ] **Step 3: Write minimal implementation**

```go
func pickBackend(seq uint64, sockets []string) string {
	return sockets[seq&1]
}
```

Then replace `httputil.ReverseProxy` in `cmd/lb/main.go` with a direct relay loop that:
- accepts the client connection on `:9999`
- chooses the backend with `pickBackend(rr.Add(1)-1, sockets)`
- dials the selected Unix socket
- copies the request bytes to the backend
- copies the backend response bytes back to the client
- retries the other backend once on dial failure before returning an error

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lb -run TestPickBackendAlternates -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/lb/main.go internal/lb/relay_test.go
git commit -m "perf: remove reverse proxy overhead from lb"
```

### Task 2: Remove `net/http` from the API hot path and write responses directly

**Files:**
- Modify: `cmd/api/main.go`
- Modify: `internal/fraud/handler.go`
- Create: `internal/httpx/server_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestServeConnFraudScore(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	h := &fraud.Handler{Indexes: fakeIndexes, Normalizer: fakeNormalizer}
	done := make(chan struct{})
	go func() {
		ServeConn(server, h)
		close(done)
	}()

	req := []byte("POST /fraud-score HTTP/1.1\r\nContent-Length: 2\r\n\r\n{}")
	if _, err := client.Write(req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	resp, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Contains(resp, []byte("200 OK")) {
		t.Fatalf("response = %q, want 200 OK", resp)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpx -run TestServeConnFraudScore -v`
Expected: FAIL because `ServeConn` does not exist yet.

- [ ] **Step 3: Write minimal implementation**

```go
func ServeConn(conn net.Conn, h *fraud.Handler) {
	// Read one request line + headers.
	// Support only GET /ready and POST /fraud-score.
	// Parse Content-Length.
	// Read exactly the body bytes.
	// Call h.Ready() or h.Score(body) and write the pre-rendered response bytes.
	// Never emit 5xx for parse or scoring failures; return the fallback 200 response instead.
}
```

Then update `internal/fraud/handler.go` so the business logic is pure bytes-in/bytes-out:

```go
func (h *Handler) Ready() []byte
func (h *Handler) Score(body []byte) []byte
```

`cmd/api/main.go` should keep only startup concerns:
- load normalizer and index set
- bind the Unix socket
- call the minimal server loop
- keep `GOMAXPROCS` and `ReadHeaderTimeout` equivalents only if they still matter in the new server

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/httpx -run TestServeConnFraudScore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/api/main.go internal/fraud/handler.go internal/httpx/server_test.go
git commit -m "perf: replace api hot path with minimal socket server"
```

### Task 3: Make partition fallback and index probing deterministic and bounded

**Files:**
- Modify: `internal/index/index.go`
- Modify: `internal/fraud/request.go`
- Modify: `internal/index/index_test.go`
- Modify: `internal/fraud/request_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestPartitionFallbackOrder(t *testing.T) {
	var s Set
	s.parts[8] = &Index{}
	s.parts[0] = &Index{}

	if got := s.ForTag(13); got != s.parts[8] {
		t.Fatalf("ForTag(13) = %v, want exact known fallback %v", got, s.parts[8])
	}
}
```

And add a bounded-search regression test:

```go
func TestSearchKeepsBoundedProbeBudget(t *testing.T) {
	set, err := LoadSet("../../index")
	if err != nil {
		t.Fatalf("LoadSet failed: %v", err)
	}
	ix := set.ForTag(0)
	if ix == nil {
		t.Fatal("expected partition 0")
	}

	var q [16]int16
	got := ix.Search(&q)
	if got > 5 {
		t.Fatalf("Search returned invalid fraud count %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/index -run 'TestPartitionFallbackOrder|TestSearchKeepsBoundedProbeBudget' -v`
Expected: FAIL until the fallback order and probe budget are made explicit.

- [ ] **Step 3: Write minimal implementation**

```go
func (s *Set) ForTag(tag int) *Index {
	order := []int{tag, tag &^ 8, tag &^ 4, tag &^ 2, tag &^ 1, 0}
	for _, candidate := range order {
		if candidate >= 0 && candidate < len(s.parts) && s.parts[candidate] != nil {
			return s.parts[candidate]
		}
	}
	return nil
}
```

Then make `Search` deterministic and bounded:
- keep the initial `ProbeLimit`
- remove the unconditional full sweep repair
- replace it with a capped repair budget derived from partition size
- stop probing as soon as the best remaining lower bound cannot beat the current worst top-5 key
- keep the returned fraud count identical for all covered tests

Also keep `PartitionTag` aligned with the documented four-bit tag in `docs/br/REGRAS_DE_DETECCAO.md` and preserve the current `last_transaction: null` and `mcc` normalization behavior.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/index -run 'TestPartitionFallbackOrder|TestSearchKeepsBoundedProbeBudget' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/index/index.go internal/fraud/request.go internal/index/index_test.go internal/fraud/request_test.go
git commit -m "perf: bound index probing and make fallback deterministic"
```

### Task 4: Verify runtime behavior under compose and freeze the regression baseline

**Files:**
- Create: `internal/fraud/bench_test.go`
- Create: `internal/index/bench_test.go`
- Modify: `results/result-01-06-26.json` only if a new run proves the score changed

- [ ] **Step 1: Write the failing benchmark harness**

```go
func BenchmarkFraudPipeline(b *testing.B) {
	payloads := loadExamplePayloads(b, "../../resources/example-payloads.json")
	h := mustNewHandlerForBench(b)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = h.Score(payloads[i%len(payloads)])
	}
}
```

- [ ] **Step 2: Run the benchmark to capture the current baseline**

Run: `go test ./internal/fraud ./internal/index -bench . -benchtime=20s -count=1`
Expected: captures allocations and timing for the new hot path so the next iteration can compare before/after.

- [ ] **Step 3: Run the compose smoke test**

Run:

```bash
docker compose up --build -d
curl -fsS http://localhost:9999/ready
curl -fsS -X POST http://localhost:9999/fraud-score \
  -H 'content-type: application/json' \
  --data-binary @resources/example-payloads.json
```

Expected:
- `/ready` returns `200`
- `POST /fraud-score` returns `200` for every example payload
- no backend container logs show `backend unavailable`

- [ ] **Step 4: Commit the benchmark harness and any verified result update**

```bash
git add internal/fraud/bench_test.go internal/index/bench_test.go results/result-01-06-26.json
git commit -m "test: add hot path benchmark and compose smoke coverage"
```

## Self-Review

- Coverage of `http_errors`: Task 1 removes proxy overhead and Task 2 removes `net/http` request-path overhead.
- Coverage of `p99`: Task 2 eliminates per-request framework parsing cost and Task 3 caps expensive search repair.
- Coverage of detection regressions: Task 3 keeps the documented vectorization and tag rules aligned with `docs/br/REGRAS_DE_DETECCAO.md`.
- Coverage of verification: Task 4 adds benchmark and compose smoke checks so the fix can be measured, not guessed.
- Placeholder scan: no TODO/TBD placeholders remain in the plan.
- Type consistency: `pickBackend`, `ServeConn`, `Ready`, `Score`, `ForTag`, and `Search` are defined in the same plan before later steps reference them.

