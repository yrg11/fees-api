package fees

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrCurrencyNotFound = errors.New("currency not found")

type CurrencyRecord struct {
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	DecimalPlaces int       `json:"decimal_places"`
	IsBase        bool      `json:"is_base"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
}

func getCurrency(ctx context.Context, code Currency) (CurrencyRecord, error) {
	const query = `
		SELECT code, name, decimal_places, is_base, active, created_at
		FROM currencies
		WHERE code = $1
	`

	var c CurrencyRecord
	err := db.QueryRow(ctx, query, code).Scan(
		&c.Code,
		&c.Name,
		&c.DecimalPlaces,
		&c.IsBase,
		&c.Active,
		&c.CreatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return CurrencyRecord{}, ErrCurrencyNotFound
	}
	if err != nil {
		return CurrencyRecord{}, fmt.Errorf("get currency: %w", err)
	}

	return c, nil
}

func listActiveCurrencies(ctx context.Context) ([]CurrencyRecord, error) {
	const query = `
		SELECT code, name, decimal_places, is_base, active, created_at
		FROM currencies
		WHERE active = TRUE
		ORDER BY is_base DESC, code ASC
	`

	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list currencies: %w", err)
	}
	defer rows.Close()

	var currencies []CurrencyRecord
	for rows.Next() {
		var c CurrencyRecord
		if err := rows.Scan(&c.Code, &c.Name, &c.DecimalPlaces, &c.IsBase, &c.Active, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan currency: %w", err)
		}
		currencies = append(currencies, c)
	}

	return currencies, rows.Err()
}

// listActiveNonBaseCurrencies returns all active currencies except the base (USD).
func listActiveNonBaseCurrencies(ctx context.Context) ([]CurrencyRecord, error) {
	const query = `
		SELECT code, name, decimal_places, is_base, active, created_at
		FROM currencies
		WHERE active = TRUE AND is_base = FALSE
		ORDER BY code ASC
	`

	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list non-base currencies: %w", err)
	}
	defer rows.Close()

	var currencies []CurrencyRecord
	for rows.Next() {
		var c CurrencyRecord
		if err := rows.Scan(&c.Code, &c.Name, &c.DecimalPlaces, &c.IsBase, &c.Active, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan currency: %w", err)
		}
		currencies = append(currencies, c)
	}

	return currencies, rows.Err()
}

func insertCurrency(ctx context.Context, code Currency, name string, decimalPlaces int) (CurrencyRecord, error) {
	const query = `
		INSERT INTO currencies (code, name, decimal_places, is_base, active)
		VALUES ($1, $2, $3, FALSE, TRUE)
		RETURNING code, name, decimal_places, is_base, active, created_at
	`

	var c CurrencyRecord
	err := db.QueryRow(ctx, query, code, name, decimalPlaces).Scan(
		&c.Code,
		&c.Name,
		&c.DecimalPlaces,
		&c.IsBase,
		&c.Active,
		&c.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return CurrencyRecord{}, fmt.Errorf("currency %s already exists", code)
		}
		return CurrencyRecord{}, fmt.Errorf("insert currency: %w", err)
	}

	return c, nil
}

// validateCurrencyFromDB checks that a currency code exists and is active.
func validateCurrencyFromDB(ctx context.Context, c Currency) error {
	rec, err := getCurrency(ctx, c)
	if err != nil {
		return ErrInvalidCurrency
	}
	if !rec.Active {
		return ErrInvalidCurrency
	}
	return nil
}
