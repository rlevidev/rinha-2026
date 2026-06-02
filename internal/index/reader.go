package index

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

type IvfIndex struct {
	data      []byte
	NClusters int
	NVectors  int

	clusterOffsets   []uint32
	bboxMin, bboxMax []int16

	pairs [NPairs][]int16

	labels []uint8

	bpsoaMin, bpsoaMax []int16
}

type Set struct {
	parts [NPartitions]*IvfIndex
}

func align64(x int) int { return (x + 63) &^ 63 }

func viewAt[T any](data []byte, off, n int) []T {
	return unsafe.Slice((*T)(unsafe.Pointer(&data[off])), n)
}

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

func (s *Set) ForTag(tag int) *IvfIndex {
	fallbacks := [7]int{tag, tag &^ 8, tag &^ 4, tag &^ 12, 0}
	for _, c := range fallbacks {
		if c >= 0 && c < len(s.parts) && s.parts[c] != nil {
			return s.parts[c]
		}
	}
	return nil
}

func Open(path string) (*IvfIndex, error) {
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
	data, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_PRIVATE|unix.MAP_POPULATE)
	if err != nil {
		return nil, err
	}
	unix.Mlock(data)
	unix.Madvise(data, unix.MADV_HUGEPAGE)
	unix.Madvise(data, unix.MADV_WILLNEED)

	if size < 64 || string(data[0:8]) != magic {
		return nil, fmt.Errorf("index: bad magic in %s", path)
	}
	nc := int(binary.LittleEndian.Uint32(data[8:12]))
	if nc < 1 || nc > NClusters {
		return nil, fmt.Errorf("index: n_clusters=%d, want 1..%d", nc, NClusters)
	}
	nv := int(binary.LittleEndian.Uint32(data[12:16]))

	ix := &IvfIndex{data: data, NClusters: nc, NVectors: nv}

	off := 64
	ix.clusterOffsets = viewAt[uint32](data, off, nc+1)
	off = align64(off + (nc+1)*4)
	ix.bboxMin = viewAt[int16](data, off, nc*16)
	off = align64(off + nc*32)
	ix.bboxMax = viewAt[int16](data, off, nc*16)
	off = align64(off + nc*32)
	for p := 0; p < NPairs; p++ {
		ix.pairs[p] = viewAt[int16](data, off, 2*nv+16)
		off = align64(off + nv*4)
	}
	ix.labels = viewAt[uint8](data, off, nv)
	off += nv
	if off > size {
		return nil, fmt.Errorf("index: sections overrun file (%d > %d)", off, size)
	}

	ix.buildBPSOA()
	return ix, nil
}

func (ix *IvfIndex) Close() error {
	if ix.data != nil {
		err := unix.Munmap(ix.data)
		ix.data = nil
		return err
	}
	return nil
}

func (ix *IvfIndex) buildBPSOA() {
	K := ix.NClusters
	nGroups := (K + 7) / 8
	ix.bpsoaMin = make([]int16, nGroups*7*16)
	ix.bpsoaMax = make([]int16, nGroups*7*16)
	for g := 0; g < nGroups; g++ {
		for p := 0; p < 7; p++ {
			dst := (g*7 + p) * 16
			for l := 0; l < 8; l++ {
				c := g*8 + l
				di := dst + l*2
				if c < K {
					ix.bpsoaMin[di] = ix.bboxMin[c*16+2*p]
					ix.bpsoaMin[di+1] = ix.bboxMin[c*16+2*p+1]
					ix.bpsoaMax[di] = ix.bboxMax[c*16+2*p]
					ix.bpsoaMax[di+1] = ix.bboxMax[c*16+2*p+1]
				} else {
					ix.bpsoaMin[di], ix.bpsoaMin[di+1] = 0x7FFF, 0x7FFF
					ix.bpsoaMax[di], ix.bpsoaMax[di+1] = -0x8000, -0x8000
				}
			}
		}
	}
}
