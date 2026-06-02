package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <tcp_port> <socket1> [socket2 ...]", os.Args[0])
	}

	tcpPortStr := os.Args[1]
	unixSocketPaths := os.Args[2:]

	tcpPort, err := strconv.Atoi(tcpPortStr)
	if err != nil {
		log.Fatalf("Invalid TCP port: %v", err)
	}

	// Wait for workers to be available
	waitForWorkers(unixSocketPaths)

	log.Printf("Starting TCP listener on :%d", tcpPort)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
	if err != nil {
		log.Fatalf("Error listening on TCP port %d: %v", tcpPort, err)
	}
	defer listener.Close()

	var nextWorkerIndex uint64

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting client connection: %v", err)
			continue
		}

		// Round-robin selection of worker
		workerIdx := atomic.AddUint64(&nextWorkerIndex, 1) % uint64(len(unixSocketPaths))
		workerSocketPath := unixSocketPaths[workerIdx]

		go handleClient(clientConn, workerSocketPath)
	}
}

func waitForWorkers(unixSocketPaths []string) {
	const maxRetries = 60 // 30 seconds with 500ms sleep
	const retryInterval = 500 * time.Millisecond

	log.Println("Waiting for workers to be available...")
	for _, socketPath := range unixSocketPaths {
		for i := 0; i < maxRetries; i++ {
			conn, err := net.Dial("unix", socketPath)
			if err == nil {
				conn.Close()
				log.Printf("Worker %s is available.", socketPath)
				break
			}
			if i == maxRetries-1 {
				log.Fatalf("Worker %s not available after multiple retries: %v", socketPath, err)
			}
			time.Sleep(retryInterval)
		}
	}
	log.Println("All workers are available.")
}

func handleClient(clientConn net.Conn, workerSocketPath string) {
	defer clientConn.Close()

	workerConn, err := net.Dial("unix", workerSocketPath)
	if err != nil {
		log.Printf("Error connecting to worker %s: %v", workerSocketPath, err)
		return
	}
	defer workerConn.Close()

	// Copy data bidirectionally
	done := make(chan struct{}, 2)
	go func() {
		_, err := io.Copy(workerConn, clientConn)
		if err != nil && err != io.EOF {
			log.Printf("Error copying from client to worker: %v", err)
		}
		done <- struct{}{}
	}()
	go func() {
		_, err := io.Copy(clientConn, workerConn)
		if err != nil && err != io.EOF {
			log.Printf("Error copying from worker to client: %v", err)
		}
		done <- struct{}{}
	}()

	<-done
	<-done
}
