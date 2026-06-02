package index

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const (
	vectorSize      = 14
	entrySizeBytes  = vectorSize*2 + 1 // 14 int16 (2 bytes each) + 1 byte for label
	NumNearest      = 5                // K for KNN (exported for use by other packages)
	FraudThreshold = 0.6              // For fraud_score calculation (exported for use by other packages)
)

// Entry is a quantized vector + label
type Entry struct {
	Vec   [vectorSize]int16
	Fraud bool
}

// Partition is an index loaded in memory
type Partition struct {
	entries []Entry
}

// Entries returns the slice of entries in the partition. This is primarily for testing.
func (p *Partition) Entries() []Entry {
	return p.entries
}

// Load loads a partition_TAG.bin file
func Load(path string) (*Partition, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening partition file %s: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var entries []Entry

	buf := make([]byte, entrySizeBytes)
	for {
		n, err := io.ReadFull(reader, buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading from partition file %s: %w", path, err)
		}
		if n != entrySizeBytes {
			return nil, fmt.Errorf("unexpected number of bytes read from %s: got %d, want %d", path, n, entrySizeBytes)
		}

		var entry Entry
		for i := 0; i < vectorSize; i++ {
			// Read uint16 and convert to int16
			entry.Vec[i] = int16(binary.LittleEndian.Uint16(buf[i*2 : i*2+2]))
		}
		entry.Fraud = buf[vectorSize*2] == 1
		entries = append(entries, entry)
	}

	return &Partition{entries: entries}, nil
}

// Set contains all partitions indexed by tag (0-15)
type Set struct {
	partitions [16]*Partition
}

// Partition returns the partition for the given tag. This is primarily for testing.
func (s *Set) Partition(tag uint8) *Partition {
	return s.partitions[tag]
}

// LoadSet loads all partitions from a directory
func LoadSet(dir string) (*Set, error) {
	set := &Set{}
	for i := 0; i < 16; i++ {
		path := filepath.Join(dir, fmt.Sprintf("partition_%d.bin", i))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue // Partition file doesn't exist, skip it
		}

		partition, err := Load(path)
		if err != nil {
			return nil, fmt.Errorf("error loading partition %d: %w", i, err)
		}
		set.partitions[i] = partition
	}
	return set, nil
}

// SearchResult represents a single nearest neighbor with its distance and fraud status
type SearchResult struct {
	Distance int64 // Squared Euclidean distance
	Fraud    bool
}

// Search searches for the K nearest neighbors and returns the count of frauds (0-K)
// tag is the 4-bit value calculated for the query
func (s *Set) Search(queryVector [vectorSize]float32, tag uint8) uint8 {
	partition := s.findPartition(tag)
	if partition == nil || len(partition.entries) == 0 {
		return 0 // No partition found or empty, assume no fraud
	}

	// Quantize the query vector
	quantizedQuery := quantize(queryVector)

	// Use a min-heap to keep track of the K nearest neighbors
	// We'll use a slice and sort it as we add, which is simpler than a heap
	// for a small K (NumNearest)
	nearestNeighbors := make([]SearchResult, 0, NumNearest)

	for _, entry := range partition.entries {
		dist := squaredEuclideanDistance(quantizedQuery, entry.Vec)

		if len(nearestNeighbors) < NumNearest {
			nearestNeighbors = append(nearestNeighbors, SearchResult{Distance: dist, Fraud: entry.Fraud})
			// Keep sorted to easily identify the largest distance for replacement
			sort.Slice(nearestNeighbors, func(i, j int) bool {
				return nearestNeighbors[i].Distance < nearestNeighbors[j].Distance
			})
		} else if dist < nearestNeighbors[NumNearest-1].Distance {
			// Replace the farthest neighbor if current distance is smaller
			nearestNeighbors[NumNearest-1] = SearchResult{Distance: dist, Fraud: entry.Fraud}
			sort.Slice(nearestNeighbors, func(i, j int) bool {
				return nearestNeighbors[i].Distance < nearestNeighbors[j].Distance
			})
		}
	}

	fraudCount := uint8(0)
	for _, res := range nearestNeighbors {
		if res.Fraud {
			fraudCount++
		}
	}
	return fraudCount
}

// findPartition returns the partition for the given tag, with fallback logic
func (s *Set) findPartition(tag uint8) *Partition {
	if s.partitions[tag] != nil {
		return s.partitions[tag]
	}
	// Fallback logic as specified in the plan
	// If the exact partition doesn't exist, try less specific tags.
	// This order matters for the fallback strategy.
	if s.partitions[tag&^8] != nil { // Try without card_present bit
		return s.partitions[tag&^8]
	}
	if s.partitions[tag&^4] != nil { // Try without is_online bit
		return s.partitions[tag&^4]
	}
	// Default to partition 0 if no specific partition or fallbacks are found
	// This ensures we always have a partition, even if it's not the ideal one.
	return s.partitions[0]
}

// quantize converts a float32 vector to an int16 vector
func quantize(v [vectorSize]float32) [vectorSize]int16 {
	var q [vectorSize]int16
	for i, f := range v {
		if f == -1 {
			q[i] = -32767 // Sentinel for -1
		} else {
			// Clamp to [0, 1] then scale to [0, 32767] for int16
			clampedF := math.Min(1.0, math.Max(0.0, float64(f)))
			q[i] = int16(clampedF * 32767)
		}
	}
	return q
}

// squaredEuclideanDistance calculates the squared Euclidean distance between two int16 vectors
func squaredEuclideanDistance(v1, v2 [vectorSize]int16) int64 {
	var sum int64
	for i := 0; i < vectorSize; i++ {
		diff := int64(v1[i]) - int64(v2[i])
		sum += diff * diff
	}
	return sum
}
