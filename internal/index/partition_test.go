package index_test

import (
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/rlevidev/rinha-2026/internal/index"
)

const (
	vectorSize     = 14
	entrySizeBytes = vectorSize*2 + 1
)

// Helper to create a synthetic binary partition file
func createSyntheticPartitionFile(t *testing.T, path string, entries []index.Entry) {
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create synthetic partition file: %v", err)
	}
	defer file.Close()

	for _, entry := range entries {
		buf := make([]byte, entrySizeBytes)
		for i, val := range entry.Vec {
			binary.LittleEndian.PutUint16(buf[i*2:i*2+2], uint16(val))
		}
		if entry.Fraud {
			buf[vectorSize*2] = 1
		} else {
			buf[vectorSize*2] = 0
		}
		_, err := file.Write(buf)
		if err != nil {
			t.Fatalf("Failed to write entry to synthetic partition file: %v", err)
		}
	}
}

func TestLoad(t *testing.T) {
	// Test 1: Round-trip - Write 10 synthetic vectors, load, verify data
	tmpDir := t.TempDir()
	testFilePath := filepath.Join(tmpDir, "partition_0.bin")

	syntheticEntries := []index.Entry{
		{Vec: [vectorSize]int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}, Fraud: false},
		{Vec: [vectorSize]int16{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140}, Fraud: true},
		{Vec: [vectorSize]int16{11, 22, 33, 44, 55, 66, 77, 88, 99, 110, 121, 132, 143, 154}, Fraud: false},
		{Vec: [vectorSize]int16{-32767, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, Fraud: true}, // Sentinel test
	}
	createSyntheticPartitionFile(t, testFilePath, syntheticEntries)

	partition, err := index.Load(testFilePath)
	if err != nil {
		t.Fatalf("Failed to load partition: %v", err)
	}

	if len(partition.Entries()) != len(syntheticEntries) { // Access Entries() method
		t.Fatalf("Loaded entry count mismatch. Expected %d, got %d", len(syntheticEntries), len(partition.Entries()))
	}

	for i, expected := range syntheticEntries {
		actual := partition.Entries()[i] // Access Entries() method
		if actual.Fraud != expected.Fraud {
			t.Errorf("Entry %d Fraud mismatch. Expected %t, got %t", i, expected.Fraud, actual.Fraud)
		}
		for j := 0; j < vectorSize; j++ {
			if actual.Vec[j] != expected.Vec[j] {
				t.Errorf("Entry %d Vector[%d] mismatch. Expected %d, got %d", i, j, expected.Vec[j], actual.Vec[j])
			}
		}
	}
}

func TestLoadSet(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a few synthetic partition files
	createSyntheticPartitionFile(t, filepath.Join(tmpDir, "partition_0.bin"), []index.Entry{{Vec: [vectorSize]int16{1}, Fraud: false}})
	createSyntheticPartitionFile(t, filepath.Join(tmpDir, "partition_1.bin"), []index.Entry{{Vec: [vectorSize]int16{2}, Fraud: true}})
	createSyntheticPartitionFile(t, filepath.Join(tmpDir, "partition_3.bin"), []index.Entry{{Vec: [vectorSize]int16{3}, Fraud: false}})

	set, err := index.LoadSet(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load set: %v", err)
	}

	// Verify loaded partitions
	if set.Partition(0) == nil || len(set.Partition(0).Entries()) != 1 {
		t.Errorf("Partition 0 not loaded correctly")
	}
	if set.Partition(1) == nil || len(set.Partition(1).Entries()) != 1 {
		t.Errorf("Partition 1 not loaded correctly")
	}
	if set.Partition(2) != nil {
		t.Errorf("Partition 2 should not be loaded")
	}
	if set.Partition(3) == nil || len(set.Partition(3).Entries()) != 1 {
		t.Errorf("Partition 3 not loaded correctly")
	}
}

func TestSearch_Trivial(t *testing.T) {
	// Test 2: Trivial search - 3 identical fraud, 7 distant legit -> returns 3
	tmpDir := t.TempDir()
	testFilePath := filepath.Join(tmpDir, "partition_0.bin")

	// Query vector (quantized from 0.5 for all dimensions)
	queryFloatVector := [vectorSize]float32{}
	for i := 0; i < vectorSize; i++ {
		queryFloatVector[i] = 0.5
	}
	queryIntVector := [vectorSize]int16{}
	for i := 0; i < vectorSize; i++ {
		queryIntVector[i] = int16(16383)
	}

	var syntheticEntries []index.Entry

	// 3 identical fraud entries
	for i := 0; i < 3; i++ {
		syntheticEntries = append(syntheticEntries, index.Entry{Vec: queryIntVector, Fraud: true})
	}
	// 7 distant legit entries
	for i := 0; i < 7; i++ {
		distantVector := [vectorSize]int16{}
		for j := 0; j < vectorSize; j++ {
			distantVector[j] = int16(rand.Intn(100) + 20000) // Large values
		}
		syntheticEntries = append(syntheticEntries, index.Entry{Vec: distantVector, Fraud: false})
	}
	createSyntheticPartitionFile(t, testFilePath, syntheticEntries)

	set, err := index.LoadSet(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load set: %v", err)
	}

	fraudCount := set.Search(queryFloatVector, 0)
	if fraudCount != 3 {
		t.Errorf("Expected fraud count 3, got %d", fraudCount)
	}
}

func TestSearch_CountZero(t *testing.T) {
	// Test 3: Count 0 - All 5 nearest are legit -> Search returns 0
	tmpDir := t.TempDir()
	testFilePath := filepath.Join(tmpDir, "partition_0.bin")

	queryFloatVector := [vectorSize]float32{}
	for i := 0; i < vectorSize; i++ {
		queryFloatVector[i] = 0.5
	}
	queryIntVector := [vectorSize]int16{}
	for i := 0; i < vectorSize; i++ {
		queryIntVector[i] = int16(16383)
	}

	var syntheticEntries []index.Entry

	// 5 nearest legit entries (very close to query)
	for i := 0; i < 5; i++ {
		closeVector := [vectorSize]int16{}
		for j := 0; j < vectorSize; j++ {
			closeVector[j] = queryIntVector[j] + int16(rand.Intn(5)) // Slightly different
		}
		syntheticEntries = append(syntheticEntries, index.Entry{Vec: closeVector, Fraud: false})
	}

	// Some distant fraud entries (should not be among the 5 nearest)
	for i := 0; i < 5; i++ {
		distantVector := [vectorSize]int16{}
		for j := 0; j < vectorSize; j++ {
			distantVector[j] = int16(rand.Intn(100) + 20000)
		}
		syntheticEntries = append(syntheticEntries, index.Entry{Vec: distantVector, Fraud: true})
	}
	createSyntheticPartitionFile(t, testFilePath, syntheticEntries)

	set, err := index.LoadSet(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load set: %v", err)
	}

	fraudCount := set.Search(queryFloatVector, 0)
	if fraudCount != 0 {
		t.Errorf("Expected fraud count 0, got %d", fraudCount)
	}
}
