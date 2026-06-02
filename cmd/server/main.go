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
	"time"

	"github.com/rlevidev/rinha-2026/internal/index"
	"github.com/rlevidev/rinha-2026/internal/vectorize"
)

var (
	mccRisk           vectorize.MCCRisk
	normalizationConstants vectorize.NormalizationConstants
	indexSet          *index.Set
	indexLoaded       bool
)

// Pre-rendered responses to avoid fmt.Sprintf or json.Marshal in the hot path
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

	unixSocketPath := os.Args[1]
	indexDir := os.Args[2]

	// Remove old socket if it exists
	if err := os.RemoveAll(unixSocketPath); err != nil {
		log.Fatalf("Error removing old socket: %v", err)
	}

	listener, err := net.Listen("unix", unixSocketPath)
	if err != nil {
		log.Fatalf("Error listening on unix socket %s: %v", unixSocketPath, err)
	}
	defer listener.Close()

	log.Printf("Listening on Unix socket: %s", unixSocketPath)

	// Load MCC Risk and Normalization Constants
	err = loadConfig()
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}
	log.Println("Configuration loaded.")

	// Load index in a goroutine
	go func() {
		log.Printf("Loading index from %s...", indexDir)
		loadedIndex, loadErr := index.LoadSet(indexDir)
		if loadErr != nil {
			log.Fatalf("Error loading index: %v", loadErr)
		}
		indexSet = loadedIndex
		indexLoaded = true
		log.Println("Index loaded successfully.")
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

func loadConfig() error {
	// Load mcc_risk.json
	mccRiskBytes, err := os.ReadFile("/resources/mcc_risk.json")
	if err != nil {
		return fmt.Errorf("error reading mcc_risk.json: %w", err)
	}
	if err := json.Unmarshal(mccRiskBytes, &mccRisk); err != nil {
		return fmt.Errorf("error unmarshaling mcc_risk.json: %w", err)
	}

	// Load normalization.json
	normalizationBytes, err := os.ReadFile("/resources/normalization.json")
	if err != nil {
		return fmt.Errorf("error reading normalization.json: %w", err)
	}
	if err := json.Unmarshal(normalizationBytes, &normalizationConstants); err != nil {
		return fmt.Errorf("error unmarshaling normalization.json: %w", err)
	}
	return nil
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		log.Printf("Error reading HTTP request: %v", err)
		return
	}

	switch req.URL.Path {
	case "/ready":
		handleReady(conn, req)
	case "/fraud-score":
		handleFraudScore(conn, req)
	default:
		sendResponse(conn, http.StatusNotFound, []byte("404 Not Found"))
	}
}

func handleReady(conn net.Conn, req *http.Request) {
	if req.Method != http.MethodGet {
		sendResponse(conn, http.StatusMethodNotAllowed, []byte("Method Not Allowed"))
		return
	}

	if indexLoaded {
		sendResponse(conn, http.StatusOK, []byte("OK"))
	} else {
		sendResponse(conn, http.StatusServiceUnavailable, []byte("Loading index..."))
	}
}

func handleFraudScore(conn net.Conn, req *http.Request) {
	if req.Method != http.MethodPost {
		sendResponse(conn, http.StatusMethodNotAllowed, []byte("Method Not Allowed"))
		return
	}

	if !indexLoaded {
		sendResponse(conn, http.StatusServiceUnavailable, []byte("Index not loaded yet"))
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		sendDefaultFraudResponse(conn)
		return
	}

	var txPayload struct {
		ID          string `json:"id"`
		Transaction struct {
			Amount      float64 `json:"amount"`
			Installments int     `json:"installments"`
			RequestedAt  string  `json:"requested_at"`
		} `json:"transaction"`
		Customer struct {
			AvgAmount      float64  `json:"avg_amount"`
			TxCount24h     int      `json:"tx_count_24h"`
			KnownMerchants []string `json:"known_merchants"`
		} `json:"customer"`
		Merchant struct {
			ID          string  `json:"id"`
			MCC         string  `json:"mcc"`
			AvgAmount   float64 `json:"avg_amount"`
		} `json:"merchant"`
		Terminal struct {
			IsOnline   bool    `json:"is_online"`
			CardPresent bool    `json:"card_present"`
			KmFromHome float64 `json:"km_from_home"`
		} `json:"terminal"`
		LastTransaction *struct {
			Timestamp     string  `json:"timestamp"`
			KmFromCurrent float64 `json:"km_from_current"`
		} `json:"last_transaction"`
	}

	if err := json.Unmarshal(body, &txPayload); err != nil {
		log.Printf("Error unmarshaling transaction payload: %v", err)
		sendDefaultFraudResponse(conn)
		return
	}

	// Convert payload to vectorize.Transaction
	requestedAt, err := time.Parse(time.RFC3339, txPayload.Transaction.RequestedAt)
	if err != nil {
		log.Printf("Error parsing requested_at: %v", err)
		sendDefaultFraudResponse(conn)
		return
	}

	var lastTx *vectorize.LastTransaction
	if txPayload.LastTransaction != nil {
		lastTxTimestamp, parseErr := time.Parse(time.RFC3339, txPayload.LastTransaction.Timestamp)
		if parseErr != nil {
			log.Printf("Error parsing last_transaction.timestamp: %v", parseErr)
			sendDefaultFraudResponse(conn)
			return
		}
		lastTx = &vectorize.LastTransaction{
			Timestamp:     lastTxTimestamp,
			KmFromCurrent: txPayload.LastTransaction.KmFromCurrent,
		}
	}

	tx := vectorize.Transaction{
		ID:              txPayload.ID,
		Amount:          txPayload.Transaction.Amount,
		Installments:    txPayload.Transaction.Installments,
		RequestedAt:     requestedAt,
		CustomerAvg:     txPayload.Customer.AvgAmount,
		TxCount24h:      txPayload.Customer.TxCount24h,
		KnownMerchants:  txPayload.Customer.KnownMerchants,
		MerchantID:      txPayload.Merchant.ID,
		MerchantMCC:     txPayload.Merchant.MCC,
		MerchantAvg:     txPayload.Merchant.AvgAmount,
		IsOnline:        txPayload.Terminal.IsOnline,
		CardPresent:     txPayload.Terminal.CardPresent,
		KmFromHome:      txPayload.Terminal.KmFromHome,
		LastTransaction: lastTx,
	}

	vector := vectorize.Vectorize(tx, mccRisk, normalizationConstants)

	// Calculate tag for partitioning
	t := uint8(0)
	if vector[5] != -1 { t |= 1 }          // has_last_tx (bit 0)
	if vector[11] > 0.5 { t |= 2 }         // unknown_merchant (bit 1)
	if vector[9] > 0.5 { t |= 4 }          // is_online (bit 2)
	if vector[10] > 0.5 { t |= 8 }         // card_present (bit 3)

	fraudCount := indexSet.Search(vector, t)

	fraudScore := float32(fraudCount) / float32(index.NumNearest)

	var responseBytes []byte
	if fraudScore < index.FraudThreshold {
		// Approved
		switch fraudCount {
		case 0:
			responseBytes = responses[0] // 0.0
		case 1:
			responseBytes = responses[1] // 0.2
		case 2:
			responseBytes = responses[2] // 0.4
		default:
			// Should not happen for fraudScore < FraudThreshold (0.6)
			// If fraudCount is 3, score is 0.6, so it would be !approved
			// But for safety, send a default approved response
			responseBytes = responses[0] // Fallback to 0.0
		}
	} else {
		// Not approved
		switch fraudCount {
		case 3:
			responseBytes = responses[3] // 0.6
		case 4:
			responseBytes = responses[4] // 0.8
		case 5:
			responseBytes = responses[5] // 1.0
		default:
			// Should not happen for fraudScore >= FraudThreshold (0.6)
			// If fraudCount is 0,1,2, score is < 0.6, so it would be approved
			// But for safety, send a default not approved response
			responseBytes = responses[5] // Fallback to 1.0
		}
	}

	sendResponse(conn, http.StatusOK, responseBytes)
}

func sendResponse(conn net.Conn, statusCode int, body []byte) {
	statusText := http.StatusText(statusCode)
	if statusText == "" {
		statusText = "Unknown Status"
	}
	response := fmt.Sprintf(
		"HTTP/1.1 %d %s\r\n"+
			"Content-Type: application/json\r\n"+
			"Content-Length: %d\r\n"+
			"\r\n"+
			"%s",
		statusCode, statusText, len(body), body,
	)
	_, err := conn.Write([]byte(response))
	if err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

func sendDefaultFraudResponse(conn net.Conn) {
	// "approved":true,"fraud_score":0.0 corresponds to fraudCount = 0 or 1 or 2 with fraudScore < 0.6.
	// We want to return { "approved": true, "fraud_score": 0.0 } as per plan: "It is better a false positive than a HTTP error."
	sendResponse(conn, http.StatusOK, responses[0])
}
