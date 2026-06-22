-- Add idempotency_key to bill_line_items to prevent duplicate inserts
-- from Temporal activity retries (at-least-once delivery).
ALTER TABLE bill_line_items ADD COLUMN idempotency_key TEXT;

-- Unique constraint scoped to bill: same key on same bill = duplicate
CREATE UNIQUE INDEX idx_bill_line_items_idempotency
    ON bill_line_items (bill_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
