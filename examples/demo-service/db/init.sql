-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Business table: orders
-- (Your application tables go here)
CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id TEXT NOT NULL,
    total DECIMAL(10,2) NOT NULL,
    items JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Note: outbox and outbox_dead_letter tables are created by pg-outboxer setup command
-- This is just a demo init script. In production, you would:
-- 1. Create your business tables
-- 2. Run: pg-outboxer setup --config=config.yaml

-- Insert some test data (optional)
INSERT INTO orders (id, customer_id, total, items) VALUES
    ('00000000-0000-0000-0000-000000000001', 'cust_demo', 100.00, '["Demo Item 1", "Demo Item 2"]'::jsonb);

INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES
    ('order', '00000000-0000-0000-0000-000000000001', 'order.created',
     '{"order_id": "00000000-0000-0000-0000-000000000001", "customer_id": "cust_demo", "total": 100.00}'::jsonb);
