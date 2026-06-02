package index

func bruteForceSearch(ix *IvfIndex, q *[16]int16) uint8 {
	n := ix.NVectors
	var topK [5]int64
	maxKey := int64(0x7FFFFFFFFFFFFFFF)
	for i := range topK {
		topK[i] = maxKey
	}

	for v := 0; v < n; v++ {
		var sum int64
		for p := 0; p < NPairs; p++ {
			lo := int64(int16(ix.pairs[p][2*v]))
			hi := int64(int16(ix.pairs[p][2*v+1]))
			d0 := int64(q[2*p]) - lo
			d1 := int64(q[2*p+1]) - hi
			sum += d0*d0 + d1*d1
		}
		key := (sum << IdxBits) | int64(v)
		if key >= topK[4] {
			continue
		}
		for j := 0; j < 5; j++ {
			if key < topK[j] {
				copy(topK[j+1:], topK[j:4])
				topK[j] = key
				break
			}
		}
	}

	cnt := uint8(0)
	for _, k := range topK {
		if k == maxKey {
			continue
		}
		idx := int(k & IdxMask)
		cnt += ix.labels[idx]
	}
	return cnt
}
