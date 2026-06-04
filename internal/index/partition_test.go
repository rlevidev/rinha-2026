package index

import (
	"os"
	"testing"
	"unsafe"
)

func TestOpen(t *testing.T) {
	// Create a dummy file
	f, err := os.CreateTemp("", "part_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// 1 Entry (32 bytes to match entrySize=32)
	entry := Entry{Vec: [14]int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}, Fraud: true}
	
	// Create a buffer of 32 bytes
	b := make([]byte, 32)
	
	// Copy entry to buffer
	ptr := (*Entry)(unsafe.Pointer(&b[0]))
	*ptr = entry
	
	_, err = f.Write(b)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	p, err := Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if len(p.entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(p.entries))
	}
	
	if p.entries[0].Fraud != true {
		t.Errorf("expected fraud true, got %v", p.entries[0].Fraud)
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
