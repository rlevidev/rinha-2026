package fraud

import (
	"io"
	"net/http"

	"github.com/rlevidev/rinha-2026/internal/index"
)

var (
	okResponses = [6][]byte{
		[]byte(`{"approved":true,"fraud_score":0.0}`),
		[]byte(`{"approved":true,"fraud_score":0.2}`),
		[]byte(`{"approved":true,"fraud_score":0.4}`),
		[]byte(`{"approved":false,"fraud_score":0.6}`),
		[]byte(`{"approved":false,"fraud_score":0.8}`),
		[]byte(`{"approved":false,"fraud_score":1.0}`),
	}
	fallbackResponse = okResponses[0]
)

// Handler serves GET /ready and POST /fraud-score on a unix socket listener.
type Handler struct {
	Indexes    *index.Set
	Normalizer *Normalizer
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/ready":
		w.WriteHeader(http.StatusOK)
	case "/fraud-score":
		h.handleFraudScore(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *Handler) handleFraudScore(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	defer func() {
		if rec := recover(); rec != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fallbackResponse)
		}
	}()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fallbackResponse)
		return
	}

	var req Request
	if !ParseRequest(body, &req) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fallbackResponse)
		return
	}

	tag := PartitionTag(&req)
	ix := h.Indexes.ForTag(tag)
	if ix == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fallbackResponse)
		return
	}

	query := Vectorize(&req, h.Normalizer)
	fraudCount := ix.Search(&query)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(okResponses[fraudCount])
}
