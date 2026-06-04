package index

import (
	"container/heap"
	"encoding/binary"
	"fmt"
	"os"
)

type Set struct {
	partitions map[uint8]*Partition
}

type Partition struct {
	data   []int16
	labels []uint8
	size   int
}

type Result struct {
	id   int64
	dist int64
}

type MaxHeap []Result

func (h MaxHeap) Len() int           { return len(h) }
func (h MaxHeap) Less(i, j int) bool { return h[i].dist > h[j].dist }
func (h MaxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *MaxHeap) Push(x any)        { *h = append(*h, x.(Result)) }
func (h *MaxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func LoadSet(dir string) (*Set, error) {
	s := &Set{partitions: make(map[uint8]*Partition)}
	for i := 0; i < 16; i++ {
		fName := fmt.Sprintf("%s/partition_%d.bin", dir, i)
		f, err := os.Open(fName)
		if err != nil {
			continue
		}
		
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		
		size := int(info.Size() / 29)
		data := make([]int16, size*14)
		labels := make([]uint8, size)
		
		for j := 0; j < size; j++ {
			for k := 0; k < 14; k++ {
				binary.Read(f, binary.LittleEndian, &data[j*14+k])
			}
			binary.Read(f, binary.LittleEndian, &labels[j])
		}
		f.Close()
		
		s.partitions[uint8(i)] = &Partition{data: data, labels: labels, size: size}
	}
	return s, nil
}

func NewSet(dir string) (*Set, error) {
	return LoadSet(dir)
}


func (s *Set) Search(query [14]float32, tag uint8) uint8 {
	p := s.findPartition(tag)
	if p == nil {
		return 0
	}
	
	// Quantize
	q := make([]int16, 14)
	for i, v := range query {
		val := v * 32767
		if val > 32767 { val = 32767 } else if val < -32768 { val = -32768 }
		q[i] = int16(val)
	}

	// KNN
	h := &MaxHeap{}
	heap.Init(h)
	for j := 0; j < p.size; j++ {
		var dist int64
		for k := 0; k < 14; k++ {
			diff := int64(q[k]) - int64(p.data[j*14+k])
			dist += diff * diff
		}
		
		if h.Len() < 5 {
			heap.Push(h, Result{id: int64(j), dist: dist})
		} else if dist < (*h)[0].dist {
			heap.Pop(h)
			heap.Push(h, Result{id: int64(j), dist: dist})
		}
	}
	
	var fraudCount uint8
	for h.Len() > 0 {
		idx := heap.Pop(h).(Result).id
		if p.labels[idx] == 1 {
			fraudCount++
		}
	}
	return fraudCount
}


func (s *Set) findPartition(tag uint8) *Partition {
	if p, ok := s.partitions[tag]; ok {
		return p
	}
	// Fallback logic
	for i := 0; i < 16; i++ {
		if p, ok := s.partitions[uint8(i)]; ok {
			return p
		}
	}
	return nil
}
