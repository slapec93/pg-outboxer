package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type Event struct {
	ID            string                 `json:"id"`
	AggregateType string                 `json:"aggregate_type"`
	AggregateID   string                 `json:"aggregate_id"`
	EventType     string                 `json:"event_type"`
	Payload       map[string]interface{} `json:"payload"`
	ReceivedAt    time.Time              `json:"received_at"`
}

var (
	events []Event
	mu     sync.RWMutex
)

func main() {
	http.HandleFunc("/webhook", handleWebhook)
	http.HandleFunc("/events", handleGetEvents)
	http.HandleFunc("/health", handleHealth)

	log.Println("Webhook receiver starting on :8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Failed to read body: %v", err)
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse event
	var event Event
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("Failed to parse event: %v", err)
		http.Error(w, "Failed to parse event", http.StatusBadRequest)
		return
	}

	event.ReceivedAt = time.Now()

	// Store event (for demo purposes)
	mu.Lock()
	events = append(events, event)
	mu.Unlock()

	// Log event details
	log.Printf("✅ Event received: %s | %s | aggregate=%s | payload=%v",
		event.ID,
		event.EventType,
		event.AggregateID,
		event.Payload,
	)

	// Log headers (to show HMAC signature if configured)
	if sig := r.Header.Get("X-Signature-256"); sig != "" {
		log.Printf("   Signature: %s", sig)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func handleGetEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.RLock()
	defer mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":  len(events),
		"events": events,
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	count := len(events)
	mu.RUnlock()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK - %d events received", count)
}
