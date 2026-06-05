package index

import (
	"encoding/binary"
	"math/rand"
	"os"
	"testing"
)

func writeEntry(t *testing.T, f *os.File, vec [14]int16, fraud bool) {
	t.Helper()
	for _, v := range vec {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	label := byte(0)
	if fraud {
		label = 1
	}
	if err := binary.Write(f, binary.LittleEndian, label); err != nil {
		t.Fatal(err)
	}
}

func TestOpen(t *testing.T) {
	// Create a dummy file
	f, err := os.CreateTemp("", "part_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Write header: num_clusters = 0 (uint64 LE)
	if err := binary.Write(f, binary.LittleEndian, uint64(0)); err != nil {
		t.Fatal(err)
	}
	// Write 1 entry using the real 29-byte format (14 int16 + 1 byte)
	writeEntry(t, f, [14]int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}, true)
	f.Close()

	p, err := Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	if len(p.entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(p.entries))
	}

	if p.entries[0].Fraud != true {
		t.Errorf("expected fraud true, got %v", p.entries[0].Fraud)
	}
	if p.entries[0].Vec[0] != 1 || p.entries[0].Vec[13] != 14 {
		t.Errorf("unexpected Vec values: %v", p.entries[0].Vec)
	}
}

func TestSearch(t *testing.T) {
	// Dummy Partition
	p := &Partition{
		entries: []Entry{
			{Vec: [14]int16{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, Fraud: false},
			{Vec: [14]int16{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10}, Fraud: true},
			{Vec: [14]int16{20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20}, Fraud: false},
		},
	}

	query := &[14]int16{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10}

	// This search will find the 2nd entry (distance 0) and the 1st/3rd as neighbors.
	// 2nd entry is fraud=true, others are false.
	// fraudCount should be 1.
	fraudCount := p.Search(query)

	if fraudCount == 0 {
		t.Errorf("expected fraud count > 0, got 0")
	}
}

func TestClusterSearchExactMatch(t *testing.T) {
	entries := make([]Entry, 500)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 250; i++ {
		for j := 0; j < 14; j++ {
			entries[i].Vec[j] = 100 + int16(rng.Intn(50))
		}
		entries[i].Fraud = i%5 == 0
	}
	for i := 250; i < 500; i++ {
		for j := 0; j < 14; j++ {
			entries[i].Vec[j] = int16(rng.Intn(50))
		}
		entries[i].Fraud = i%5 == 0
	}

	centers := []ClusterCenter{
		{Vec: [14]int16{125, 125, 125, 125, 125, 125, 125, 125, 125, 125, 125, 125, 125, 125}},
		{Vec: [14]int16{25, 25, 25, 25, 25, 25, 25, 25, 25, 25, 25, 25, 25, 25}},
	}
	assignments := make([]uint8, 500)
	for i := 0; i < 250; i++ {
		assignments[i] = 0
		var d int64
		for j := 0; j < 14; j++ {
			diff := int64(entries[i].Vec[j]) - int64(centers[0].Vec[j])
			d += diff * diff
		}
		if d > centers[0].MaxRadius {
			centers[0].MaxRadius = d
		}
	}
	for i := 250; i < 500; i++ {
		assignments[i] = 1
		var d int64
		for j := 0; j < 14; j++ {
			diff := int64(entries[i].Vec[j]) - int64(centers[1].Vec[j])
			d += diff * diff
		}
		if d > centers[1].MaxRadius {
			centers[1].MaxRadius = d
		}
	}

	clusteredP := &Partition{
		entries:            entries,
		clusterCenters:     centers,
		clusterAssignments: assignments,
	}
	plainP := &Partition{entries: entries}

	for i := 0; i < 100; i++ {
		var query [14]int16
		for j := 0; j < 14; j++ {
			query[j] = int16(rng.Intn(150))
		}
		r1 := clusteredP.Search(&query)
		r2 := plainP.Search(&query)
		if r1 != r2 {
			t.Errorf("Mismatch for query %v: clustered=%d, plain=%d", query, r1, r2)
		}
	}
}
