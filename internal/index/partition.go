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

// Search busca os 5 vizinhos mais próximos da query quantizada.
// Usa early termination em 2 estágios com loop manualmente desenrolado
// e hints para eliminar verificações de bounds do compilador.
func (p *Partition) Search(query *[14]int16) uint8 {
	q := query
	_ = q[13]
	top5dist := [5]int64{1 << 62, 1 << 62, 1 << 62, 1 << 62, 1 << 62}
	var top5fraud [5]bool
	entries := p.entries

	for i := range entries {
		e := &entries[i]
		v := &e.Vec
		_ = v[13]

		diff := int64(q[0]) - int64(v[0])
		partial := diff * diff
		diff = int64(q[1]) - int64(v[1])
		partial += diff * diff
		diff = int64(q[2]) - int64(v[2])
		partial += diff * diff
		diff = int64(q[3]) - int64(v[3])
		partial += diff * diff
		diff = int64(q[4]) - int64(v[4])
		partial += diff * diff
		diff = int64(q[5]) - int64(v[5])
		partial += diff * diff
		diff = int64(q[6]) - int64(v[6])
		partial += diff * diff

		if partial >= top5dist[4] {
			continue
		}

		dist := partial
		diff = int64(q[7]) - int64(v[7])
		dist += diff * diff
		diff = int64(q[8]) - int64(v[8])
		dist += diff * diff
		diff = int64(q[9]) - int64(v[9])
		dist += diff * diff
		diff = int64(q[10]) - int64(v[10])
		dist += diff * diff
		diff = int64(q[11]) - int64(v[11])
		dist += diff * diff
		diff = int64(q[12]) - int64(v[12])
		dist += diff * diff
		diff = int64(q[13]) - int64(v[13])
		dist += diff * diff

		if dist < top5dist[4] {
			if dist < top5dist[0] {
				top5dist[4] = top5dist[3]
				top5fraud[4] = top5fraud[3]
				top5dist[3] = top5dist[2]
				top5fraud[3] = top5fraud[2]
				top5dist[2] = top5dist[1]
				top5fraud[2] = top5fraud[1]
				top5dist[1] = top5dist[0]
				top5fraud[1] = top5fraud[0]
				top5dist[0] = dist
				top5fraud[0] = e.Fraud
			} else if dist < top5dist[1] {
				top5dist[4] = top5dist[3]
				top5fraud[4] = top5fraud[3]
				top5dist[3] = top5dist[2]
				top5fraud[3] = top5fraud[2]
				top5dist[2] = top5dist[1]
				top5fraud[2] = top5fraud[1]
				top5dist[1] = dist
				top5fraud[1] = e.Fraud
			} else if dist < top5dist[2] {
				top5dist[4] = top5dist[3]
				top5fraud[4] = top5fraud[3]
				top5dist[3] = top5dist[2]
				top5fraud[3] = top5fraud[2]
				top5dist[2] = dist
				top5fraud[2] = e.Fraud
			} else if dist < top5dist[3] {
				top5dist[4] = top5dist[3]
				top5fraud[4] = top5fraud[3]
				top5dist[3] = dist
				top5fraud[3] = e.Fraud
			} else {
				top5dist[4] = dist
				top5fraud[4] = e.Fraud
			}
		}
	}

	var fraudCount uint8
	if top5fraud[0] {
		fraudCount++
	}
	if top5fraud[1] {
		fraudCount++
	}
	if top5fraud[2] {
		fraudCount++
	}
	if top5fraud[3] {
		fraudCount++
	}
	if top5fraud[4] {
		fraudCount++
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
		if x < 0 {
			x = 0
		} else if x > 1 {
			x = 1
		}
		q[i] = int16(x*float64(Scale) + 0.5)
	}
	return q
}
