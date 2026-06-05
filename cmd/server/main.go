package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/rlevidev/rinha-2026/internal/index"
	"github.com/rlevidev/rinha-2026/internal/vectorize"
	"golang.org/x/sys/unix"
)

var responses = [6][]byte{
	[]byte(`{"approved":true,"fraud_score":0.0}`),
	[]byte(`{"approved":true,"fraud_score":0.2}`),
	[]byte(`{"approved":true,"fraud_score":0.4}`),
	[]byte(`{"approved":false,"fraud_score":0.6}`),
	[]byte(`{"approved":false,"fraud_score":0.8}`),
	[]byte(`{"approved":false,"fraud_score":1.0}`),
}

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <unix_socket_path> <index_dir>", os.Args[0])
	}
	socketPath := os.Args[1]
	indexDir := os.Args[2]

	fmt.Printf("DEBUG: Server socketPath is: %s\n", socketPath)

	// Load index
	idx, err := index.LoadSet(indexDir)
	if err != nil {
		log.Fatalf("Failed to load index: %v", err)
	}

	// Load mcc_risk
	mccRiskFile, err := os.ReadFile("/resources/mcc_risk.json")
	if err != nil {
		log.Fatalf("Failed to load /resources/mcc_risk.json: %v", err)
	}
	var mccRisk vectorize.MCCRisk
	if err := json.Unmarshal(mccRiskFile, &mccRisk); err != nil {
		log.Fatalf("Failed to unmarshal mcc_risk: %v", err)
	}

	// Setup socket
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Failed to remove existing socket %s: %v", socketPath, err)
	}

	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		log.Fatalf("Socket error: %v", err)
	}
	defer unix.Close(fd)

	addr := &unix.SockaddrUnix{Name: socketPath}
	if err := unix.Bind(fd, addr); err != nil {
		log.Fatalf("Bind error: %v", err)
	}
	if err := unix.Listen(fd, 128); err != nil {
		log.Fatalf("Listen error: %v", err)
	}

	fmt.Printf("Server listening on %s\n", socketPath)

	// FD passing loop
	for {
		nfd, _, err := unix.Accept(fd)
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		
		// Receive FD from LB
		oob := make([]byte, 1024)
		buf := make([]byte, 1)
		_, oobn, _, _, err := unix.Recvmsg(nfd, buf, oob, 0)
		unix.Close(nfd)
		if err != nil {
			log.Printf("Recvmsg error: %v", err)
			continue
		}
		
		msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil || len(msgs) == 0 {
			log.Printf("Control message error: %v", err)
			continue
		}
		
		fds, err := unix.ParseUnixRights(&msgs[0])
		if err != nil || len(fds) == 0 {
			log.Printf("ParseUnixRights error: %v", err)
			continue
		}
		
		// Handle the FD (now in fds[0])
		go handleConn(fds[0], idx, mccRisk)
	}
}

func handleConn(fd int, idx *index.Set, mccRisk vectorize.MCCRisk) {
	f := os.NewFile(uintptr(fd), "socket")
	defer f.Close()
	conn, err := net.FileConn(f)
	if err != nil {
		return
	}
	defer conn.Close()
	
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	defer req.Body.Close()

	if req.URL.Path == "/ready" {
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
		return
	}

	if req.URL.Path == "/fraud-score" && req.Method == "POST" {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
			return
		}
		
		var tx vectorize.Transaction
		if err := json.Unmarshal(body, &tx); err != nil {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
			return
		}

		// Inline pipeline
		floatVector := vectorize.Vectorize(tx, mccRisk)
		intVector := index.Quantize(floatVector)
		
		// Calculate tag
		var tag byte
		if tx.CardPresent { tag |= (1 << 3) }
		if tx.IsOnline { tag |= (1 << 2) }
		isKnown := false
		for _, m := range tx.KnownMerchants {
			if m == tx.MerchantID { isKnown = true; break }
		}
		if !isKnown { tag |= (1 << 1) }
		if tx.HasLastTx { tag |= 1 }

		partition := idx.FindPartition(tag)
		fraudCount := partition.Search(&intVector)
		
		respIdx := 0
		if fraudCount == 0 { respIdx = 0 } else if fraudCount == 1 { respIdx = 1 } else if fraudCount == 2 { respIdx = 2 } else if fraudCount == 3 { respIdx = 3 } else if fraudCount == 4 { respIdx = 4 } else { respIdx = 5 }

		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "))
		conn.Write([]byte(fmt.Sprintf("%d", len(responses[respIdx]))))
		conn.Write([]byte("\r\n\r\n"))
		conn.Write(responses[respIdx])
		return
	}

	conn.Write([]byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"))
}
