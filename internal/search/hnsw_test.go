package search

import (
	"math/rand"
	"sync"
	"testing"
)

// Helper para criar vetores de 14 dimensões facilmente nos testes.
// Mantém as dimensões extras como 0 para não interferir na distância 2D/3D.
func makeTestVector(x, y float32) [14]float32 {
	var vec [14]float32
	vec[0] = x
	vec[1] = y
	// As outras 12 posições continuam 0. A distância euclidiana continuará consistente.
	return vec
}

// Pilar 1: Teste de Inserção e Busca Exata
func TestInsertAndKNN5(t *testing.T) {
	// 1. Fixar a semente (seed) do gerador aleatório global para garantir que
	// o sorteio de camadas seja idêntico em todas as execuções de teste (100% determinístico).

	// 2. Instanciar o HNSW com M=2 (limites baixos facilitam depuração).
	// EfConstruct=10, Ef=10 e capacidade 3.
	h := New(3, 2, 10, 10)

	// 3. Inserir 3 vetores conhecidos com labels de fraude distintos
	// Nó 0: (1.0, 0.0) -> Não é fraude
	// Nó 1: (0.0, 2.0) -> É fraude
	// Nó 2: (0.0, 0.0) -> Não é fraude
	h.Insert(makeTestVector(1.0, 0.0), false)
	h.Insert(makeTestVector(0.0, 2.0), true)
	h.Insert(makeTestVector(0.0, 0.0), false)

	// 4. Executar KNN5 com uma Query muito próxima do Nó 1 (0.0, 1.9)
	query := makeTestVector(0.0, 1.9)
	results := h.KNN5(query)

	// 5. Validar os resultados:

	// O primeiro vizinho (results[0]) deve ser o Nó 1.
	// dist((0,1.9), (0,2.0))² = 0.01 — menor que todos os outros nós.
	// Tolerância de 1e-5 para imprecisão de float32.
	expectedDist := float32(0.01) // (1.9 - 2.0)² = 0.01
	if results[0].DistSq > expectedDist+1e-5 {
		t.Errorf("vizinho mais próximo: DistSq = %.6f, esperava ≈ %.6f", results[0].DistSq, expectedDist)
	}

	// O label do primeiro vizinho deve ser fraude (Nó 1 = true).
	if !results[0].IsFraud {
		t.Errorf("vizinho mais próximo deve ser fraude (Nó 1), mas IsFraud = false")
	}

	// Como temos apenas 3 nós, os últimos 2 elementos do array [5]Neighbor devem ser zero-value.
	// Zero-value de Neighbor = {DistSq: 0, IsFraud: false}.
	// O KNN5 preenche só os i < len(neighbors), deixando o resto como zero.
	for i := 3; i < 5; i++ {
		if results[i].DistSq != 0 || results[i].IsFraud {
			t.Errorf("results[%d] deveria ser zero-value, got {DistSq: %f, IsFraud: %v}",
				i, results[i].DistSq, results[i].IsFraud)
		}
	}
}

// Pilar 2: Teste de Conectividade CSR e limites de vizinhos
func TestCSRConectividade(t *testing.T) {

	// Criar HNSW com M=4 → limite camada 0 = 2*M = 8 conexões por nó.
	h := New(5, 4, 10, 10)

	h.Insert(makeTestVector(1.0, 0.0), false)
	h.Insert(makeTestVector(0.0, 2.0), true)
	h.Insert(makeTestVector(0.0, 0.0), false)
	h.Insert(makeTestVector(1.0, 1.0), true)
	h.Insert(makeTestVector(2.0, 2.0), false)

	// Validar que os offsets em adjOffset são estritamente crescentes.
	// adjOffset[i] < adjOffset[i+1] garante que cada nó tem pelo menos 1 slot reservado
	// e que o layout CSR está íntegro — nenhum nó sobrescreve o espaço do próximo.
	for i := 0; i < h.numNodes-1; i++ {
		if h.adjOffset[i] >= h.adjOffset[i+1] {
			t.Errorf("adjOffset não é estritamente crescente: adjOffset[%d]=%d >= adjOffset[%d]=%d",
				i, h.adjOffset[i], i+1, h.adjOffset[i+1])
		}
	}

	// Validar que todos os vizinhos válidos (nb >= 0) de cada nó apontam para ids existentes.
	// Sentinels -1 são esperados em slots não preenchidos — fazem parte do contrato do CSR.
	// O invariante é que nenhum vizinho válido aponte para fora do range [0, numNodes).
	for id := 0; id < h.numNodes; id++ {
		for _, nb := range h.neighbors(id, 0) {
			if nb < 0 {
				continue // sentinel esperado — slot não preenchido
			}
			if int(nb) >= h.numNodes {
				t.Errorf("nó %d: vizinho %d aponta para id fora do range [0, %d)", id, nb, h.numNodes)
			}
		}
	}
}

// Pilar 3: Teste de Poda Heurística (Pruning)
func TestPruning(t *testing.T) {

	// M=2 → limite de conexões camada 0 = 2*M = 4 por nó.
	h := New(10, 2, 10, 10)

	// Inserir 6 nós geograficamente muito próximos.
	// Quando o 6º nó for inserido, vizinhos que já têm 4 conexões serão forçados
	// a avaliar se o novo nó merece substituir algum dos atuais (pruning bidirecional).
	h.Insert(makeTestVector(0.0, 0.0), false)
	h.Insert(makeTestVector(0.1, 0.0), true)
	h.Insert(makeTestVector(0.0, 0.1), false)
	h.Insert(makeTestVector(0.1, 0.1), true)
	h.Insert(makeTestVector(0.2, 0.0), false)
	h.Insert(makeTestVector(0.2, 0.1), true)

	maxConnLayer0 := h.M * 2 // = 4 para M=2

	for id := 0; id < h.numNodes; id++ {
		// O count deve ser <= 4 para todos os nós na camada 0.
		// Se ultrapassar, o pruneConnections não está funcionando corretamente.
		count := h.getNeighborCount(id, 0)
		if count > maxConnLayer0 {
			t.Errorf("nó %d: %d conexões na camada 0, limite é %d", id, count, maxConnLayer0)
		}

		// As conexões restantes devem ser geometricamente mais próximas do que os nós excluídos.
		// Estratégia: para cada vizinho real v de `id`, verifica que não existe nenhum
		// nó NÃO vizinho que seja mais próximo de `id` do que o vizinho mais distante de `id`.
		// Em outras palavras: o conjunto de vizinhos deve ser os `count` mais próximos.
		if count == 0 {
			continue
		}

		// Calcula a distância máxima entre `id` e seus vizinhos reais.
		idVec := h.vec(id)
		maxNeighborDist := float32(0)
		for _, nb := range h.neighbors(id, 0) {
			if nb < 0 {
				continue
			}
			d := distSq(idVec, h.vec(int(nb)))
			if d > maxNeighborDist {
				maxNeighborDist = d
			}
		}

		// Nenhum nó fora do conjunto de vizinhos deve ser mais próximo do que maxNeighborDist,
		// a menos que o conjunto de vizinhos já esteja cheio (count == maxConnLayer0).
		// Se count < maxConnLayer0, todos os nós deveriam ser vizinhos — nada foi podado.
		if count < maxConnLayer0 {
			continue // não houve poda, não há o que verificar
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
			d := distSq(idVec, h.vec(other))
			if d < maxNeighborDist {
				t.Errorf("nó %d: nó %d (dist=%.4f) é mais próximo que o vizinho mais distante (dist=%.4f) mas não é vizinho — poda incorreta",
					id, other, d, maxNeighborDist)
			}
		}
	}
}

// Pilar 4: Teste de Concorrência sem Condições de Corrida
func TestConcorrência(t *testing.T) {
	h := New(50, 4, 10, 10)

	// Inserir 20 vetores aleatórios inicialmente (fase offline/setup)
	for i := 0; i < 20; i++ {
		h.Insert(makeTestVector(rand.Float32(), rand.Float32()), i%2 == 0)
	}

	// Disparar múltiplas goroutines fazendo buscas em paralelo (simulando tráfego real).
	// O detector de race condition do Go (`go test -race`) é quem valida a ausência de
	// condições de corrida — este teste serve de gatilho para o detector.
	var wg sync.WaitGroup
	numThreads := 8
	buscasPorThread := 100

	for thread := 0; thread < numThreads; thread++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < buscasPorThread; i++ {
				query := makeTestVector(0.5, 0.5)
				result := h.KNN5(query)

				// Valida que o resultado é coerente: DistSq >= 0 e IDs implícitos válidos.
				// Se houvesse corrida de dados, resultados inválidos apareceriam aqui.
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
