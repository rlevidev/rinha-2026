package index

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Scale is the factor to convert float32 (0.0-1.0) to int16.
const Scale = 32767

// Entry (usado para processamento, não para mapeamento direto do arquivo)
type Entry struct {
	Vec   [14]int16
	Fraud bool
}

// Partition representa uma partição do índice.
type Partition struct {
	entries []Entry
}

// NPartitions define o espaço de tag de 4 bits.
const NPartitions = 16

// Open lê o arquivo do índice, deserializando as entradas de 29 bytes.
func Open(path string) (*Partition, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()

	const entrySize = 29
	numEntries := int(size) / entrySize

	entries := make([]Entry, numEntries)
	buf := make([]byte, entrySize)

	for i := 0; i < numEntries; i++ {
		_, err := file.Read(buf)
		if err != nil {
			return nil, err
		}

		for j := 0; j < 14; j++ {
			entries[i].Vec[j] = int16(binary.LittleEndian.Uint16(buf[j*2 : j*2+2]))
		}
		entries[i].Fraud = (buf[28] == 1)
	}

	return &Partition{
		entries: entries,
	}, nil
}

// Search busca os 5 vizinhos mais próximos da query quantizada (otimizado).
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
		var dist int64
		for j := 0; j < 14; j++ {
			diff := int64(query[j] - entry.Vec[j])
			dist += diff * diff
		}
		if dist < top5[4].dist {
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

// Quantize converte um vetor de 14 float32 para um vetor de 14 int16.
func Quantize(v [14]float32) [14]int16 {
	var q [14]int16
	for i, f := range v {
		x := float64(f)
		if x < 0 { x = 0 } else if x > 1 { x = 1 }
		q[i] = int16(x*float64(Scale) + 0.5)
	}
	return q
}
