CREATE TABLE currencies (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    is_base BOOLEAN NOT NULL DEFAULT FALSE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed USD (base currency) and GEL
INSERT INTO currencies (code, name, is_base) VALUES ('USD', 'US Dollar', TRUE);
INSERT INTO currencies (code, name, is_base) VALUES ('GEL', 'Georgian Lari', FALSE);

-- Remove the hardcoded currency CHECK constraints from bills and bill_line_items
ALTER TABLE bills DROP CONSTRAINT IF EXISTS bills_currency_check;
ALTER TABLE bill_line_items DROP CONSTRAINT IF EXISTS bill_line_items_base_currency_check;
ALTER TABLE bill_line_items DROP CONSTRAINT IF EXISTS bill_line_items_bill_currency_check;

-- Add foreign key references to currencies table
ALTER TABLE bills ADD CONSTRAINT bills_currency_fk FOREIGN KEY (currency) REFERENCES currencies(code);
ALTER TABLE bill_line_items ADD CONSTRAINT bill_line_items_base_currency_fk FOREIGN KEY (base_currency) REFERENCES currencies(code);
ALTER TABLE bill_line_items ADD CONSTRAINT bill_line_items_bill_currency_fk FOREIGN KEY (bill_currency) REFERENCES currencies(code);
ALTER TABLE fx_rates ADD CONSTRAINT fx_rates_base_currency_fk FOREIGN KEY (base_currency) REFERENCES currencies(code);
ALTER TABLE fx_rates ADD CONSTRAINT fx_rates_quote_currency_fk FOREIGN KEY (quote_currency) REFERENCES currencies(code);
