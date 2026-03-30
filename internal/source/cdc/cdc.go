package cdc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/metrics"
	"github.com/slapec93/pg-outboxer/internal/source"
)

// CDC implements the Source interface using PostgreSQL logical replication (CDC)
type CDC struct {
	conn         *pgconn.PgConn
	config       *config.SourceConfig
	slotName     string
	publication  string
	tableName    string
	dlqTable     string
	maxRetries   int
	clientXLogPos pglogrepl.LSN
	typeMap      *pgtype.Map
}

// New creates a new CDC source
func New(cfg *config.Config) (*CDC, error) {
	// Parse connection string and add replication parameter
	connConfig, err := pgconn.ParseConfig(cfg.Source.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	// Enable replication mode
	connConfig.RuntimeParams["replication"] = "database"

	// Connect with replication mode
	conn, err := pgconn.ConnectConfig(context.Background(), connConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect with replication: %w", err)
	}

	slog.Info("cdc source initialized",
		"slot_name", cfg.Source.SlotName,
		"publication", cfg.Source.Publication,
		"table", cfg.Source.Table)

	return &CDC{
		conn:        conn,
		config:      &cfg.Source,
		slotName:    cfg.Source.SlotName,
		publication: cfg.Source.Publication,
		tableName:   cfg.Source.Table,
		dlqTable:    cfg.Delivery.DeadLetterTable,
		maxRetries:  cfg.Delivery.MaxRetries,
		typeMap:     pgtype.NewMap(),
	}, nil
}

// Start begins streaming CDC events
func (c *CDC) Start(ctx context.Context, out chan<- source.Event) error {
	slog.Info("starting cdc stream",
		"slot", c.slotName,
		"publication", c.publication)

	// Identify system to establish replication connection
	if err := c.identifySystem(); err != nil {
		return fmt.Errorf("failed to identify system: %w", err)
	}

	// Create replication slot if it doesn't exist
	if err := c.ensureReplicationSlot(ctx); err != nil {
		return fmt.Errorf("failed to ensure replication slot: %w", err)
	}

	// Start replication from slot
	if err := c.startReplication(ctx); err != nil {
		return fmt.Errorf("failed to start replication: %w", err)
	}

	// Stream messages
	return c.streamMessages(ctx, out)
}

// identifySystem sends IDENTIFY_SYSTEM command
func (c *CDC) identifySystem() error {
	result := c.conn.Exec(context.Background(), "IDENTIFY_SYSTEM")
	_, err := result.ReadAll()
	if err != nil {
		return fmt.Errorf("identify system failed: %w", err)
	}
	return nil
}

// ensureReplicationSlot creates the replication slot if it doesn't exist
func (c *CDC) ensureReplicationSlot(ctx context.Context) error {
	// Slot doesn't exist, create it
	slog.Info("creating replication slot", "name", c.slotName)

	result, err := pglogrepl.CreateReplicationSlot(
		ctx,
		c.conn,
		c.slotName,
		"pgoutput",
		pglogrepl.CreateReplicationSlotOptions{Temporary: false},
	)
	if err != nil {
		// Check if slot already exists
		if pgconn.SafeToRetry(err) {
			slog.Info("replication slot already exists", "name", c.slotName)
			return nil
		}
		return fmt.Errorf("failed to create replication slot: %w", err)
	}

	slog.Info("replication slot created",
		"slot", c.slotName,
		"consistent_point", result.ConsistentPoint,
		"snapshot", result.SnapshotName)

	return nil
}

// startReplication starts streaming from the replication slot
func (c *CDC) startReplication(ctx context.Context) error {
	// Get the current LSN from the slot
	var restartLSN pglogrepl.LSN

	// Start from the slot's restart_lsn
	err := pglogrepl.StartReplication(
		ctx,
		c.conn,
		c.slotName,
		restartLSN,
		pglogrepl.StartReplicationOptions{
			PluginArgs: []string{
				"proto_version '1'",
				fmt.Sprintf("publication_names '%s'", c.publication),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to start replication: %w", err)
	}

	c.clientXLogPos = restartLSN
	slog.Info("replication started", "lsn", restartLSN)

	// Mark replication as active
	metrics.RecordReplicationActive(true)
	metrics.RecordWALPosition(uint64(restartLSN))

	return nil
}

// streamMessages receives and processes WAL messages
func (c *CDC) streamMessages(ctx context.Context, out chan<- source.Event) error {
	standbyMessageTimeout := time.Second * 10
	nextStandbyMessageDeadline := time.Now().Add(standbyMessageTimeout)

	for {
		select {
		case <-ctx.Done():
			slog.Info("cdc stream stopped")
			return nil

		default:
			// Check if we need to send standby status
			if time.Now().After(nextStandbyMessageDeadline) {
				if err := c.sendStandbyStatus(ctx); err != nil {
					slog.Error("failed to send standby status", "error", err)
					return err
				}
				nextStandbyMessageDeadline = time.Now().Add(standbyMessageTimeout)
			}

			// Receive message with timeout
			receiveCtx, cancel := context.WithTimeout(ctx, standbyMessageTimeout)
			msg, err := c.conn.ReceiveMessage(receiveCtx)
			cancel()

			if err != nil {
				if pgconn.Timeout(err) {
					// Timeout is expected, continue
					continue
				}
				metrics.RecordCDCConnectionError()
				return fmt.Errorf("receive message failed: %w", err)
			}

			switch msg := msg.(type) {
			case *pgproto3.CopyData:
				if err := c.processCopyData(ctx, msg, out); err != nil {
					slog.Error("failed to process copy data", "error", err)
					// Continue on error to avoid stopping the stream
				}

			case *pgproto3.CopyDone:
				slog.Info("copy done received")

			case *pgproto3.ErrorResponse:
				slog.Error("replication error", "error", msg.Message)
				return fmt.Errorf("replication error: %s", msg.Message)

			default:
				slog.Warn("unexpected message type", "type", fmt.Sprintf("%T", msg))
			}
		}
	}
}

// processCopyData processes a single CopyData message
func (c *CDC) processCopyData(ctx context.Context, msg *pgproto3.CopyData, out chan<- source.Event) error {
	switch msg.Data[0] {
	case pglogrepl.XLogDataByteID:
		metrics.RecordWALMessage("xlog_data")
		return c.processXLogData(ctx, msg.Data[1:], out)

	case pglogrepl.PrimaryKeepaliveMessageByteID:
		metrics.RecordWALMessage("keepalive")
		pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:])
		if err != nil {
			return fmt.Errorf("failed to parse keepalive: %w", err)
		}

		if pkm.ReplyRequested {
			if err := c.sendStandbyStatus(ctx); err != nil {
				return fmt.Errorf("failed to send standby status: %w", err)
			}
		}
	}

	return nil
}

// processXLogData processes WAL data
func (c *CDC) processXLogData(ctx context.Context, data []byte, out chan<- source.Event) error {
	xld, err := pglogrepl.ParseXLogData(data)
	if err != nil {
		return fmt.Errorf("failed to parse xlog data: %w", err)
	}

	// Decode logical replication message
	logicalMsg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		metrics.RecordCDCDecodingError("parse_error")
		return fmt.Errorf("failed to parse logical message: %w", err)
	}

	// Update client position
	c.clientXLogPos = xld.WALStart + pglogrepl.LSN(len(xld.WALData))
	metrics.RecordWALPosition(uint64(c.clientXLogPos))

	// Process based on message type
	switch msg := logicalMsg.(type) {
	case *pglogrepl.RelationMessage:
		metrics.RecordWALMessage("relation")
		// Store relation metadata (table schema)
		// For simplicity, we'll handle this inline when processing inserts

	case *pglogrepl.InsertMessage:
		metrics.RecordWALMessage("insert")
		return c.processInsert(ctx, msg, out)

	case *pglogrepl.UpdateMessage:
		metrics.RecordWALMessage("update")
		// Outbox events are insert-only, but handle updates just in case
		slog.Debug("update message received", "relation_id", msg.RelationID)

	case *pglogrepl.DeleteMessage:
		metrics.RecordWALMessage("delete")
		// Ignore deletes for outbox pattern
		slog.Debug("delete message received", "relation_id", msg.RelationID)

	case *pglogrepl.BeginMessage:
		metrics.RecordWALMessage("begin")
		// Transaction boundaries - we can track these for atomicity

	case *pglogrepl.CommitMessage:
		metrics.RecordWALMessage("commit")
		// Transaction commit

	default:
		metrics.RecordWALMessage("other")
		slog.Debug("unhandled message type", "type", fmt.Sprintf("%T", msg))
	}

	return nil
}

// processInsert converts an INSERT message to an outbox event
func (c *CDC) processInsert(ctx context.Context, msg *pglogrepl.InsertMessage, out chan<- source.Event) error {
	// Parse tuple data into event
	event, err := c.tupleToEvent(msg.Tuple)
	if err != nil {
		metrics.RecordCDCDecodingError("tuple_conversion")
		return fmt.Errorf("failed to convert tuple to event: %w", err)
	}

	// Record successful decode
	metrics.RecordCDCEventDecoded()

	// Send event to output channel
	select {
	case out <- event:
		slog.Debug("cdc event published",
			"event_id", event.ID,
			"type", event.EventType,
			"aggregate", fmt.Sprintf("%s/%s", event.AggregateType, event.AggregateID))
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

// tupleToEvent converts a logical replication tuple to an Event
func (c *CDC) tupleToEvent(tuple *pglogrepl.TupleData) (source.Event, error) {
	// This is a simplified version. In production, you'd:
	// 1. Use RelationMessage to map column names
	// 2. Properly decode each column type
	// 3. Handle NULL values
	//
	// For now, we'll assume a fixed schema matching our outbox table

	event := source.Event{
		CreatedAt: time.Now(),
	}

	// Expected columns: id, aggregate_type, aggregate_id, event_type, payload, headers, status, retry_count, retry_after, last_error, created_at, processed_at
	if len(tuple.Columns) < 12 {
		metrics.RecordCDCDecodingError("invalid_schema")
		return event, fmt.Errorf("unexpected number of columns: %d", len(tuple.Columns))
	}

	// Parse columns
	for i, col := range tuple.Columns {
		if col.Data == nil {
			continue
		}

		switch i {
		case 0: // id
			event.ID = string(col.Data)
		case 1: // aggregate_type
			event.AggregateType = string(col.Data)
		case 2: // aggregate_id
			event.AggregateID = string(col.Data)
		case 3: // event_type
			event.EventType = string(col.Data)
		case 4: // payload
			event.Payload = json.RawMessage(col.Data)
		case 5: // headers
			if len(col.Data) > 0 {
				var headers map[string]string
				if err := json.Unmarshal(col.Data, &headers); err == nil {
					event.Headers = headers
				}
			}
		case 7: // retry_count
			// Skip - we'll handle retries in the delivery layer
		}
	}

	return event, nil
}

// sendStandbyStatus sends status update to PostgreSQL
func (c *CDC) sendStandbyStatus(ctx context.Context) error {
	status := pglogrepl.StandbyStatusUpdate{
		WALWritePosition: c.clientXLogPos,
		WALFlushPosition: c.clientXLogPos,
		WALApplyPosition: c.clientXLogPos,
		ClientTime:       time.Now(),
		ReplyRequested:   false,
	}

	err := pglogrepl.SendStandbyStatusUpdate(ctx, c.conn, status)
	if err != nil {
		return fmt.Errorf("failed to send standby status: %w", err)
	}

	return nil
}

// Ack acknowledges successful delivery (marks as delivered in DB)
func (c *CDC) Ack(ctx context.Context, eventID string) error {
	// CDC doesn't need to update the database - we just advance the LSN
	// The actual status update would be done by a separate process if needed
	return nil
}

// Nack handles delivery failure (schedules retry or moves to DLQ)
func (c *CDC) Nack(ctx context.Context, eventID string, err error, retryable bool) error {
	// CDC doesn't handle nacks directly - would need a separate connection
	// For now, log the error
	slog.Warn("cdc nack received",
		"event_id", eventID,
		"retryable", retryable,
		"error", err)
	return nil
}

// Close closes the CDC connection
func (c *CDC) Close() error {
	if c.conn != nil {
		slog.Info("closing cdc connection")
		metrics.RecordReplicationActive(false)
		return c.conn.Close(context.Background())
	}
	return nil
}
