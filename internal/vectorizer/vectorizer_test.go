package vectorizer

import (
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

func TestVectoriz(t *testing.T) {
	// Valores extraidos de resources/normalization.json
	n := &Normalizer{
		MaxAmount:        10000,
		MaxInstallments:  12.0,
		AmountVsAvgRatio: 10.0,
		MaxMinutes: 	  1440.0,
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
	tx := &Transaction{
		Amount: 41.12,
		Installments: 2,
		RequestedAt: "2026-03-11T18:45:53Z",
		Customer: struct {
			AvgAmount float32
			TxCount24h int
			KnowMerchants []string
		}{
			AvgAmount: 83.24,
			TxCount24h: 3,
			KnowMerchants: []string{"MERC-003", "MERC-016"},
		},
		Terminal: struct {
			KmFromHome float64
			IsOnline bool
			CardPresent bool
		}{
			KmFromHome: 29.2331,
			IsOnline: false,
			CardPresent: true,
		},
		LastTransaction: nil,
		Merchant: struct {
			ID string
			MCC string
			AvgAmount float64
		}{
			ID: "MERC-016",
			MCC: "5411",
			AvgAmount: 60.25,
		},
	}

	// Define exatamente qual saída esperada receber.
	// O vetor foi calculado manualmente para garantir que o teste não falhe por um erro de cálculo.
	expected := [14]float32{0.0041, 0.1667, 0.0494, 0.7826, 0.3333, -1, -1, 0.0292, 0.15, 0, 1, 0, 0.15, 0.0060}
	// 0.0041 - valor da transação normalizado
	// 0.1667 - parcelas normalizadas
	// 0.0494 - razão entre valor da transação e média do cliente
	// 0.7826 - tempo desde a última transação normalizado
	// 0.3333 - distância em km normalizada
	// -1 - transações nas últimas 24h normalizadas
	// -1 - transações nas últimas 24h normalizadas
	// 0.0292 - distância em km normalizada
	// 0.15 - risco do MCC
	// 0 - terminal online
	// 1 - terminal presencial
	// 0 - cliente conhece o comerciante
	// 0.15 - risco do MCC
	// 0.0060 - média do comerciante normalizada


	// Chama a função Vectorize() passando a transação de teste. O resultado é armazenado na variável got.
	got := n.Vectorize(tx)

	// Compara o resultado obtido (got) com o resultado esperado (expected).
	if !almostEqual(got, expected) {
		t.Errorf("Vectorize() = %v, want %v", got, expected)
	}
}