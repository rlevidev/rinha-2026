package fraud

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/rlevidev/rinha-2026/internal/index"
)

func loadExamplePayloads(b *testing.B, path string) [][]byte {
	b.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatalf("read payloads: %v", err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		b.Fatalf("unmarshal payloads: %v", err)
	}
	if len(entries) == 0 {
		b.Fatalf("no payloads found in %s", path)
	}

	payloads := make([][]byte, len(entries))
	for i, e := range entries {
		payloads[i] = []byte(e)
	}
	return payloads
}

func mustNewHandlerForBench(b *testing.B) *Handler {
	b.Helper()

	set, err := index.LoadSet("../../index")
	if err != nil {
		b.Fatalf("load index set: %v", err)
	}
	norm, err := LoadNormalizer("../../resources/normalization.json", "../../resources/mcc_risk.json")
	if err != nil {
		b.Fatalf("load normalizer: %v", err)
	}
	return &Handler{Indexes: set, Normalizer: norm}
}

func BenchmarkFraudPipeline(b *testing.B) {
	payloads := loadExamplePayloads(b, "../../resources/example-payloads.json")
	h := mustNewHandlerForBench(b)

	b.ReportAllocs()
	b.SetBytes(int64(len(payloads[0])))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.Score(payloads[i%len(payloads)])
	}
}

func BenchmarkFraudParseOnly(b *testing.B) {
	payloads := loadExamplePayloads(b, "../../resources/example-payloads.json")

	b.ReportAllocs()
	b.SetBytes(int64(len(payloads[0])))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req Request
		_ = ParseRequest(payloads[i%len(payloads)], &req)
	}
}
