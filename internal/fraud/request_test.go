package fraud

import "testing"

func TestParseRequestAndVectorize(t *testing.T) {
	payload := []byte(`{
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
	}`)

	var req Request
	if !ParseRequest(payload, &req) {
		t.Fatalf("ParseRequest returned false")
	}
	if req.HasLastTx {
		t.Fatalf("expected last tx to be absent")
	}
	if !req.KnownMerchant {
		t.Fatalf("expected merchant to be known")
	}
	if req.RequestedAt == 0 {
		t.Fatalf("expected requested_at to be parsed")
	}

	norm := &Normalizer{
		MaxAmount:        10000,
		MaxInstallments:  12,
		AmountVsAvgRatio: 10,
		MaxMinutes:       1440,
		MaxKm:            1000,
		MaxTxCount24h:    20,
		MaxMerchantAvg:   10000,
		MccRisk: map[[4]byte]int16{
			[4]byte{'5', '4', '1', '1'}: 1500,
		},
	}

	vec := Vectorize(&req, norm)
	want := [16]int16{41, 1667, 500, 7826, 3333, -10000, -10000, 292, 1500, 0, 10000, 0, 1500, 60, 0, 0}
	if vec != want {
		t.Fatalf("vector mismatch\n got: %#v\nwant: %#v", vec, want)
	}

	if got := PartitionTag(&req); got != 8 {
		t.Fatalf("PartitionTag() = %d, want 8", got)
	}
}
