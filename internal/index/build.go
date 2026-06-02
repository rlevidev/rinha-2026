package index

import (
	"math"
	"runtime"
	"sort"
	"sync"

	"simd/archsimd"
)

type Ref struct {
	V     [16]float32
	Label uint8
}

func (r *Ref) Tag() int {
	tag := 0
	if r.V[5] >= 0 {
		tag |= 1
	}
	if r.V[11] > 0.5 {
		tag |= 2
	}
	if r.V[9] > 0.5 {
		tag |= 4
	}
	if r.V[10] > 0.5 {
		tag |= 8
	}
	return tag
}

func FilterByTag(refs []Ref, tag int) []Ref {
	dst := 0
	for src := range refs {
		if refs[src].Tag() == tag {
			if src != dst {
				refs[dst] = refs[src]
			}
			dst++
		}
	}
	return refs[:dst]
}

func quantize(v *[16]float32) [16]int16 {
	var q [16]int16
	for d := 0; d < NDims; d++ {
		x := math.RoundToEven(float64(v[d]) * Scale)
		if x > math.MaxInt16 {
			x = math.MaxInt16
		} else if x < math.MinInt16 {
			x = math.MinInt16
		}
		q[d] = int16(x)
	}
	return q
}

func l2sqF32(a, b *[16]float32) float32 {
	a0 := archsimd.LoadFloat32x8Slice(a[0:8])
	a1 := archsimd.LoadFloat32x8Slice(a[8:16])
	b0 := archsimd.LoadFloat32x8Slice(b[0:8])
	b1 := archsimd.LoadFloat32x8Slice(b[8:16])
	d0 := a0.Sub(b0)
	d1 := a1.Sub(b1)
	sq := d1.Mul(d1).Add(d0.Mul(d0))
	var out [8]float32
	sq.StoreSlice(out[:])
	return out[0] + out[1] + out[2] + out[3] + out[4] + out[5] + out[6] + out[7]
}

type Centroids [][16]float32

func KMeans(refs []Ref, k, iters int) (cent Centroids, assignments []int32) {
	n := len(refs)
	if k > n {
		k = n
	}
	if k < 1 {
		k = 1
	}
	cent = make(Centroids, k)
	assignments = make([]int32, n)

	step := n / k
	if step < 1 {
		step = 1
	}
	for c := 0; c < k; c++ {
		src := c * step
		if src >= n {
			src = n - 1
		}
		copy(cent[c][0:NDims], refs[src].V[0:NDims])
	}

	workers := runtime.NumCPU()
	sums := make([][NDims]float64, k)
	counts := make([]uint64, k)
	for it := 0; it < iters; it++ {
		var wg sync.WaitGroup
		chunk := (n + workers - 1) / workers
		for w := 0; w < workers; w++ {
			lo := w * chunk
			hi := lo + chunk
			if hi > n {
				hi = n
			}
			if lo >= hi {
				break
			}
			wg.Add(1)
			go func(lo, hi int) {
				defer wg.Done()
				for i := lo; i < hi; i++ {
					best := l2sqF32(&refs[i].V, &cent[0])
					bestC := int32(0)
					for c := 1; c < k; c++ {
						if d := l2sqF32(&refs[i].V, &cent[c]); d < best {
							best = d
							bestC = int32(c)
						}
					}
					assignments[i] = bestC
				}
			}(lo, hi)
		}
		wg.Wait()

		for c := range sums {
			sums[c] = [NDims]float64{}
			counts[c] = 0
		}
		for i := 0; i < n; i++ {
			c := assignments[i]
			counts[c]++
			row := &sums[c]
			v := &refs[i].V
			for d := 0; d < NDims; d++ {
				row[d] += float64(v[d])
			}
		}
		for c := 0; c < k; c++ {
			if counts[c] == 0 {
				continue
			}
			inv := 1.0 / float64(counts[c])
			for d := 0; d < NDims; d++ {
				cent[c][d] = float32(sums[c][d] * inv)
			}
		}
	}
	return cent, assignments
}

func SortWithinClusters(refs []Ref, cent Centroids, assignments []int32, offsets, order []uint32) {
	k := len(cent)
	for c := 0; c < k; c++ {
		lo, hi := offsets[c], offsets[c+1]
		if hi-lo < 2 {
			continue
		}
		seg := order[lo:hi]
		ctr := &cent[c]
		sort.Slice(seg, func(i, j int) bool {
			return l2sqF32(&refs[seg[i]].V, ctr) < l2sqF32(&refs[seg[j]].V, ctr)
		})
	}
}

func CountingSortByCluster(assignments []int32, k int) (offsets []uint32, order []uint32) {
	n := len(assignments)
	offsets = make([]uint32, k+1)
	order = make([]uint32, n)
	for _, c := range assignments {
		offsets[c+1]++
	}
	for c := 0; c < k; c++ {
		offsets[c+1] += offsets[c]
	}
	cursor := make([]uint32, k)
	copy(cursor, offsets[:k])
	for i, c := range assignments {
		order[cursor[c]] = uint32(i)
		cursor[c]++
	}
	return offsets, order
}

func BBoxPack(refs []Ref, order, offsets []uint32, k int) (bboxMin, bboxMax []int16, pairArr [NPairs][]int32, labels []uint8) {
	n := len(order)
	bboxMin = make([]int16, k*16)
	bboxMax = make([]int16, k*16)
	for c := 0; c < k; c++ {
		for lane := 0; lane < NDims; lane++ {
			bboxMin[c*16+lane] = math.MaxInt16
			bboxMax[c*16+lane] = math.MinInt16
		}
	}
	for p := 0; p < NPairs; p++ {
		pairArr[p] = make([]int32, n)
	}
	labels = make([]uint8, n)

	cid := 0
	for pos := 0; pos < n; pos++ {
		for offsets[cid+1] <= uint32(pos) {
			cid++
		}
		ref := &refs[order[pos]]
		labels[pos] = ref.Label
		qv := quantize(&ref.V)

		base := cid * 16
		for lane := 0; lane < 16; lane++ {
			if qv[lane] < bboxMin[base+lane] {
				bboxMin[base+lane] = qv[lane]
			}
			if qv[lane] > bboxMax[base+lane] {
				bboxMax[base+lane] = qv[lane]
			}
		}
		for p := 0; p < NPairs; p++ {
			lo := uint32(uint16(qv[2*p]))
			hi := uint32(uint16(qv[2*p+1]))
			pairArr[p][pos] = int32(lo | hi<<16)
		}
	}
	return bboxMin, bboxMax, pairArr, labels
}
