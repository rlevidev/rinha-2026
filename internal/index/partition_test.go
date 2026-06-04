package index

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestSearch(t *testing.T) {
	s := &Set{
		partitions: map[uint8]*Partition{
			0: {
				data:   []int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14},
				labels: []uint8{0},
				size:   1,
			},
		},
	}
	query := [14]float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	res := s.Search(query, 0)
	if len(res) == 0 || res[0] != 0 {
		t.Errorf("Expected ID 0, got %v", res)
	}
}

func TestRoundTrip(t *testing.T) {
	f, _ := os.Create("partition_0.bin")
	defer f.Close()
	defer os.Remove("partition_0.bin")

	// 14 * int16
	for i := 0; i < 14; i++ {
		binary.Write(f, binary.LittleEndian, int16(i+1))
	}
	// 1 * uint8
	binary.Write(f, binary.LittleEndian, uint8(0))

	s, err := NewSet(".")
	if err != nil {
		t.Fatal(err)
	}
	if s.partitions[0] == nil {
		t.Errorf("Partition 0 not loaded")
	}
}

func TestContagem0(t *testing.T) {
	s := &Set{partitions: make(map[uint8]*Partition)}
	// Should fallback to any existing partition, if none, nil
	res := s.Search([14]float32{}, 1)
	if res != nil {
		t.Errorf("Expected nil, got %v", res)
	}
}
