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
	"sync"
	"time"

	"github.com/rlevidev/rinha-2026/internal/index"
	"github.com/rlevidev/rinha-2026/internal/vectorize"
	"golang.org/x/sys/unix"
)

var readerPool = sync.Pool{
	New: func() interface{} {
		return bufio.NewReaderSize(nil, 4096)
	},
}

// request JSON structures matching the nested payload format
type fraudScoreRequest struct {
	Transaction transactionData `json:"transaction"`
	Customer    customerData    `json:"customer"`
	Merchant    merchantData    `json:"merchant"`
	Terminal    terminalData    `json:"terminal"`
	LastTx      *lastTxData     `json:"last_transaction"`
}

type transactionData struct {
	Amount       float64 `json:"amount"`
	Installments int     `json:"installments"`
	RequestedAt  string  `json:"requested_at"`
}

type customerData struct {
	AvgAmount      float64  `json:"avg_amount"`
	TxCount24h     int      `json:"tx_count_24h"`
	KnownMerchants []string `json:"known_merchants"`
}

type merchantData struct {
	ID        string  `json:"id"`
	MCC       string  `json:"mcc"`
	AvgAmount float64 `json:"avg_amount"`
}

type terminalData struct {
	IsOnline    bool    `json:"is_online"`
	CardPresent bool    `json:"card_present"`
	KmFromHome  float64 `json:"km_from_home"`
}

type lastTxData struct {
	Timestamp     string  `json:"timestamp"`
	KmFromCurrent float64 `json:"km_from_current"`
}

func parseRequestBody(body []byte) (vectorize.Transaction, error) {
	var req fraudScoreRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return vectorize.Transaction{}, err
	}

	tx := vectorize.Transaction{
		Amount:         req.Transaction.Amount,
		Installments:   req.Transaction.Installments,
		CustomerAvg:    req.Customer.AvgAmount,
		TxCount24h:     req.Customer.TxCount24h,
		KnownMerchants: req.Customer.KnownMerchants,
		MerchantID:     req.Merchant.ID,
		MerchantMCC:    req.Merchant.MCC,
		MerchantAvg:    req.Merchant.AvgAmount,
		IsOnline:       req.Terminal.IsOnline,
		CardPresent:    req.Terminal.CardPresent,
		KmFromHome:     req.Terminal.KmFromHome,
	}

	t, err := time.Parse(time.RFC3339, req.Transaction.RequestedAt)
	if err == nil {
		tx.RequestedAt = t
	}

	if req.LastTx != nil {
		tx.HasLastTx = true
		lastTxTime, err := time.Parse(time.RFC3339, req.LastTx.Timestamp)
		if err == nil {
			tx.LastTxMinutesAgo = t.Sub(lastTxTime).Minutes()
		}
		tx.LastTxKmFromCurrent = req.LastTx.KmFromCurrent
	}

	return tx, nil
}

var fullResponses [6][]byte

func init() {
	bodies := [6]string{
		`{"approved":true,"fraud_score":0.0}`,
		`{"approved":true,"fraud_score":0.2}`,
		`{"approved":true,"fraud_score":0.4}`,
		`{"approved":false,"fraud_score":0.6}`,
		`{"approved":false,"fraud_score":0.8}`,
		`{"approved":false,"fraud_score":1.0}`,
	}
	statusLine := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "
	for i, body := range bodies {
		header := fmt.Sprintf("%s%d\r\n\r\n", statusLine, len(body))
		fullResponses[i] = append([]byte(header), body...)
	}
}

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <unix_socket_path> <index_dir>", os.Args[0])
	}
	socketPath := os.Args[1]
	indexDir := os.Args[2]

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
		log.Fatalf("Bind error on %s: %v", socketPath, err)
	}
	if err := unix.Listen(fd, 128); err != nil {
		log.Fatalf("Listen error: %v", err)
	}

	fmt.Printf("Server listening on %s\n", socketPath)

	// Accept one control connection from LB and keep receiving FDs
	for {
		ctrlFd, _, err := unix.Accept(fd)
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		// Loop receiving FDs from this control connection
		oob := make([]byte, 1024)
		buf := make([]byte, 1)
		for {
			_, oobn, _, _, err := unix.Recvmsg(ctrlFd, buf, oob, 0)
			if err != nil {
				log.Printf("Recvmsg error (LB disconnected?): %v", err)
				unix.Close(ctrlFd)
				break
			}

			msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
			if err != nil || len(msgs) == 0 {
				continue
			}

			receivedFds, err := unix.ParseUnixRights(&msgs[0])
			if err != nil || len(receivedFds) == 0 {
				continue
			}

			go handleConn(receivedFds[0], idx, mccRisk)
		}
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

	reader := readerPool.Get().(*bufio.Reader)
	reader.Reset(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		readerPool.Put(reader)
		return
	}
	defer req.Body.Close()
	defer readerPool.Put(reader)

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

		tx, err := parseRequestBody(body)
		if err != nil {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
			return
		}

		// Inline pipeline
		floatVector := vectorize.Vectorize(tx, mccRisk)
		intVector := index.Quantize(floatVector)

		// Calculate tag
		var tag byte
		if tx.CardPresent {
			tag |= (1 << 3)
		}
		if tx.IsOnline {
			tag |= (1 << 2)
		}
		isKnown := false
		for _, m := range tx.KnownMerchants {
			if m == tx.MerchantID {
				isKnown = true
				break
			}
		}
		if !isKnown {
			tag |= (1 << 1)
		}
		if tx.HasLastTx {
			tag |= 1
		}

		partition := idx.FindPartition(tag)
		fraudCount := partition.Search(&intVector)

		respIdx := 0
		if fraudCount == 0 {
			respIdx = 0
		} else if fraudCount == 1 {
			respIdx = 1
		} else if fraudCount == 2 {
			respIdx = 2
		} else if fraudCount == 3 {
			respIdx = 3
		} else if fraudCount == 4 {
			respIdx = 4
		} else {
			respIdx = 5
		}

		conn.Write(fullResponses[respIdx])
		return
	}

	conn.Write([]byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"))
}
