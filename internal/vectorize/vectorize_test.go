package vectorize

import (
	"testing"
	"time"
)

func TestVectorize_Legit(t *testing.T) {
	tx := Transaction{
		Amount:         41.12,
		Installments:   2,
		RequestedAt:    time.Date(2026, 3, 11, 18, 45, 53, 0, time.UTC),
		CustomerAvg:    82.24,
		TxCount24h:     3,
		KnownMerchants: []string{"MERC-003", "MERC-016"},
		MerchantID:     "MERC-016",
		MerchantMCC:    "5411",
		MerchantAvg:    60.25,
		IsOnline:       false,
		CardPresent:    true,
		KmFromHome:     29.23,
		HasLastTx:      false,
	}
	mcc := MCCRisk{"5411": 0.15}
	vec := Vectorize(tx, mcc)

	if vec[5] != -1 {
		t.Errorf("v[5] expected -1, got %f", vec[5])
	}
	if vec[6] != -1 {
		t.Errorf("v[6] expected -1, got %f", vec[6])
	}
	if vec[9] != 0 {
		t.Errorf("v[9] expected 0, got %f", vec[9])
	}
	if vec[10] != 1 {
		t.Errorf("v[10] expected 1, got %f", vec[10])
	}
	if vec[11] != 0 {
		t.Errorf("v[11] expected 0, got %f", vec[11])
	}
	if vec[12] != 0.15 {
		t.Errorf("v[12] expected 0.15, got %f", vec[12])
	}
}

func TestVectorize_Fraud(t *testing.T) {
	tx := Transaction{
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
		HasLastTx:      false,
	}
	mcc := MCCRisk{"7802": 0.75}
	vec := Vectorize(tx, mcc)

	if vec[0] < 0.95 || vec[0] > 0.96 {
		t.Errorf("v[0] expected ~0.95, got %f", vec[0])
	}
	if vec[8] != 1.0 {
		t.Errorf("v[8] expected 1.0, got %f", vec[8])
	}
	if vec[11] != 1.0 {
		t.Errorf("v[11] expected 1.0, got %f", vec[11])
	}
	if vec[12] != 0.75 {
		t.Errorf("v[12] expected 0.75, got %f", vec[12])
	}
}

func TestVectorize_DayOfWeek(t *testing.T) {
	// 2026-03-11 é quarta-feira → (3+6)%7 = 2. 2/6 = 0.333333
	tx := Transaction{
		RequestedAt: time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC),
	}
	vec := Vectorize(tx, nil)
	expected := float32(2.0 / 6.0)
	if vec[4] != expected {
		t.Errorf("v[4] expected %f, got %f", expected, vec[4])
	}
}

func TestVectorize_MCCNotFound(t *testing.T) {
	tx := Transaction{MerchantMCC: "9999"}
	vec := Vectorize(tx, nil)
	if vec[12] != 0.5 {
		t.Errorf("v[12] expected 0.5, got %f", vec[12])
	}
}

func TestVectorize_Clamp(t *testing.T) {
	tx1 := Transaction{Amount: 15000}
	vec1 := Vectorize(tx1, nil)
	if vec1[0] != 1.0 {
		t.Errorf("v[0] expected 1.0, got %f", vec1[0])
	}

	tx2 := Transaction{Amount: -100}
	vec2 := Vectorize(tx2, nil)
	if vec2[0] != 0.0 {
		t.Errorf("v[0] expected 0.0, got %f", vec2[0])
	}
}

func BenchmarkVectorize(b *testing.B) {
	tx := Transaction{
		Amount:         41.12,
		Installments:   2,
		RequestedAt:    time.Date(2026, 3, 11, 18, 45, 53, 0, time.UTC),
		CustomerAvg:    82.24,
		TxCount24h:     3,
		KnownMerchants: []string{"MERC-003", "MERC-016"},
		MerchantID:     "MERC-016",
		MerchantMCC:    "5411",
		MerchantAvg:    60.25,
		IsOnline:       false,
		CardPresent:    true,
		KmFromHome:     29.23,
		HasLastTx:      false,
	}
	mcc := MCCRisk{"5411": 0.15}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Vectorize(tx, mcc)
	}
}
