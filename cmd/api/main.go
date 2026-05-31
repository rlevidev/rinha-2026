package main

import (
	"log"
	"math/rand"
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
	// GOMAXPROCS: configura paralelismo para casar com o limite de CPU do container.
	// Com cpus="0.45", o padrão seria 4 (NumCPU do host), causando context switching.
	// GOMAXPROCS=2 usa 2 threads OS para 0.45 CPU — equilíbrio entre paralelismo e contenção.
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
	log.Printf("Índice carregado. Ef=%d, M=%d, nodes=%d", index.Ef, index.M, index.NumNodes())

	norm, err := vectorizer.LoadNormalizer(
		"/resources/normalization.json",
		"/resources/mcc_risk.json",
	)
	if err != nil {
		log.Fatalf("Erro ao carregar normalizer: %v", err)
	}

	// Warmup: faz 200 buscas com vetores aleatórios antes de sinalizar pronto.
	// Isso aquece o page cache do kernel para as regiões críticas do índice,
	// evitando page faults durante o teste real que causariam picos de latência.
	log.Printf("Aquecendo page cache com 200 buscas dummy...")
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		var q [14]float32
		for j := range q {
			q[j] = rng.Float32()
		}
		index.KNN5(q)
	}
	log.Printf("Warmup concluído.")

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
