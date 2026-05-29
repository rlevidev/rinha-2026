package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"github.com/rlevidev/rinha-2026/internal/handler"
	"github.com/rlevidev/rinha-2026/internal/search"
	"github.com/rlevidev/rinha-2026/internal/vectorizer"
)

func main() {
	// Carrega índice via mmap MAP_SHARED, compartilhado com a outra instância.
	// Duas instâncias mapeando o mesmo index.bin compartilham as mesmas páginas
	// físicas — ~280MB no kernel, ~140MB de RSS por container.
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

	log.Printf("API pronta em %s", socketPath)
	log.Fatal(http.Serve(ln, h))
}
