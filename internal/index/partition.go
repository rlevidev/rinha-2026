package index

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

const (
	vectorSize     = 14
	entrySizeBytes = vectorSize*2 + 1 // 14 int16 (2 bytes each) + 1 byte for label
	NumNearest     = 5                // K for KNN (exported for use by other packages)
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

// Search searches for the K nearest neighbors and returns the count of frauds (0-K)
// tag is the 4-bit value calculated for the query
func (s *Set) Search(queryVector [vectorSize]float32, tag uint8) uint8 {
	partition := s.findPartition(tag)
	if partition == nil || len(partition.entries) == 0 {
		return 0 // No partition found or empty, assume no fraud
	}

	// Quantize the query vector
	quantizedQuery := quantize(queryVector)

	// Keep track of the K nearest neighbors using fixed-size arrays
	// This avoids heap allocations and reflection (sort.Slice) in the hot path.
	var (
		maxDistances [NumNearest]int64
		isFraud      [NumNearest]bool
		count        int
	)

	// Initialize distances to max
	for i := 0; i < NumNearest; i++ {
		maxDistances[i] = math.MaxInt64
	}

	for i := range partition.entries {
		entry := &partition.entries[i]
		dist := squaredEuclideanDistance(quantizedQuery, entry.Vec)

		// If this entry is closer than the farthest of our current top-K
		if dist < maxDistances[NumNearest-1] {
			// Replace the farthest neighbor
			maxDistances[NumNearest-1] = dist
			isFraud[NumNearest-1] = entry.Fraud

			// Re-sort the small array (Insertion Sort)
			for j := NumNearest - 1; j > 0 && maxDistances[j] < maxDistances[j-1]; j-- {
				maxDistances[j], maxDistances[j-1] = maxDistances[j-1], maxDistances[j]
				isFraud[j], isFraud[j-1] = isFraud[j-1], isFraud[j]
			}
			if count < NumNearest {
				count++
			}
		}
	}

	fraudCount := uint8(0)
	// Only count neighbors that were actually found (in case partition < K)
	for i := 0; i < count; i++ {
		if isFraud[i] {
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
