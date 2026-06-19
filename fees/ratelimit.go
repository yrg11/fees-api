package fees

import (
	"context"
	"fmt"
	"time"
)

const (
	// RateLimitWindow is the time window for rate limiting.
	RateLimitWindow = 1 * time.Minute
	// RateLimitMaxRequests is the max requests per window per customer.
	RateLimitMaxRequests = 60
)

// checkRateLimit increments the request count for the customer in the current window.
// Returns ErrRateLimited if the limit is exceeded.
func checkRateLimit(ctx context.Context, customerID string) error {
	// Truncate to the current window start
	windowStart := time.Now().UTC().Truncate(RateLimitWindow)

	const query = `
		INSERT INTO rate_limit_entries (customer_id, window_start, request_count)
		VALUES ($1, $2, 1)
		ON CONFLICT (customer_id, window_start)
		DO UPDATE SET request_count = rate_limit_entries.request_count + 1
		RETURNING request_count
	`

	var count int
	err := db.QueryRow(ctx, query, customerID, windowStart).Scan(&count)
	if err != nil {
		return fmt.Errorf("check rate limit: %w", err)
	}

	if count > RateLimitMaxRequests {
		return ErrRateLimited
	}

	return nil
}
