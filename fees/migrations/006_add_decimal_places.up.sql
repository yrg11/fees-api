ALTER TABLE currencies ADD COLUMN decimal_places INTEGER NOT NULL DEFAULT 2;

-- Update seed data (USD and GEL both use 2 decimal places, which matches the default)
