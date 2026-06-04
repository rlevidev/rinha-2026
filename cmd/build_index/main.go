package main

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Reference struct {
	Vector [14]float32 `json:"vector"`
	Label  string      `json:"label"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: build_index <json.gz_path> <output_dir>")
		return
	}

	jsonGzPath := os.Args[1]
	outputDir := os.Args[2]

	file, err := os.Open(jsonGzPath)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		panic(err)
	}
	defer gzReader.Close()

	decoder := json.NewDecoder(gzReader)
	
	// Read opening bracket
	_, err = decoder.Token()
	if err != nil {
		panic(err)
	}

	partitions := make(map[uint8][]Reference)

	for decoder.More() {
		var ref Reference
		err := decoder.Decode(&ref)
		if err != nil {
			panic(err)
		}

		// Calculate tag
		// 0: card_present, 1: is_online, 2: unknown_merchant, 3: has_last_tx
		// Based on spec logic for index/vectorize, tag bits:
		// card_present (dim 10), is_online (dim 9), unknown_merchant (dim 11), has_last_tx (dim 5/6)
		
		// Note: The spec says: tag = (card_present_bit << 3) | (is_online_bit << 2) | (unknown_merchant_bit << 1) | has_last_tx_bit
		// Features (dim indices):
		// 9: is_online
		// 10: card_present
		// 11: unknown_merchant
		// 5/6: has_last_tx
		
		// For build_index, we need the features from the vector itself, but they are already normalized?
		// Wait, the vector *is* the normalized features.
		// dim 10: card_present
		// dim 9: is_online
		// dim 11: unknown_merchant
		// has_last_tx: In vectorize, we have a bool in Transaction.
		// If dim 5 is -1, then has_last_tx is false.
		
		cardPresentBit := uint8(0)
		if ref.Vector[10] > 0.5 { cardPresentBit = 1 }
		
		isOnlineBit := uint8(0)
		if ref.Vector[9] > 0.5 { isOnlineBit = 1 }
		
		unknownMerchantBit := uint8(0)
		if ref.Vector[11] > 0.5 { unknownMerchantBit = 1 }
		
		hasLastTxBit := uint8(0)
		if ref.Vector[5] > -0.5 { hasLastTxBit = 1 }
		
		tag := (cardPresentBit << 3) | (isOnlineBit << 2) | (unknownMerchantBit << 1) | hasLastTxBit
		partitions[tag] = append(partitions[tag], ref)
	}

	err = os.MkdirAll(outputDir, 0755)
	if err != nil {
		panic(err)
	}

	for tag, refs := range partitions {
		// Sort by norm (simplified)
		sort.Slice(refs, func(i, j int) bool {
			var normI, normJ float32
			for k := 0; k < 14; k++ {
				normI += refs[i].Vector[k] * refs[i].Vector[k]
				normJ += refs[j].Vector[k] * refs[j].Vector[k]
			}
			return normI < normJ
		})

		outputPath := filepath.Join(outputDir, fmt.Sprintf("partition_%d.bin", tag))
		f, err := os.Create(outputPath)
		if err != nil {
			panic(err)
		}

		for _, ref := range refs {
			// Quantize 14 float32 to 14 int16
			const scale = 32767
			var q [14]int16
			for i := 0; i < 14; i++ {
				val := ref.Vector[i]
				if val < 0 { val = 0 }
				if val > 1 { val = 1 }
				q[i] = int16(val * scale)
			}
			// Write 14 * int16
			for _, v := range q {
				err := binary.Write(f, binary.LittleEndian, v)
				if err != nil {
					panic(err)
				}
			}
			// Write 1 byte label
			var label byte = 0
			if ref.Label == "fraud" {
				label = 1
			}
			err = binary.Write(f, binary.LittleEndian, label)
			if err != nil {
				panic(err)
			}
		}
		f.Close()
		fmt.Printf("Partition %d: %d entries\n", tag, len(refs))
	}
}
