package cdc

import (
	"context"
	"testing"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_InvalidDSN(t *testing.T) {
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         "invalid-dsn",
			Table:       "outbox",
			SlotName:    "test_slot",
			Publication: "test_pub",
		},
	}

	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse DSN")
}

func TestNew_ValidConfig(t *testing.T) {
	t.Skip("Requires real PostgreSQL with logical replication - covered by integration tests")

	// This test would need a real PostgreSQL with logical replication
	// For unit tests, we just verify the structure
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         "postgres://user:pass@localhost:5432/testdb",
			Table:       "outbox",
			SlotName:    "test_slot",
			Publication: "test_pub",
		},
		Delivery: config.DeliveryConfig{
			MaxRetries:      10,
			DeadLetterTable: "outbox_dead_letter",
		},
	}

	// We can't actually connect without a real database
	// This test documents the expected initialization behavior
	_ = cfg
}

func TestAck(t *testing.T) {
	// CDC Ack is a no-op (LSN advancement happens separately)
	cdc := &CDC{}
	err := cdc.Ack(context.TODO(), "test-event-id")
	assert.NoError(t, err)
}

func TestNack(t *testing.T) {
	// CDC Nack just logs (can't update DB from replication connection)
	cdc := &CDC{}
	err := cdc.Nack(context.TODO(), "test-event-id", assert.AnError, true)
	assert.NoError(t, err)
}

func TestClose_Nil(t *testing.T) {
	cdc := &CDC{}
	err := cdc.Close()
	assert.NoError(t, err)
}
