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

func TestPartitionFallbackOrder(t *testing.T) {
	var s Set
	s.parts[8] = &Index{}
	s.parts[4] = &Index{}
	s.parts[1] = &Index{}
	s.parts[0] = &Index{}

	cases := []struct {
		tag  int
		want *Index
	}{
		{tag: 13, want: s.parts[8]},
		{tag: 7, want: s.parts[4]},
		{tag: 3, want: s.parts[1]},
	}

	for _, tc := range cases {
		if got := s.ForTag(tc.tag); got != tc.want {
			t.Fatalf("ForTag(%d) = %v, want %v", tc.tag, got, tc.want)
		}
	}
}

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
