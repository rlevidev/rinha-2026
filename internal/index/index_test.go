package index

import "testing"

func TestLoadSetAndSearch(t *testing.T) {
	set, err := LoadSet("../../index")
	if err != nil {
		t.Fatalf("LoadSet failed: %v", err)
	}

	ix := set.ForTag(0)
	if ix == nil {
		t.Fatalf("expected partition 0 to be present")
	}

	var q [16]int16
	got := ix.Search(&q)
	if got > 5 {
		t.Fatalf("Search returned invalid fraud count %d", got)
	}
}
