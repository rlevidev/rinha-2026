package index

import (
	"math/rand"
	"testing"
)

func TestOpenIndex(t *testing.T) {
	set, err := LoadSet("../../index")
	if err != nil {
		t.Fatalf("LoadSet failed: %v", err)
	}
	if set == nil {
		t.Fatal("set is nil")
	}

	ix := set.ForTag(0)
	if ix == nil {
		t.Fatalf("ForTag(0) expected a partition")
	}

	var q [16]int16
	q[0] = 5000
	cnt := ix.Search(&q)
	if cnt > 5 {
		t.Fatalf("search >5: %d", cnt)
	}
}

func TestSearchMatchesBruteForce(t *testing.T) {
	set, err := LoadSet("../../index")
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(42))
	mismatches := 0
	for tag := 0; tag < 16; tag++ {
		ix := set.ForTag(tag)
		if ix == nil {
			continue
		}
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
