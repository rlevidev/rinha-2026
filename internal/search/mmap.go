package search

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// SaveBinary serializa o índice HNSW para arquivo binário.
//
// Formato (em ordem para garantir alinhamento natural):
//
//	Offset 0:       Header (7×int64 = 56 bytes)
//	Offset 56:      vectors (uint16 array: numNodes × 14 × 2 bytes)
//	Offset 56+V:    adjOffset (int32 array: numNodes × 4 bytes)
//	Offset 56+V+A:  adjData (int32 array: len(adjData) × 4 bytes)
//	Offset 56+V+A+D: isFraud (bool array: numNodes × 1 byte)
//
// Esta ordem garante:
//   - Header alinhado em múltiplo de 8 (int64)
//   - vectors alinhado em múltiplo de 2 (uint16)
//   - adjOffset alinhado em múltiplo de 4 (int32)
//   - adjData alinhado em múltiplo de 4 (int32)
//   - isFraud pode começar em qualquer offset (bool = 1 byte)
//
// Matemática de alinhamento verificada:
//   - Header: 56 bytes = múltiplo de 8 ✓
//   - vectors: 3M × 14 × 4 = 168.000.000 bytes = múltiplo de 4 ✓
//   - adjOffset: 3M × 4 = 12.000.000 bytes = múltiplo de 4 ✓
//   - adjData: N × 4 = múltiplo de 4 ✓
func (h *HNSW) SaveBinary(path string) error {
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create tmp file: %w", err)
	}
	// Defers são LIFO: Remove executa antes de Close.
	// Em Linux, remover um arquivo aberto é seguro — o inode persiste até o Close.
	// Se o Rename teve sucesso, Remove tenta apagar um path inexistente → erro ignorado (inócuo).
	// Se houve falha antes do Rename, Remove apaga o .tmp deixado para trás.
	defer f.Close()
	defer os.Remove(tmpPath)

	// Escreve header (7×int64)
	header := [7]int64{
		int64(h.M),
		int64(h.EfConstruct),
		int64(h.Ef),
		int64(h.numNodes),
		int64(h.entryPoint),
		int64(h.maxLayer),
		int64(len(h.adjData)), // tamanho explícito de adjData para LoadBinaryMmap
	}
	if err := binary.Write(f, binary.LittleEndian, header); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// Escreve arrays na ordem que garante alinhamento natural de ponteiros
	if err := binary.Write(f, binary.LittleEndian, h.vectors); err != nil {
		return fmt.Errorf("failed to write vectors: %w", err)
	}
	if err := binary.Write(f, binary.LittleEndian, h.adjOffset); err != nil {
		return fmt.Errorf("failed to write adjOffset: %w", err)
	}
	if err := binary.Write(f, binary.LittleEndian, h.adjData); err != nil {
		return fmt.Errorf("failed to write adjData: %w", err)
	}
	if err := binary.Write(f, binary.LittleEndian, h.isFraud); err != nil {
		return fmt.Errorf("failed to write isFraud: %w", err)
	}

	// Força escrita no disco físico antes do Rename (garante atomicidade)
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync tmp file: %w", err)
	}

	// Atomic Rename: o index.bin final surge inteiro no filesystem.
	// Após o Rename, o defer Remove tenta apagar tmpPath inexistente — sem efeito.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to atomically rename file: %w", err)
	}

	return nil
}

// LoadBinaryMmap carrega o índice HNSW via mmap com MAP_SHARED.
//
// Benefícios do mmap compartilhado:
//   - Duas instâncias (API1, API2) no mesmo host compartilham as mesmas páginas
//     físicas de RAM através do page cache do SO.
//   - Resultado: ~280MB de índice ocupa ~280MB no kernel, não 560MB (2×280).
//   - RSS por container: ~140MB + ~25MB de runtime = ~165MB ✓
//
// MAP_SHARED: alterações (se fossem permitidas) seriam visíveis em todos
// os mapeamentos. Aqui usamos PROT_READ apenas, então é read-only.
// MAP_POPULATE: pré-carrega as páginas na RAM no startup em vez de esperar
// por page faults durante a execução. Reduz latência de p99.
// Contingência: se o OOM killer do Docker disparar no startup (improvável com
// MAP_SHARED, pois as páginas ficam no page cache e não no RSS anônimo do
// processo), remover MAP_POPULATE. As páginas serão carregadas sob demanda no
// primeiro batch de requests — custo aceitável vs risco de OOM.
//
// O índice retornado é marcado como readonly=true para evitar acidentais
// tentativas de escrita (que causariam SIGSEGV).
func LoadBinaryMmap(path string) (*HNSW, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open index file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	size := int(fi.Size())

	// Validação: arquivo deve ter pelo menos o header (56 bytes)
	if size < 56 {
		return nil, fmt.Errorf("index file too small: %d bytes", size)
	}

	// Mapeia arquivo em memória com leitura compartilhada entre processos
	// Removido o MAP_POPULATE
	// Em maquinas fracas/HDDs lentos, forçar o carregamento síncrono de 300MB
	// trava o runtime do Go e estoura os timeouts do container
	data, err := syscall.Mmap(
		int(f.Fd()),
		0,
		size,
		syscall.PROT_READ,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to mmap file: %w", err)
	}

	// Parse do header (56 bytes = 7×int64 em little-endian)
	var header [7]int64
	for i := 0; i < 7; i++ {
		offset := i * 8
		header[i] = int64(data[offset]) |
			int64(data[offset+1])<<8 |
			int64(data[offset+2])<<16 |
			int64(data[offset+3])<<24 |
			int64(data[offset+4])<<32 |
			int64(data[offset+5])<<40 |
			int64(data[offset+6])<<48 |
			int64(data[offset+7])<<56
	}

	M := int(header[0])
	EfConstruct := int(header[1])
	Ef := int(header[2])
	if envEf := os.Getenv("EF_OVERRIDE"); envEf != "" {
		if parsed, err := strconv.Atoi(envEf); err == nil && parsed > 0 {
			Ef = parsed
		}
	}
	numNodes := int(header[3])
	entryPoint := int(header[4])
	maxLayer := int(header[5])
	lenAdjData := int(header[6]) // Novo: lido explícito

	// Validações de sanidade
	if numNodes <= 0 || numNodes > 3_000_000 {
		syscall.Munmap(data)
		return nil, fmt.Errorf("invalid numNodes: %d", numNodes)
	}
	if M <= 0 || M > 64 {
		syscall.Munmap(data)
		return nil, fmt.Errorf("invalid M: %d", M)
	}
	if maxLayer < 0 || maxLayer > 20 {
		syscall.Munmap(data)
		return nil, fmt.Errorf("invalid maxLayer: %d", maxLayer)
	}

	// Calcula offsets dos arrays dentro do mmap
	// Ordem: header (56) -> vectors -> adjOffset -> adjData -> isFraud
	offset := 56

	// vectors: uint16 array (numNodes × 14)
	vectorsSize := numNodes * 14
	vectorsBytes := vectorsSize * 2
	vectorsEnd := offset + vectorsBytes
	if vectorsEnd > size {
		syscall.Munmap(data)
		return nil, fmt.Errorf("vectors exceeds file size")
	}
	vectorsData := unsafe.Pointer(&data[offset])
	vectors := unsafe.Slice((*uint16)(vectorsData), vectorsSize)

	// adjOffset: int32 array (numNodes)
	// len(h.adjOffset) == numNodes porque Insert faz um append por nó.
	// O neighbors() usa len(h.adjData) como sentinela para o último nó.
	adjOffsetStart := vectorsEnd
	adjOffsetSize := numNodes
	adjOffsetBytes := adjOffsetSize * 4
	adjOffsetEnd := adjOffsetStart + adjOffsetBytes
	if adjOffsetEnd > size {
		syscall.Munmap(data)
		return nil, fmt.Errorf("adjOffset exceeds file size")
	}
	adjOffsetData := unsafe.Pointer(&data[adjOffsetStart])
	adjOffset := unsafe.Slice((*int32)(adjOffsetData), adjOffsetSize)

	// adjData: int32 array (tamanho explícito lido do header)
	adjDataStart := adjOffsetEnd
	adjDataSize := lenAdjData
	adjDataBytes := adjDataSize * 4
	adjDataEnd := adjDataStart + adjDataBytes
	if adjDataEnd > size {
		syscall.Munmap(data)
		return nil, fmt.Errorf("adjData exceeds file size")
	}
	adjDataData := unsafe.Pointer(&data[adjDataStart])
	adjData := unsafe.Slice((*int32)(adjDataData), adjDataSize)

	// isFraud: bool array (numNodes)
	isFraudStart := adjDataEnd
	if isFraudStart+numNodes > size {
		syscall.Munmap(data)
		return nil, fmt.Errorf("isFraud exceeds file size")
	}
	isFraudData := unsafe.Pointer(&data[isFraudStart])
	isFraud := unsafe.Slice((*bool)(isFraudData), numNodes)

	// Cria instância do HNSW apontando para o mmap
	h := &HNSW{
		M:           M,
		EfConstruct: EfConstruct,
		Ef:          Ef,
		vectors:     vectors,
		isFraud:     isFraud,
		adjOffset:   adjOffset,
		adjData:     adjData,
		entryPoint:  int(entryPoint),
		maxLayer:    int(maxLayer),
		numNodes:    numNodes,
		rnd:         nil,  // não necessário para leitura
		readonly:    true, // marca como imutável
		mmapData:    data, // salva para Close()
	}

	return h, nil
}

// Close libera a memória mapeada via mmap.
// DEVE SER CHAMADO quando a API desligar, idealmente via defer na função main
// ou em um hook de shutdown (signal handler).
//
// Se o índice não foi carregado via mmap (readonly=false), Close() é um no-op.
func (h *HNSW) Close() error {
	// Só libera memória se foi carregada via mmap
	if !h.readonly || h.mmapData == nil {
		return nil
	}

	if err := syscall.Munmap(h.mmapData); err != nil {
		return fmt.Errorf("failed to munmap: %w", err)
	}

	// Limpa para evitar double-free
	h.mmapData = nil
	return nil
}
