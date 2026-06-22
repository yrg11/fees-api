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

	// Periodically clean up old rate limit entries (older than 2 windows ago).
	// Fire-and-forget to avoid blocking the request.
	go cleanupOldRateLimitEntries(context.Background(), windowStart)

	return nil
}

// cleanupOldRateLimitEntries removes expired rate limit entries.
func cleanupOldRateLimitEntries(ctx context.Context, currentWindow time.Time) {
	cutoff := currentWindow.Add(-2 * RateLimitWindow)
	const query = `DELETE FROM rate_limit_entries WHERE window_start < $1`
	_, _ = db.Exec(ctx, query, cutoff)
}

// checkBruteForceLimit checks if this key hash has exceeded the brute-force attempt limit.
// It only reads the current count without incrementing.
func checkBruteForceLimit(ctx context.Context, keyHash string) error {
	identifier := "bf:" + keyHash[:16]
	windowStart := time.Now().UTC().Truncate(RateLimitWindow)

	const query = `
		SELECT request_count FROM rate_limit_entries
		WHERE customer_id = $1 AND window_start = $2
	`

	var count int
	err := db.QueryRow(ctx, query, identifier, windowStart).Scan(&count)
	if err != nil {
		// No row means no failed attempts yet — allow through.
		return nil
	}

	if count >= BruteForceMaxAttempts {
		return ErrRateLimited
	}

	return nil
}

// incrementBruteForceCounter records a failed auth attempt for brute-force tracking.
func incrementBruteForceCounter(ctx context.Context, keyHash string) {
	identifier := "bf:" + keyHash[:16]
	windowStart := time.Now().UTC().Truncate(RateLimitWindow)

	const query = `
		INSERT INTO rate_limit_entries (customer_id, window_start, request_count)
		VALUES ($1, $2, 1)
		ON CONFLICT (customer_id, window_start)
		DO UPDATE SET request_count = rate_limit_entries.request_count + 1
	`

	_, _ = db.Exec(ctx, query, identifier, windowStart)
}
