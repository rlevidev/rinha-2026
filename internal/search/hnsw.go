package search

import (
	"container/heap"
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

// Len informa ao pacote container/heap quantos elementos existem
// Sem isso, heap não consegue calcular posições pai/filho na árvore binária
func (h minHeap) Len() int {
	return len(h)
}

// Less define a ordem: distância menor = prioridade maior
// O heap vai reorganizar os elementos para que o mais próximo fique no topo.
func (h minHeap) Less(i, j int) bool {
	return h[i].dist < h[j].dist
}

// Swap troca dois elementos de posição.
// Chamado internamento pelo heap durante as operações de reorganização (sift up/down)
func (h minHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

// Push adiciona um elemento ao final do slice.
// O heap chama isso e depois reorganiza a árvore (sift up) automaticamente
// Recebe ponteiro para modificar o slice original, não uma cópia.
func (h *minHeap) Push(x any) {
	// Por isso é usado o ponteiro "*", pois ela não pega uma "cópia" do slice, ele pega o slice.
	*h = append(*h, x.(nodeDist)) // type assertion: x chega como "any" (interface vazia), afirmamos que é nodeDist
}

// Pop remove e retorna o elemento do topo (o mais próximo)
// O heap já trocou o topo com o último antes de chamar Pop, então basta encurtar o slice e devolver o elemento removido.
func (h *minHeap) Pop() any {
	old := *h             // copia a referencia do slice atual
	x := old[len(old)-1]  // pega o último elemento (que o heap já colocou aqui)
	*h = old[:len(old)-1] // encurta o slice, "removendo" o último
	return x              // retorna o elemento que foi removido
}

// =============================================================================
// maxHeap - heap de resultados (maior distância = maior prioridade)
// =============================================================================

// maxHeap mantém o nó mais distante da query no topo.
// Usado em searchLayer0 para descartar facilmente o pior resultado quando o heap transborda: basta um Pop() para remover o mais distante.
type maxHeap []nodeDist

func (h maxHeap) Len() int {
	return len(h)
}

// Less inverte a ordem em relação ao minHeap: distância maior = topo.
// Isso é o que transforma o heap num maxHeap.
func (h maxHeap) Less(i, j int) bool {
	return h[i].dist > h[j].dist
}

func (h maxHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *maxHeap) Push(x any) {
	*h = append(*h, x.(nodeDist))
}

func (h *maxHeap) Pop() interface{} {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
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
}

// =============================================================================
// Funções auxiliares
// =============================================================================

// vec retorna o slice de 14 floats do nó id dentro do array sequencial
// Centraliza o cálculo do offset para evitar erros de indexação.
func (h *HNSW) vec(id int) []float32 {
	return h.vectors[id*14 : id*14+14]
}

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

	// Conexões reservadas por camada no slot CSR de cada nó: M (superiores) ou 2M (camada 0).
	maxPerLayer := h.M
	if layer == 0 {
		maxPerLayer = h.M * 2 // camada 0 tem o dobro de conexões
	}

	// Calculam o endereçamento de memória para dividir uma estrutura linear (como um array gigante) em segmentos fixos para cada camada (layer) do grafo HNSW
	layerStart := layer * maxPerLayer    // Calcula o índice inicial (o offset) onde os dados da camada "layer" começam no array global
	layerEnd := layerStart + maxPerLayer // Calcula o limite superior (o índice onde essa camada termina). Isso define a "janela" de memória que pertence exclusivamente àquela camada.

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
			if nb < 0 {	// slot não preenchido
				continue
			}
			if d := distSq(query, h.vec(int(nb))); d < bestDist {
				bestDist, best, improved = d, int(nb), true // bestDist = d, best = nb, improved = true
			}
		}

		// Se não encontrou nenhum vizinho melhor, retorna o melhor nó encontrado nessa camada
		if !improved {
			return best
		}
	}
}

// searchLayer0 executa beam search na camada 0 mantendo os Ef melhores candidatos
//
// Diferente do greedySearch (que avança para um único melhor vizinho), aqui mantemos uma fila de candidatos e exploramos todos sistematicamente.
// Isso evita ficar preso em mínimos locais, necessário na camada 0 que é densa.
func (h *HNSW) searchLayer0(query []float32, ep int, cands *minHeap, res *maxHeap, visited map[int]struct{}) []nodeDist {
	// cands, res e visited vêm do sync.Pool: já chegam com len=0 e visited limpo (limpeza feita pelo KNN5 antes de devolver ao pool).
	// Inicializa cands e res com o nó de entrada ep.
	epDist := distSq(query, h.vec(ep))
	*cands = append(*cands, nodeDist{ep, epDist})
	heap.Init(cands)
	*res = append(*res, nodeDist{ep, epDist})
	heap.Init(res)

	// ep já foi marcado como visitado pelo caller (KNN5) antes de chamar searchLayer0.

	// Loop até esvaziar a fila de candidatos
	for cands.Len() > 0 {
		// Pega o melhor candidato (menor distância) para explorar
		cur := heap.Pop(cands).(nodeDist)

		// Poda: se o melhor candidato restante já é pior que o pior resultado atual, todos os próximos candidatos também serão piores, podemos parar a busca.
		// Isso reduz o número de nós explorados significativamente.
		if res.Len() >= h.Ef && cur.dist > (*res)[0].dist {
			break
		}

		// Explora todos os vizinhos do nó atual
		for _, nb := range h.neighbors(cur.id, 0) {
			// Se o vizinho já foi visitado, pula.
			// O if está consultando o id "nbID" no mapa "visited" e "seen" é um boolean (true -> encontrado, false -> não encontrado)
			nbID := int(nb)
			if nbID < 0 {	// slot não preenchido
				continue
			}
			if _, seen := visited[nbID]; seen {
				continue
			}
			visited[nbID] = struct{}{} // Marca "nbID" como visitado antes de explorar os vizinhos.

			d := distSq(query, h.vec(nbID))

			// Adiciona nbID aos candidatos e resultados só se for promissor
			// (melhor que o pior resultado atual, ou resultados ainda não estão cheios).
			if res.Len() < h.Ef || d < (*res)[0].dist {
				heap.Push(cands, nodeDist{nbID, d})
				heap.Push(res, nodeDist{nbID, d})
				if res.Len() > h.Ef {
					heap.Pop(res)
				}
			}
		}
	}

	// Drena o maxHeap de trás para frente: Pop retorna o mais distante,
	// preenchendo out[n-1], out[n-2], ..., out[0]. Resultado final: do mais próximo ao mais distante (que é o que o KNN5 espera).
	// Evita alocar um slice auxiliar para ordenar.
	out := make([]nodeDist, res.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(res).(nodeDist)
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

	neighbors := h.searchLayer0(q, ep, cands, res, visited)

	// Limpa visited e devolve tudo ao pool APÓS extrair os resultados.
	// A limpeza é feita aqui (não dentro de searchLayer0) para que a responsabilidade
	// de limpar fique no mesmo lugar que a responsabilidade de devolver ao pool —
	// evitando que um futuro chamador pegue um mapa com dados antigos.
	for k := range visited {
		delete(visited, k)
	}
	candidatesPool.Put(cands)
	resultsPool.Put(res)
	visitedPool.Put(visited)

	// Fase 3 - monta os 5 resultados com o label de fraude de cada vizinho.
	var result [5]Neighbor
	for i := 0; i < 5 && i < len(neighbors); i++ {
		result[i] = Neighbor{
			DistSq:  neighbors[i].dist,
			IsFraud: h.isFraud[neighbors[i].id],
		}
	}

	return result
}
