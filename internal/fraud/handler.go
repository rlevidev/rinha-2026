package fraud

import (
	"github.com/rlevidev/rinha-2026/internal/index"
)

var (
	okResponses = [6][]byte{
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.0}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.2}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.4}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":0.6}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":0.8}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":1.0}"),
	}
	fallbackResponse = okResponses[0]
	readyResponse    = []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
)

// Handler serves GET /ready and POST /fraud-score on a unix socket listener.
type Handler struct {
	Indexes    *index.Set
	Normalizer *Normalizer
}

func (h *Handler) Ready() []byte {
	return readyResponse
}

func (h *Handler) Fallback() []byte { return fallbackResponse }

func (h *Handler) Score(body []byte) []byte {
	var req Request
	if !ParseRequest(body, &req) {
		return fallbackResponse
	}

	tag := PartitionTag(&req)
	ix := h.Indexes.ForTag(tag)
	if ix == nil {
		return fallbackResponse
	}

	query := Vectorize(&req, h.Normalizer)
	fraudCount := ix.Search(&query)
	if fraudCount > 5 {
		return fallbackResponse
	}
	return okResponses[fraudCount]
}
