package index

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	NDims              = 14
	NPairs             = 7
	NClusters          = 2048
	IdxBits            = 22
	CidBits            = 12
	CidMask            = 0xFFF
	initialProbeBudget = 12
	repairProbeDivisor = 8
	magic              = "RNH4-IDX"
)

type Index struct {
	data []byte

	clusters int
	vectors  int

	clusterOffsets []uint32
	bboxMin        []int16
	bboxMax        []int16
	pairs          [NPairs][]int16
	labels         []uint8
}

// Set holds the partitioned indices for the whole model.
type Set struct {
	parts [16]*Index
}

func align64(v int) int { return (v + 63) &^ 63 }

func view[T any](data []byte, off, n int) []T {
	return unsafe.Slice((*T)(unsafe.Pointer(&data[off])), n)
}

// LoadSet opens the available partition files from dir.
func LoadSet(dir string) (*Set, error) {
	var set Set
	loaded := 0
	for tag := 0; tag < len(set.parts); tag++ {
		path := filepath.Join(dir, fmt.Sprintf("index_p%d.bin", tag))
		ix, err := Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		set.parts[tag] = ix
		loaded++
	}
	if loaded == 0 {
		return nil, fmt.Errorf("index: no partition files found in %s", dir)
	}
	return &set, nil
}

// ForTag returns the best available partition for tag.
func (s *Set) ForTag(tag int) *Index {
	fallbacks := [6]int{tag}
	n := 1
	for _, bit := range [4]int{8, 4, 2, 1} {
		candidate := tag & bit
		if candidate != 0 {
			fallbacks[n] = candidate
			n++
		}
	}
	fallbacks[n] = 0
	for _, candidate := range fallbacks[:n+1] {
		if candidate >= 0 && candidate < len(s.parts) && s.parts[candidate] != nil {
			return s.parts[candidate]
		}
	}
	return nil
}

// Open maps and validates one partition file.
func Open(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := int(st.Size())
	data, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	_ = syscall.Madvise(data, syscall.MADV_WILLNEED)
	_ = syscall.Madvise(data, syscall.MADV_SEQUENTIAL)

	if size < 64 || string(data[:8]) != magic {
		_ = syscall.Munmap(data)
		return nil, fmt.Errorf("index: bad magic in %s", path)
	}

	nc := int(binary.LittleEndian.Uint32(data[8:12]))
	nv := int(binary.LittleEndian.Uint32(data[12:16]))
	ix := &Index{data: data, clusters: nc, vectors: nv}

	off := 64
	ix.clusterOffsets = view[uint32](data, off, nc+1)
	off = align64(off + (nc+1)*4)
	ix.bboxMin = view[int16](data, off, nc*16)
	off = align64(off + nc*32)
	ix.bboxMax = view[int16](data, off, nc*16)
	off = align64(off + nc*32)
	for p := 0; p < NPairs; p++ {
		ix.pairs[p] = view[int16](data, off, 2*nv+16)
		off = align64(off + nv*4)
	}
	ix.labels = view[uint8](data, off, nv)
	off += nv + 64
	if off > size {
		_ = syscall.Munmap(data)
		return nil, fmt.Errorf("index: truncated file %s", path)
	}
	return ix, nil
}

// Close releases the mmapped partition.
func (ix *Index) Close() error {
	if ix == nil || ix.data == nil {
		return nil
	}
	data := ix.data
	ix.data = nil
	return syscall.Munmap(data)
}

type top5 struct {
	key   [5]int64
	label [5]uint8
}

func (t *top5) worst() int64 {
	w := t.key[0]
	for i := 1; i < len(t.key); i++ {
		if t.key[i] > w {
			w = t.key[i]
		}
	}
	return w
}

func (t *top5) add(key int64, label uint8) {
	worstIdx := 0
	worst := t.key[0]
	for i := 1; i < len(t.key); i++ {
		if t.key[i] > worst {
			worst = t.key[i]
			worstIdx = i
		}
	}
	if key >= worst {
		return
	}
	t.key[worstIdx] = key
	t.label[worstIdx] = label
}

func clusterLowerBound(bmin, bmax []int16, c int, q *[16]int16) int64 {
	base := c * 16
	var sum int64
	for d := 0; d < NDims; d++ {
		v := int64(q[d])
		lo := int64(bmin[base+d])
		hi := int64(bmax[base+d])
		if v < lo {
			diff := lo - v
			sum += diff * diff
		} else if v > hi {
			diff := v - hi
			sum += diff * diff
		}
	}
	return sum
}

func (ix *Index) scanCluster(c int, q *[16]int16, top *top5, worstKey *int64) {
	start := int(ix.clusterOffsets[c])
	end := int(ix.clusterOffsets[c+1])
	base := start * 2
	worstDist := *worstKey >> IdxBits
	order := [NPairs]int{3, 5, 0, 1, 2, 4, 6}

	for i := start; i < end; i++ {
		off := base + (i-start)*2
		dist := int64(0)
		for _, p := range order {
			d0 := int64(ix.pairs[p][off]) - int64(q[p*2])
			d1 := int64(ix.pairs[p][off+1]) - int64(q[p*2+1])
			dist += d0*d0 + d1*d1
			if dist >= worstDist {
				break
			}
		}
		if dist >= worstDist {
			continue
		}
		key := (dist << IdxBits) | int64(i)
		top.add(key, ix.labels[i])
		worst := top.worst()
		*worstKey = worst
		worstDist = worst >> IdxBits
	}
}

func (ix *Index) Search(q *[16]int16) uint8 {
	var packed [NClusters]int64
	var top top5
	maxKey := int64(^uint64(0) >> 1)
	for i := range top.key {
		top.key[i] = maxKey
	}
	worstKey := maxKey

	for c := 0; c < ix.clusters; c++ {
		lb := clusterLowerBound(ix.bboxMin, ix.bboxMax, c, q)
		packed[c] = (lb << IdxBits) | int64(c)
	}

	probeClusters := func(limit int) {
		for probes := 0; probes < limit; probes++ {
			bestKey := maxKey
			bestCluster := -1
			for c := 0; c < ix.clusters; c++ {
				if packed[c] < bestKey {
					bestKey = packed[c]
					bestCluster = c
				}
			}
			if bestCluster < 0 || bestKey == maxKey {
				return
			}
			bestLower := bestKey >> IdxBits
			if bestLower >= (worstKey >> IdxBits) {
				return
			}
			packed[bestCluster] = maxKey
			ix.scanCluster(bestCluster, q, &top, &worstKey)
		}
	}

	// Phase 1: short probe budget that quickly prunes obvious misses.
	probeClusters(initialProbeBudget)

	fraudCount := uint8(0)
	for _, label := range top.label {
		fraudCount += label
	}
	if fraudCount > 0 && fraudCount < 5 {
		// Phase 2: only expand with partition size, never to a full sweep.
		repairBudget := ix.clusters / repairProbeDivisor
		if repairBudget < 1 {
			repairBudget = 1
		}
		probeClusters(repairBudget)
		fraudCount = 0
		for _, label := range top.label {
			fraudCount += label
		}
	}

	return fraudCount
}
