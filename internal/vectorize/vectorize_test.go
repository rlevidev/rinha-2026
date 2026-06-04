package vectorize

import (
	"math"
	"testing"
	"time"
)

func TestClamp(t *testing.T) {
	tests := []struct {
		input    float64
		expected float32
	}{
		{0.5, 0.5},
		{0.0, 0.0},
		{1.0, 1.0},
		{-0.1, 0.0},
		{1.1, 1.0},
		{15000.0 / 10000.0, 1.0},
		{-100.0 / 10000.0, 0.0},
	}

	for _, tt := range tests {
		result := clamp(tt.input)
		if result != tt.expected {
			t.Errorf("clamp(%f): expected %f, got %f", tt.input, tt.expected, result)
		}
	}
}

func TestVectorize(t *testing.T) {
	// Sample MCC risk for testing
	mccRisk := MCCRisk{
		"5411": 0.15,
		"5912": 0.20,
		"7802": 0.75,
	}

	// Test 1 - Legitimate transaction example
	tx1 := Transaction{
		Amount:         384.88,
		Installments:   3,
		RequestedAt:    time.Date(2026, 3, 11, 20, 23, 35, 0, time.UTC),
		CustomerAvg:    769.76,
		TxCount24h:     3,
		KnownMerchants: []string{"MERC-009", "MERC-001"},
		MerchantID:     "MERC-001",
		MerchantMCC:    "5912",
		MerchantAvg:    298.95,
		IsOnline:       false,
		CardPresent:    true,
		KmFromHome:     13.7090520965,
		HasLastTx:      true,
		LastTxMinutesAgo:    (time.Date(2026, 3, 11, 20, 23, 35, 0, time.UTC).Sub(time.Date(2026, 3, 11, 14, 58, 35, 0, time.UTC)).Minutes()),
		LastTxKmFromCurrent: 18.8626479774,
	}
	expectedVector1 := [14]float32{0.038488, 0.25, 0.05, 0.869565, 0.333333, 0.310417, 0.018863, 0.013709, 0.15, 0.0, 1.0, 0.0, 0.20, 0.029895}
	vector1 := Vectorize(tx1, mccRisk)
	for i := 0; i < 14; i++ {
		if math.Abs(float64(vector1[i])-float64(expectedVector1[i])) > 1e-6 {
			t.Errorf("Vector1[%d]: Expected %f, Got %f", i, expectedVector1[i], vector1[i])
		}
	}

	// Test 2 - Fraudulent transaction example (from spec, but adjusted for no last_tx)
	tx2 := Transaction{
		Amount:         9505.97,
		Installments:   10,
		RequestedAt:    time.Date(2026, 3, 14, 5, 15, 12, 0, time.UTC),
		CustomerAvg:    81.28,
		TxCount24h:     20,
		KnownMerchants: []string{"MERC-008", "MERC-007", "MERC-005"},
		MerchantID:     "MERC-068",
		MerchantMCC:    "7802",
		MerchantAvg:    54.86,
		IsOnline:       false,
		CardPresent:    true,
		KmFromHome:     952.27,
		HasLastTx:      false, // Explicitly set to false for this test
	}
	// Expected based on provided example, but with v[5] and v[6] as -1 due to HasLastTx: false
	expectedVector2 := [14]float32{0.950597, 0.833333, 1.0, 0.217391, 0.666667, -1.0, -1.0, 0.95227, 1.0, 0.0, 1.0, 1.0, 0.75, 0.005486}
	vector2 := Vectorize(tx2, mccRisk)
	for i := 0; i < 14; i++ {
		if math.Abs(float64(vector2[i])-float64(expectedVector2[i])) > 1e-6 {
			t.Errorf("Vector2[%d]: Expected %f, Got %f", i, expectedVector2[i], vector2[i])
		}
	}

	// Test 3 - day_of_week
	// 2026-03-11 is a Wednesday (time.Wednesday = 3).
	// (3 + 6) % 7 = 9 % 7 = 2. Expected v[4] = 2/6 = 0.333333
	tx3 := Transaction{RequestedAt: time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)}
	vector3 := Vectorize(tx3, mccRisk)
	if math.Abs(float64(vector3[4])-0.333333) > 1e-6 {
		t.Errorf("day_of_week: Expected %f, Got %f", 0.333333, vector3[4])
	}

	// Test 4 - MCC not found
	tx4 := Transaction{MerchantMCC: "9999"} // MCC 9999 is not in mccRisk
	vector4 := Vectorize(tx4, mccRisk)
	if vector4[12] != 0.5 {
		t.Errorf("MCC not found: Expected %f, Got %f", 0.5, vector4[12])
	}

	// Test 5 - Clamp behavior
	tx5_clamp := Transaction{Amount: 15000.0, TxCount24h: -5, MerchantAvg: -100.0}
	vector5_clamp := Vectorize(tx5_clamp, mccRisk)
	if vector5_clamp[0] != 1.0 {
		t.Errorf("Clamp amount: Expected %f, Got %f", 1.0, vector5_clamp[0])
	}
	if vector5_clamp[8] != 0.0 {
		t.Errorf("Clamp tx_count_24h: Expected %f, Got %f", 0.0, vector5_clamp[8])
	}
	if vector5_clamp[13] != 0.0 {
		t.Errorf("Clamp merchant_avg_amount: Expected %f, Got %f", 0.0, vector5_clamp[13])
	}
}

// Test for allocation in hot path
func BenchmarkVectorize(b *testing.B) {
	mccRisk := MCCRisk{"5411": 0.15}
	tx := Transaction{
		Amount:         384.88,
		Installments:   3,
		RequestedAt:    time.Date(2026, 3, 11, 20, 23, 35, 0, time.UTC),
		CustomerAvg:    769.76,
		TxCount24h:     3,
		KnownMerchants: []string{"MERC-009", "MERC-001"},
		MerchantID:     "MERC-001",
		MerchantMCC:    "5912",
		MerchantAvg:    298.95,
		IsOnline:       false,
		CardPresent:    true,
		KmFromHome:     13.7090520965,
		HasLastTx:      true,
		LastTxMinutesAgo:    (time.Date(2026, 3, 11, 20, 23, 35, 0, time.UTC).Sub(time.Date(2026, 3, 11, 14, 58, 35, 0, time.UTC)).Minutes()),
		LastTxKmFromCurrent: 18.8626479774,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Vectorize(tx, mccRisk)
	}
	// To check allocations, run: go test -bench=. -benchmem
	// Expected: 0 B/op, 0 allocs/op
}
