package index

import "testing"

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
