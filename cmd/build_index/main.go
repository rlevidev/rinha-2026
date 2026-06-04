package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
)

type Record struct {
	Vector []float32 `json:"vector"`
	Label  string    `json:"label"`
}

func main() {
	f, err := os.Open("resources/references.json.gz")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		panic(err)
	}
	defer gz.Close()

	decoder := json.NewDecoder(gz)

	// Consume opening bracket '['
	_, err = decoder.Token()
	if err != nil {
		panic(err)
	}

	stats := make(map[uint8]int)
	writers := make(map[uint8]*bufio.Writer)
	files := make(map[uint8]*os.File)

	defer func() {
		for _, w := range writers {
			w.Flush()
		}
		for _, f := range files {
			f.Close()
		}
	}()

	for decoder.More() {
		var r Record
		err := decoder.Decode(&r)
		if err != nil {
			panic(err)
		}

		// Compute bits
		cardPresent := uint8(r.Vector[10])
		isOnline := uint8(r.Vector[9])
		unknownMerchant := uint8(r.Vector[11])
		hasLastTx := uint8(0)
		if r.Vector[5] != -1.0 {
			hasLastTx = 1
		}

		tag := (cardPresent << 3) | (isOnline << 2) | (unknownMerchant << 1) | hasLastTx

		// Scale
		scaled := make([]int16, 14)
		for i, v := range r.Vector {
			val := v * 32767
			if val > 32767 {
				val = 32767
			} else if val < -32768 {
				val = -32768
			}
			scaled[i] = int16(val)
		}

		// Label
		var label uint8
		if r.Label == "fraud" {
			label = 1
		} else {
			label = 0
		}

		// Get or create writer
		w, ok := writers[tag]
		if !ok {
			fName := fmt.Sprintf("index/partition_%d.bin", tag)
			file, err := os.Create(fName)
			if err != nil {
				panic(err)
			}
			files[tag] = file
			w = bufio.NewWriter(file)
			writers[tag] = w
		}

		// Write: 14 * int16 + 1 * uint8 = 29 bytes
		for _, v := range scaled {
			err = binary.Write(w, binary.LittleEndian, v)
			if err != nil {
				panic(err)
			}
		}
		err = binary.Write(w, binary.LittleEndian, label)
		if err != nil {
			panic(err)
		}

		stats[tag]++
	}

	// Stats
	fmt.Println("Stats:")
	for tag, count := range stats {
		fmt.Printf("Partition %d: %d entries\n", tag, count)
	}
}
