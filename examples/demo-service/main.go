package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type WebhookPayload struct {
	ID            string                 `json:"id"`
	Type          string                 `json:"type"`
	Created       int64                  `json:"created"`
	AggregateType string                 `json:"aggregate_type"`
	AggregateID   string                 `json:"aggregate_id"`
	Data          map[string]interface{} `json:"data"`
	Headers       map[string]string      `json:"headers,omitempty"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/webhook", handleWebhook)
	http.HandleFunc("/health", handleHealth)

	log.Printf("Webhook receiver listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("❌ Failed to read body: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("❌ Failed to unmarshal: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Log the received event
	log.Printf("✅ Received event: id=%s type=%s aggregate=%s/%s created=%s",
		payload.ID,
		payload.Type,
		payload.AggregateType,
		payload.AggregateID,
		time.Unix(payload.Created, 0).Format(time.RFC3339))

	// Log headers for debugging
	if len(payload.Headers) > 0 {
		log.Printf("   Headers: %+v", payload.Headers)
	}

	// Log the payload data
	if payload.Data != nil {
		dataJSON, _ := json.MarshalIndent(payload.Data, "   ", "  ")
		log.Printf("   Data: %s", string(dataJSON))
	}

	// Log signature verification header
	if sig := r.Header.Get("X-Signature-256"); sig != "" {
		log.Printf("   Signature: %s", sig[:20]+"...")
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}
