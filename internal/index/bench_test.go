package index

import (
	"math/rand"
	"testing"
)

func benchmarkIndexSetup(b *testing.B) *Index {
	b.Helper()
	set, err := LoadSet("../../index")
	if err != nil {
		b.Fatalf("load index set: %v", err)
	}
	ix := set.ForTag(0)
	if ix == nil {
		b.Fatal("expected partition 0 to be present")
	}
	return ix
}

func randomQuery(rng *rand.Rand) [16]int16 {
	var q [16]int16
	for i := range q {
		q[i] = int16(rng.Intn(20001) - 10000)
	}
	return q
}

func BenchmarkIndexSearch(b *testing.B) {
	ix := benchmarkIndexSetup(b)
	rng := rand.New(rand.NewSource(1))
	queries := make([][16]int16, 64)
	for i := range queries {
		queries[i] = randomQuery(rng)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := queries[i&63]
		_ = ix.Search(&q)
	}
}

func BenchmarkIndexSearchRepairPath(b *testing.B) {
	ix := benchmarkIndexSetup(b)
	var q [16]int16
	for i := range q {
		q[i] = 5000
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ix.Search(&q)
	}
}
