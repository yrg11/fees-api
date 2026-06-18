CREATE TABLE bills (
    id BIGSERIAL PRIMARY KEY,
    customer_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK (currency IN ('USD', 'GEL')),
    status TEXT NOT NULL CHECK (status IN ('OPEN', 'CLOSED')),
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    total_amount_minor BIGINT NOT NULL DEFAULT 0,
    temporal_workflow_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at TIMESTAMPTZ
);

CREATE INDEX idx_bills_status ON bills(status);
CREATE INDEX idx_bills_customer_id ON bills(customer_id);

CREATE TABLE bill_line_items (
    id BIGSERIAL PRIMARY KEY,
    bill_id BIGINT NOT NULL REFERENCES bills(id),
    description TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency IN ('USD', 'GEL')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bill_line_items_bill_id ON bill_line_items(bill_id);