package vectorize

import "time"

type MCCRisk map[string]float32

type Transaction struct {
	Amount              float64
	Installments        int
	RequestedAt         time.Time
	CustomerAvg         float64
	TxCount24h          int
	KnownMerchants      []string
	MerchantID          string
	MerchantMCC         string
	MerchantAvg         float64
	IsOnline            bool
	CardPresent         bool
	KmFromHome          float64
	HasLastTx           bool
	LastTxMinutesAgo    float64
	LastTxKmFromCurrent float64
}

func Vectorize(tx Transaction, mcc MCCRisk) [14]float32 {
	var vec [14]float32
	vec[0] = clamp(tx.Amount / 10000)
	vec[1] = clamp(float64(tx.Installments) / 12)
	vec[2] = clamp((tx.Amount / tx.CustomerAvg) / 10)

	t := tx.RequestedAt.UTC()
	vec[3] = float32(t.Hour()) / 23
	wd := (int(t.Weekday()) + 6) % 7
	vec[4] = float32(wd) / 6

	if tx.HasLastTx {
		vec[5] = clamp(tx.LastTxMinutesAgo / 1440)
		vec[6] = clamp(tx.LastTxKmFromCurrent / 1000)
	} else {
		vec[5] = -1
		vec[6] = -1
	}

	vec[7] = clamp(tx.KmFromHome / 1000)
	vec[8] = clamp(float64(tx.TxCount24h) / 20)

	if tx.IsOnline {
		vec[9] = 1.0
	} else {
		vec[9] = 0.0
	}
	if tx.CardPresent {
		vec[10] = 1.0
	} else {
		vec[10] = 0.0
	}

	isUnknown := true
	for _, m := range tx.KnownMerchants {
		if m == tx.MerchantID {
			isUnknown = false
			break
		}
	}
	if isUnknown {
		vec[11] = 1.0
	} else {
		vec[11] = 0.0
	}

	if val, ok := mcc[tx.MerchantMCC]; ok {
		vec[12] = val
	} else {
		vec[12] = 0.5
	}

	vec[13] = clamp(tx.MerchantAvg / 10000)

	return vec
}

func clamp(v float64) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return float32(v)
}
