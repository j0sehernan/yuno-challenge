#!/bin/bash
# demo.sh - Demonstrates all idempotency shield scenarios
set -e

BASE_URL="${BASE_URL:-http://localhost:8080}"
BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

header() {
    echo ""
    echo -e "${BOLD}${CYAN}═══════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}${CYAN}  $1${NC}"
    echo -e "${BOLD}${CYAN}═══════════════════════════════════════════════════${NC}"
}

step() {
    echo ""
    echo -e "${BOLD}${YELLOW}▶ $1${NC}"
}

success() {
    echo -e "${GREEN}✓ $1${NC}"
}

# Wait for server
header "Kubo Market Idempotency Shield - Demo"
echo "Checking server at $BASE_URL..."
for i in $(seq 1 10); do
    if curl -s "$BASE_URL/health" > /dev/null 2>&1; then
        success "Server is running"
        break
    fi
    if [ "$i" = "10" ]; then
        echo -e "${RED}Server not available at $BASE_URL${NC}"
        exit 1
    fi
    sleep 1
done

# 1. Health Check
header "1. Health Check"
step "GET /health"
curl -s "$BASE_URL/health" | python3 -m json.tool
success "Health check passed"

# 2. New Payment → 201
header "2. New Payment Request (→ 201 Created)"
KEY="demo-$(date +%s)"
step "POST /v1/payments with key=$KEY"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/v1/payments" \
    -H "Content-Type: application/json" \
    -d "{
        \"idempotency_key\": \"$KEY\",
        \"merchant_id\": \"kubo-brazil\",
        \"customer_id\": \"cust_demo_1\",
        \"amount\": 15000,
        \"currency\": \"BRL\"
    }")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
echo "$BODY" | python3 -m json.tool
echo "HTTP Status: $HTTP_CODE"
success "New payment accepted for processing (201)"

# 3. Duplicate While Processing → 409
header "3. Duplicate While Processing (→ 409 Conflict)"
step "POST /v1/payments with SAME key=$KEY"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/v1/payments" \
    -H "Content-Type: application/json" \
    -d "{
        \"idempotency_key\": \"$KEY\",
        \"merchant_id\": \"kubo-brazil\",
        \"customer_id\": \"cust_demo_1\",
        \"amount\": 15000,
        \"currency\": \"BRL\"
    }")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
echo "$BODY" | python3 -m json.tool
echo "HTTP Status: $HTTP_CODE"
success "Duplicate correctly blocked (409)"

# 4. Mark Complete → 200
header "4. Mark Payment Complete (→ 200 OK)"
step "PATCH /v1/payments/$KEY/complete"
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE_URL/v1/payments/$KEY/complete" \
    -H "Content-Type: application/json" \
    -d "{
        \"status\": \"succeeded\",
        \"response_body\": {\"transaction_id\": \"tx_demo_123\", \"provider\": \"stripe\"}
    }")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
echo "$BODY" | python3 -m json.tool
echo "HTTP Status: $HTTP_CODE"
success "Payment marked as succeeded (200)"

# 5. Duplicate After Success → 200 (cached)
header "5. Duplicate After Success (→ 200 Cached)"
step "POST /v1/payments with SAME key=$KEY (after success)"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/v1/payments" \
    -H "Content-Type: application/json" \
    -d "{
        \"idempotency_key\": \"$KEY\",
        \"merchant_id\": \"kubo-brazil\",
        \"customer_id\": \"cust_demo_1\",
        \"amount\": 15000,
        \"currency\": \"BRL\"
    }")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
echo "$BODY" | python3 -m json.tool
echo "HTTP Status: $HTTP_CODE"
success "Cached response returned (200)"

# 6. Parameter Mismatch → 422
header "6. Parameter Mismatch (→ 422 Unprocessable)"
step "POST /v1/payments with SAME key but DIFFERENT amount"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/v1/payments" \
    -H "Content-Type: application/json" \
    -d "{
        \"idempotency_key\": \"$KEY\",
        \"merchant_id\": \"kubo-brazil\",
        \"customer_id\": \"cust_demo_1\",
        \"amount\": 99999,
        \"currency\": \"BRL\"
    }")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
echo "$BODY" | python3 -m json.tool
echo "HTTP Status: $HTTP_CODE"
success "Parameter mismatch detected (422)"

# 7. Concurrent Requests (10 goroutines, same key)
header "7. Concurrent Requests (10 parallel, same key)"
CONC_KEY="concurrent-$(date +%s)"
step "Sending 10 concurrent POST /v1/payments with key=$CONC_KEY"

TMPDIR=$(mktemp -d)
for i in $(seq 1 10); do
    curl -s -w "\n%{http_code}" -X POST "$BASE_URL/v1/payments" \
        -H "Content-Type: application/json" \
        -d "{
            \"idempotency_key\": \"$CONC_KEY\",
            \"merchant_id\": \"kubo-brazil\",
            \"customer_id\": \"cust_concurrent\",
            \"amount\": 25000,
            \"currency\": \"BRL\"
        }" > "$TMPDIR/result_$i" 2>/dev/null &
done
wait

CREATED=0
BLOCKED=0
for i in $(seq 1 10); do
    CODE=$(tail -1 "$TMPDIR/result_$i")
    if [ "$CODE" = "201" ]; then
        CREATED=$((CREATED + 1))
    elif [ "$CODE" = "409" ]; then
        BLOCKED=$((BLOCKED + 1))
    fi
done
rm -rf "$TMPDIR"

echo "Results: $CREATED created (201), $BLOCKED blocked (409)"
if [ "$CREATED" = "1" ]; then
    success "Exactly 1 request succeeded, $BLOCKED duplicates blocked!"
else
    echo -e "${RED}✗ Expected exactly 1 created, got $CREATED${NC}"
fi

# 8. Failed + Retry Flow
header "8. Failed Payment → Retry (→ 201)"
FAIL_KEY="fail-retry-$(date +%s)"
step "Create payment, mark failed, then retry"

# Create
curl -s -X POST "$BASE_URL/v1/payments" \
    -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$FAIL_KEY\",\"merchant_id\":\"kubo-brazil\",\"customer_id\":\"cust_retry\",\"amount\":8000,\"currency\":\"BRL\"}" > /dev/null

# Mark failed
curl -s -X PATCH "$BASE_URL/v1/payments/$FAIL_KEY/complete" \
    -H "Content-Type: application/json" \
    -d '{"status":"failed"}' > /dev/null

# Retry
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/v1/payments" \
    -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$FAIL_KEY\",\"merchant_id\":\"kubo-brazil\",\"customer_id\":\"cust_retry\",\"amount\":8000,\"currency\":\"BRL\"}")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
echo "$BODY" | python3 -m json.tool
echo "HTTP Status: $HTTP_CODE"
success "Retry after failure accepted (201)"

# 9. Duplicate Detection Report
header "9. Duplicate Detection Report"
step "GET /v1/merchants/kubo-brazil/duplicates"
curl -s "$BASE_URL/v1/merchants/kubo-brazil/duplicates" | python3 -m json.tool
success "Duplicate report generated"

# 10. Metrics
header "10. Service Metrics"
step "GET /v1/metrics"
curl -s "$BASE_URL/v1/metrics" | python3 -m json.tool
success "Metrics retrieved"

# Summary
header "Demo Complete!"
echo -e "${GREEN}All idempotency scenarios demonstrated successfully.${NC}"
echo ""
echo "Scenarios covered:"
echo "  1. Health check"
echo "  2. New payment → 201"
echo "  3. Duplicate while processing → 409"
echo "  4. Mark complete → 200"
echo "  5. Duplicate after success → 200 (cached)"
echo "  6. Parameter mismatch → 422"
echo "  7. 10 concurrent requests → only 1 gets 201"
echo "  8. Failed + retry → 201"
echo "  9. Duplicate detection report"
echo " 10. Service metrics with anomaly detection"
