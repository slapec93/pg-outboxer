package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/source"
)

func TestWebhook_New(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name:          "test-webhook",
		URL:           "https://example.com/webhook",
		Timeout:       5 * time.Second,
		SigningSecret: "secret",
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
	}

	webhook, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create webhook: %v", err)
	}
	defer webhook.Close()

	if webhook.name != "test-webhook" {
		t.Errorf("expected name 'test-webhook', got '%s'", webhook.name)
	}
	if webhook.url != "https://example.com/webhook" {
		t.Errorf("expected url 'https://example.com/webhook', got '%s'", webhook.url)
	}
	if webhook.signingSecret != "secret" {
		t.Errorf("expected signing secret 'secret', got '%s'", webhook.signingSecret)
	}
	if webhook.timeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", webhook.timeout)
	}
}

func TestWebhook_NewRequiresURL(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name: "test-webhook",
		URL:  "", // Missing URL
	}

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for missing URL, got nil")
	}

	if !strings.Contains(err.Error(), "url is required") {
		t.Errorf("expected error about missing URL, got: %v", err)
	}
}

func TestWebhook_PublishSuccess(t *testing.T) {
	// Create test server that expects a webhook
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Verify headers
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}
		if ua := r.Header.Get("User-Agent"); ua != "pg-outboxer" {
			t.Errorf("expected User-Agent pg-outboxer, got %s", ua)
		}

		// Verify event ID header (for quick reference)
		if eventID := r.Header.Get("X-Event-ID"); eventID != "event-123" {
			t.Errorf("expected X-Event-ID event-123, got %s", eventID)
		}

		// Verify body has Stripe-style envelope
		body, _ := io.ReadAll(r.Body)
		var envelope struct {
			ID            string `json:"id"`
			Type          string `json:"type"`
			Created       int64  `json:"created"`
			AggregateType string `json:"aggregate_type"`
			AggregateID   string `json:"aggregate_id"`
			Data          struct {
				Object map[string]any `json:"object"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("failed to unmarshal envelope: %v", err)
		}

		// Verify envelope fields
		if envelope.ID != "event-123" {
			t.Errorf("expected id=event-123, got %s", envelope.ID)
		}
		if envelope.Type != "order.created" {
			t.Errorf("expected type=order.created, got %s", envelope.Type)
		}
		if envelope.AggregateType != "order" {
			t.Errorf("expected aggregate_type=order, got %s", envelope.AggregateType)
		}
		if envelope.AggregateID != "order-1" {
			t.Errorf("expected aggregate_id=order-1, got %s", envelope.AggregateID)
		}
		if envelope.Created == 0 {
			t.Error("expected created timestamp to be set")
		}

		// Verify nested payload
		if amount, ok := envelope.Data.Object["amount"].(float64); !ok || amount != 100 {
			t.Errorf("expected data.object.amount=100, got %v", envelope.Data.Object["amount"])
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.PublisherConfig{
		Name: "test-webhook",
		URL:  server.URL,
	}

	webhook, _ := New(cfg)
	defer webhook.Close()

	event := source.Event{
		ID:            "event-123",
		AggregateType: "order",
		AggregateID:   "order-1",
		EventType:     "order.created",
		Payload:       []byte(`{"amount": 100}`),
		CreatedAt:     time.Now(),
	}

	result := webhook.Publish(context.Background(), event)

	if !result.Success {
		t.Errorf("expected success, got: %s", result.ErrorMsg)
	}
	if result.Retryable {
		t.Errorf("expected retryable=false, got true")
	}
}

func TestWebhook_PublishWithSignature(t *testing.T) {
	secret := "my-secret"
	var receivedSignature string
	var receivedBody []byte

	// Create test server that captures the signature and body
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSignature = r.Header.Get("X-Signature-256")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.PublisherConfig{
		Name:          "test-webhook",
		URL:           server.URL,
		SigningSecret: secret,
	}

	webhook, _ := New(cfg)
	defer webhook.Close()

	payload := []byte(`{"test": true}`)
	event := source.Event{
		ID:            "event-123",
		EventType:     "order.created",
		AggregateType: "order",
		AggregateID:   "order-1",
		Payload:       payload,
		CreatedAt:     time.Now(),
	}

	result := webhook.Publish(context.Background(), event)

	if !result.Success {
		t.Errorf("expected success, got: %s", result.ErrorMsg)
	}

	// Verify signature format
	if !strings.HasPrefix(receivedSignature, "sha256=") {
		t.Errorf("expected signature to start with 'sha256=', got: %s", receivedSignature)
	}

	// Verify signature is correct (should sign the entire envelope body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expectedSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if receivedSignature != expectedSignature {
		t.Errorf("signature mismatch:\nexpected: %s\ngot:      %s", expectedSignature, receivedSignature)
	}
}

func TestWebhook_PublishWithCustomHeaders(t *testing.T) {
	var receivedAuth string
	var receivedCustom string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedCustom = r.Header.Get("X-Custom")

		// Verify event headers are in envelope body, not HTTP headers
		body, _ := io.ReadAll(r.Body)
		var envelope struct {
			Headers map[string]string `json:"headers"`
		}
		json.Unmarshal(body, &envelope)

		if envelope.Headers["Foo"] != "bar" {
			t.Errorf("expected envelope.headers.Foo='bar', got '%s'", envelope.Headers["Foo"])
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.PublisherConfig{
		Name: "test-webhook",
		URL:  server.URL,
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Custom":      "custom-value",
		},
	}

	webhook, _ := New(cfg)
	defer webhook.Close()

	event := source.Event{
		ID:            "event-123",
		EventType:     "test",
		AggregateType: "test",
		AggregateID:   "agg-1",
		Payload:       []byte(`{}`),
		Headers:       map[string]string{"Foo": "bar"},
		CreatedAt:     time.Now(),
	}

	result := webhook.Publish(context.Background(), event)

	if !result.Success {
		t.Errorf("expected success, got: %s", result.ErrorMsg)
	}

	if receivedAuth != "Bearer token123" {
		t.Errorf("expected Authorization 'Bearer token123', got '%s'", receivedAuth)
	}
	if receivedCustom != "custom-value" {
		t.Errorf("expected X-Custom 'custom-value', got '%s'", receivedCustom)
	}
}

func TestWebhook_Publish2xxSuccess(t *testing.T) {
	successCodes := []int{200, 201, 202, 204}

	for _, statusCode := range successCodes {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))
			defer server.Close()

			cfg := &config.PublisherConfig{Name: "test", URL: server.URL}
			webhook, _ := New(cfg)
			defer webhook.Close()

			event := source.Event{ID: "event-123", EventType: "test", AggregateType: "test", AggregateID: "agg-1", Payload: []byte(`{}`), CreatedAt: time.Now()}
			result := webhook.Publish(context.Background(), event)

			if !result.Success {
				t.Errorf("expected success for %d, got error: %s", statusCode, result.ErrorMsg)
			}
		})
	}
}

func TestWebhook_Publish429Retryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limit exceeded"))
	}))
	defer server.Close()

	cfg := &config.PublisherConfig{Name: "test", URL: server.URL}
	webhook, _ := New(cfg)
	defer webhook.Close()

	event := source.Event{ID: "event-123", EventType: "test", AggregateID: "agg-1"}
	result := webhook.Publish(context.Background(), event)

	if result.Success {
		t.Error("expected failure for 429")
	}
	if !result.Retryable {
		t.Error("expected 429 to be retryable")
	}
	if !strings.Contains(result.ErrorMsg, "429") {
		t.Errorf("expected error to mention 429, got: %s", result.ErrorMsg)
	}
}

func TestWebhook_Publish5xxRetryable(t *testing.T) {
	serverErrors := []int{500, 502, 503, 504}

	for _, statusCode := range serverErrors {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
				w.Write([]byte("server error"))
			}))
			defer server.Close()

			cfg := &config.PublisherConfig{Name: "test", URL: server.URL}
			webhook, _ := New(cfg)
			defer webhook.Close()

			event := source.Event{ID: "event-123", EventType: "test", AggregateType: "test", AggregateID: "agg-1", Payload: []byte(`{}`), CreatedAt: time.Now()}
			result := webhook.Publish(context.Background(), event)

			if result.Success {
				t.Errorf("expected failure for %d", statusCode)
			}
			if !result.Retryable {
				t.Errorf("expected %d to be retryable", statusCode)
			}
		})
	}
}

func TestWebhook_Publish4xxFatal(t *testing.T) {
	clientErrors := []int{400, 401, 403, 404, 422}

	for _, statusCode := range clientErrors {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
				w.Write([]byte("client error"))
			}))
			defer server.Close()

			cfg := &config.PublisherConfig{Name: "test", URL: server.URL}
			webhook, _ := New(cfg)
			defer webhook.Close()

			event := source.Event{ID: "event-123", EventType: "test", AggregateType: "test", AggregateID: "agg-1", Payload: []byte(`{}`), CreatedAt: time.Now()}
			result := webhook.Publish(context.Background(), event)

			if result.Success {
				t.Errorf("expected failure for %d", statusCode)
			}
			if result.Retryable {
				t.Errorf("expected %d to be fatal (not retryable)", statusCode)
			}
		})
	}
}

func TestWebhook_Publish3xxFatal(t *testing.T) {
	redirects := []int{301, 302, 307, 308}

	for _, statusCode := range redirects {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://example.com/new")
				w.WriteHeader(statusCode)
			}))
			defer server.Close()

			cfg := &config.PublisherConfig{Name: "test", URL: server.URL}
			webhook, _ := New(cfg)
			defer webhook.Close()

			event := source.Event{ID: "event-123", EventType: "test", AggregateType: "test", AggregateID: "agg-1", Payload: []byte(`{}`), CreatedAt: time.Now()}
			result := webhook.Publish(context.Background(), event)

			if result.Success {
				t.Errorf("expected failure for redirect %d", statusCode)
			}
			if result.Retryable {
				t.Errorf("expected redirect %d to be fatal (configuration issue)", statusCode)
			}
		})
	}
}

func TestWebhook_PublishNetworkError(t *testing.T) {
	// Use invalid URL to trigger network error
	cfg := &config.PublisherConfig{
		Name:    "test",
		URL:     "http://localhost:1", // Port 1 is unlikely to have a service
		Timeout: 100 * time.Millisecond,
	}

	webhook, _ := New(cfg)
	defer webhook.Close()

	event := source.Event{ID: "event-123", EventType: "test", AggregateID: "agg-1"}
	result := webhook.Publish(context.Background(), event)

	if result.Success {
		t.Error("expected failure for network error")
	}
	if !result.Retryable {
		t.Error("expected network error to be retryable")
	}
}

func TestWebhook_PublishTimeout(t *testing.T) {
	// Server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.PublisherConfig{
		Name:    "test",
		URL:     server.URL,
		Timeout: 50 * time.Millisecond, // Short timeout
	}

	webhook, _ := New(cfg)
	defer webhook.Close()

	event := source.Event{ID: "event-123", EventType: "test", AggregateID: "agg-1"}
	result := webhook.Publish(context.Background(), event)

	if result.Success {
		t.Error("expected failure for timeout")
	}
	if !result.Retryable {
		t.Error("expected timeout to be retryable")
	}
}

func TestWebhook_Name(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name: "my-webhook",
		URL:  "https://example.com",
	}

	webhook, _ := New(cfg)
	defer webhook.Close()

	if webhook.Name() != "my-webhook" {
		t.Errorf("expected name 'my-webhook', got '%s'", webhook.Name())
	}
}

func TestWebhook_Close(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name: "test",
		URL:  "https://example.com",
	}

	webhook, _ := New(cfg)

	err := webhook.Close()
	if err != nil {
		t.Errorf("expected no error from Close, got: %v", err)
	}

	// Close should be idempotent
	err = webhook.Close()
	if err != nil {
		t.Errorf("expected no error from second Close, got: %v", err)
	}
}

func TestWebhook_TruncatesLongResponseBody(t *testing.T) {
	longBody := strings.Repeat("x", 500) // Longer than 200 char limit

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(longBody))
	}))
	defer server.Close()

	cfg := &config.PublisherConfig{Name: "test", URL: server.URL}
	webhook, _ := New(cfg)
	defer webhook.Close()

	event := source.Event{ID: "event-123", EventType: "test", AggregateID: "agg-1"}
	result := webhook.Publish(context.Background(), event)

	// Error message should be truncated
	if len(result.ErrorMsg) > 250 { // 200 + "client error (400): " + "..."
		t.Errorf("expected error message to be truncated, got length %d", len(result.ErrorMsg))
	}
	if !strings.Contains(result.ErrorMsg, "...") {
		t.Error("expected truncated error message to contain '...'")
	}
}
