#!/bin/bash
set -e

echo "🚀 Testing pg-outboxer demo..."
echo ""

# Wait for services to be ready
echo "⏳ Waiting for services to start..."
sleep 5

# Check health endpoints
echo "🏥 Checking health endpoints..."
curl -sf http://localhost:8080/health > /dev/null && echo "✅ demo-service healthy"
curl -sf http://localhost:8081/health > /dev/null && echo "✅ webhook-receiver healthy"
curl -sf http://localhost:9090/metrics > /dev/null && echo "✅ pg-outboxer metrics available"
echo ""

# Create test orders
echo "📦 Creating test orders..."
echo ""

echo "Order 1: iPhone order"
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "customer_id": "cust_alice",
    "total": 999.99,
    "items": ["iPhone 15 Pro", "USB-C Cable"]
  }' | jq '.'

echo ""
echo "Order 2: MacBook order"
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "customer_id": "cust_bob",
    "total": 2499.99,
    "items": ["MacBook Pro 16\"", "Magic Mouse"]
  }' | jq '.'

echo ""
echo "Order 3: AirPods order"
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "customer_id": "cust_charlie",
    "total": 249.99,
    "items": ["AirPods Pro"]
  }' | jq '.'

echo ""
echo "⏳ Waiting for events to be delivered..."
sleep 3

echo ""
echo "📨 Events received by webhook:"
curl -s http://localhost:8081/events | jq '.'

echo ""
echo "📊 Prometheus metrics:"
curl -s http://localhost:9090/metrics | grep pg_outboxer | head -10

echo ""
echo "✅ Demo complete! Check docker compose logs to see the full flow."
echo ""
echo "Next steps:"
echo "  - View logs: docker compose logs -f"
echo "  - Stop webhook: docker compose stop webhook-receiver"
echo "  - Create order (will retry): curl -X POST http://localhost:8080/orders ..."
echo "  - Restart webhook: docker compose start webhook-receiver"
