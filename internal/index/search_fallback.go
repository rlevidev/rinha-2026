//go:build !goexperiment.simd

package index

// HasAVX2 reports whether the CPU supports the AVX2 instructions the search
// kernels rely on.
func HasAVX2() bool { return false }

// Search returns the fraud count (0..5) among the query's 5 nearest neighbours.
// This is the fallback implementation when GOEXPERIMENT=simd is disabled.
func (ix *IvfIndex) Search(q *[16]int16) uint8 {
	const maxI64 = int64(0x7FFFFFFFFFFFFFFF)

	// Naive baseline linear scan through all clusters and vectors.
	// Since search_fallback is only used in tests/dev environments that run without
	// GOEXPERIMENT=simd, this is perfectly fine for correctness.
	var topkK [5]int64
	var topkL [5]uint8
	for i := 0; i < 5; i++ {
		topkK[i] = maxI64
	}
	worstKey := maxI64

	// Total vectors count:
	n := len(ix.labels)
	for idx := 0; idx < n; idx++ {
		// Calculate exact L2 distance
		var dist int64
		for p := 0; p < NPairs; p++ {
			// Extract qv[2p] and qv[2p+1]
			qLo := int64(q[2*p])
			qHi := int64(q[2*p+1])

			// Extract v[2p] and v[2p+1] from packed pairs safely using uint16 casts
			val := ix.pairs[p][idx]
			vLo := int64(int16(uint16(val)))
			vHi := int64(int16(uint16(val >> 16)))

			dLo := qLo - vLo
			dHi := qHi - vHi
			dist += dLo*dLo + dHi*dHi
		}

		key := (dist << IdxBits) | int64(idx)
		if key >= worstKey {
			continue
		}

		// Replace worst
		wi, mx := 0, topkK[0]
		for t := 1; t < 5; t++ {
			if topkK[t] > mx {
				mx, wi = topkK[t], t
			}
		}
		topkK[wi] = key
		topkL[wi] = ix.labels[idx]

		// Update worstKey
		worstKey = topkK[0]
		for t := 1; t < 5; t++ {
			if topkK[t] > worstKey {
				worstKey = topkK[t]
			}
		}
	}

	cnt := uint8(0)
	for _, l := range topkL {
		cnt += l
	}
	return cnt
}
