package httpx

import (
	"bytes"
	"io"
	"net"
	"testing"

	"github.com/rlevidev/rinha-2026/internal/fraud"
	"github.com/rlevidev/rinha-2026/internal/index"
)

var (
	fakeIndexes    = &index.Set{}
	fakeNormalizer = &fraud.Normalizer{}
)

func TestServeConnFraudScore(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	h := &fraud.Handler{Indexes: fakeIndexes, Normalizer: fakeNormalizer}
	done := make(chan struct{})
	go func() {
		ServeConn(server, h)
		close(done)
	}()

	req := []byte("POST /fraud-score HTTP/1.1\r\nContent-Length: 2\r\n\r\n{}")
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
