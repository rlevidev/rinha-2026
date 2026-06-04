#!/bin/bash
# smoke_test.sh
BASE_URL="http://localhost:9999"

echo "=== Testando /ready ==="
curl -s -o /dev/null -w "%{http_code}" $BASE_URL/ready
echo ""

echo "=== Testando transação legítima ==="
RESPONSE=$(curl -s -X POST $BASE_URL/fraud-score \
  -H "Content-Type: application/json" \
  -d '{"id":"tx-1329056812","transaction":{"amount":41.12,"installments":2,"requested_at":"2026-03-11T18:45:53Z"},"customer":{"avg_amount":82.24,"tx_count_24h":3,"known_merchants":["MERC-003","MERC-016"]},"merchant":{"id":"MERC-016","mcc":"5411","avg_amount":60.25},"terminal":{"is_online":false,"card_present":true,"km_from_home":29.23},"last_transaction":null}')
echo $RESPONSE
# Esperado: approved=true

echo "=== Testando transação fraudulenta ==="
RESPONSE=$(curl -s -X POST $BASE_URL/fraud-score \
  -H "Content-Type: application/json" \
  -d '{"id":"tx-3330991687","transaction":{"amount":9505.97,"installments":10,"requested_at":"2026-03-14T05:15:12Z"},"customer":{"avg_amount":81.28,"tx_count_24h":20,"known_merchants":["MERC-008","MERC-007","MERC-005"]},"merchant":{"id":"MERC-068","mcc":"7802","avg_amount":54.86},"terminal":{"is_online":false,"card_present":true,"km_from_home":952.27},"last_transaction":null}')
echo $RESPONSE
# Esperado: approved=false
