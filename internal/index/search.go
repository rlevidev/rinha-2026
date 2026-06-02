package index

import "simd/archsimd"

func HasAVX2() bool { return archsimd.X86.AVX2() }

func (ix *IvfIndex) computeClusterPacked(qp *[NPairs]archsimd.Int16x16, out []int64) {
	nGroups := (ix.NClusters + 7) / 8
	var zero16 [16]int16
	z := archsimd.LoadInt16x16(&zero16)
	var sq [8]int32

	for g := 0; g < nGroups; g++ {
		base := g * NPairs * 16
		bmin := archsimd.LoadInt16x16Slice(ix.bpsoaMin[base:])
		bmax := archsimd.LoadInt16x16Slice(ix.bpsoaMax[base:])
		below := bmin.Sub(qp[0]).Max(z)
		above := qp[0].Sub(bmax).Max(z)
		gap := below.Max(above)
		acc := gap.DotProductPairs(gap)

		for p := 1; p < NPairs; p++ {
			o := base + p*16
			bmn := archsimd.LoadInt16x16Slice(ix.bpsoaMin[o:])
			bmx := archsimd.LoadInt16x16Slice(ix.bpsoaMax[o:])
			bl := bmn.Sub(qp[p]).Max(z)
			ab := qp[p].Sub(bmx).Max(z)
			gp := bl.Max(ab)
			acc = acc.Add(gp.DotProductPairs(gp))
		}
		acc.StoreSlice(sq[:])

		for l := 0; l < 8; l++ {
			c := g*8 + l
			if c >= ix.NClusters {
				break
			}
			lb := int64(sq[l])
			out[c] = (lb << CidBits) | int64(c)
		}
	}
}

func (ix *IvfIndex) pairSq(p, off int, qp *[NPairs]archsimd.Int16x16) archsimd.Int32x8 {
	d := archsimd.LoadInt16x16Slice(ix.pairs[p][off:]).Sub(qp[p])
	return d.DotProductPairs(d)
}

func (ix *IvfIndex) scanCluster(bestC int, qp *[NPairs]archsimd.Int16x16, topkK *[5]int64, topkL *[5]uint8, worstKey *int64) {
	start := int(ix.clusterOffsets[bestC])
	end := int(ix.clusterOffsets[bestC+1])
	count := end - start
	wk := *worstKey
	var dists [8]int32

	for i := 0; i < count; i += 8 {
		base := start + i
		off := 2 * base

		wd := wk >> IdxBits
		gate := wd <= 0x7FFFFFFF
		var thresh archsimd.Int32x8
		if gate {
			thresh = archsimd.BroadcastInt32x8(int32(wd))
		}

		accA := ix.pairSq(3, off, qp)
		accB := ix.pairSq(5, off, qp)
		if gate && accA.Add(accB).Less(thresh).ToBits() == 0 {
			continue
		}

		accA = accA.Add(ix.pairSq(0, off, qp))
		accB = accB.Add(ix.pairSq(1, off, qp))
		if gate && accA.Add(accB).Less(thresh).ToBits() == 0 {
			continue
		}

		accA = accA.Add(ix.pairSq(2, off, qp)).Add(ix.pairSq(6, off, qp))
		accB = accB.Add(ix.pairSq(4, off, qp))
		accA.Add(accB).StoreSlice(dists[:])

		valid := count - i
		if valid > 8 {
			valid = 8
		}
		for j := 0; j < valid; j++ {
			key := (int64(uint32(dists[j])) << IdxBits) | int64(base+j)
			if key >= wk {
				continue
			}
			wi, mx := 0, topkK[0]
			for t := 1; t < 5; t++ {
				if topkK[t] > mx {
					mx, wi = topkK[t], t
				}
			}
			topkK[wi] = key
			topkL[wi] = ix.labels[base+j]
			wk = topkK[0]
			for t := 1; t < 5; t++ {
				if topkK[t] > wk {
					wk = topkK[t]
				}
			}
		}
	}
	*worstKey = wk
}

func (ix *IvfIndex) searchCore(q *[16]int16, maxProbes int, topkK *[5]int64, topkL *[5]uint8) {
	const maxI64 = int64(0x7FFFFFFFFFFFFFFF)

	var qpairs [NPairs][16]int16
	for p := 0; p < NPairs; p++ {
		lo, hi := q[2*p], q[2*p+1]
		for l := 0; l < 8; l++ {
			qpairs[p][2*l] = lo
			qpairs[p][2*l+1] = hi
		}
	}
	var qp [NPairs]archsimd.Int16x16
	for p := 0; p < NPairs; p++ {
		qp[p] = archsimd.LoadInt16x16(&qpairs[p])
	}

	var packed [NClusters]int64
	ix.computeClusterPacked(&qp, packed[:])

	for i := 0; i < 5; i++ {
		topkK[i] = maxI64
	}
	*topkL = [5]uint8{}
	worstKey := maxI64

	for probe := 0; probe < maxProbes; probe++ {
		best := maxI64
		for c := 0; c < ix.NClusters; c++ {
			if packed[c] < best {
				best = packed[c]
			}
		}
		if best == maxI64 {
			break
		}
		bestLb := best >> CidBits
		if (bestLb << IdxBits) >= worstKey {
			break
		}
		bestC := int(best & CidMask)
		packed[bestC] = maxI64
		ix.scanCluster(bestC, &qp, topkK, topkL, &worstKey)
	}
}

func (ix *IvfIndex) Search(q *[16]int16) uint8 {
	var topkK [5]int64
	var topkL [5]uint8
	ix.searchCore(q, NProbeInitial, &topkK, &topkL)
	cnt := uint8(0)
	for _, l := range topkL {
		cnt += l
	}
	return cnt
}
