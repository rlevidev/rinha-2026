package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/rlevidev/rinha-2026/internal/handler"
	"github.com/rlevidev/rinha-2026/internal/search"
	"github.com/rlevidev/rinha-2026/internal/vectorizer"
)

func main() {
	// GOMAXPROCS: com cpus="0.45", usar 2 threads OS permite processar requisições
	// concorrentemente sem exceder o time-slice da CPU. Com GOMAXPROCS=1 e HNSW search
	// sendo CPU-bound puro (sem I/O), uma única goroutine bloqueia todo o pipeline,
	// criando fila de espera que empurra o p99 para cima do timeout de 2001ms.
	// Pode ser sobrescrito via variável de ambiente para experimentação.
	if gmp := os.Getenv("GOMAXPROCS"); gmp != "" {
		if n, err := strconv.Atoi(gmp); err == nil && n > 0 {
			runtime.GOMAXPROCS(n)
		}
	} else {
		runtime.GOMAXPROCS(2)
	}

	indexPath := "/index.bin"
	log.Printf("Carregando índice de %s via mmap...", indexPath)

	index, err := search.LoadBinaryMmap(indexPath)
	if err != nil {
		log.Fatalf("Erro ao carregar índice: %v", err)
	}
	log.Printf("Índice carregado. Ef=%d, M=%d, nodes=%d, size=%dMB",
		index.Ef, index.M, index.NumNodes(), len(index.MmapData())/1024/1024)

	// Pré-carrega o índice inteiro no page cache do kernel.
	// O índice tem ~340MB. Sem pré-carregamento, cada busca sofre dezenas de page faults
	// (1-10ms cada), que somados ultrapassam o timeout de 2001ms no p99.
	// Estratégia: MADV_WILLNEED (hint ao kernel) + sequential scan (fallback).
	log.Printf("Pré-carregando %dMB no page cache...", len(index.MmapData())/1024/1024)
	if err := index.MadviseWillneed(); err != nil {
		log.Printf("MADV_WILLNEED ignorado (%v), usando sequential scan fallback...", err)
		index.WarmupPageCache()
	}
	log.Printf("Page cache aquecido.")

	norm, err := vectorizer.LoadNormalizer(
		"/resources/normalization.json",
		"/resources/mcc_risk.json",
	)
	if err != nil {
		log.Fatalf("Erro ao carregar normalizer: %v", err)
	}

	h := &handler.FraudHandler{
		Index:      index,
		Normalizer: norm,
	}

	socketPath := os.Getenv("SOCKET_PATH")
	if socketPath == "" {
		socketPath = "/sockets/api.sock"
	}

	// Remove socket anterior (restart do container não deixa arquivo órfão).
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen unix: %v", err)
	}

	// Permissão 0666 para que o LB (outro usuário/grupo no container) consiga conectar.
	os.Chmod(socketPath, 0666)

	// /sockets/ready criado APÓS net.Listen, garante que quando o LB
	// detectar o sinal de pronto, o socket já existe e aceita conexões.
	// Criar antes causaria race condition: LB tentaria conectar num socket
	// que ainda não existe e receberia "connection refused", gerando 502.
	if err := os.WriteFile("/sockets/ready", []byte("ok"), 0644); err != nil {
		log.Fatalf("Erro ao criar arquivo ready: %v", err)
	}

	log.Printf("API pronta em %s (GOMAXPROCS=%d)", socketPath, runtime.GOMAXPROCS(0))
	log.Fatal(http.Serve(ln, h))
}
