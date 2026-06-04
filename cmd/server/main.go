package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/rlevidev/rinha-2026/internal/index"
	"github.com/rlevidev/rinha-2026/internal/vectorize"
)

var (
	ready     int32
	idx       *index.Set
	mccRisk   vectorize.MCCRisk
	responses = [6][]byte{
		[]byte(`{"approved":true,"fraud_score":0.0}`),
		[]byte(`{"approved":true,"fraud_score":0.2}`),
		[]byte(`{"approved":true,"fraud_score":0.4}`),
		[]byte(`{"approved":false,"fraud_score":0.6}`),
		[]byte(`{"approved":false,"fraud_score":0.8}`),
		[]byte(`{"approved":false,"fraud_score":1.0}`),
	}
)

type Request struct {
	Transaction struct {
		Amount       float64   `json:"amount"`
		Installments int       `json:"installments"`
		RequestedAt  time.Time `json:"requested_at"`
	} `json:"transaction"`
	Customer struct {
		AvgAmount      float64  `json:"avg_amount"`
		TxCount24h     int      `json:"tx_count_24h"`
		KnownMerchants []string `json:"known_merchants"`
	} `json:"customer"`
	Merchant struct {
		ID        string  `json:"id"`
		MCC       string  `json:"mcc"`
		AvgAmount float64 `json:"avg_amount"`
	} `json:"merchant"`
	Terminal struct {
		IsOnline    bool    `json:"is_online"`
		CardPresent bool    `json:"card_present"`
		KmFromHome  float64 `json:"km_from_home"`
	} `json:"terminal"`
	LastTransaction *struct {
		Timestamp     time.Time `json:"timestamp"`
		KmFromCurrent float64   `json:"km_from_current"`
	} `json:"last_transaction"`
}

func main() {
	if len(os.Args) < 3 {
		panic("usage: server <unix_socket> <index_dir>")
	}
	socketPath := os.Args[1]
	indexDir := os.Args[2]

	// Load MCCRisk
	mccFile, err := os.Open("resources/mcc_risk.json")
	if err != nil {
		panic(err)
	}
	defer mccFile.Close()
	json.NewDecoder(mccFile).Decode(&mccRisk)

	// Load Index
	idx, err = index.LoadSet(indexDir)
	if err != nil {
		panic(err)
	}
	atomic.StoreInt32(&ready, 1)

	// Unix socket
	os.Remove(socketPath)
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(handler),
	}
	srv.Serve(l)
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ready" {
		if atomic.LoadInt32(&ready) == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}

	if r.URL.Path == "/fraud-score" {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Write(responses[0])
			return
		}

		tx := vectorize.Transaction{
			Amount:       req.Transaction.Amount,
			Installments: req.Transaction.Installments,
			RequestedAt:  req.Transaction.RequestedAt,
			CustomerAvg:  req.Customer.AvgAmount,
			TxCount24h:   req.Customer.TxCount24h,
			KnownMerchants: req.Customer.KnownMerchants,
			MerchantID:   req.Merchant.ID,
			MerchantMCC:  req.Merchant.MCC,
			MerchantAvg:  req.Merchant.AvgAmount,
			IsOnline:     req.Terminal.IsOnline,
			CardPresent:  req.Terminal.CardPresent,
			KmFromHome:   req.Terminal.KmFromHome,
		}

		if req.LastTransaction != nil {
			tx.HasLastTx = true
			tx.LastTxMinutesAgo = req.Transaction.RequestedAt.Sub(req.LastTransaction.Timestamp).Minutes()
			tx.LastTxKmFromCurrent = req.LastTransaction.KmFromCurrent
		}

		vec := vectorize.Vectorize(tx, mccRisk)
		tag := calculateTag(vec)
		score := idx.Search(vec, tag)
		if score >= 6 {
			score = 5 // Safety cap
		}

		w.Write(responses[score])
		return
	}
}

func calculateTag(v [14]float32) uint8 {
	t := uint8(0)
	if v[5] != -1 { t |= 1 }
	if v[11] > 0.5 { t |= 2 }
	if v[9] > 0.5 { t |= 4 }
	if v[10] > 0.5 { t |= 8 }
	return t
}
