package vectorize

import (
	"math"
	"time"
)

// MCCRisk is a map of MCC code → risk score (loaded from mcc_risk.json)
type MCCRisk map[string]float32

// Transaction represents the request payload
type Transaction struct {
	Amount             float64
	Installments       int
	RequestedAt        time.Time
	CustomerAvg        float64
	TxCount24h         int
	KnownMerchants     []string
	MerchantID         string
	MerchantMCC        string
	MerchantAvg        float64
	IsOnline           bool
	CardPresent        bool
	KmFromHome         float64
	HasLastTx          bool
	LastTxMinutesAgo   float64 // only if HasLastTx=true
	LastTxKmFromCurrent float64 // only if HasLastTx=true
}

// clamp applies min(1.0, max(0.0, x))
func clamp(v float64) float32 {
	return float32(math.Min(1.0, math.Max(0.0, v)))
}

// Vectorize converts a Transaction into a 14-dimension float32 vector
func Vectorize(tx Transaction, mcc MCCRisk) [14]float32 {
	var vector [14]float32

	// 0: amount
	vector[0] = clamp(tx.Amount / 10000.0)

	// 1: installments
	vector[1] = clamp(float64(tx.Installments) / 12.0)

	// 2: amount_vs_avg
	amountVsAvg := 0.0
	if tx.CustomerAvg > 0 {
		amountVsAvg = (tx.Amount / tx.CustomerAvg) / 10.0
	}
	vector[2] = clamp(amountVsAvg)

	// 3: hour_of_day (UTC)
	vector[3] = float32(tx.RequestedAt.UTC().Hour()) / 23.0

	// 4: day_of_week (UTC, Monday=0, Sunday=6)
	// Go's time.Weekday() returns Sunday=0, Monday=1, ..., Saturday=6
	// Convert: (weekday + 6) % 7
	vector[4] = float32((tx.RequestedAt.UTC().Weekday() + 6) % 7) / 6.0

	// 5: minutes_since_last_tx
	if tx.HasLastTx {
		vector[5] = clamp(tx.LastTxMinutesAgo / 1440.0)
	} else {
		vector[5] = -1.0
	}

	// 6: km_from_last_tx
	if tx.HasLastTx {
		vector[6] = clamp(tx.LastTxKmFromCurrent / 1000.0)
	} else {
		vector[6] = -1.0
	}

	// 7: km_from_home
	vector[7] = clamp(tx.KmFromHome / 1000.0)

	// 8: tx_count_24h
	vector[8] = clamp(float64(tx.TxCount24h) / 20.0)

	// 9: is_online
	if tx.IsOnline {
		vector[9] = 1.0
	} else {
		vector[9] = 0.0
	}

	// 10: card_present
	if tx.CardPresent {
		vector[10] = 1.0
	} else {
		vector[10] = 0.0
	}

	// 11: unknown_merchant
	isKnownMerchant := false
	for _, km := range tx.KnownMerchants {
		if km == tx.MerchantID {
			isKnownMerchant = true
			break
		}
	}
	if isKnownMerchant {
		vector[11] = 0.0
	} else {
		vector[11] = 1.0
	}

	// 12: mcc_risk
	if risk, ok := mcc[tx.MerchantMCC]; ok {
		vector[12] = risk
	} else {
		vector[12] = 0.5 // Default value
	}

	// 13: merchant_avg_amount
	vector[13] = clamp(tx.MerchantAvg / 10000.0)

	return vector
}
