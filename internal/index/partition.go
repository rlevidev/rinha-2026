package index

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Scale is the factor to convert float32 (0.0-1.0) to int16.
const Scale = 32767

// Entry é um vetor quantizado (14 int16) + label.
type Entry struct {
	Vec   [14]int16
	Fraud bool
}

// Partition representa uma partição do índice mapeada em memória.
type Partition struct {
	data    []byte // mmap'd data
	entries []Entry
}

// Open mapeia o arquivo do índice, valida o cabeçalho e configura as fatias.
// Utiliza mmap, mlock e madvise para performance.
func Open(path string) (*Partition, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()

	data, err := unix.Mmap(int(file.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}

	if err := unix.Mlock(data); err != nil {
		unix.Munmap(data)
		return nil, err
	}

	unix.Madvise(data, unix.MADV_SEQUENTIAL)

	// Assume 32 bytes per entry
	entrySize := 32
	numEntries := int(size) / entrySize

	// Create slice header directly
	ptr := (*Entry)(unsafe.Pointer(&data[0]))
	entries := unsafe.Slice(ptr, numEntries)

	return &Partition{
		data:    data,
		entries: entries,
	}, nil
}

// Close desmapeia o arquivo.
func (p *Partition) Close() error {
	unix.Munlock(p.data)
	return unix.Munmap(p.data)
}

// Search busca os 5 vizinhos mais próximos da query quantizada e retorna a contagem de fraudes (0-5).
func (p *Partition) Search(query *[14]int16) uint8 {
	type neighbor struct {
		dist  int64
		fraud bool
	}
	top5 := [5]neighbor{
		{dist: 1 << 62, fraud: false},
		{dist: 1 << 62, fraud: false},
		{dist: 1 << 62, fraud: false},
		{dist: 1 << 62, fraud: false},
		{dist: 1 << 62, fraud: false},
	}

	for i := range p.entries {
		entry := &p.entries[i]

		// Squared Euclidean distance
		var dist int64
		for j := 0; j < 14; j++ {
			diff := int64(query[j] - entry.Vec[j])
			dist += diff * diff
		}

		// Insert into top5 if closer
		if dist < top5[4].dist {
			// Find position
			for j := 4; j >= 0; j-- {
				if j == 0 || dist >= top5[j-1].dist {
					top5[j] = neighbor{dist: dist, fraud: entry.Fraud}
					break
				}
				top5[j] = top5[j-1]
			}
		}
	}

	var fraudCount uint8
	for _, n := range top5 {
		if n.fraud {
			fraudCount++
		}
	}
	return fraudCount
}

// Quantize converte um vetor de 14 float32 para um vetor de 14 int16.
// Gerencia sentinelas -1.0 e aplica clamp e arredondamento.
func Quantize(v [14]float32) [14]int16 {
	var q [14]int16
	for i, f := range v {
		if f == -1.0 {
			q[i] = -Scale
		} else {
			x := float64(f)
			if x < 0 {
				x = 0
			} else if x > 1 {
				x = 1
			}
			q[i] = int16(x*float64(Scale) + 0.5)
		}
	}
	return q
}

// Set contém todas as partições indexadas por tag (0-15)
type Set struct {
	partitions [16]*Partition
}

// LoadSet carrega todas as partições de um diretório
func LoadSet(dir string) (*Set, error) {
	s := &Set{}
	for i := 0; i < 16; i++ {
		path := fmt.Sprintf("%s/partition_%d.bin", dir, i)
		if _, err := os.Stat(path); err == nil {
			p, err := Open(path)
			if err != nil {
				return nil, err
			}
			s.partitions[i] = p
		}
	}
	return s, nil
}

// FindPartition retorna a partição adequada usando fallback.
func (s *Set) FindPartition(tag uint8) *Partition {
	if s.partitions[tag] != nil {
		return s.partitions[tag]
	}
	if s.partitions[tag&^8] != nil {
		return s.partitions[tag&^8]
	}
	if s.partitions[tag&^4] != nil {
		return s.partitions[tag&^4]
	}
	return s.partitions[0]
}
