package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/rlevidev/rinha-2026/internal/fraud"
	"github.com/rlevidev/rinha-2026/internal/index"
)

func main() {
	if gmp := os.Getenv("GOMAXPROCS"); gmp != "" {
		if n, err := strconv.Atoi(gmp); err == nil && n > 0 {
			runtime.GOMAXPROCS(n)
		}
	} else {
		runtime.GOMAXPROCS(1)
	}

	normalizer, err := fraud.LoadNormalizer("/resources/normalization.json", "/resources/mcc_risk.json")
	if err != nil {
		log.Fatalf("load normalizer: %v", err)
	}

	indexDir := os.Getenv("INDEX_DIR")
	if indexDir == "" {
		indexDir = "/index"
	}
	partitions, err := index.LoadSet(indexDir)
	if err != nil {
		log.Fatalf("load index set: %v", err)
	}

	socketPath := os.Getenv("SOCKET_PATH")
	if socketPath == "" {
		socketPath = "/sockets/api.sock"
	}
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen unix: %v", err)
	}
	_ = os.Chmod(socketPath, 0o666)

	handler := &fraud.Handler{
		Indexes:    partitions,
		Normalizer: normalizer,
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
	}

	log.Printf("api ready on %s", socketPath)
	log.Fatal(srv.Serve(ln))
}
