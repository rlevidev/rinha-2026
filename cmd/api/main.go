package main

import (
	"log"
	"net"
	"os"
	"runtime"
	"strconv"

	"github.com/rlevidev/rinha-2026/internal/fraud"
	"github.com/rlevidev/rinha-2026/internal/httpx"
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

	log.Printf("api ready on %s", socketPath)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatalf("accept: %v", err)
		}
		go httpx.ServeConn(conn, handler)
	}
}
