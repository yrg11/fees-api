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
	// BruteForceMaxAttempts is the max auth attempts per key hash per window.
	// This protects against brute-force key guessing.
	BruteForceMaxAttempts = 10
)

// checkRateLimit increments the request count for the customer in the current window.
// Returns ErrRateLimited if the limit is exceeded.
func checkRateLimit(ctx context.Context, customerID string) error {
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

// checkBruteForceLimit rate-limits by key hash to prevent brute-force key guessing.
// Uses the same rate_limit_entries table with a "bf:" prefix on the identifier.
func checkBruteForceLimit(ctx context.Context, keyHash string) error {
	// Use first 16 chars of hash as identifier to save space
	identifier := "bf:" + keyHash[:16]
	windowStart := time.Now().UTC().Truncate(RateLimitWindow)

	const query = `
		INSERT INTO rate_limit_entries (customer_id, window_start, request_count)
		VALUES ($1, $2, 1)
		ON CONFLICT (customer_id, window_start)
		DO UPDATE SET request_count = rate_limit_entries.request_count + 1
		RETURNING request_count
	`

	var count int
	err := db.QueryRow(ctx, query, identifier, windowStart).Scan(&count)
	if err != nil {
		return fmt.Errorf("check brute force limit: %w", err)
	}

	if count > BruteForceMaxAttempts {
		return ErrRateLimited
	}

	return nil
}
