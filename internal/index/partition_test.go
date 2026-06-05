package index

import (
	"encoding/binary"
	"os"
	"testing"
)

func writeEntry(f *os.File, vec [14]int16, fraud bool) {
	for _, v := range vec {
		binary.Write(f, binary.LittleEndian, v)
	}
	label := byte(0)
	if fraud {
		label = 1
	}
	binary.Write(f, binary.LittleEndian, label)
}

func TestOpen(t *testing.T) {
	// Create a dummy file
	f, err := os.CreateTemp("", "part_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Write 1 entry using the real 29-byte format (14 int16 + 1 byte)
	writeEntry(f, [14]int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}, true)
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
