package search

import (
	"math"
	"math/rand"
	"sync"
)

// =============================================================================
// Pools - reutilização de estruturas de dados entre requisições
// =============================================================================

// candidatesPool reutiliza os minHeaps de candidatos entre requisições.
// Sem o pool, cada KNN5 alocaria um novo slice — com centenas de req/s isso pressiona o GC
// e causa pausas que afetam diretamente o p99.
// Capacidade inicial 128: cobre Ef até ~100 sem realocar durante a busca.
var candidatesPool = sync.Pool{
	New: func() any {
		h := make(minHeap, 0, 128)
		return &h
	},
}

// resultsPool reutiliza os maxHeaps de resultados entre requisições, pelo mesmo motivo do candidatesPool.
// Capacidade inicial 128: mesma justificativa — cobre Ef até ~100 sem realocar.
var resultsPool = sync.Pool{
	New: func() any {
		h := make(maxHeap, 0, 128)
		return &h
	},
}

// O mapa é SEMPRE limpo antes de ser devolvido ao pool (não na entrada).
// Isso é feito para evitar que o GC tenha que percorrer o mapa inteiro para limpar as referências.
// O mapa é inicializado com capacidade 256 para evitar alocações desnecessárias.
var visitedPool = sync.Pool{
	New: func() any {
		return make(map[int]struct{}, 256)
	},
}

// =============================================================================
// nodeDist - unidade de dado que circula pelos heaps
// =============================================================================

// nodeDist representa um nó do grafo a sua distância até a query
//
// - id: índice do nó no dataset (0 à 2.999.999)
// - dist: distância euclidiana ao quadrado até a query.
//   - Ao quadrado porque sqrt(a) < sqrt(b) <-> a < b, a raiz não muda a ordem,
//     então podemos eliminá-la e economizar 3M chamadas sqrt por request
type nodeDist struct {
	id   int
	dist float32
}

// =============================================================================
// minHeap - fila de candidatos (menor distância = maior prioridade)
// =============================================================================

// minHeap mantém o nó mais próximo da query no topo.
// Ussdo em searchLayer0 para decidir qual nó explorar primeiro
type minHeap []nodeDist

// push adiciona um elemento ao heap e sifts upward para manter a invariante.
func (h *minHeap) push(x nodeDist) {
	*h = append(*h, x)
	h.up(len(*h) - 1)
}

// pop remove e retorna o elemento do topo (o mais próximo).
func (h *minHeap) pop() nodeDist {
	old := *h
	if len(old) == 0 {
		return nodeDist{}
	}
	n := len(old)
	x := old[0]
	old[0] = old[n-1]
	*h = old[:n-1]
	if len(*h) > 0 {
		h.down(0)
	}
	return x
}

func (h minHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h[parent].dist <= h[i].dist {
			break
		}
		h[parent], h[i] = h[i], h[parent]
		i = parent
	}
}

func (h minHeap) down(i int) {
	n := len(h)
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		j := left
		if right := left + 1; right < n && h[right].dist < h[left].dist {
			j = right
		}
		if h[i].dist <= h[j].dist {
			break
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
}

// peek retorna o elemento do topo sem removê-lo.
func (h minHeap) peek() nodeDist {
	if len(h) == 0 {
		return nodeDist{}
	}
	return h[0]
}

// len retorna o número de elementos no heap.
func (h minHeap) len() int {
	return len(h)
}

// =============================================================================
// maxHeap - heap de resultados (maior distância = maior prioridade)
// =============================================================================

// maxHeap mantém o nó mais distante da query no topo.
// Usado em searchLayer0 para descartar facilmente o pior resultado quando o heap transborda: basta um Pop() para remover o mais distante.
type maxHeap []nodeDist

// push adiciona um elemento ao heap e sifts upward para manter a invariante.
func (h *maxHeap) push(x nodeDist) {
	*h = append(*h, x)
	h.up(len(*h) - 1)
}

// pop remove e retorna o elemento do topo.
func (h *maxHeap) pop() nodeDist {
	old := *h
	if len(old) == 0 {
		return nodeDist{}
	}
	n := len(old)
	x := old[0]
	old[0] = old[n-1]
	*h = old[:n-1]
	if len(*h) > 0 {
		h.down(0)
	}
	return x
}

func (h maxHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h[parent].dist >= h[i].dist {
			break
		}
		h[parent], h[i] = h[i], h[parent]
		i = parent
	}
}

func (h maxHeap) down(i int) {
	n := len(h)
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		j := left
		if right := left + 1; right < n && h[right].dist > h[left].dist {
			j = right
		}
		if h[i].dist >= h[j].dist {
			break
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
}

// peek retorna o elemento do topo sem removê-lo.
func (h maxHeap) peek() nodeDist {
	if len(h) == 0 {
		return nodeDist{}
	}
	return h[0]
}

// len retorna o número de elementos no heap.
func (h maxHeap) len() int {
	return len(h)
}

// =============================================================================
// Neighbor - resultado final do KNN5
// =============================================================================

// Neighbor é um dos 5 vizinhos mais próximos encontrados pelo KNN5.
//
//   - DistSq: distância ao quadrado até a query (para debug/logging).
//   - IsFraud: label do vizinho no dataset.
//     O handler conta quantos têm IsFraud=true e divide por 5 para obter o fraud_score.
type Neighbor struct {
	DistSq  float32
	IsFraud bool
}

// =============================================================================
// HNSW - índice vetorial hierárquico
// =============================================================================

// HNSW (Hierarchical Navigable Small World) é o grafo de busca vetorial.
//
// Funciona em camadas: a camada mais alta é esparsa (poucos nós, grande alcance),
// a camada 0 é densa (todos os nós, vizinhos próximos). A busca começa no topo,
// desce rapidamente para perto da query, e faz a varredura detalhada na camada 0.
// Análogo a navegar um mapa: primeiro pelo estado, depois pela cidade, depois pela rua.
//
// Performance: O(log N) por consulta. Com 3M vetores, ~0.1-0.5ms.
//
// Design cache-friendly: todos os dados ficam em arrays contíguos (sem ponteiros).
// Pointer chasing em 3M nós destruiria o cache L1/L2 e inviabilizaria p99 < 1ms.
type HNSW struct {
	M           int // Número de vizinhos que cada nó mantém por camada
	EfConstruct int // Candidatos avaliados ao inserir um nó maior = grafo mais preciso, mais lento para construir
	Ef          int // Candidatos avaliados para busca maior = mais preciso, maior lantência

	// vector armazena todos os vetores do dataset de forma sequencial.
	// O vetor do nó i ocupa vectors[i*14 : i*14+14], ou seja, 14 floats por nó.
	// Sequencial = processador carrega 14 floats de uma vez no cache L1 ao acessar vectors[i*14].
	vectors []float32 // 3M × 14 = 42M floats32

	// isFraud armazena o label de cada nó. Acesso O(1) por índice.
	isFraud []bool // 3M bools

	// adjOffset e adjData implementam CSR (Compress Sparse Row) para as arestas do grafo.
	// Em vez de um slice de slices ([][]int32, ponteiros espalhados na memória), todas as conexões ficam sequenciais em adjData.
	// Os vizinhos do nó i estão em adjData[adjOffset[i] : adjOffset[i+1]]. Sendo i o índice do array adjOffset.

	// adjOffset: [0, 3, 7, 10, ...]
	// adjData: [5, 12, 8, 2, 9, 1, 4, 7, 0, 3, ....]

	// i = 0 -> adjData[adjOffset[0] : adjOffset[1]] -> adjData[0:3] = [5, 12, 8]
	// i = 1 -> adjData[adjOffset[1] : adjOffset[2]] -> adjData[3:7] = [2, 9, 1, 4]
	// i = 2 -> adjData[adjOffset[2] : adjOffset[3]] -> adjData[7:10] = [7, 0, 3]

	// adjOffset: [0,   3,   7,   10, ...]
	//			   ^    ^    ^    ^
	//			  nó0  nó1  nó2  nó3
	//
	// adjData: [5, 12, 8,   2, 9, 1, 4   7, 0, 3, ...]
	//			 ^______^    ^________^   ^_____^
	//           vizinhos	  vizinhos    vizinhos
	//			 do nó 0	  do nó 1	  do nó 2
	//
	adjOffset []int32
	adjData   []int32

	entryPoint int          // nó de entrada para todas as buscas, fica no topo da hierarquia.
	maxLayer   int          // camada mais alta construída, a busca desce de maxLayer até 0
	numNodes   int          // total de nós inseridos (até 3M)
	mu         sync.RWMutex // permite múltiplas leituras simultâneas; bloqueia apenas para escrita
	rnd        *rand.Rand   // gerador aleatório local - isolado por instância, sem contenção de Mutex global

	// campos necessários para mmap.go compilar corretamente.
	// readonly: true quando o índice foi carregado via LoadBinaryMmap.
	//   Impede tentativas acidentais de Insert() num índice mapeado em memória
	//   read-only (causaria SIGSEGV por escrita em página PROT_READ).
	// mmapData: slice do mmap retornado por syscall.Mmap.
	//   Guardado aqui para que Close() possa chamar syscall.Munmap e liberar
	//   o mapeamento corretamente quando a API desligar.
	readonly bool
	mmapData []byte
}

// =============================================================================
// New - construtor do índice vazio
// =============================================================================

// New cria um índice HNSW vazio, pronto para receber inserções via Insert().
//
// Pré-aloca os slices com a capacidade esperada para evitar realocações durante
// a inserção de 3M vetores — cada realloc de um slice de 160MB seria catastrófico.
//
// capacity: número esperado de nós (ex: 3_000_000)
// m:        número de vizinhos por camada (ex: 8)
// efConstruct: candidatos avaliados na inserção (ex: 200)
// ef:       candidatos avaliados na busca (ex: 50)
func New(capacity, m, efConstruct, ef int) *HNSW {
	// Capacidade do adjData — por que usamos a média estatística e não o pior caso?
	//
	// Cada nó ocupa em adjData: 2M slots (camada 0) + M*nodeLevel slots (camadas superiores).
	// O nodeLevel de cada nó é sorteado com distribuição geométrica de parâmetro mL = 1/ln(M).
	// Portanto, o valor esperado de nodeLevel é mL = 1/ln(M).
	//
	// Para M=8: E[nodeLevel] = 1/ln(8) ≈ 0.48
	// → E[slots por nó] = 2M + M * 0.48 ≈ 2*8 + 8*0.48 ≈ 20 slots
	// → adjData esperado = 3M nós × 20 slots × 4 bytes ≈ 240 MB ✓
	//
	// Usar o pior caso (maxLayerEstimate=6) resultaria em:
	// → 3M × (2*8 + 8*6) × 4 bytes = 3M × 64 × 4 = 768 MB — viola o limite de 350 MB.
	//
	// O Go fará realocações (dobra a capacidade) se ultrapassarmos a pré-alocação,
	// mas isso só acontece para os poucos nós de camada alta (~5% acima do esperado).
	// O custo de 1-2 realocações é desprezível comparado a estourar o container.
	//
	// Fórmula: 2*M (camada 0 fixa) + M/2 (média das camadas superiores, arredondado para cima)
	avgSlotsPerNode := 2*m + m/2 // Para M=8: 16 + 4 = 20 slots/nó → ~240 MB total

	return &HNSW{
		M:           m,
		EfConstruct: efConstruct,
		Ef:          ef,
		vectors:     make([]float32, 0, capacity*14),
		isFraud:     make([]bool, 0, capacity),
		adjOffset:   make([]int32, 0, capacity),
		adjData:     make([]int32, 0, capacity*avgSlotsPerNode),
		entryPoint:  0,
		maxLayer:    0,
		numNodes:    0,
		rnd:         rand.New(rand.NewSource(1337)), //	seed fixa -> build 100% determinístico e reproduzível
	}
}

// =============================================================================
// Funções auxiliares
// =============================================================================

// vec retorna o slice de 14 floats do nó id dentro do array sequencial
// Centraliza o cálculo do offset para evitar erros de indexação.
func (h *HNSW) vec(id int) []float32 {
	return h.vectors[id*14 : id*14+14]
}

func (h *HNSW) NumNodes() int { return h.numNodes }

// distSq calcula a distância euclidiana ao quadrado entre dois vetores de 14 dimensões.
// Sem raiz quadrada: sqrt(a) < sqrt(b) <-> a < b, então a raiz não é necessária para comparar.
// Economiza ~3M chamadas sqrt por request.
func distSq(a, b []float32) float32 {
	a = a[:14:14] // reslice com len e cap fixos em 14: o compilador prova que len(a)==14 e elimina os bounds checks individuais do loop (BCE — Bounds Check Elimination), permitindo vetorização SIMD automática.
	b = b[:14:14] // mesmo motivo para b.
	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return sum
}

// neighbors retorna os vizinhos do nó id na chamada layer.
// A camada 0 tem o dobro de conexões (Mx2) por ser a mais densa.
// Camadas superiores têm M conexões cada.
// Isolar os vizinhos por camada evita explorar conexões irrelevantes durante a descida.
func (h *HNSW) neighbors(id, layer int) []int32 {
	// adjOffset[id] é o índice em adjData onde começa a lista de adjacência do nó id (CSR).
	start := h.adjOffset[id]

	// Para o último nó, adjOffset[id+1] não existe, usa o fim de adjData.
	// adjOffset: [0, 3, 7, 10, ...]
	// adjData: [5, 12, 8, 2, 9, 1, 4, 7, 0, 3, ....]
	// id = 1 -> if 1+1 < 4 -> true -> end = adjOffset[2] = 7
	end := int32(len(h.adjData))
	if id+1 < len(h.adjOffset) {
		end = h.adjOffset[id+1]
	}

	// Todos os vizinhos do nó "id" (em todas as camadas, num único slice)
	all := h.adjData[start:end]

	var layerStart, layerEnd int
	if layer == 0 {
		layerStart = 0
		layerEnd = h.M * 2
	} else {
		layerStart = 2*h.M + (layer-1)*h.M
		layerEnd = layerStart + h.M
	}

	// Retorna apenas os vizinhos da camada "layer"
	if layerStart >= len(all) {
		return nil
	}
	if layerEnd > len(all) {
		layerEnd = len(all)
	}
	return all[layerStart:layerEnd]
}

// =============================================================================
// randomLevel - sorteia a camada máxima do novo nó
// =============================================================================

// randomLevel sorteia em qual camada máxima o novo nó vai participar.
//
// A distribuição é geométrica: cada nível tem probabilidade p = 1/M de ser promovido
// ao próximo. Resultado: a maioria dos nós fica só na camada 0, poucos chegam mais alto.
//
// A fórmula é do paper original: level = floor(-ln(uniform(0,1)) * mL)
// onde mL = 1/ln(M) é o fator de normalização.
func (h *HNSW) randomLevel() int {
	mL := 1.0 / math.Log(float64(h.M))

	// -ln(uniform) gera uma variável exponencial.
	level := int(math.Floor(-math.Log(h.rnd.Float64()+1e-10) * mL))

	// Limita ao maxLayerEstimate para não explodir o adjData.
	const maxLayerEstimate = 6
	if level > maxLayerEstimate {
		level = maxLayerEstimate
	}
	return level
}

// =============================================================================
// setNeighbor / getNeighborSlot - acesso direto ao adjData por (nó, camada, slot)
// =============================================================================

// neighborSlotOffset retorna o índice em adjData onde começa o slot `slot` do nó `id` na camada `layer`.
func (h *HNSW) neighborSlotOffset(id, layer, slot int) int32 {
	nodeBase := int(h.adjOffset[id])
	var layerOffset int
	if layer == 0 {
		layerOffset = slot // slots 0..2M-1
	} else {
		layerOffset = 2*h.M + (layer-1)*h.M + slot // 2M fixo da camada0, depois M por camada
	}
	return int32(nodeBase + layerOffset)
}

// setNeighbor escreve o vizinho `neighbor` no slot `slot` do nó `id` na camada `layer`.
func (h *HNSW) setNeighbor(id, layer, slot, neighbor int) {
	h.adjData[h.neighborSlotOffset(id, layer, slot)] = int32(neighbor)
}

// getNeighborCount retorna quantos vizinhos válidos (não -1) o nó `id` tem na camada `layer`.
func (h *HNSW) getNeighborCount(id, layer int) int {
	maxSlots := h.M
	if layer == 0 {
		maxSlots = h.M * 2
	}
	count := 0
	base := int(h.adjOffset[id])
	var layerBase int
	if layer == 0 {
		layerBase = 0
	} else {
		layerBase = 2*h.M + (layer-1)*h.M
	}
	for s := 0; s < maxSlots; s++ {
		if h.adjData[base+layerBase+s] >= 0 {
			count++
		}
	}
	return count
}

// =============================================================================
// pruneConnections - mantém no máximo M vizinhos por nó por camada
// =============================================================================

// pruneConnections garante que o nó `id` na camada `layer` tenha no máximo `maxConn` vizinhos.
func (h *HNSW) pruneConnections(id, layer, maxConn int, candidates []nodeDist) {
	base := int(h.adjOffset[id])
	var layerBase int
	if layer == 0 {
		layerBase = 0
	} else {
		layerBase = 2*h.M + (layer-1)*h.M
	}
	for s := 0; s < maxConn; s++ {
		h.adjData[base+layerBase+s] = -1
	}

	limit := maxConn
	if len(candidates) < limit {
		limit = len(candidates)
	}
	for s := 0; s < limit; s++ {
		h.adjData[base+layerBase+s] = int32(candidates[s].id)
	}
}

// =============================================================================
// Busca
// =============================================================================

// greedySearch navega na camada layer a partir de ep até encontrar o nó mais próximo da query() nessa camada.
// Avança sempre para o vizinho mais próximo e para quando não há melhora.
//
// Usado nas camadas 1, 2, ... (esparsas) como "descida rápida": cada passo elimina grande parte do espaço de busca com custo mínimo.
// Na camada o 0 (densa), searchLayer0 substitui este método para mais precisão.
func (h *HNSW) greedySearch(query []float32, ep, layer int) int {
	// Começa com o nó de entrada, calcula sua distância até a query
	best := ep
	bestDist := distSq(query, h.vec(ep))

	// Loop até não encontrar mais vizinhos melhores
	for {
		improved := false

		// Para cada vizinho do nó atual, calcula a distância até a query
		// Se encontrar um vizinho mais próximo, atualiza o melhor nó
		for _, nb := range h.neighbors(best, layer) {
			if nb < 0 { // sentinel: slot não preenchido
				continue
			}
			if d := distSq(query, h.vec(int(nb))); d < bestDist {
				bestDist, best, improved = d, int(nb), true // bestDist = d, best = nb, improved = true
			}
		}
		if !improved {
			return best
		}
	}
}

// greedySearchInsert é idêntico ao greedySearch, mas filtra sentinels -1 nos vizinhos.
// Usado exclusivamente durante Insert, onde slots não preenchidos contêm -1.
func (h *HNSW) greedySearchInsert(query []float32, ep, layer int) int {
	best := ep
	bestDist := distSq(query, h.vec(ep))
	for {
		improved := false
		for _, nb := range h.neighbors(best, layer) {
			if nb < 0 { // sentinel: slot não preenchido
				continue
			}
			if d := distSq(query, h.vec(int(nb))); d < bestDist {
				bestDist, best, improved = d, int(nb), true
			}
		}
		if !improved {
			return best
		}
	}
}

// searchLayer0 executa beam search na camada 0, preenche diretamente result[0:5].
// Retorna o número de vizinhos encontrados (0-5).
func (h *HNSW) searchLayer0(query []float32, ep int, cands *minHeap, res *maxHeap, visited map[int]struct{}, result *[5]Neighbor) int {
	// Inicializa cands e res com o nó de entrada ep.
	epDist := distSq(query, h.vec(ep))
	cands.push(nodeDist{ep, epDist})
	res.push(nodeDist{ep, epDist})

	// Loop até esvaziar a fila de candidatos
	for cands.len() > 0 {
		// Pega o melhor candidato (menor distância) para explorar
		cur := cands.pop()

		// Poda: se o melhor candidato restante já é pior que o pior resultado atual, podemos parar.
		if res.len() >= h.Ef && cur.dist > res.peek().dist {
			break
		}

		// Explora todos os vizinhos do nó atual
		for _, nb := range h.neighbors(cur.id, 0) {
			// Se o vizinho já foi visitado, pula.
			// O if está consultando o id "nbID" no mapa "visited" e "seen" é um boolean (true -> encontrado, false -> não encontrado)
			nbID := int(nb)
			if nbID < 0 { // slot vazio (sentinel -1)
				continue
			}
			if _, seen := visited[nbID]; seen {
				continue
			}
			visited[nbID] = struct{}{} // Marca "nbID" como visitado antes de explorar os vizinhos.

			d := distSq(query, h.vec(nbID))

			// Adiciona nbID aos candidatos e resultados só se for promissor
			if res.len() < h.Ef || d < res.peek().dist {
				cands.push(nodeDist{nbID, d})
				res.push(nodeDist{nbID, d})
				if res.len() > h.Ef {
					res.pop()
				}
			}
		}
	}

	// Drena res (maxHeap) nos slots do result em ordem crescente de distância.
	// O maxHeap tem o mais distante no topo; o pop de maxHeap retorna elementos
	// em ordem decrescente de distância.
	// Pegamos os top-5 e invertemos a ordem para ficar crescente.
	n := res.len()
	if n > 5 {
		n = 5
	}
	// Extrai tudo do res num buffer temporário
	var tmp [128]nodeDist
	total := 0
	for res.len() > 0 {
		tmp[total] = res.pop()
		total++
	}
	// tmp[0..total-1] está em ordem decrescente de distância
	// Os menores estão no final. Pegamos os últimos `n` em ordem reversa.
	for i := 0; i < n; i++ {
		nd := tmp[total-1-i] // menor distância primeiro
		result[i] = Neighbor{
			DistSq:  nd.dist,
			IsFraud: h.isFraud[nd.id],
		}
	}
	return n
}

// searchLayerN - beam search em camadas superiores durante a inserção
func (h *HNSW) searchLayerN(query []float32, ep, layer, efConstruct int) []nodeDist {
	// visited precisa ser isolado: cada chamada de searchLayerN tem seu próprio mapa
	visited := make(map[int]struct{}, efConstruct*2)
	visited[ep] = struct{}{}

	epDist := distSq(query, h.vec(ep))
	cands := &minHeap{{ep, epDist}}
	cands.up(0)
	res := &maxHeap{{ep, epDist}}
	res.up(0)

	for cands.len() > 0 {
		cur := cands.pop()

		if res.len() >= efConstruct && cur.dist > res.peek().dist {
			break
		}

		for _, nb := range h.neighbors(cur.id, layer) {
			nbID := int(nb)
			if nbID < 0 { // slot vazio (sentinel -1)
				continue
			}
			if _, seen := visited[nbID]; seen {
				continue
			}
			visited[nbID] = struct{}{}

			d := distSq(query, h.vec(nbID))
			if res.len() < efConstruct || d < res.peek().dist {
				cands.push(nodeDist{nbID, d})
				res.push(nodeDist{nbID, d})
				if res.len() > efConstruct {
					res.pop()
				}
			}
		}
	}

	out := make([]nodeDist, res.len())
	for i := 0; i < len(out); i++ {
		// pop de maxHeap retorna em ordem decrescente de distância
		// vamos inverter preenchendo de trás para frente
		out[len(out)-1-i] = res.pop()
	}
	return out
}

// =============================================================================
// KNN5 - ponto de entrada público
// =============================================================================

// KNN5 recebe a query vetorizada e retorna os 5 vizinhos mais próximos do dataset.
//
// Fluxo:
//  1. Adquire RLock, múltiplas goroutines podem buscar simultaneamente
//  2. Desce do topo (maxLayer) até a camada 1 com greedySearch (rápido, esparso).
//  3. Faz beam search na camada 0 (preciso, denso) com searchLayer0.
//  4. Monta os 5 resultados consultando isFraud para cada vizinho.
//
// O handler chama KNN5, conta quantos Neighbor.IsFraud == true e divide por 5 para obter o fraud_score.
func (h *HNSW) KNN5(query [14]float32) [5]Neighbor {
	// RLock permite múltiplas goroutines lendo ao mesmo tempo.
	// Essencial para servir centenas de requests HTTP em paralelo sem serializar.
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Guard clause: índice ainda vazio (startup em andamento), retorna vazio em vez de panic.
	if h.numNodes == 0 {
		return [5]Neighbor{}
	}

	q := query[:] // Converte o array [14]float32 para o slice []float32, pois as funções (distSq, searchLayer0, greedySearch) esperam slice.

	// Fase 1 - descida rápida (greedySearch) pelas camadas esparsas.
	// Cada iteração refina o ponto de entrada para a próxima camada.
	ep := h.entryPoint
	for layer := h.maxLayer; layer > 0; layer-- {
		ep = h.greedySearch(q, ep, layer)
	}

	// Fase 2 - beam search na camada 0 (preciso, denso).
	// Retira cands, res e visited do pool para evitar alocações por requisição.
	// O pool mantém os slices/mapa já alocados de requisições anteriores — apenas zeramos o len, sem liberar memória.
	cands := candidatesPool.Get().(*minHeap)
	res := resultsPool.Get().(*maxHeap)
	visited := visitedPool.Get().(map[int]struct{})

	// Zera o len sem liberar a capacidade alocada: reutiliza a memória da requisição anterior.
	*cands = (*cands)[:0]
	*res = (*res)[:0]

	// Marca ep como visitado antes de chamar searchLayer0.
	// searchLayer0 confia que o caller já fez isso — não limpa o mapa na entrada.
	visited[ep] = struct{}{}

	var result [5]Neighbor
	h.searchLayer0(q, ep, cands, res, visited, &result)

	// Limpa visited e devolve tudo ao pool APÓS extrair os resultados.
	// A limpeza é feita aqui (não dentro de searchLayer0) para que a responsabilidade
	// de limpar fique no mesmo lugar que a responsabilidade de devolver ao pool —
	// evitando que um futuro chamador pegue um mapa com dados antigos.
	clear(visited)
	candidatesPool.Put(cands)
	resultsPool.Put(res)
	visitedPool.Put(visited)

	return result
}

// =============================================================================
// Insert - insere um novo vetor no índice
// =============================================================================

// Insert adiciona um novo vetor ao índice HNSW.
func (h *HNSW) Insert(vector [14]float32, isFraud bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	newID := h.numNodes
	nodeLevel := h.randomLevel()

	// --- Registra o vetor e o label ---
	h.vectors = append(h.vectors, vector[:]...)
	h.isFraud = append(h.isFraud, isFraud)

	// --- Aloca os slots de adjacência do novo nó em adjData ---
	slotsNeeded := 2*h.M + h.M*nodeLevel
	h.adjOffset = append(h.adjOffset, int32(len(h.adjData)))
	for i := 0; i < slotsNeeded; i++ {
		h.adjData = append(h.adjData, -1) // -1 = slot vazio
	}

	h.numNodes++

	// Caso especial: primeiro nó — não há vizinhos para conectar.
	if newID == 0 {
		h.entryPoint = 0
		h.maxLayer = nodeLevel
		return
	}

	q := vector[:]
	ep := h.entryPoint

	// --- Fase 1: descida rápida nas camadas acima de nodeLevel ---
	for layer := h.maxLayer; layer > nodeLevel; layer-- {
		ep = h.greedySearchInsert(q, ep, layer)
	}

	// --- Fase 2: inserção em cada camada de nodeLevel até 0 ---
	for layer := nodeLevel; layer >= 0; layer-- {
		maxConn := h.M
		if layer == 0 {
			maxConn = h.M * 2
		}

		// Busca os EfConstruct vizinhos mais próximos nessa camada.
		candidates := h.searchLayerN(q, ep, layer, h.EfConstruct)

		// Seleciona os maxConn mais próximos como vizinhos do novo nó.
		limit := maxConn
		if len(candidates) < limit {
			limit = len(candidates)
		}
		for slot, c := range candidates[:limit] {
			h.setNeighbor(newID, layer, slot, c.id)
		}

		// Conecta cada vizinho selecionado de volta ao novo nó (grafo bidirecional).
		for _, c := range candidates[:limit] {
			nbID := c.id
			nbMaxConn := h.M
			if layer == 0 {
				nbMaxConn = h.M * 2
			}

			// Conta quantos vizinhos nb já tem nessa camada.
			count := h.getNeighborCount(nbID, layer)

			if count < nbMaxConn {
				// nb ainda tem espaço: adiciona o novo nó no primeiro slot livre.
				h.setNeighbor(nbID, layer, count, newID)
			} else {
				// nb está cheio: avalia se o novo nó é melhor que algum vizinho atual.
				nbVec := h.vec(nbID)
				existing := h.neighbors(nbID, layer)

				// Array fixo na stack — nunca escapa para o heap, zero alocações.
				var poolBuf [17]nodeDist // 2*M_max + 1 = 2*8 + 1; suficiente para M≤8
				pool := poolBuf[:0]
				for _, eid := range existing {
					if eid >= 0 {
						pool = append(pool, nodeDist{int(eid), distSq(nbVec, h.vec(int(eid)))})
					}
				}
				pool = append(pool, nodeDist{newID, distSq(nbVec, h.vec(newID))})

				// Ordena do mais próximo ao mais distante (insertion sort)
				for i := 1; i < len(pool); i++ {
					for j := i; j > 0 && pool[j].dist < pool[j-1].dist; j-- {
						pool[j], pool[j-1] = pool[j-1], pool[j]
					}
				}

				h.pruneConnections(nbID, layer, nbMaxConn, pool)
			}
		}

		// O melhor candidato da camada atual vira o ponto de entrada da camada abaixo.
		if len(candidates) > 0 {
			ep = candidates[0].id
		}
	}

	// --- Fase 3: atualiza entryPoint se o novo nó atingiu uma camada mais alta ---
	if nodeLevel > h.maxLayer {
		h.maxLayer = nodeLevel
		h.entryPoint = newID
	}
}
