CREATE TABLE customers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE,
    api_key_hash TEXT NOT NULL,
    api_key_prefix TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    disabled_at TIMESTAMPTZ
);

CREATE INDEX idx_customers_api_key_hash ON customers(api_key_hash);

CREATE TABLE audit_log (
    id BIGSERIAL PRIMARY KEY,
    customer_id TEXT,
    event_type TEXT NOT NULL,
    detail JSONB,
    ip_address TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_customer_id ON audit_log(customer_id);
CREATE INDEX idx_audit_log_event_type ON audit_log(event_type);
CREATE INDEX idx_audit_log_created_at ON audit_log(created_at DESC);

CREATE TABLE rate_limit_entries (
    id BIGSERIAL PRIMARY KEY,
    customer_id TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    request_count INT NOT NULL DEFAULT 1,
    UNIQUE(customer_id, window_start)
);

CREATE INDEX idx_rate_limit_customer_window ON rate_limit_entries(customer_id, window_start DESC);
