package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

type Order struct {
	ID         string          `json:"id"`
	CustomerID string          `json:"customer_id"`
	Total      float64         `json:"total"`
	Items      json.RawMessage `json:"items"`
	CreatedAt  time.Time       `json:"created_at"`
}

type CreateOrderRequest struct {
	CustomerID string          `json:"customer_id"`
	Total      float64         `json:"total"`
	Items      json.RawMessage `json:"items"`
}

var db *sql.DB

func main() {
	// Connect to database
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	var err error
	db, err = sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Wait for database to be ready
	for i := 0; i < 10; i++ {
		if err := db.Ping(); err == nil {
			break
		}
		log.Printf("Waiting for database... (%d/10)", i+1)
		time.Sleep(2 * time.Second)
	}

	if err := db.Ping(); err != nil {
		log.Fatalf("Database not available: %v", err)
	}

	log.Println("Connected to database")

	// Routes
	http.HandleFunc("/orders", handleOrders)
	http.HandleFunc("/orders/", handleGetOrder)
	http.HandleFunc("/health", handleHealth)

	log.Println("Demo service starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Validate
	if req.CustomerID == "" {
		http.Error(w, "customer_id is required", http.StatusBadRequest)
		return
	}
	if req.Total <= 0 {
		http.Error(w, "total must be > 0", http.StatusBadRequest)
		return
	}

	// Create order with outbox event in same transaction
	order, err := createOrder(r.Context(), req)
	if err != nil {
		log.Printf("Failed to create order: %v", err)
		http.Error(w, "Failed to create order", http.StatusInternalServerError)
		return
	}

	log.Printf("Order created: %s (customer: %s, total: %.2f)",
		order.ID, order.CustomerID, order.Total)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(order)
}

func handleGetOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	id := r.URL.Path[len("/orders/"):]
	if id == "" {
		http.Error(w, "Order ID is required", http.StatusBadRequest)
		return
	}

	var order Order
	err := db.QueryRow(`
		SELECT id, customer_id, total, items, created_at
		FROM orders WHERE id = $1
	`, id).Scan(&order.ID, &order.CustomerID, &order.Total, &order.Items, &order.CreatedAt)

	if err == sql.ErrNoRows {
		http.Error(w, "Order not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("Failed to get order: %v", err)
		http.Error(w, "Failed to get order", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// createOrder creates an order and writes an outbox event in the same transaction
func createOrder(ctx context.Context, req CreateOrderRequest) (*Order, error) {
	// Start transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Generate UUID for order
	orderID := uuid.New().String()
	now := time.Now()

	// Insert order
	_, err = tx.ExecContext(ctx, `
		INSERT INTO orders (id, customer_id, total, items, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, orderID, req.CustomerID, req.Total, req.Items, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert order: %w", err)
	}

	// Create event payload
	payload := map[string]interface{}{
		"order_id":    orderID,
		"customer_id": req.CustomerID,
		"total":       req.Total,
		"items":       req.Items,
		"created_at":  now.Format(time.RFC3339),
	}
	payloadJSON, _ := json.Marshal(payload)

	// Insert outbox event (in same transaction!)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4)
	`, "order", orderID, "order.created", payloadJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to insert outbox event: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Both order and outbox event are now committed atomically!
	return &Order{
		ID:         orderID,
		CustomerID: req.CustomerID,
		Total:      req.Total,
		Items:      req.Items,
		CreatedAt:  now,
	}, nil
}
