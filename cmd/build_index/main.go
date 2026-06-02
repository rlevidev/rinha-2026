package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// ReferenceEntry represents a single entry in the references.json.gz file
type ReferenceEntry struct {
	Vector [14]float32 `json:"vector"`
	Label  string      `json:"label"`
}

// tag calculates the 4-bit tag for a given vector
func tag(v [14]float32) uint8 {
	t := uint8(0)
	if v[5] != -1 {
		t |= 1
	} // has_last_tx (bit 0)
	if v[11] > 0.5 {
		t |= 2
	} // unknown_merchant (bit 1)
	if v[9] > 0.5 {
		t |= 4
	} // is_online (bit 2)
	if v[10] > 0.5 {
		t |= 8
	} // card_present (bit 3)
	return t
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run main.go <references.json.gz_path> <output_dir>")
		os.Exit(1)
	}

	referencesPath := os.Args[1]
	outputDir := os.Args[2]

	err := os.MkdirAll(outputDir, 0755)
	if err != nil {
		fmt.Printf("Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Reading references from %s and writing partitions to %s\n", referencesPath, outputDir)

	file, err := os.Open(referencesPath)
	if err != nil {
		fmt.Printf("Error opening references file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		fmt.Printf("Error creating gzip reader: %v\n", err)
		os.Exit(1)
	}
	defer gzReader.Close()

	// Using a bufio.Reader for efficient reading
	reader := bufio.NewReader(gzReader)

	// To handle the large JSON array, we'll read byte by byte
	// until we find the start of the array '[', and then process objects.
	// A simpler approach for now, assuming the file is a single JSON array of objects,
	// is to use json.NewDecoder and read token by token.
	decoder := json.NewDecoder(reader)

	// Read the opening bracket of the JSON array
	_, err = decoder.Token()
	if err != nil {
		fmt.Printf("Error reading JSON start token: %v\n", err)
		os.Exit(1)
	}

	partitions := make(map[uint8][][]byte) // tag -> list of serialized entries
	stats := make(map[uint8]int)           // tag -> count

	totalEntries := 0

	for decoder.More() {
		var entry ReferenceEntry
		err := decoder.Decode(&entry)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Error decoding JSON entry: %v\n", err)
			os.Exit(1)
		}

		totalEntries++

		// Calculate tag
		t := tag(entry.Vector)

		// Quantize vector and determine label byte
		var quantizedVector [14]int16
		for i, f := range entry.Vector {
			if f == -1 {
				quantizedVector[i] = -32767 // Sentinel for -1
			} else {
				// Clamp to [0, 1] then scale to [0, 32767] for int16
				// The input floats from references.json.gz should already be within [0, 1]
				// or -1 for specific dimensions.
				clampedF := math.Min(1.0, math.Max(0.0, float64(f)))
				quantizedVector[i] = int16(clampedF * 32767)
			}
		}

		labelByte := uint8(0)
		if entry.Label == "fraud" {
			labelByte = 1
		}

		// Serialize to 29 bytes
		buf := make([]byte, 29)
		for i, val := range quantizedVector {
			// Convert int16 to uint16 for binary.LittleEndian.PutUint16.
			// This is safe as long as we read it back as int16 later.
			binary.LittleEndian.PutUint16(buf[i*2:i*2+2], uint16(val)) 
		}
		buf[28] = labelByte

		partitions[t] = append(partitions[t], buf)
		stats[t]++
	}

	// Read the closing bracket of the JSON array
	_, err = decoder.Token()
	if err != nil && err != io.EOF {
		fmt.Printf("Error reading JSON end token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Processed %d total entries.\n", totalEntries)
	fmt.Println("Writing partitioned index files...")

	for t, serializedEntries := range partitions {
		outputPath := filepath.Join(outputDir, fmt.Sprintf("partition_%d.bin", t))
		outFile, err := os.Create(outputPath)
		if err != nil {
			fmt.Printf("Error creating partition file %s: %v\n", outputPath, err)
			os.Exit(1)
		}
		defer outFile.Close()

		writer := bufio.NewWriter(outFile)
		for _, entryBytes := range serializedEntries {
			_, err := writer.Write(entryBytes)
			if err != nil {
				fmt.Printf("Error writing to partition file %s: %v\n", outputPath, err)
				os.Exit(1)
			}
		}
		writer.Flush()
		fmt.Printf("Wrote partition_%d.bin with %d entries.\n", t, stats[t])
	}

	fmt.Println("Done.")
}
