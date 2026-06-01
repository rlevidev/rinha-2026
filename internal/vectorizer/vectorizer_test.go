package vectorizer

import (
	"encoding/json"
	"math"
	"testing"
)

// almostEqual compara dois vetores de float32 com uma tolerância de 0.0001
func almostEqual(a, b [14]float32) bool {
	// Percorre cada uma das 14 posições dos dois vetores
	for i := range a {
		// Calcula a distância entre os dois valores
		if math.Abs(float64(a[i]-b[i])) > 0.0001 {
			return false
		}
	}
	return true
}

func TestVectorize(t *testing.T) {
	// Valores extraidos de resources/normalization.json
	n := &Normalizer{
		MaxAmount:        10000,
		MaxInstallments:  12.0,
		AmountVsAvgRatio: 10.0,
		MaxMinutes:       1440.0,
		MaxKm:            1000.0,
		MaxTxCount24h:    20.0,
		MaxMerchantAvg:   10000.0,
		MccRisk: map[string]float32{
			"5411": 0.15, "5812": 0.30, "5912": 0.20, "5944": 0.45,
			"7801": 0.80, "7802": 0.75, "7995": 0.85, "4511": 0.35,
			"5311": 0.25, "5999": 0.50,
		},
	}

	// Caso de teste baseado no primeiro elemento de resource/example-payloads.json e o primeiro vetor de resource/example-references.json
	tx := &Transaction{}
	tx.Transaction.Amount = 41.12
	tx.Transaction.Installments = 2
	tx.Transaction.RequestedAt = "2026-03-11T18:45:53Z"
	tx.Customer.AvgAmount = 82.24
	tx.Customer.TxCount24h = 3
	tx.Customer.KnowMerchants = []string{"MERC-003", "MERC-016"}
	tx.Terminal.KmFromHome = 29.2331
	tx.Terminal.IsOnline = false
	tx.Terminal.CardPresent = true
	tx.Merchant.ID = "MERC-016"
	tx.Merchant.MCC = "5411"
	tx.Merchant.AvgAmount = 60.25

	// Define exatamente qual saída esperada receber.
	// O vetor foi calculado manualmente para garantir que o teste não falhe por um erro de cálculo.
	// Valores extraídos da execução real (float32). A tolerância de 0.0001 no almostEqual
	// cobre as pequenas diferenças de arredondamento entre float32 e float64.
	expected := [14]float32{0.004112, 0.16666667, 0.05, 0.7826087, 0.33333334, -1, -1, 0.029233102, 0.15, 0, 1, 0, 0.15, 0.006025}
	// 0.004112   - v[0]  amount / max_amount (41.12 / 10000)
	// 0.16666667 - v[1]  installments / max_installments (2 / 12)
	// 0.05       - v[2]  (amount / avg_amount) / amount_vs_avg_ratio (41.12/82.24/10)
	// 0.7826087  - v[3]  hour / 23 (18 / 23)
	// 0.33333334 - v[4]  weekday / 6 (seg=0, qua=2→2/6)
	// -1         - v[5]  last_transaction null → sentinel
	// -1         - v[6]  last_transaction null → sentinel
	// 0.029233102- v[7]  km_from_home / max_km (29.2331 / 1000)
	// 0.15       - v[8]  tx_count_24h / max_tx_count (3 / 20)
	// 0          - v[9]  is_online? false → 0
	// 1          - v[10] card_present? true → 1
	// 0          - v[11] unknown_merchant? MERC-016 is known → 0
	// 0.15       - v[12] mcc_risk["5411"] = 0.15
	// 0.006025   - v[13] merchant_avg / max_merchant_avg (60.25 / 10000)

	// Chama a função Vectorize() passando a transação de teste. O resultado é armazenado na variável got.
	got := n.Vectorize(tx)

	// Compara o resultado obtido (got) com o resultado esperado (expected).
	if !almostEqual(got, expected) {
		t.Errorf("Vectorize() = %v, want %v", got, expected)
	}
}

// TestVectorizeJSONParse verifica que a struct com tags json parseia corretamente.
func TestVectorizeJSONParse(t *testing.T) {
	payload := `{
		"id": "tx-1329056812",
		"transaction": {
			"amount": 41.12,
			"installments": 2,
			"requested_at": "2026-03-11T18:45:53Z"
		},
		"customer": {
			"avg_amount": 82.24,
			"tx_count_24h": 3,
			"known_merchants": ["MERC-003", "MERC-016"]
		},
		"merchant": {
			"id": "MERC-016",
			"mcc": "5411",
			"avg_amount": 60.25
		},
		"terminal": {
			"is_online": false,
			"card_present": true,
			"km_from_home": 29.2331
		},
		"last_transaction": null
	}`

	var tx Transaction
	if err := json.Unmarshal([]byte(payload), &tx); err != nil {
		t.Fatalf("Unmarshal falhou: %v", err)
	}

	if tx.Transaction.Amount != 41.12 {
		t.Errorf("amount: got %.2f want 41.12", tx.Transaction.Amount)
	}
	if tx.Customer.AvgAmount != 82.24 {
		t.Errorf("avg_amount: got %.2f want 82.24", tx.Customer.AvgAmount)
	}
	if tx.Customer.TxCount24h != 3 {
		t.Errorf("tx_count_24h: got %d want 3", tx.Customer.TxCount24h)
	}
	if len(tx.Customer.KnowMerchants) != 2 {
		t.Errorf("known_merchants: got %d want 2", len(tx.Customer.KnowMerchants))
	}
	if tx.Merchant.MCC != "5411" {
		t.Errorf("mcc: got %s want 5411", tx.Merchant.MCC)
	}
	if !tx.Terminal.CardPresent {
		t.Errorf("card_present: got false want true")
	}
	if tx.LastTransaction != nil {
		t.Errorf("last_transaction: expected nil, got non-nil")
	}
}
