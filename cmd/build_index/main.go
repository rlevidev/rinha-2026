package main

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
)

type Reference struct {
	Vector [14]float32 `json:"vector"`
	Label  string      `json:"label"`
}

func kMeansPlusPlus(entries []Reference, K int) ([][14]int16, []uint8) {
	n := len(entries)
	assignments := make([]uint8, n)
	rng := rand.New(rand.NewSource(42))

	centers := make([][14]float32, K)
	centers[0] = entries[rng.Intn(n)].Vector

	minDist := make([]float64, n)
	for i := 0; i < n; i++ {
		var sum float64
		for d := 0; d < 14; d++ {
			diff := float64(entries[i].Vector[d] - centers[0][d])
			sum += diff * diff
		}
		minDist[i] = sum
	}

	for c := 1; c < K; c++ {
		totalDist := 0.0
		for i := 0; i < n; i++ {
			totalDist += minDist[i]
		}

		if totalDist == 0 {
			centers[c] = entries[rng.Intn(n)].Vector
			continue
		}

		threshold := rng.Float64() * totalDist
		cumulative := 0.0
		selected := 0
		for i := 0; i < n; i++ {
			cumulative += minDist[i]
			if cumulative >= threshold {
				selected = i
				break
			}
		}
		centers[c] = entries[selected].Vector

		for i := 0; i < n; i++ {
			var sum float64
			for d := 0; d < 14; d++ {
				diff := float64(entries[i].Vector[d] - centers[c][d])
				sum += diff * diff
			}
			if sum < minDist[i] {
				minDist[i] = sum
			}
		}
	}

	counts := make([]int, K)
	sums := make([][14]float64, K)

	for iter := 0; iter < 30; iter++ {
		changed := false

		for i := 0; i < n; i++ {
			best := 0
			var bestDist float64
			for d := 0; d < 14; d++ {
				diff := float64(entries[i].Vector[d] - centers[0][d])
				bestDist += diff * diff
			}
			for c := 1; c < K; c++ {
				var sum float64
				for d := 0; d < 14; d++ {
					diff := float64(entries[i].Vector[d] - centers[c][d])
					sum += diff * diff
				}
				if sum < bestDist {
					bestDist = sum
					best = c
				}
			}
			if assignments[i] != uint8(best) {
				changed = true
				assignments[i] = uint8(best)
			}
		}

		if !changed {
			break
		}

		for c := 0; c < K; c++ {
			counts[c] = 0
			for d := 0; d < 14; d++ {
				sums[c][d] = 0
			}
		}

		for i := 0; i < n; i++ {
			c := assignments[i]
			counts[c]++
			for d := 0; d < 14; d++ {
				sums[c][d] += float64(entries[i].Vector[d])
			}
		}

		for c := 0; c < K; c++ {
			if counts[c] > 0 {
				for d := 0; d < 14; d++ {
					centers[c][d] = float32(sums[c][d] / float64(counts[c]))
				}
			}
		}
	}

	const scale = 32767
	quantizedCenters := make([][14]int16, K)
	for c := 0; c < K; c++ {
		for d := 0; d < 14; d++ {
			val := centers[c][d]
			if val < 0 {
				val = 0
			}
			if val > 1 {
				val = 1
			}
			quantizedCenters[c][d] = int16(val * scale)
		}
	}

	return quantizedCenters, assignments
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
		if ref.Vector[10] > 0.5 {
			cardPresentBit = 1
		}

		isOnlineBit := uint8(0)
		if ref.Vector[9] > 0.5 {
			isOnlineBit = 1
		}

		unknownMerchantBit := uint8(0)
		if ref.Vector[11] > 0.5 {
			unknownMerchantBit = 1
		}

		hasLastTxBit := uint8(0)
		if ref.Vector[5] > -0.5 {
			hasLastTxBit = 1
		}

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

		const partitionThreshold = 50000
		const K = 64

		numClusters := uint64(0)
		var centers [][14]int16
		var assignments []uint8

		if len(refs) > partitionThreshold {
			centers, assignments = kMeansPlusPlus(refs, K)
			numClusters = K
		}

		// Write header: num_clusters (uint64 LE)
		err = binary.Write(f, binary.LittleEndian, numClusters)
		if err != nil {
			panic(err)
		}

		// Write centers
		for _, center := range centers {
			for _, v := range center {
				err = binary.Write(f, binary.LittleEndian, v)
				if err != nil {
					panic(err)
				}
			}
		}

		// Write entries
		const scale = 32767
		for i, ref := range refs {
			var q [14]int16
			for j := 0; j < 14; j++ {
				val := ref.Vector[j]
				if val < 0 {
					val = 0
				}
				if val > 1 {
					val = 1
				}
				q[j] = int16(val * scale)
			}
			for _, v := range q {
				err := binary.Write(f, binary.LittleEndian, v)
				if err != nil {
					panic(err)
				}
			}
			var label byte = 0
			if ref.Label == "fraud" {
				label = 1
			}
			err = binary.Write(f, binary.LittleEndian, label)
			if err != nil {
				panic(err)
			}
			if numClusters > 0 {
				err = binary.Write(f, binary.LittleEndian, assignments[i])
				if err != nil {
					panic(err)
				}
			}
		}
		f.Close()
		fmt.Printf("Partition %d: %d entries\n", tag, len(refs))
	}
}
