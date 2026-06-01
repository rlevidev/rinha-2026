package search

import (
	"math"
	"math/rand"
	"sync"
	"testing"
)

// encodeFloat32ToUint16 converte float32 no range [-1, 1] para uint16
// seguindo a mesma fórmula do build-index.
func encodeFloat32ToUint16(v float32) uint16 {
	return uint16((v + 1.0) * 32767.5)
}

// makeTestQuery cria um vetor de consulta float32 com 2 primeiras dimensões preenchidas
// e as demais em 0.0 (equivalente ao valor normalizado ~0.0 após decode).
func makeTestQuery(x, y float32) [14]float32 {
	var q [14]float32
	q[0] = x
	q[1] = y
	// Demais dimensões: 0.0 (corresponde a uint16(32767) após decode)
	return q
}

// makeTestNode cria um vetor uint16 para Insert, com 2 primeiras dimensões preenchidas
// e as demais codificando 0.0 (uint16 ≈ 32767).
func makeTestNode(x, y float32) [14]uint16 {
	var v [14]uint16
	v[0] = encodeFloat32ToUint16(x)
	v[1] = encodeFloat32ToUint16(y)
	// Demais dimensões codificam 0.0
	for i := 2; i < 14; i++ {
		v[i] = 32767 // (0.0+1.0) * 32767.5 ≈ 32767
	}
	return v
}

// Pilar 1: Teste de Inserção e Busca Exata
func TestInsertAndKNN5(t *testing.T) {
	h := New(3, 2, 10, 10)

	// Nó 0: (0.8, 0.0) → Não é fraude
	// Nó 1: (0.0, 0.8) → É fraude
	// Nó 2: (0.0, 0.0) → Não é fraude
	h.Insert(makeTestNode(0.8, 0.0), false)
	h.Insert(makeTestNode(0.0, 0.8), true)
	h.Insert(makeTestNode(0.0, 0.0), false)

	if h.NumNodes() != 3 {
		t.Fatalf("esperava 3 nós, tem %d", h.NumNodes())
	}

	// Query perto do Nó 1: (0.0, 0.75) → dist² até Nó 1 ≈ (0.0-0.0)² + (0.75-0.8)² = 0.0025
	query := makeTestQuery(0.0, 0.75)
	results := h.KNN5(query)

	// O primeiro vizinho deve ser o Nó 1 (fraude)
	expectedDist := float32(0.0025)
	if results[0].DistSq > expectedDist+1e-4 {
		t.Errorf("vizinho mais próximo: DistSq = %.6f, esperava ≈ %.6f", results[0].DistSq, expectedDist)
	}
	if !results[0].IsFraud {
		t.Errorf("vizinho mais próximo deve ser fraude (Nó 1), mas IsFraud = false")
	}

	// Verifica que o KNN5 retorna no máximo 3 vizinhos (temos 3 nós)
	// results[3] e results[4] devem ser zero-value porque searchLayer0 retornou ≤3.
	for i := 3; i < 5; i++ {
		if results[i].DistSq != 0 || results[i].IsFraud {
			t.Errorf("results[%d] deveria ser zero-value, got {DistSq: %f, IsFraud: %v}",
				i, results[i].DistSq, results[i].IsFraud)
		}
	}
}

// Pilar 2: Teste de Conectividade CSR e limites de vizinhos
func TestCSRConectividade(t *testing.T) {
	h := New(5, 4, 10, 10)

	h.Insert(makeTestNode(1.0, 0.0), false)
	h.Insert(makeTestNode(0.0, 1.0), true)
	h.Insert(makeTestNode(0.0, 0.0), false)
	h.Insert(makeTestNode(1.0, 1.0), true)
	h.Insert(makeTestNode(0.5, 0.5), false)

	// Validar que os offsets em adjOffset são estritamente crescentes.
	for i := 0; i < h.numNodes-1; i++ {
		if h.adjOffset[i] >= h.adjOffset[i+1] {
			t.Errorf("adjOffset não é estritamente crescente: adjOffset[%d]=%d >= adjOffset[%d]=%d",
				i, h.adjOffset[i], i+1, h.adjOffset[i+1])
		}
	}

	// Validar que todos os vizinhos válidos apontam para ids existentes.
	for id := 0; id < h.numNodes; id++ {
		for _, nb := range h.neighbors(id, 0) {
			if nb < 0 {
				continue
			}
			if int(nb) >= h.numNodes {
				t.Errorf("nó %d: vizinho %d aponta para id fora do range [0, %d)", id, nb, h.numNodes)
			}
		}
	}
}

// Pilar 3: Teste de Poda Heurística (Pruning)
func TestPruning(t *testing.T) {
	h := New(10, 2, 10, 10)

	// Inserir 6 nós próximos para forçar poda bidirecional.
	h.Insert(makeTestNode(0.0, 0.0), false)
	h.Insert(makeTestNode(0.1, 0.0), true)
	h.Insert(makeTestNode(0.0, 0.1), false)
	h.Insert(makeTestNode(0.1, 0.1), true)
	h.Insert(makeTestNode(0.2, 0.0), false)
	h.Insert(makeTestNode(0.2, 0.1), true)

	maxConnLayer0 := h.M * 2 // = 4 para M=2

	for id := 0; id < h.numNodes; id++ {
		count := h.getNeighborCount(id, 0)
		if count > maxConnLayer0 {
			t.Errorf("nó %d: %d conexões na camada 0, limite é %d", id, count, maxConnLayer0)
		}
		if count == 0 {
			continue
		}

		idVec := h.vec(id)
		maxNeighborDist := float32(0)
		for _, nb := range h.neighbors(id, 0) {
			if nb < 0 {
				continue
			}
			d := distSqNode(idVec, h.vec(int(nb)))
			if d > maxNeighborDist {
				maxNeighborDist = d
			}
		}

		if count < maxConnLayer0 {
			continue
		}
		neighborSet := make(map[int32]bool)
		for _, nb := range h.neighbors(id, 0) {
			if nb >= 0 {
				neighborSet[nb] = true
			}
		}
		for other := 0; other < h.numNodes; other++ {
			if other == id || neighborSet[int32(other)] {
				continue
			}
			d := distSqNode(idVec, h.vec(other))
			if d < maxNeighborDist {
				t.Errorf("nó %d: nó %d (dist=%.4f) é mais próximo que o vizinho mais distante (dist=%.4f) mas não é vizinho — poda incorreta",
					id, other, math.Sqrt(float64(d)), math.Sqrt(float64(maxNeighborDist)))
			}
		}
	}
}

// Pilar 4: Teste de Concorrência sem Condições de Corrida
func TestConcorrência(t *testing.T) {
	h := New(50, 4, 10, 10)

	// Inserir 20 vetores aleatórios
	for i := 0; i < 20; i++ {
		h.Insert(makeTestNode(rand.Float32(), rand.Float32()), i%2 == 0)
	}

	var wg sync.WaitGroup
	numThreads := 8
	buscasPorThread := 100

	for thread := 0; thread < numThreads; thread++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < buscasPorThread; i++ {
				query := makeTestQuery(0.5, 0.5)
				result := h.KNN5(query)

				for _, nb := range result {
					if nb.DistSq < 0 {
						t.Errorf("DistSq negativo (%f) — possível corrida de dados", nb.DistSq)
					}
				}
			}
		}()
	}

	wg.Wait()
}
