package fees

import (
	"context"
	"encoding/json"
	"log"
)

// Audit event types
const (
	AuditEventAuthSuccess   = "auth_success"
	AuditEventAuthFailure   = "auth_failure"
	AuditEventKeyRotated    = "key_rotated"
	AuditEventCustomerCreated = "customer_created"
	AuditEventRateLimited   = "rate_limited"
)

type AuditDetail map[string]interface{}

// logAuditEvent records an event in the audit_log table.
// Errors are logged but not propagated to avoid disrupting the request flow.
func logAuditEvent(ctx context.Context, customerID string, eventType string, detail AuditDetail) {
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		log.Printf("audit: failed to marshal detail: %v", err)
		return
	}

	const query = `
		INSERT INTO audit_log (customer_id, event_type, detail)
		VALUES ($1, $2, $3)
	`

	_, err = db.Exec(ctx, query, nilIfEmpty(customerID), eventType, detailJSON)
	if err != nil {
		log.Printf("audit: failed to log event %s: %v", eventType, err)
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// cleanupOldAuditLogs removes audit entries older than 90 days.
// Called periodically in the background.
func cleanupOldAuditLogs(ctx context.Context) {
	const query = `DELETE FROM audit_log WHERE created_at < now() - interval '90 days'`
	_, _ = db.Exec(ctx, query)
}
