package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/rlevidev/rinha-2026/internal/search"
	"github.com/rlevidev/rinha-2026/internal/vectorizer"
)

type FraudHandler struct {
	Index      *search.HNSW
	Normalizer *vectorizer.Normalizer
}

// fallback: aprovamos para evitar HTTP 500 (peso 5) ao custo de um FP (peso 1).
// Declarado como var de pacote para zero alocação — reutilizado em toda falha.
var fallback = []byte(`{"approved":true,"fraud_score":0.0}` + "\n")

// Respostas pré-calculadas para os 6 valores possíveis (0/5 a 5/5).
// Elimina json.Encode + reflection em todo request.
var responses = [6][]byte{
	[]byte(`{"approved":true,"fraud_score":0.0}` + "\n"),
	[]byte(`{"approved":true,"fraud_score":0.2}` + "\n"),
	[]byte(`{"approved":true,"fraud_score":0.4}` + "\n"),
	[]byte(`{"approved":false,"fraud_score":0.6}` + "\n"),
	[]byte(`{"approved":false,"fraud_score":0.8}` + "\n"),
	[]byte(`{"approved":false,"fraud_score":1.0}` + "\n"),
}

func (h *FraudHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/ready":
		if _, err := os.Stat("/sockets/ready"); err == nil {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	case "/fraud-score":
		h.handleFraudScore(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *FraudHandler) handleFraudScore(w http.ResponseWriter, r *http.Request) {
	// FIX #3a: drena o body em qualquer caminho de saída (decode ok, decode erro ou panic).
	// Sem drenagem, o servidor Go fecha a conexão TCP/UDS ao invés de reutilizá-la
	// via keep-alive, desperdiçando conexões sob carga e aumentando latência.
	defer io.Copy(io.Discard, r.Body) //nolint:errcheck

	// FIX #3b: recover captura panics do HNSW ou vectorizer.
	// Responde 200 OK com fallback em vez de 500 — conforme regra da Rinha
	// (HTTP 500 tem peso 5×, FP tem peso 1×).
	defer func() {
		if rec := recover(); rec != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(fallback) //nolint:errcheck
		}
	}()

	var tx vectorizer.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		// FIX #3c: WriteHeader explícito antes do Write.
		// Sem isso, o Go infere 200 implicitamente na primeira chamada a Write,
		// mas apenas após os headers serem enviados — se o status já tiver sido
		// escrito por outro caminho (ex: pelo defer de recover acima), o segundo
		// WriteHeader seria ignorado com um log de aviso. Ser explícito evita ambiguidade.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(fallback) //nolint:errcheck
		return
	}

	query := h.Normalizer.Vectorize(&tx)
	neighbors := h.Index.KNN5(query)

	fraudCount := 0
	for _, n := range neighbors {
		if n.IsFraud {
			fraudCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responses[fraudCount])
}
