package vectorize_test

import (
	"encoding/json"
	"io/ioutil"
	"math"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/rlevidev/rinha-2026/internal/vectorize"
)

var (
	testMCCRisk        vectorize.MCCRisk
	testNormalizationConstants vectorize.NormalizationConstants
)

func init() {
	_, b, _, _ := runtime.Caller(0)
	basepath := filepath.Dir(b)

	// Load mcc_risk.json
	mccRiskPath := filepath.Join(basepath, "../../resources/mcc_risk.json")
	mccRiskBytes, err := ioutil.ReadFile(mccRiskPath)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(mccRiskBytes, &testMCCRisk); err != nil {
		panic(err)
	}

	// Load normalization.json
	normalizationPath := filepath.Join(basepath, "../../resources/normalization.json")
	normalizationBytes, err := ioutil.ReadFile(normalizationPath)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(normalizationBytes, &testNormalizationConstants); err != nil {
		panic(err)
	}
}

func TestVectorize_LegitTransaction(t *testing.T) {
	tx := vectorize.Transaction{
		ID:              "tx-1329056812",
		Amount:          41.12,
		Installments:    2,
		RequestedAt:     time.Date(2026, 3, 11, 18, 45, 53, 0, time.UTC),
		CustomerAvg:     82.24,
		TxCount24h:      3,
		KnownMerchants:  []string{"MERC-003", "MERC-016"},
		MerchantID:      "MERC-016",
		MerchantMCC:     "5411",
		MerchantAvg:     60.25,
		IsOnline:        false,
		CardPresent:     true,
		KmFromHome:      29.23,
		LastTransaction: nil, // This will make indices 5 and 6 -1
	}

	vector := vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)

	// Expected vector from plan: [0.0041, 0.1667, 0.05, 0.7826, 0.3333, -1, -1, 0.0292, 0.15, 0, 1, 0, 0.15, 0.006]
	// Using a small delta for float comparisons due to precision

	expected := []float32{0.004112, 0.16666667, 0.05, 0.7826087, 0.33333334, -1.0, -1.0, 0.02923, 0.15, 0.0, 1.0, 0.0, 0.15, 0.006025}

	if len(vector) != len(expected) {
		t.Fatalf("Vector length mismatch. Expected %d, got %d", len(expected), len(vector))
	}

	delta := float32(1e-5) // Using a delta for float comparison

	for i := 0; i < len(vector); i++ {
		if math.Abs(float64(vector[i]-expected[i])) > float64(delta) {
			t.Errorf("Mismatch at index %d. Expected %f, got %f", i, expected[i], vector[i])
		}
	}

	// Specific checks
	if vector[5] != -1.0 || vector[6] != -1.0 {
		t.Errorf("Expected vector[5] and vector[6] to be -1.0 for nil LastTransaction, got %f, %f", vector[5], vector[6])
	}
	if vector[9] != 0.0 { // not online
		t.Errorf("Expected vector[9] to be 0.0 (IsOnline=false), got %f", vector[9])
	}
	if vector[10] != 1.0 { // card present
		t.Errorf("Expected vector[10] to be 1.0 (CardPresent=true), got %f", vector[10])
	}
	if vector[11] != 0.0 { // known merchant MERC-016 in KnownMerchants
		t.Errorf("Expected vector[11] to be 0.0 (KnownMerchant), got %f", vector[11])
	}
	if vector[12] != 0.15 { // MCC 5411 -> 0.15
		t.Errorf("Expected vector[12] to be 0.15 (MCC 5411), got %f", vector[12])
	}
}

func TestVectorize_FraudulentTransaction(t *testing.T) {
	tx := vectorize.Transaction{
		ID:              "tx-3330991687",
		Amount:          9505.97,
		Installments:    10,
		RequestedAt:     time.Date(2026, 3, 14, 5, 15, 12, 0, time.UTC),
		CustomerAvg:     81.28,
		TxCount24h:      20,
		KnownMerchants:  []string{"MERC-008", "MERC-007", "MERC-005"},
		MerchantID:      "MERC-068", // Not in known merchants
		MerchantMCC:     "7802",
		MerchantAvg:     54.86,
		IsOnline:        false,
		CardPresent:     true,
		KmFromHome:      952.27,
		LastTransaction: nil, // This will make indices 5 and 6 -1
	}

	vector := vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)

	// Expected vector from plan: [0.9506, 0.8333, 1.0, 0.2174, 0.8333, -1, -1, 0.9523, 1.0, 0, 1, 1, 0.75, 0.0055]
	expected := []float32{0.950597, 0.8333333, 1.0, 0.2173913, 0.83333334, -1.0, -1.0, 0.95227, 1.0, 0.0, 1.0, 1.0, 0.75, 0.005486}

	if len(vector) != len(expected) {
		t.Fatalf("Vector length mismatch. Expected %d, got %d", len(expected), len(vector))
	}

	delta := float32(1e-5) // Using a delta for float comparison

	for i := 0; i < len(vector); i++ {
		if math.Abs(float64(vector[i]-expected[i])) > float64(delta) {
			t.Errorf("Mismatch at index %d. Expected %f, got %f", i, expected[i], vector[i])
		}
	}

	// Specific checks
	if math.Abs(float64(vector[0]-0.950597)) > float64(delta) { // amount/10000
		t.Errorf("Expected vector[0] to be approx 0.9506, got %f", vector[0])
	}
	if vector[8] != 1.0 { // tx_count=20, clamped
		t.Errorf("Expected vector[8] to be 1.0 (tx_count=20, clamped), got %f", vector[8])
	}
	if vector[11] != 1.0 { // MERC-068 not in known merchants
		t.Errorf("Expected vector[11] to be 1.0 (UnknownMerchant), got %f", vector[11])
	}
	if vector[12] != 0.75 { // MCC 7802 -> 0.75
		t.Errorf("Expected vector[12] to be 0.75 (MCC 7802), got %f", vector[12])
	}
}

func TestVectorize_DayOfWeek(t *testing.T) {
	// 2026-03-11 is a Wednesday.
	// Go's time.Weekday() for Wednesday is 3.
	// Spec: (weekday + 6) % 7 -> (3 + 6) % 7 = 9 % 7 = 2
	// Expected vector[4] = 2/6 = 0.333333...
	tx := vectorize.Transaction{
		RequestedAt: time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC),
	}
	vector := vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)
	expectedDayOfWeek := float32(2.0 / 6.0)
	delta := float32(1e-5)
	if math.Abs(float64(vector[4]-expectedDayOfWeek)) > float64(delta) {
		t.Errorf("Expected vector[4] for Wednesday to be approx %f, got %f", expectedDayOfWeek, vector[4])
	}

	// Test Sunday (Go = 0, Spec = 6)
	tx.RequestedAt = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) // Sunday
	vector = vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)
	expectedDayOfWeek = float32(6.0 / 6.0) // (0 + 6) % 7 = 6
	if math.Abs(float64(vector[4]-expectedDayOfWeek)) > float64(delta) {
		t.Errorf("Expected vector[4] for Sunday to be approx %f, got %f", expectedDayOfWeek, vector[4])
	}

	// Test Monday (Go = 1, Spec = 0)
	tx.RequestedAt = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // Monday
	vector = vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)
	expectedDayOfWeek = float32(0.0 / 6.0) // (1 + 6) % 7 = 0
	if math.Abs(float64(vector[4]-expectedDayOfWeek)) > float64(delta) {
		t.Errorf("Expected vector[4] for Monday to be approx %f, got %f", expectedDayOfWeek, vector[4])
	}
}

func TestVectorize_MCCNotFound(t *testing.T) {
	tx := vectorize.Transaction{
		MerchantMCC: "9999", // MCC not in mcc_risk.json
	}
	vector := vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)
	expectedMCCRisk := float32(0.5)
	if vector[12] != expectedMCCRisk {
		t.Errorf("Expected vector[12] to be default 0.5 for unknown MCC, got %f", vector[12])
	}
}

func TestClamp(t *testing.T) {
	// Test max_amount clamp indirectly via Vectorize
	tx := vectorize.Transaction{
		Amount: testNormalizationConstants.MaxAmount * 1.5, // 15000
	}
	vector := vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)
	if vector[0] != 1.0 {
		t.Errorf("Expected vector[0] to be 1.0 when amount > max_amount, got %f", vector[0])
	}

	// Test negative value clamp directly
	clampedValue := vectorize.Clamp(-100.0)
	if clampedValue != 0.0 {
		t.Errorf("Expected clamped value for -100.0 to be 0.0, got %f", clampedValue)
	}

	// Test value within range
	clampedValue = vectorize.Clamp(0.5)
	if clampedValue != 0.5 {
		t.Errorf("Expected clamped value for 0.5 to be 0.5, got %f", clampedValue)
	}

	// Test value above range
	clampedValue = vectorize.Clamp(1.5)
	if clampedValue != 1.0 {
		t.Errorf("Expected clamped value for 1.5 to be 1.0, got %f", clampedValue)
	}
}

func BenchmarkVectorize(b *testing.B) {
	tx := vectorize.Transaction{
		ID:              "tx-3576980410",
		Amount:          384.88,
		Installments:    3,
		RequestedAt:     time.Date(2026, 3, 11, 20, 23, 35, 0, time.UTC),
		CustomerAvg:     769.76,
		TxCount24h:      3,
		KnownMerchants:  []string{"MERC-009", "MERC-001"},
		MerchantID:      "MERC-001",
		MerchantMCC:     "5912",
		MerchantAvg:     298.95,
		IsOnline:        false,
		CardPresent:     true,
		KmFromHome:      13.7090520965,
		LastTransaction: &vectorize.LastTransaction{
			Timestamp:     time.Date(2026, 3, 11, 14, 58, 35, 0, time.UTC),
			KmFromCurrent: 18.8626479774,
		},
	}

	b.ResetTimer()
	b.ReportAllocs() // Report memory allocations

	for i := 0; i < b.N; i++ {
		_ = vectorize.Vectorize(tx, testMCCRisk, testNormalizationConstants)
	}
}
