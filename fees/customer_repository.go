package fees

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrCustomerNotFound  = errors.New("customer not found")
	ErrCustomerDisabled  = errors.New("customer account is disabled")
	ErrEmailAlreadyTaken = errors.New("email already taken")
	ErrInvalidAPIKey     = errors.New("invalid api key")
	ErrRateLimited       = errors.New("rate limit exceeded")
)

type Customer struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	APIKeyPrefix string     `json:"api_key_prefix"`
	CreatedAt    time.Time  `json:"created_at"`
	DisabledAt   *time.Time `json:"disabled_at,omitempty"`
}

// generateAPIKey creates a new API key with format: fk_live_<32 hex chars>
func generateAPIKey() (string, error) {
	bytes := make([]byte, 16) // 128 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return "fk_live_" + hex.EncodeToString(bytes), nil
}

// generateCustomerID creates a customer ID with format: cust_<16 hex chars>
func generateCustomerID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate customer id: %w", err)
	}
	return "cust_" + hex.EncodeToString(bytes), nil
}

// hashAPIKey returns the SHA-256 hash of an API key.
func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// apiKeyPrefix returns the first 12 characters of the key for identification.
func apiKeyPrefix(key string) string {
	if len(key) > 12 {
		return key[:12]
	}
	return key
}

func createCustomer(ctx context.Context, name, email string) (Customer, string, error) {
	customerID, err := generateCustomerID()
	if err != nil {
		return Customer{}, "", err
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		return Customer{}, "", err
	}

	keyHash := hashAPIKey(apiKey)
	prefix := apiKeyPrefix(apiKey)

	const query = `
		INSERT INTO customers (id, name, email, api_key_hash, api_key_prefix)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, email, api_key_prefix, created_at, disabled_at
	`

	var c Customer
	err = db.QueryRow(ctx, query, customerID, name, email, keyHash, prefix).Scan(
		&c.ID,
		&c.Name,
		&c.Email,
		&c.APIKeyPrefix,
		&c.CreatedAt,
		&c.DisabledAt,
	)

	if err != nil {
		if isUniqueViolation(err) {
			return Customer{}, "", ErrEmailAlreadyTaken
		}
		return Customer{}, "", fmt.Errorf("create customer: %w", err)
	}

	return c, apiKey, nil
}

func getCustomerByAPIKeyHash(ctx context.Context, keyHash string) (Customer, error) {
	const query = `
		SELECT id, name, email, api_key_prefix, created_at, disabled_at
		FROM customers
		WHERE api_key_hash = $1
	`

	var c Customer
	err := db.QueryRow(ctx, query, keyHash).Scan(
		&c.ID,
		&c.Name,
		&c.Email,
		&c.APIKeyPrefix,
		&c.CreatedAt,
		&c.DisabledAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return Customer{}, ErrInvalidAPIKey
	}
	if err != nil {
		return Customer{}, fmt.Errorf("get customer by key: %w", err)
	}

	return c, nil
}

func getCustomerByID(ctx context.Context, customerID string) (Customer, error) {
	const query = `
		SELECT id, name, email, api_key_prefix, created_at, disabled_at
		FROM customers
		WHERE id = $1
	`

	var c Customer
	err := db.QueryRow(ctx, query, customerID).Scan(
		&c.ID,
		&c.Name,
		&c.Email,
		&c.APIKeyPrefix,
		&c.CreatedAt,
		&c.DisabledAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return Customer{}, ErrCustomerNotFound
	}
	if err != nil {
		return Customer{}, fmt.Errorf("get customer: %w", err)
	}

	return c, nil
}

func rotateAPIKey(ctx context.Context, customerID string) (string, error) {
	apiKey, err := generateAPIKey()
	if err != nil {
		return "", err
	}

	keyHash := hashAPIKey(apiKey)
	prefix := apiKeyPrefix(apiKey)

	const query = `
		UPDATE customers
		SET api_key_hash = $2, api_key_prefix = $3
		WHERE id = $1 AND disabled_at IS NULL
	`

	result, err := db.Exec(ctx, query, customerID, keyHash, prefix)
	if err != nil {
		return "", fmt.Errorf("rotate api key: %w", err)
	}
	if result.RowsAffected() == 0 {
		return "", ErrCustomerNotFound
	}

	return apiKey, nil
}

// isUniqueViolation checks if the error is a PostgreSQL unique constraint violation (code 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
