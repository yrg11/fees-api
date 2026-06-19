package fees

import (
	"context"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
)

// getAuthCustomerID returns the authenticated customer's ID.
func getAuthCustomerID() string {
	uid, _ := auth.UserID()
	return string(uid)
}

// AuthData is injected into the context for authenticated requests.
type AuthData struct {
	CustomerID string `json:"customer_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
}

// AuthHandler validates the API key from the Authorization header.
// Encore automatically extracts the Bearer token and passes it as `token`.
//
//encore:authhandler
func AuthHandler(ctx context.Context, token string) (auth.UID, *AuthData, error) {
	if token == "" {
		logAuditEvent(ctx, "", AuditEventAuthFailure, AuditDetail{
			"reason": "empty_token",
		})
		return "", nil, &errs.Error{Code: errs.Unauthenticated, Message: "missing api key"}
	}

	keyHash := hashAPIKey(token)
	customer, err := getCustomerByAPIKeyHash(ctx, keyHash)
	if err != nil {
		logAuditEvent(ctx, "", AuditEventAuthFailure, AuditDetail{
			"reason":     "invalid_key",
			"key_prefix": apiKeyPrefix(token),
		})
		return "", nil, &errs.Error{Code: errs.Unauthenticated, Message: "invalid api key"}
	}

	if customer.DisabledAt != nil {
		logAuditEvent(ctx, customer.ID, AuditEventAuthFailure, AuditDetail{
			"reason": "account_disabled",
		})
		return "", nil, &errs.Error{Code: errs.Unauthenticated, Message: "account is disabled"}
	}

	// Check rate limit
	if err := checkRateLimit(ctx, customer.ID); err != nil {
		logAuditEvent(ctx, customer.ID, AuditEventRateLimited, AuditDetail{
			"window":      RateLimitWindow.String(),
			"max_requests": RateLimitMaxRequests,
		})
		return "", nil, &errs.Error{Code: errs.ResourceExhausted, Message: "rate limit exceeded"}
	}

	logAuditEvent(ctx, customer.ID, AuditEventAuthSuccess, nil)

	return auth.UID(customer.ID), &AuthData{
		CustomerID: customer.ID,
		Name:       customer.Name,
		Email:      customer.Email,
	}, nil
}
