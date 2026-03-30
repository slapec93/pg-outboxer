package publisher

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/slapec93/pg-outboxer/internal/source"
)

func TestMulti_AllPublishersSucceed(t *testing.T) {
	publishers := []Publisher{
		&mockPublisher{name: "pub1", result: PublishResult{Success: true}},
		&mockPublisher{name: "pub2", result: PublishResult{Success: true}},
		&mockPublisher{name: "pub3", result: PublishResult{Success: true}},
	}

	multi := NewMulti(publishers)
	event := source.Event{ID: "event-1"}

	result := multi.Publish(context.Background(), event)

	if !result.Success {
		t.Errorf("expected success=true, got false")
	}
	if result.Retryable {
		t.Errorf("expected retryable=false, got true")
	}
	if result.ErrorMsg != "" {
		t.Errorf("expected empty error message, got: %s", result.ErrorMsg)
	}
}

func TestMulti_SinglePublisherFailsRetryable(t *testing.T) {
	publishers := []Publisher{
		&mockPublisher{name: "pub1", result: PublishResult{Success: true}},
		&mockPublisher{name: "pub2", result: PublishResult{Success: false, Retryable: true, ErrorMsg: "rate limited"}},
		&mockPublisher{name: "pub3", result: PublishResult{Success: true}},
	}

	multi := NewMulti(publishers)
	event := source.Event{ID: "event-1"}

	result := multi.Publish(context.Background(), event)

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if !result.Retryable {
		t.Errorf("expected retryable=true, got false")
	}
	if !strings.Contains(result.ErrorMsg, "pub2") {
		t.Errorf("expected error to mention pub2, got: %s", result.ErrorMsg)
	}
	if !strings.Contains(result.ErrorMsg, "rate limited") {
		t.Errorf("expected error to contain 'rate limited', got: %s", result.ErrorMsg)
	}
}

func TestMulti_SinglePublisherFailsFatal(t *testing.T) {
	publishers := []Publisher{
		&mockPublisher{name: "pub1", result: PublishResult{Success: true}},
		&mockPublisher{name: "pub2", result: PublishResult{Success: false, Retryable: false, ErrorMsg: "bad request"}},
		&mockPublisher{name: "pub3", result: PublishResult{Success: true}},
	}

	multi := NewMulti(publishers)
	event := source.Event{ID: "event-1"}

	result := multi.Publish(context.Background(), event)

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	// Should still be retryable=false because pub1 and pub3 succeeded
	// Actually no - if ANY publisher fails with fatal, and no others are retryable, then it's fatal
	// But here pub1 and pub3 succeeded, so we have mixed success/fatal
	// The logic is: if ANY fails and ANY of the failures is retryable, then retryable
	// So here: pub2 failed with fatal, no retryable failures, so it's fatal
	if result.Retryable {
		t.Errorf("expected retryable=false (fatal), got true")
	}
	if !strings.Contains(result.ErrorMsg, "pub2") {
		t.Errorf("expected error to mention pub2, got: %s", result.ErrorMsg)
	}
}

func TestMulti_MixedFailures(t *testing.T) {
	publishers := []Publisher{
		&mockPublisher{name: "pub1", result: PublishResult{Success: false, Retryable: true, ErrorMsg: "timeout"}},
		&mockPublisher{name: "pub2", result: PublishResult{Success: false, Retryable: false, ErrorMsg: "bad request"}},
		&mockPublisher{name: "pub3", result: PublishResult{Success: true}},
	}

	multi := NewMulti(publishers)
	event := source.Event{ID: "event-1"}

	result := multi.Publish(context.Background(), event)

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if !result.Retryable {
		t.Errorf("expected retryable=true (because pub1 is retryable), got false")
	}
	if !strings.Contains(result.ErrorMsg, "pub1") {
		t.Errorf("expected error to mention pub1, got: %s", result.ErrorMsg)
	}
	if !strings.Contains(result.ErrorMsg, "pub2") {
		t.Errorf("expected error to mention pub2, got: %s", result.ErrorMsg)
	}
}

func TestMulti_AllPublishersFailFatal(t *testing.T) {
	publishers := []Publisher{
		&mockPublisher{name: "pub1", result: PublishResult{Success: false, Retryable: false, ErrorMsg: "unauthorized"}},
		&mockPublisher{name: "pub2", result: PublishResult{Success: false, Retryable: false, ErrorMsg: "bad request"}},
	}

	multi := NewMulti(publishers)
	event := source.Event{ID: "event-1"}

	result := multi.Publish(context.Background(), event)

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if result.Retryable {
		t.Errorf("expected retryable=false (all fatal), got true")
	}
	if !strings.Contains(result.ErrorMsg, "pub1") {
		t.Errorf("expected error to mention pub1, got: %s", result.ErrorMsg)
	}
	if !strings.Contains(result.ErrorMsg, "pub2") {
		t.Errorf("expected error to mention pub2, got: %s", result.ErrorMsg)
	}
}

func TestMulti_EmptyPublishers(t *testing.T) {
	multi := NewMulti([]Publisher{})
	event := source.Event{ID: "event-1"}

	result := multi.Publish(context.Background(), event)

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if result.Retryable {
		t.Errorf("expected retryable=false, got true")
	}
	if !strings.Contains(result.ErrorMsg, "no publishers") {
		t.Errorf("expected error about no publishers, got: %s", result.ErrorMsg)
	}
}

func TestMulti_SinglePublisher(t *testing.T) {
	// Test fast path for single publisher
	publishers := []Publisher{
		&mockPublisher{name: "pub1", result: PublishResult{Success: false, Retryable: true, ErrorMsg: "timeout"}},
	}

	multi := NewMulti(publishers)
	event := source.Event{ID: "event-1"}

	result := multi.Publish(context.Background(), event)

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if !result.Retryable {
		t.Errorf("expected retryable=true, got false")
	}
	if result.ErrorMsg != "timeout" {
		t.Errorf("expected error 'timeout', got: %s", result.ErrorMsg)
	}
}

func TestMulti_Name(t *testing.T) {
	publishers := []Publisher{
		&mockPublisher{name: "stripe"},
		&mockPublisher{name: "chartmogul"},
	}

	multi := NewMulti(publishers)

	expected := "multi[stripe,chartmogul]"
	if multi.Name() != expected {
		t.Errorf("expected name '%s', got '%s'", expected, multi.Name())
	}
}

func TestMulti_Close(t *testing.T) {
	pub1 := &mockPublisher{name: "pub1", result: PublishResult{Success: true}}
	pub2 := &mockPublisher{name: "pub2", result: PublishResult{Success: true}}

	multi := NewMulti([]Publisher{pub1, pub2})

	err := multi.Close()
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	if !pub1.closed {
		t.Error("expected pub1 to be closed")
	}
	if !pub2.closed {
		t.Error("expected pub2 to be closed")
	}
}

// mockPublisherWithCloseError fails on Close
type mockPublisherWithCloseError struct {
	name string
}

func (m *mockPublisherWithCloseError) Publish(_ context.Context, _ source.Event) PublishResult {
	return PublishResult{Success: true}
}

func (m *mockPublisherWithCloseError) Name() string {
	return m.name
}

func (m *mockPublisherWithCloseError) Close() error {
	return fmt.Errorf("close failed")
}

func TestMulti_CloseWithErrors(t *testing.T) {
	publishers := []Publisher{
		&mockPublisherWithCloseError{name: "pub1"},
		&mockPublisher{name: "pub2", result: PublishResult{Success: true}},
		&mockPublisherWithCloseError{name: "pub3"},
	}

	multi := NewMulti(publishers)

	err := multi.Close()
	if err == nil {
		t.Fatal("expected error from Close, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "pub1") {
		t.Errorf("expected error to mention pub1, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "pub3") {
		t.Errorf("expected error to mention pub3, got: %s", errMsg)
	}
}
