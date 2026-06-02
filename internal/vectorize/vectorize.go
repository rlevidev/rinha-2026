package vectorize

import (
	"math"
	"time"
)

// MCCRisk is a map of MCC code → risk score (carregado de mcc_risk.json)
type MCCRisk map[string]float32

// NormalizationConstants holds the constants for vector normalization
type NormalizationConstants struct {
	MaxAmount            float64 `json:"max_amount"`
	MaxInstallments      float64 `json:"max_installments"`
	AmountVsAvgRatio     float64 `json:"amount_vs_avg_ratio"`
	MaxMinutes           float64 `json:"max_minutes"`
	MaxKm                float64 `json:"max_km"`
	MaxTxCount24h        float64 `json:"max_tx_count_24h"`
	MaxMerchantAvgAmount float64 `json:"max_merchant_avg_amount"`
}

// Transaction represents the payload da requisição
type Transaction struct {
	ID              string    `json:"id"`
	Amount          float64   `json:"amount"`
	Installments    int       `json:"installments"`
	RequestedAt     time.Time `json:"requested_at"`
	CustomerAvg     float64   `json:"customer_avg_amount"`
	TxCount24h      int       `json:"tx_count_24h"`
	KnownMerchants  []string  `json:"known_merchants"`
	MerchantID      string    `json:"merchant_id"`
	MerchantMCC     string    `json:"mcc"`
	MerchantAvg     float64   `json:"merchant_avg_amount"`
	IsOnline        bool      `json:"is_online"`
	CardPresent     bool      `json:"card_present"`
	KmFromHome      float64   `json:"km_from_home"`
	LastTransaction *LastTransaction `json:"last_transaction"` // Pointer to handle null
}

// LastTransaction represents the last transaction details, can be null
type LastTransaction struct {
	Timestamp     time.Time `json:"timestamp"`
	KmFromCurrent float64   `json:"km_from_current"`
}

// Vectorize converte uma Transaction em vetor de 14 dimensões
func Vectorize(tx Transaction, mccRisk MCCRisk, constants NormalizationConstants) [14]float32 {
	var vector [14]float32

	vector[0] = Clamp(tx.Amount / constants.MaxAmount)

	// 1: installments           | Clamp(transaction.installments / max_installments)
	vector[1] = Clamp(float64(tx.Installments) / constants.MaxInstallments)

	// 2: amount_vs_avg          | Clamp((transaction.amount / customer.avg_amount) / amount_vs_avg_ratio)
	var amountVsCustomerAvg float64
	if tx.CustomerAvg != 0 { // Avoid division by zero
		amountVsCustomerAvg = tx.Amount / tx.CustomerAvg
	}
	vector[2] = Clamp(amountVsCustomerAvg / constants.AmountVsAvgRatio)

	// 3: hour_of_day            | hora(transaction.requested_at) / 23  (0-23, UTC)
	vector[3] = float32(tx.RequestedAt.UTC().Hour()) / 23.0

	// 4: day_of_week            | dia_da_semana(transaction.requested_at) / 6    (seg=0, dom=6)
	// Go's time.Weekday() returns Sunday=0, Monday=1, ..., Saturday=6
	// Spec uses Monday=0, ..., Sunday=6. Conversion: (weekday + 6) % 7
	weekday := tx.RequestedAt.UTC().Weekday()
	vector[4] = float32((int(weekday) + 6) % 7) / 6.0

	// 5: minutes_since_last_tx  | Clamp(minutos / max_minutes) ou -1 se last_transaction: null
	// 6: km_from_last_tx        | Clamp(last_transaction.km_from_current / max_km) ou -1 se last_transaction: null
	if tx.LastTransaction != nil {
		minutes := tx.RequestedAt.UTC().Sub(tx.LastTransaction.Timestamp.UTC()).Minutes()
		vector[5] = Clamp(minutes / constants.MaxMinutes)
		vector[6] = Clamp(tx.LastTransaction.KmFromCurrent / constants.MaxKm)
	} else {
		vector[5] = -1.0
		vector[6] = -1.0
	}

	// 7: km_from_home           | Clamp(terminal.km_from_home / max_km)
	vector[7] = Clamp(tx.KmFromHome / constants.MaxKm)

	// 8: tx_count_24h           | Clamp(customer.tx_count_24h / max_tx_count_24h)
	vector[8] = Clamp(float64(tx.TxCount24h) / constants.MaxTxCount24h)

	// 9: is_online              | 1 se terminal.is_online, senão 0
	if tx.IsOnline {
		vector[9] = 1.0
	} else {
		vector[9] = 0.0
	}

	// 10: card_present          | 1 se terminal.card_present, senão 0
	if tx.CardPresent {
		vector[10] = 1.0
	} else {
		vector[10] = 0.0
	}

	// 11: unknown_merchant       | 1 se merchant.id NÃO estiver em customer.known_merchants, senão 0 (invertido: 1 = desconhecido)
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

	// 12: mcc_risk               | mcc_risk.json[merchant.mcc] (valor padrão 0.5)
	if risk, ok := mccRisk[tx.MerchantMCC]; ok {
		vector[12] = risk
	} else {
		vector[12] = 0.5 // Default value
	}

	// 13: merchant_avg_amount    | Clamp(merchant.avg_amount / max_merchant_avg_amount)
	vector[13] = Clamp(tx.MerchantAvg / constants.MaxMerchantAvgAmount)

	return vector
}

// clamp limits the value to the interval [0.0, 1.0]
func Clamp(v float64) float32 {
	return float32(math.Min(1.0, math.Max(0.0, v)))
}
