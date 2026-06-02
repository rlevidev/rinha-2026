#!/bin/bash
# smoke_test.sh

BASE_URL="http://localhost:9999"

echo "=== Starting Docker Compose Services ==="
docker compose up -d
sleep 5 # Give services some time to start and for index to load

echo "=== Testando /ready ==="
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" $BASE_URL/ready)
if [ "$HTTP_CODE" -eq 200 ]; then
  echo "✔ /ready returned 200 OK"
else
  echo "✖ /ready returned $HTTP_CODE. Expected 200."
  exit 1
fi

echo ""
echo "=== Testando transação legítima ==="
LEGIT_PAYLOAD='{ "id": "tx-3576980410", "transaction": { "amount": 384.88, "installments": 3, "requested_at": "2026-03-11T20:23:35Z" }, "customer": { "avg_amount": 769.76, "tx_count_24h": 3, "known_merchants": ["MERC-009","MERC-001"] }, "merchant": { "id": "MERC-001", "mcc": "5912", "avg_amount": 298.95 }, "terminal": { "is_online": false, "card_present": true, "km_from_home": 13.7090520965 }, "last_transaction": { "timestamp": "2026-03-11T14:58:35Z", "km_from_current": 18.8626479774 } }'
LEGIT_RESPONSE=$(curl -s -X POST $BASE_URL/fraud-score -H "Content-Type: application/json" -d "$LEGIT_PAYLOAD")
echo "Response: $LEGIT_RESPONSE"
if echo "$LEGIT_RESPONSE" | grep -q '"approved":true,"fraud_score":0.0'; then
  echo "✔ Transação legítima aprovada com score 0.0"
else
  echo "✖ Resposta inesperada para transação legítima."
  exit 1
fi

echo ""
echo "=== Testando transação fraudulenta ==="
FRAUD_PAYLOAD='{ "id": "tx-3330991687", "transaction":      { "amount": 9505.97, "installments": 10, "requested_at": "2026-03-14T05:15:12Z" }, "customer":         { "avg_amount": 81.28, "tx_count_24h": 20, "known_merchants": ["MERC-008", "MERC-007", "MERC-005"] }, "merchant":         { "id": "MERC-068", "mcc": "7802", "avg_amount": 54.86 }, "terminal":         { "is_online": false, "card_present": true, "km_from_home": 952.27 }, "last_transaction": null }'
FRAUD_RESPONSE=$(curl -s -X POST $BASE_URL/fraud-score -H "Content-Type: application/json" -d "$FRAUD_PAYLOAD")
echo "Response: $FRAUD_RESPONSE"
if echo "$FRAUD_RESPONSE" | grep -q '"approved":false,"fraud_score":1.0'; then
  echo "✔ Transação fraudulenta negada com score 1.0"
else
  echo "✖ Resposta inesperada para transação fraudulenta."
  exit 1
fi

echo ""
echo "=== Smoke Test Concluído com Sucesso ==="

echo "=== Shutting down Docker Compose Services ==="
docker compose down
