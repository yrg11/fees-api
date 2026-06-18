CREATE TABLE fx_rates (
    id BIGSERIAL PRIMARY KEY,
    base_currency TEXT NOT NULL,
    quote_currency TEXT NOT NULL,
    rate NUMERIC(18,8) NOT NULL,
    rate_date DATE NOT NULL,
    source TEXT NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(base_currency, quote_currency, rate_date)
);

CREATE INDEX idx_fx_rates_lookup ON fx_rates(base_currency, quote_currency, rate_date DESC);

-- Alter bill_line_items to support cross-currency
ALTER TABLE bill_line_items
    ADD COLUMN base_currency TEXT,
    ADD COLUMN base_amount_minor BIGINT,
    ADD COLUMN bill_currency TEXT,
    ADD COLUMN bill_amount_minor BIGINT,
    ADD COLUMN fx_rate NUMERIC(18,8),
    ADD COLUMN fx_rate_date DATE;

-- Migrate existing data
UPDATE bill_line_items
SET
    base_currency = currency,
    base_amount_minor = amount_minor,
    bill_currency = currency,
    bill_amount_minor = amount_minor;

-- Make new columns NOT NULL after backfill
ALTER TABLE bill_line_items
    ALTER COLUMN base_currency SET NOT NULL,
    ALTER COLUMN base_amount_minor SET NOT NULL,
    ALTER COLUMN bill_currency SET NOT NULL,
    ALTER COLUMN bill_amount_minor SET NOT NULL;

-- Drop old columns
ALTER TABLE bill_line_items
    DROP COLUMN amount_minor,
    DROP COLUMN currency;
