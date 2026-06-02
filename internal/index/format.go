package index

import (
	"bufio"
	"encoding/binary"
	"os"
	"unsafe"
)

const (
	NDims       = 14
	NPairs      = 7
	NClusters   = 2048
	KNeighbors  = 5
	Scale       = 10000
	NPartitions = 16

	IdxBits       = 22
	CidBits       = 12
	CidMask       = 0xFFF
	NProbeInitial = 128
	IdxMask       = (1 << IdxBits) - 1

	magic = "RNH4-IDX"
)

func bytesOf[T any](s []T) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*int(unsafe.Sizeof(s[0])))
}

func writePadded(w *bufio.Writer, b []byte) error {
	if _, err := w.Write(b); err != nil {
		return err
	}
	if pad := (64 - len(b)%64) % 64; pad > 0 {
		var z [64]byte
		if _, err := w.Write(z[:pad]); err != nil {
			return err
		}
	}
	return nil
}

func WriteIndexBin(
	path string, n int,
	clusterOffsets []uint32,
	bboxMin, bboxMax []int16,
	pairArr [NPairs][]int32,
	labels []uint8,
) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)

	var hdr [64]byte
	copy(hdr[0:8], magic)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(clusterOffsets)-1))
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(n))
	if err := writePadded(w, hdr[:]); err != nil {
		return err
	}
	if err := writePadded(w, bytesOf(clusterOffsets)); err != nil {
		return err
	}
	if err := writePadded(w, bytesOf(bboxMin)); err != nil {
		return err
	}
	if err := writePadded(w, bytesOf(bboxMax)); err != nil {
		return err
	}
	for p := 0; p < NPairs; p++ {
		if err := writePadded(w, bytesOf(pairArr[p])); err != nil {
			return err
		}
	}
	if _, err := w.Write(labels); err != nil {
		return err
	}
	var tail [64]byte
	if _, err := w.Write(tail[:]); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}
