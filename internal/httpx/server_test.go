package httpx

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/rlevidev/rinha-2026/internal/fraud"
	"github.com/rlevidev/rinha-2026/internal/index"
)

func TestServeConnFraudScore(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	set, err := index.LoadSet("../../index")
	if err != nil {
		t.Fatalf("LoadSet failed: %v", err)
	}
	norm, err := fraud.LoadNormalizer("../../resources/normalization.json", "../../resources/mcc_risk.json")
	if err != nil {
		t.Fatalf("LoadNormalizer failed: %v", err)
	}

	h := &fraud.Handler{Indexes: set, Normalizer: norm}
	go ServeConn(server, h)

	body := "{\n\t\"id\": \"tx-3330991687\",\n\t\"transaction\": {\"amount\": 9505.97, \"installments\": 10, \"requested_at\": \"2026-03-14T05:15:12Z\"},\n\t\"customer\": {\"avg_amount\": 81.28, \"tx_count_24h\": 20, \"known_merchants\": [\"MERC-008\", \"MERC-007\", \"MERC-005\"]},\n\t\"merchant\": {\"id\": \"MERC-068\", \"mcc\": \"7802\", \"avg_amount\": 54.86},\n\t\"terminal\": {\"is_online\": false, \"card_present\": true, \"km_from_home\": 952.27},\n\t\"last_transaction\": null\n}"
	req := []byte(fmt.Sprintf("POST /fraud-score HTTP/1.1\r\nContent-Length: %d\r\n\r\n%s", len(body), body))
	if _, err := client.Write(req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	resp, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Contains(resp, []byte("\"approved\":false")) {
		t.Fatalf("response = %q, want approved false", resp)
	}
	if !bytes.Contains(resp, []byte("\"fraud_score\":1.0")) {
		t.Fatalf("response = %q, want fraud_score 1.0", resp)
	}
}

func TestServeConnReady(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	h := &fraud.Handler{Indexes: &index.Set{}, Normalizer: &fraud.Normalizer{}}
	go ServeConn(server, h)

	req := []byte("GET /ready HTTP/1.1\r\n\r\n")
	if _, err := client.Write(req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	resp, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Contains(resp, []byte("200 OK")) {
		t.Fatalf("response = %q, want 200 OK", resp)
	}
}

func TestServeConnBadRequestFallsBack(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	h := &fraud.Handler{Indexes: &index.Set{}, Normalizer: &fraud.Normalizer{}}
	go ServeConn(server, h)

	req := []byte("POST /fraud-score HTTP/1.1\r\n\r\n")
	if _, err := client.Write(req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	resp, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Contains(resp, []byte("200 OK")) {
		t.Fatalf("response = %q, want 200 OK", resp)
	}
}
