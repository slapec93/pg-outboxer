package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/metrics"
	"github.com/slapec93/pg-outboxer/internal/publisher"
	"github.com/slapec93/pg-outboxer/internal/source"
)

// Webhook implements the Publisher interface for HTTP webhooks
type Webhook struct {
	name          string
	client        *http.Client
	url           string
	signingSecret string
	headers       map[string]string
	timeout       time.Duration
}

// webhookEnvelope wraps the event payload in a Stripe-style envelope
type webhookEnvelope struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Created       int64             `json:"created"`
	Data          webhookData       `json:"data"`
	AggregateType string            `json:"aggregate_type"`
	AggregateID   string            `json:"aggregate_id"`
	Headers       map[string]string `json:"headers,omitempty"`
}

type webhookData struct {
	Object json.RawMessage `json:"object"`
}

// New creates a new webhook publisher
func New(cfg *config.PublisherConfig) (*Webhook, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("publisher.url is required for webhook publisher")
	}

	client := &http.Client{
		Timeout: cfg.Timeout,
		// Don't follow redirects automatically
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	slog.Info("webhook publisher initialized",
		"name", cfg.Name,
		"url", cfg.URL,
		"timeout", cfg.Timeout,
		"has_signing_secret", cfg.SigningSecret != "")

	return &Webhook{
		name:          cfg.Name,
		client:        client,
		url:           cfg.URL,
		signingSecret: cfg.SigningSecret,
		headers:       cfg.Headers,
		timeout:       cfg.Timeout,
	}, nil
}

// Publish sends an event to the webhook URL
func (w *Webhook) Publish(ctx context.Context, event source.Event) (result publisher.PublishResult) {
	start := time.Now()
	defer func() {
		metrics.RecordPublish(w.name, result.Success, time.Since(start).Seconds())
	}()

	// Create Stripe-style envelope
	envelope := webhookEnvelope{
		ID:            event.ID,
		Type:          event.EventType,
		Created:       event.CreatedAt.Unix(),
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		Data: webhookData{
			Object: json.RawMessage(event.Payload),
		},
		Headers: event.Headers,
	}

	// Marshal envelope to JSON
	body, err := json.Marshal(envelope)
	if err != nil {
		return publisher.PublishResult{
			Success:   false,
			Retryable: false,
			ErrorMsg:  fmt.Sprintf("failed to marshal envelope: %v", err),
		}
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(body))
	if err != nil {
		return publisher.PublishResult{
			Success:   false,
			Retryable: false,
			ErrorMsg:  fmt.Sprintf("failed to create request: %v", err),
		}
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pg-outboxer")

	// Add custom headers from config
	for key, value := range w.headers {
		req.Header.Set(key, value)
	}

	// Add HMAC signature if signing secret is configured
	if w.signingSecret != "" {
		signature := w.sign(body)
		req.Header.Set("X-Signature-256", signature)
	}

	// Add event ID header for quick reference (idempotency)
	req.Header.Set("X-Event-ID", event.ID)

	// Send request
	resp, err := w.client.Do(req)

	// Handle network errors (connection refused, timeout, etc.)
	if err != nil {
		slog.Warn("webhook request failed",
			"publisher", w.name,
			"event_id", event.ID,
			"error", err,
			"duration", time.Since(start))

		return publisher.PublishResult{
			Success:   false,
			Retryable: true, // Network errors are retryable
			ErrorMsg:  fmt.Sprintf("request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	// Read response body (limited to 1MB to prevent memory issues)
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))

	// Check status code
	result = w.classifyResponse(resp.StatusCode, string(respBody))

	if result.Success {
		slog.Debug("webhook delivered successfully",
			"publisher", w.name,
			"event_id", event.ID,
			"status", resp.StatusCode,
			"duration", time.Since(start))
	} else {
		level := slog.LevelWarn
		if !result.Retryable {
			level = slog.LevelError // Fatal errors are more severe
		}

		slog.Log(ctx, level, "webhook delivery failed",
			"publisher", w.name,
			"event_id", event.ID,
			"status", resp.StatusCode,
			"retryable", result.Retryable,
			"error", result.ErrorMsg,
			"duration", time.Since(start))
	}

	return result
}

// classifyResponse determines if a response indicates success, and if not, whether it's retryable
func (w *Webhook) classifyResponse(statusCode int, body string) publisher.PublishResult {
	switch {
	case statusCode >= 200 && statusCode < 300:
		// 2xx - Success
		return publisher.PublishResult{
			Success:   true,
			Retryable: false,
			ErrorMsg:  "",
		}

	case statusCode == 429:
		// 429 Too Many Requests - Retryable (rate limiting)
		return publisher.PublishResult{
			Success:   false,
			Retryable: true,
			ErrorMsg:  fmt.Sprintf("rate limited (429): %s", truncate(body, 200)),
		}

	case statusCode >= 500:
		// 5xx - Server errors are retryable (downstream might recover)
		return publisher.PublishResult{
			Success:   false,
			Retryable: true,
			ErrorMsg:  fmt.Sprintf("server error (%d): %s", statusCode, truncate(body, 200)),
		}

	case statusCode >= 400 && statusCode < 500:
		// 4xx (except 429) - Client errors are fatal (won't fix themselves)
		// Examples: 400 Bad Request, 401 Unauthorized, 404 Not Found
		return publisher.PublishResult{
			Success:   false,
			Retryable: false, // Fatal - move to dead letter
			ErrorMsg:  fmt.Sprintf("client error (%d): %s", statusCode, truncate(body, 200)),
		}

	case statusCode >= 300 && statusCode < 400:
		// 3xx - Redirects (we disabled auto-follow, so treat as error)
		return publisher.PublishResult{
			Success:   false,
			Retryable: false, // Configuration issue
			ErrorMsg:  fmt.Sprintf("unexpected redirect (%d): %s", statusCode, truncate(body, 200)),
		}

	default:
		// Unknown status code
		return publisher.PublishResult{
			Success:   false,
			Retryable: true, // Default to retryable for unknown cases
			ErrorMsg:  fmt.Sprintf("unexpected status (%d): %s", statusCode, truncate(body, 200)),
		}
	}
}

// sign generates HMAC-SHA256 signature for the payload
func (w *Webhook) sign(payload []byte) string {
	mac := hmac.New(sha256.New, []byte(w.signingSecret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Name returns the publisher name for logging/metrics
func (w *Webhook) Name() string {
	return w.name
}

// Close performs cleanup
func (w *Webhook) Close() error {
	w.client.CloseIdleConnections()
	return nil
}

// truncate limits string length for logging
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
