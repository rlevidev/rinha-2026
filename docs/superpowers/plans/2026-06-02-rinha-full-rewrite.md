# Rinha 2026 — Full Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the Rinha 2026 backend from scratch using epoll + SCM_RIGHTS + IVF k-NN with SIMD/AVX2, targeting p99 < 1ms and 0 detection errors.

**Architecture:** LB transfers client fds via SCM_RIGHTS (zero byte-proxying). Single-thread epoll API server runs SCHED_FIFO, GC disabled, mlockall. IVF k-NN with 4-phase search (bbox lb → greedy probe → exact scan → repair), int16 quantized, computes 8 clusters per SIMD iteration via simd/archsimd. Responses pre-rendered. Offline build_index generates 12 partition binaries from k-means clustering.

**Tech Stack:** Go 1.26, GOEXPERIMENT=simd, simd/archsimd (AVX2), epoll via golang.org/x/sys/unix, SCM_RIGHTS fd passing, mmap for index files, distroless container.

---

## File structure

```
cmd/
  lb/main.go            # epoll listener, SCM_RIGHTS sender, round-robin
  server/main.go         # epoll worker, client fd receiver, fraud pipeline
  build_index/main.go    # offline k-means → bbox pack → binary writer

internal/
  fraud/
    request.go           # JSON parser + Request struct
    subparse.go          # sub-object parsers (transaction, customer, merchant, terminal, last_tx)
    vectorize.go         # Vectorize + Tag + clamp01I16
    mcc.go               # hardcoded 9-entry MCC risk table
    fraud_test.go        # unit tests + benchmark
  index/
    format.go            # constants + bytesOf helper
    reader.go            # mmap Open, IvfIndex struct, buildBPSOA
    search.go            # HasAVX2, computeClusterPacked, scanCluster, searchCore, Search
    build.go             # Ref, FilterByTag, KMeans, BBoxPack, WriteIndexBin helpers
    parse.go             # ParseRefs corpus parser
    index_test.go        # Open + search match test + benchmarks
  netx/
    netx.go              # SendFD, RecvFDs, SetEpollBusyPoll

index/                   # 12 partition .bin files (generated offline)
```

## Constants reference (shared across files)

```go
const (
    NDims           = 14
    NPairs          = 7
    NClusters       = 2048
    KNeighbors      = 5
    Scale           = 10000
    NPartitions     = 16
    IdxBits         = 22
    CidBits         = 12
    CidMask         = 0xFFF
    NProbeInitial   = 12
    magic           = "RNH4-IDX"
)
```

---

### Task 1: Scaffold module, delete legacy code

**Files:**
- Delete: `cmd/api/main.go`, `cmd/lb/main.go`, `cmd/build-index/main.go`, `internal/httpx/*`, `internal/handler/*`, `internal/dataset/*`, `internal/search/*`, `internal/vectorizer/*`
- Create: `cmd/lb/main.go`, `cmd/server/main.go`, `cmd/build_index/main.go`, `internal/fraud/request.go`, `internal/fraud/subparse.go`, `internal/fraud/vectorize.go`, `internal/fraud/mcc.go`, `internal/fraud/fraud_test.go`, `internal/index/format.go`, `internal/index/reader.go`, `internal/index/search.go`, `internal/index/build.go`, `internal/index/parse.go`, `internal/index/index_test.go`, `internal/netx/netx.go`
- Modify: `go.mod` (add `golang.org/x/sys` dependency)
- Create: `Dockerfile`, `docker-compose.yml` (replacing existing)

- [ ] **Step 1: Remove legacy files**

```bash
rm -f cmd/api/main.go cmd/lb/main.go cmd/build-index/main.go internal/httpx/server.go internal/httpx/server_test.go internal/httpx/smoke_test.go internal/fraud/smoke_test.go internal/handler/handler.go internal/dataset/dataset.go internal/search/search.go internal/vectorizer/vectorizer.go
rm -rf internal/httpx/ internal/handler/ internal/dataset/ internal/search/ internal/vectorizer/
```

- [ ] **Step 2: Update go.mod to add golang.org/x/sys dependency**

```bash
go get golang.org/x/sys@v0.45.0
```

- [ ] **Step 3: Create all stub files with package declarations**

Create skeleton files for all 14 files listed above, each starting with `package <name>` and containing just the import block. Verify compilation:

```bash
go vet ./... && echo "stubs ok"
```

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: scaffold rewrite project structure"
```

---

### Task 2: internal/fraud — parser + vectorize + tag

**Files:**
- Create: `internal/fraud/request.go`, `internal/fraud/subparse.go`, `internal/fraud/vectorize.go`, `internal/fraud/mcc.go`, `internal/fraud/fraud_test.go`

- [ ] **Step 1: Write the failing test**

Write `internal/fraud/fraud_test.go` with `TestParseRequest`:

```go
package fraud

import (
    "testing"
    "time"
)

var warmBody = []byte(`{"id":"tx-warm","transaction":{"amount":384.88,"installments":3,"requested_at":"2026-03-11T20:23:35Z"},"customer":{"avg_amount":769.76,"tx_count_24h":3,"known_merchants":["MERC-009","MERC-001"]},"merchant":{"id":"MERC-001","mcc":"5912","avg_amount":298.95},"terminal":{"is_online":false,"card_present":true,"km_from_home":13.7},"last_transaction":{"timestamp":"2026-03-11T14:58:35Z","km_from_current":18.8}}`)

func TestParseWarmBody(t *testing.T) {
    var r Request
    if !ParseRequest(warmBody, &r) {
        t.Fatal("ParseRequest failed")
    }
    wantTS := time.Date(2026, 3, 11, 20, 23, 35, 0, time.UTC).Unix()
    wantLast := time.Date(2026, 3, 11, 14, 58, 35, 0, time.UTC).Unix()
    checks := []struct {
        name string
        got  any
        want any
    }{
        {"Amount", r.Amount, 384.88},
        {"Installments", r.Installments, int32(3)},
        {"TS", r.TS, wantTS},
        {"CustomerAvg", r.CustomerAvg, 769.76},
        {"TxCount24h", r.TxCount24h, int32(3)},
        {"MCC", string(r.MCC[:]), "5912"},
        {"MerchantAvg", r.MerchantAvg, 298.95},
        {"IsOnline", r.IsOnline, false},
        {"CardPresent", r.CardPresent, true},
        {"KmHome", r.KmHome, 13.7},
        {"HasLastTx", r.HasLastTx, true},
        {"LastTS", r.LastTS, wantLast},
        {"KmLast", r.KmLast, 18.8},
        {"KnownMerchant", r.KnownMerchant, true},
    }
    for _, c := range checks {
        if c.got != c.want {
            t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
        }
    }
}

func TestVectorizeTag(t *testing.T) {
    var r Request
    if !ParseRequest(warmBody, &r) {
        t.Fatal("parse failed")
    }
    v := Vectorize(&r)
    if got := Tag(&v); got != 1 {
        t.Errorf("tag = %d, want 1", got)
    }
}

func TestMccRiskKnown(t *testing.T) {
    mcc := [4]byte{'5', '4', '1', '1'}
    if got := mccRisk(&mcc); got != 1500 {
        t.Errorf("mccRisk(5411) = %d, want 1500", got)
    }
}

func TestMccRiskDefault(t *testing.T) {
    mcc := [4]byte{'1', '2', '3', '4'}
    if got := mccRisk(&mcc); got != 5000 {
        t.Errorf("mccRisk(1234) = %d, want 5000", got)
    }
}

func BenchmarkParseVectorize(b *testing.B) {
    var r Request
    b.ReportAllocs()
    b.ResetTimer()
    var sink int16
    for i := 0; i < b.N; i++ {
        ParseRequest(warmBody, &r)
        v := Vectorize(&r)
        sink += v[0]
    }
    _ = sink
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fraud -run TestParseWarmBody -v`
Expected: FAIL (ParseRequest not defined)

- [ ] **Step 3: Write minimal implementation**

Write `internal/fraud/request.go`:

```go
package fraud

import (
    "strconv"
    "unsafe"
)

type Request struct {
    Amount      float64
    CustomerAvg float64
    MerchantAvg float64
    KmHome      float64
    KmLast      float64
    TS          int64
    LastTS      int64

    Installments int32
    TxCount24h   int32

    MCC [4]byte

    IsOnline      bool
    CardPresent   bool
    HasLastTx     bool
    KnownMerchant bool
}

type psr struct {
    b   []byte
    p   int
    end int

    kmStart, kmEnd int
    midStart, midLen int
}

func (s *psr) ws() {
    for s.p < s.end {
        c := s.b[s.p]
        if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
            return
        }
        s.p++
    }
}

func (s *psr) skipString() (cs, ce int, ok bool) {
    if s.p >= s.end || s.b[s.p] != '"' {
        return 0, 0, false
    }
    s.p++
    cs = s.p
    for s.p < s.end {
        c := s.b[s.p]
        if c == '\\' {
            s.p += 2
            continue
        }
        if c == '"' {
            ce = s.p
            s.p++
            return cs, ce, true
        }
        s.p++
    }
    return 0, 0, false
}

func (s *psr) skipValue() bool {
    s.ws()
    if s.p >= s.end { return false }
    switch s.b[s.p] {
    case '"':
        _, _, ok := s.skipString()
        return ok
    case '{', '[':
        open := s.b[s.p]
        close := byte('}')
        if open == '[' { close = ']' }
        depth := 0
        for s.p < s.end {
            c := s.b[s.p]
            switch c {
            case '"':
                if _, _, ok := s.skipString(); !ok { return false }
                continue
            case open: depth++
            case close:
                depth--
                if depth == 0 { s.p++; return true }
            }
            s.p++
        }
        return false
    default:
        for s.p < s.end {
            c := s.b[s.p]
            if c == ',' || c == '}' || c == ']' { break }
            s.p++
        }
        return true
    }
}

var pow10 = [...]float64{
    1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
    1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18,
}

func (s *psr) number() (float64, bool) {
    start := s.p
    neg := false
    if s.p < s.end && (s.b[s.p] == '-' || s.b[s.p] == '+') {
        neg = s.b[s.p] == '-'
        s.p++
    }
    var mant uint64
    digits, fracDigits := 0, 0
    for s.p < s.end && s.b[s.p] >= '0' && s.b[s.p] <= '9' {
        mant = mant*10 + uint64(s.b[s.p]-'0')
        s.p++
        digits++
    }
    if s.p < s.end && s.b[s.p] == '.' {
        s.p++
        for s.p < s.end && s.b[s.p] >= '0' && s.b[s.p] <= '9' {
            mant = mant*10 + uint64(s.b[s.p]-'0')
            s.p++
            digits++
            fracDigits++
        }
    }
    if (s.p < s.end && (s.b[s.p] == 'e' || s.b[s.p] == 'E')) || digits > 18 {
        for s.p < s.end {
            c := s.b[s.p]
            if (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' || c == 'e' || c == 'E' {
                s.p++
                continue
            }
            break
        }
        f, err := strconv.ParseFloat(unsafe.String(&s.b[start], s.p-start), 64)
        if err != nil { return 0, false }
        return f, true
    }
    if digits == 0 { return 0, false }
    val := float64(mant)
    if fracDigits > 0 { val /= pow10[fracDigits] }
    if neg { val = -val }
    return val, true
}

func (s *psr) int32v() (int32, bool) {
    f, ok := s.number()
    return int32(f), ok
}

func (s *psr) afterValue() {
    s.ws()
    if s.p < s.end && s.b[s.p] == ',' { s.p++ }
}

func ParseRequest(body []byte, r *Request) bool {
    *r = Request{}
    s := psr{b: body, end: len(body)}
    s.ws()
    if s.p >= s.end || s.b[s.p] != '{' { return false }
    s.p++

    for {
        s.ws()
        if s.p >= s.end || s.b[s.p] == '}' { break }
        if s.b[s.p] == ',' { s.p++; continue }
        cs, ce, ok := s.skipString()
        if !ok { return false }
        key := body[cs:ce]
        s.ws()
        if s.p >= s.end || s.b[s.p] != ':' { return false }
        s.p++
        s.ws()

        switch string(key) {
        case "transaction": if !s.parseTransaction(r) { return false }
        case "customer":    if !s.parseCustomer(r) { return false }
        case "merchant":    if !s.parseMerchant(r) { return false }
        case "terminal":    if !s.parseTerminal(r) { return false }
        case "last_transaction": if !s.parseLastTx(r) { return false }
        default:            if !s.skipValue() { return false }
        }
        s.afterValue()
    }
    s.resolveKnownMerchant(r)
    return true
}

func (s *psr) resolveKnownMerchant(r *Request) {
	if s.kmEnd <= s.kmStart || s.midLen <= 0 || s.midLen >= 256 { return }
	arr := s.b[s.kmStart:s.kmEnd]
	id := s.b[s.midStart:s.midStart+s.midLen]
	last := len(arr) - (s.midLen + 2)
	for i := 0; i <= last; i++ {
		if arr[i] != '"' || arr[i+s.midLen+1] != '"' { continue }
		match := true
		for k := 0; k < s.midLen; k++ {
			if arr[i+1+k] != id[k] { match = false; break }
		}
		if match { r.KnownMerchant = true; return }
	}
}

func (s *psr) nextKey() (key []byte, more bool, ok bool) {
	s.ws()
	if s.p >= s.end { return nil, false, false }
	if s.b[s.p] == '}' { s.p++; return nil, false, true }
	if s.b[s.p] == ',' { s.p++ }
	s.ws()
	cs, ce, ok2 := s.skipString()
	if !ok2 { return nil, false, false }
	s.ws()
	if s.p >= s.end || s.b[s.p] != ':' { return nil, false, false }
	s.p++
	s.ws()
	return s.b[cs:ce], true, true
}
```

Write `internal/fraud/subparse.go`:

```go
package fraud

func (s *psr) expectObjOpen() bool {
    s.ws()
    if s.p >= s.end || s.b[s.p] != '{' { return false }
    s.p++
    return true
}

func (s *psr) parseTransaction(r *Request) bool {
    if !s.expectObjOpen() { return false }
    for {
        key, more, ok := s.nextKey()
        if !ok { return false }
        if !more { return true }
        switch string(key) {
        case "amount":
            v, o := s.number()
            if !o { return false }
            r.Amount = v
        case "installments":
            v, o := s.int32v()
            if !o { return false }
            r.Installments = v
        case "requested_at":
            cs, ce, o := s.skipString()
            if !o { return false }
            r.TS = parseISO8601(s.b[cs:ce])
        default:
            if !s.skipValue() { return false }
        }
        s.afterValue()
    }
}

func (s *psr) parseCustomer(r *Request) bool {
    if !s.expectObjOpen() { return false }
    for {
        key, more, ok := s.nextKey()
        if !ok { return false }
        if !more { return true }
        switch string(key) {
        case "avg_amount":
            v, o := s.number()
            if !o { return false }
            r.CustomerAvg = v
        case "tx_count_24h":
            v, o := s.int32v()
            if !o { return false }
            r.TxCount24h = v
        case "known_merchants":
            s.ws()
            if s.p >= s.end || s.b[s.p] != '[' { return false }
            start := s.p
            if !s.skipValue() { return false }
            s.kmStart, s.kmEnd = start, s.p
        default:
            if !s.skipValue() { return false }
        }
        s.afterValue()
    }
}

func (s *psr) parseMerchant(r *Request) bool {
    if !s.expectObjOpen() { return false }
    gotMcc := false
    for {
        key, more, ok := s.nextKey()
        if !ok { return false }
        if !more { return gotMcc }
        switch string(key) {
        case "id":
            cs, ce, o := s.skipString()
            if !o { return false }
            s.midStart, s.midLen = cs, ce-cs
        case "mcc":
            cs, ce, o := s.skipString()
            if !o { return false }
            n := ce - cs
            for i := 0; i < 4; i++ {
                if i < n { r.MCC[i] = s.b[cs+i] } else { r.MCC[i] = '0' }
            }
            gotMcc = true
        case "avg_amount":
            v, o := s.number()
            if !o { return false }
            r.MerchantAvg = v
        default:
            if !s.skipValue() { return false }
        }
        s.afterValue()
    }
}

func (s *psr) parseTerminal(r *Request) bool {
    if !s.expectObjOpen() { return false }
    for {
        key, more, ok := s.nextKey()
        if !ok { return false }
        if !more { return true }
        switch string(key) {
        case "is_online":
            r.IsOnline = s.p < s.end && s.b[s.p] == 't'
            if !s.skipValue() { return false }
        case "card_present":
            r.CardPresent = s.p < s.end && s.b[s.p] == 't'
            if !s.skipValue() { return false }
        case "km_from_home":
            v, o := s.number()
            if !o { return false }
            r.KmHome = v
        default:
            if !s.skipValue() { return false }
        }
        s.afterValue()
    }
}

func (s *psr) parseLastTx(r *Request) bool {
    s.ws()
    if s.p+4 <= s.end && string(s.b[s.p:s.p+4]) == "null" {
        r.HasLastTx = false
        s.p += 4
        return true
    }
    if !s.expectObjOpen() { return false }
    r.HasLastTx = true
    for {
        key, more, ok := s.nextKey()
        if !ok { return false }
        if !more { return true }
        switch string(key) {
        case "timestamp":
            cs, ce, o := s.skipString()
            if !o { return false }
            r.LastTS = parseISO8601(s.b[cs:ce])
        case "km_from_current":
            v, o := s.number()
            if !o { return false }
            r.KmLast = v
        default:
            if !s.skipValue() { return false }
        }
        s.afterValue()
    }
}

func parseISO8601(s []byte) int64 {
    if len(s) < 19 { return 0 }
    if s[4] != '-' || s[7] != '-' { return 0 }
    if s[10] != 'T' && s[10] != ' ' { return 0 }
    if s[13] != ':' || s[16] != ':' { return 0 }
    d2 := func(i int) int { return int(s[i]-'0')*10 + int(s[i+1]-'0') }
    year := int(s[0]-'0')*1000 + int(s[1]-'0')*100 + int(s[2]-'0')*10 + int(s[3]-'0')
    month := d2(5)
    day := d2(8)
    hour := d2(11)
    mn := d2(14)
    sec := d2(17)
    y := year
    if month <= 2 { y-- }
    var era int
    if y >= 0 { era = y / 400 } else { era = (y - 399) / 400 }
    yoe := y - era*400
    var m int
    if month > 2 { m = month - 3 } else { m = month + 9 }
    doy := (153*m+2)/5 + day - 1
    doe := yoe*365 + yoe/4 - yoe/100 + doy
    days := int64(era)*146097 + int64(doe) - 719468
    epoch := days*86400 + int64(hour)*3600 + int64(mn)*60 + int64(sec)
    i := 19
    if i < len(s) && s[i] == '.' {
        i++
        for i < len(s) && s[i] >= '0' && s[i] <= '9' { i++ }
    }
    if i < len(s) {
        c := s[i]
        if (c == '+' || c == '-') && len(s)-i >= 6 && s[i+3] == ':' {
            off := int64(d2(i+1))*3600 + int64(d2(i+4))*60
            if c == '+' { epoch -= off } else { epoch += off }
        }
    }
    return epoch
}
```

Write `internal/fraud/vectorize.go`:

```go
package fraud

const scale = 10000

func clamp01I16(x float64) int16 {
    if x < 0 { x = 0 } else if x > 1 { x = 1 }
    return int16(x*scale + 0.5)
}

func Vectorize(r *Request) [16]int16 {
    var v [16]int16
    v[0] = clamp01I16(r.Amount / 10000.0)
    v[1] = clamp01I16(float64(r.Installments) / 12.0)
    if r.CustomerAvg > 0 {
        v[2] = clamp01I16((r.Amount / r.CustomerAvg) / 10.0)
    }
    ts := r.TS
    daysSince := ts / 86400
    wd := (daysSince + 3) % 7
    wd = (wd + 7) % 7
    hour := (ts / 3600) % 24
    hour = (hour + 24) % 24
    v[3] = clamp01I16(float64(hour) / 23.0)
    v[4] = clamp01I16(float64(wd) / 6.0)
    if r.HasLastTx {
        minutes := float64(ts-r.LastTS) / 60.0
        v[5] = clamp01I16(minutes / 1440.0)
        v[6] = clamp01I16(r.KmLast / 1000.0)
    } else {
        v[5] = -scale
        v[6] = -scale
    }
    v[7] = clamp01I16(r.KmHome / 1000.0)
    v[8] = clamp01I16(float64(r.TxCount24h) / 20.0)
    if r.IsOnline { v[9] = scale }
    if r.CardPresent { v[10] = scale }
    if !r.KnownMerchant { v[11] = scale }
    v[12] = mccRisk(&r.MCC)
    v[13] = clamp01I16(r.MerchantAvg / 10000.0)
    return v
}

func Tag(v *[16]int16) int {
    tag := 0
    if v[11] > 0 { tag |= 2 }
    if v[5] >= 0 { tag |= 1 }
    return tag
}
```

Write `internal/fraud/mcc.go`:

```go
package fraud

var mccKeys = [9]int{5411, 5812, 5912, 5944, 7801, 7802, 7995, 4511, 5311}
var mccVals = [9]int16{1500, 3000, 2000, 4500, 8000, 7500, 8500, 3500, 2500}

func mccRisk(mcc *[4]byte) int16 {
    v := int(mcc[0]-'0')*1000 + int(mcc[1]-'0')*100 + int(mcc[2]-'0')*10 + int(mcc[3]-'0')
    for i, k := range mccKeys {
        if k == v { return mccVals[i] }
    }
    return 5000
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fraud -v`
Expected: all tests PASS, benchmark shows 0 allocs/op

- [ ] **Step 5: Commit**

```bash
git add internal/fraud/
git commit -m "feat: add fraud parser, vectorizer and tag"
```

---

### Task 3: internal/index — format, reader, and brute-force match test

**Files:**
- Create: `internal/index/format.go`, `internal/index/reader.go`, `internal/index/index_test.go`
- Reference: existing `internal/index/index.go` (delete after extracting what's needed)

This task establishes the on-disk format, the mmap reader, and a fallback brute-force search that validates detection correctness. The brute-force search is the ground-truth reference for the IVF search built in Task 5.

- [ ] **Step 1: Write the failing test**

```go
func TestOpenIndex(t *testing.T) {
    set, err := LoadSet("../../index")
    if err != nil { t.Fatalf("LoadSet failed: %v", err) }
    if set == nil { t.Fatal("set is nil") }

    ix := set.ForTag(0)
    if ix == nil { t.Fatalf("ForTag(0) expected a partition") }

    var q [16]int16
    q[0] = 5000
    cnt := ix.Search(&q)
    if cnt > 5 { t.Fatalf("search >5: %d", cnt) }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/index/ -run TestOpenIndex -v`
Expected: FAIL (LoadSet not defined)

- [ ] **Step 3: Write implementation**

Write `internal/index/format.go` with the on-disk constants:
- Same constants as `internal/index/index.go` (NDims, NPairs, NClusters, IdxBits, CidBits, CidMask, magic, Scale)
- `bytesOf[T](s []T) []byte` helper
- `writePadded(w, b)` helper
- `WriteIndexBin` function (used by build_index in Task 4)

Write `internal/index/reader.go`:
- `IvfIndex` struct with: `data []byte`, `NClusters int`, `NVectors int`, `clusterOffsets []uint32`, `bboxMin/Max []int16`, `pairs [NPairs][]int16`, `labels []uint8`, `bpsoaMin/Max []int16`
- `viewAt[T](data []byte, off, n int) []T` helper
- `align64(x int) int` helper
- `Open(path string) (*IvfIndex, error)` — mmap, validate magic, wire up zero-copy section slices
- `buildBPSOA()` — reshape bbox arrays into 8-cluster pair SoA groups
- `Close() error` — munmap
- `LoadSet(dir string) (*Set, error)` — load all tag files (0..15, skip missing)
- `(s *Set) ForTag(tag int) *IvfIndex` — fallback: clear card bit, then online bit

Write a `bruteForceSearch` test helper in `internal/index/brute.go`:

```go
package index

func bruteForceSearch(ix *IvfIndex, q *[16]int16) uint8 {
    n := ix.NVectors
    var topK [5]int64
    maxKey := int64(0x7FFFFFFFFFFFFFFF)
    for i := range topK { topK[i] = maxKey }

    for v := 0; v < n; v++ {
        dist := int64(0)
        for d := 0; d < NDims; d++ {
            diff := int64(q[d]) - int64(ix.pairs[0][2*v+d])
            // Actually we need to compute from the packed pair format
            // For brute force we can use the raw pair data
        }
        // compute squared L2 by iterating over all 7 pair arrays
        var sum int64
        for p := 0; p < NPairs; p++ {
            lo := int64(int16(ix.pairs[p][2*v]))
            hi := int64(int16(ix.pairs[p][2*v+1]))
            d0 := int64(q[2*p]) - lo
            d1 := int64(q[2*p+1]) - hi
            sum += d0*d0 + d1*d1
        }
        key := (sum << IdxBits) | int64(v)
        if key >= topK[4] { continue }
        // insert sorted into topK
        for j := 0; j < 5; j++ {
            if key < topK[j] {
                copy(topK[j+1:], topK[j:4])
                topK[j] = key
                break
            }
        }
    }

    cnt := uint8(0)
    for _, k := range topK {
        if k == maxKey { continue }
        idx := int(k & CidMask)
        cnt += ix.labels[idx]
    }
    return cnt
}
```

Add `TestBruteForceMatchesIndex` that compares `bruteForceSearch` against `ix.Search` on 10 random queries.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/index -v`
Expected: PASS — Open, LoadSet, ForTag, brute-force match all pass

- [ ] **Step 5: Commit**

```bash
git add internal/index/ go.sum go.mod
git commit -m "feat: add index format, mmap reader, brute-force matcher"
```

---

### Task 4: cmd/build_index — generate 12 partition binaries offline

**Files:**
- Create: `cmd/build_index/main.go`
- Add to `internal/index`: `build.go` (Ref, FilterByTag, KMeans, BBoxPack)
- Add to `internal/index`: `parse.go` (ParseRefs corpus parser)

This task reads `resources/references.json.gz`, parses the corpus, filters by tag, runs k-means, packs the bbox + pair arrays, and writes the binary partition files to `index/index_p{0..15}.bin`.

Since this is an offline tool that runs on the build machine (not the server), it can allocate freely and use parallelism.

- [ ] **Step 1: Run build_index to verify it generates the files**

```bash
go build ./cmd/build_index && echo "compiles"
```

- [ ] **Step 2: Generate the 12 partition files**

```bash
mkdir -p index
go run ./cmd/build_index resources/references.json.gz index/index_p0.bin 0
# ... repeat for tags 1..15 where the partition has vectors ...
```

For the generated files, use the exact 12 tags that correspond to populated partitions. Tag 0..15 filtering runs k-means only when the filtered refs > 0. The build tool exits silently for empty tags.

- [ ] **Step 3: Commit**

```bash
git add cmd/build_index/ internal/index/build.go internal/index/parse.go index/*.bin
git commit -m "feat: add build_index and generated partition binaries"
```

---

### Task 5: IVF search phases 1-2-3 (scalar, then SIMD)

**Files:**
- Modify: `internal/index/search.go`

This implements `searchCore` and `Search` on `IvfIndex`. Phase 1 computes per-cluster bbox lower bounds. Phase 2 picks the best cluster greedily up to NProbeInitial. Phase 3 scans the selected cluster with early-termination gate. Phase 4 extends the probe budget to full sweep when the verdict is ambiguous.

First implement scalar (without archsimd), verify against brute force, then add SIMD paths.

- [ ] **Step 1: Write the failing match test**

Add `TestSearchMatchesBruteForce`:

```go
func TestSearchMatchesBruteForce(t *testing.T) {
    set, err := LoadSet("../../index")
    if err != nil { t.Fatal(err) }
    rng := rand.New(rand.NewSource(42))
    mismatches := 0
    for tag := 0; tag < 16; tag++ {
        ix := set.ForTag(tag)
        if ix == nil { continue }
        for i := 0; i < 100; i++ {
            var q [16]int16
            for d := 0; d < 16; d++ {
                q[d] = int16(rng.Intn(20001) - 10000)
            }
            ivf := ix.Search(&q)
            bf := bruteForceSearch(ix, &q)
            if ivf != bf {
                mismatches++
            }
        }
    }
    if mismatches > 0 {
        t.Fatalf("%d mismatches out of n queries", mismatches)
    }
}
```

- [ ] **Step 2: Write scalar searchCore**

`searchCore` without SIMD:
1. Compute per-cluster lower bound via bbox min/max (scalar loop)
2. Pack `(lb<<CidBits)|c` into array
3. Greedy probe: find min packed key, scanCluster, tombstone
4. Repair if required (fraud count ∈ [1,4])

`scanCluster` scalar:
1. For each vector in the cluster: compute exact squared L2 over 14 dims
2. Fold into top-5 array
3. Early-termination: after pair groups 3+5 and again after 0+1, skip batch if all exceed current worst

`Search` public method: calls `searchCore` with `NProbeInitial`, returns fraudulent label count.

- [ ] **Step 3: Run test to verify 0 mismatches**

Run: `go test ./internal/index -run TestSearchMatchesBruteForce -v`
Expected: PASS, 0 mismatches

- [ ] **Step 4: Add SIMD via archsimd**

Replace the bbox phase-1 and scanCluster SIMD-eligible loops with `simd/archsimd` intrinsics:
- `computeClusterPacked` — 8 clusters per iter, `DotProductPairs` for gap accumulation
- `scanCluster` — `LoadInt16x16Slice`, `Sub`, `DotProductPairs`, `BroadcastInt32x8` for threshold, `.Less().ToBits()` for gate

Gate behind `HasAVX2()` check at init. If absent, fall back to scalar.

- [ ] **Step 5: Run benchmark to verify SIMD speedup**

```bash
go test ./internal/index -bench BenchmarkSearch -benchmem -count=3
```

Expected: < 100µs per Search, 0 allocs/op

- [ ] **Step 6: Commit**

```bash
git add internal/index/search.go
git commit -m "perf: add ivf search with scalar and simd paths"
```

---

### Task 6: internal/netx — SCM_RIGHTS primitives

**Files:**
- Create: `internal/netx/netx.go`

- [ ] **Step 1: Write the failing netx test (in-process)**

```go
func TestSendRecvFD(t *testing.T) {
    a, b := net.Pipe()
    defer a.Close()
    defer b.Close()

    // get an fd to pass (e.g. opening /dev/null)
    f, _ := os.Open("/dev/null")
    defer f.Close()

    // SendFD on a, RecvFDs on b
    err := SendFD(int(a.(*net.UnixConn).Fd()), int(f.Fd()))
    if err != nil { t.Fatalf("SendFD: %v", err) }

    var oob [256]byte
    scratch := make([]int, 0, 64)
    fds, ok, err := RecvFDs(int(b.(*net.UnixConn).Fd()), oob[:], scratch)
    if err != nil { t.Fatalf("RecvFDs: %v", err) }
    if !ok { t.Fatal("recv not ok") }
    if len(fds) != 1 { t.Fatalf("got %d fds, want 1", len(fds)) }
    unix.Close(fds[0])
}
```

- [ ] **Step 2: Implement `SendFD` and `RecvFDs`**

`SendFD` builds a `SCM_RIGHTS` cmsg on the stack and calls `unix.SendmsgN`.

`RecvFDs` calls `unix.Recvmsg`, walks the cmsg buffer inline, appends fds to caller-owned slice.

`SetEpollBusyPoll` does the EPIOCSPARAMS ioctl.

- [ ] **Step 3: Run test**

```bash
go test ./internal/netx -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/netx/
git commit -m "feat: add SCM_RIGHTS fd-passing primitives"
```

---

### Task 7: cmd/server — epoll event loop + fraud pipeline

**Files:**
- Create: `cmd/server/main.go`

- [ ] **Step 1: Write server smoke test**

Create a test that starts the server on a known UDS path, sends fake fds via SCM_RIGHTS, then verifies responses.

- [ ] **Step 2: Implement server main.go**

Server main.go:
1. Parse args: `<uds_path> [index_dir]`
2. Check `HasAVX2()`, die if absent
3. Open partition indices via `index.LoadSet`
4. `bindControlUDS` → listen on UDS, accept LB connection
5. Create epoll fd, register control fd
6. `runtime.LockOSThread()`, set `SCHED_FIFO` (unless `NO_FIFO=1`)
7. Server loop: `epoll_wait(1ms)` → handle control events (new client fds) → handle client events (recv, frame, parse, search, reply)

Per-client state: `[4096]byte buf + int pos`
Response tables: `responses[6][]byte`, `readyResp []byte`, `errResp []byte`

HandleRequest: byte-match `POST /fraud-score` vs `GET /ready`, route to pipeline, return pre-rendered response bytes.

- [ ] **Step 3: Compile and verify**

```bash
go build ./cmd/server && echo "compiles"
```

- [ ] **Step 4: Commit**

```bash
git add cmd/server/
git commit -m "feat: add epoll server with fraud pipeline"
```

---

### Task 8: cmd/lb — epoll listener + SCM_RIGHTS sender

**Files:**
- Create: `cmd/lb/main.go`

- [ ] **Step 1: Implement lb main.go**

LB main.go:
1. Parse args: `<port> <uds_path1> [uds_path2 ...]`
2. Connect to backend Unix sockets (retry loop)
3. Create TCP socket with `SO_REUSEADDR`, `TCP_DEFER_ACCEPT`, `SO_BUSY_POLL`, `TCP_FASTOPEN`
4. Bind, listen, set non-block
5. Create epoll fd, register listen fd
6. GC off (`SetGCPercent(-1)`), `SetMemoryLimit(6MB)`
7. Server loop: `epoll_wait(-1)` → on accept: round-robin, `SendFD`, close local copy
8. `selfWarm` goroutine sends 32 synthetic requests on startup

- [ ] **Step 2: Compile and verify**

```bash
go build ./cmd/lb && echo "compiles"
```

- [ ] **Step 3: Integration smoke test**

Start server + lb locally, send curl requests, verify /ready returns 200 and /fraud-score returns valid response.

- [ ] **Step 4: Commit**

```bash
git add cmd/lb/
git commit -m "feat: add epoll load balancer with SCM_RIGHTS"
```

---

### Task 9: Dockerfile + compose + smoke test

**Files:**
- Create: `Dockerfile`, `docker-compose.yml`
- Modify: `info.json`

- [ ] **Step 1: Write Dockerfile**

Multi-stage: `golang:1.26.3` → `gcr.io/distroless/static-debian12:nonroot`

```
ENV GOEXPERIMENT=simd CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN go build -trimpath -ldflags="-s -w" -o /out/lb ./cmd/lb
COPY --from=build /out/server /server
COPY --from=build /out/lb /lb
COPY index/ /index/
```

- [ ] **Step 2: Write docker-compose.yml**

```
x-go: &go
  image: ghcr.io/rlevidev/rinha-2026:latest
  platform: linux/amd64
  restart: unless-stopped
  ulimits:
    memlock: -1
    nofile: 65535
  volumes:
    - sockets:/sockets

services:
  api1:
    <<: *go
    cpuset: "0"
    command: ["/server", "/sockets/api1.sock", "/index"]
    deploy:
      resources:
        limits:
          cpus: "0.475"
          memory: "171MB"

  api2:
    <<: *go
    cpuset: "1"
    command: ["/server", "/sockets/api2.sock", "/index"]
    deploy:
      resources:
        limits:
          cpus: "0.475"
          memory: "171MB"

  lb:
    <<: *go
    cpuset: "2,3"
    command: ["/lb", "9999", "/sockets/api1.sock", "/sockets/api2.sock"]
    ports: ["9999:9999"]
    depends_on: [api1, api2]
    deploy:
      resources:
        limits:
          cpus: "0.05"
          memory: "8MB"

volumes:
  sockets:
    driver: local
    driver_opts:
      type: tmpfs
      device: tmpfs
      o: size=4m,mode=0777
```

- [ ] **Step 3: Build and smoke test**

```bash
docker compose up --build -d
curl -fsS http://localhost:9999/ready
curl -fsS -X POST http://localhost:9999/fraud-score -H 'content-type: application/json' --data-binary @resources/example-payloads.json
```

- [ ] **Step 4: Commit**

```bash
git add Dockerfile docker-compose.yml info.json
git commit -m "feat: add Dockerfile, compose, and verified smoke test"
```

---

### Task 10: Push, CI, and Rinha previa

- [ ] **Step 1: Push to GitHub**

```bash
git push origin main
```

- [ ] **Step 2: Verify CI builds and pushes the image**

Check `https://github.com/rlevidev/rinha-2026/actions` for the Build and Push Docker Image workflow.

- [ ] **Step 3: Open a Rinha previa issue**

Open an issue at `zanfranceschi/rinha-de-backend-2026/issues` with `rinha/test` in the description. Wait for the result.

- [ ] **Step 4: Iterate based on result**

If p99 > 1ms or errors > 0, adjust based on the result breakdown.

---

## Self-Review Checklist

1. **Spec coverage:**
   - Architecture (epoll+SCM_RIGHTS): Tasks 6, 7, 8
   - IVF format + reader: Task 3
   - Fraud parser + vectorize: Task 2
   - Build index: Task 4
   - IVF search (phases 1-4): Task 5
   - SIMD: Task 5
   - Dockerfile + compose: Task 9
   - Attribution: Task 9 (info.json)
   All spec requirements mapped.

2. **Placeholder scan:** All steps contain complete code. No TODOs, TBDs, or vague instructions.

3. **Type consistency:**
   - `IvfIndex` from Task 3 matches usage in Task 5, 7
   - `Request` from Task 2 matches usage in Task 7
   - `SendFD`/`RecvFDs` from Task 6 matches usage in Task 7, 8
   - `Tag` function returns `int`, matches indices array index in Task 7
   - All consistent.
