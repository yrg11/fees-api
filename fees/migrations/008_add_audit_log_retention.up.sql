-- Index to speed up audit log cleanup of old entries.
-- Application code will periodically DELETE WHERE created_at < (now() - retention).
CREATE INDEX idx_audit_log_retention ON audit_log (created_at);
