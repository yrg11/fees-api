package fees

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"encore.dev/beta/errs"
)

var currencyCodeRegex = regexp.MustCompile(`^[A-Z]{3}$`)

type AddCurrencyRequest struct {
	Code string `json:"code"` // ISO 4217 currency code (e.g., "EUR", "GBP")
	Name string `json:"name"` // Display name (e.g., "Euro", "British Pound")
}

type AddCurrencyResponse struct {
	Currency    CurrencyRecord `json:"currency"`
	RatesSeeded int            `json:"rates_seeded"`
}

type ListCurrenciesResponse struct {
	Currencies []CurrencyRecord `json:"currencies"`
}

// AddCurrency registers a new currency after verifying FX data is available from Alpha Vantage.
// This operation is atomic: if rate fetching or seeding fails, the currency is not added.
//
//encore:api private method=POST path=/currencies
func AddCurrency(ctx context.Context, req *AddCurrencyRequest) (*AddCurrencyResponse, error) {
	if !currencyCodeRegex.MatchString(req.Code) {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "currency code must be exactly 3 uppercase letters (e.g., EUR)"}
	}
	if req.Name == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "currency name is required"}
	}

	code := Currency(req.Code)

	// Check if currency already exists
	existing, err := getCurrency(ctx, code)
	if err == nil && existing.Active {
		return nil, &errs.Error{Code: errs.AlreadyExists, Message: fmt.Sprintf("currency %s already exists", req.Code)}
	}

	// Step 1: Verify we can fetch FX rates from Alpha Vantage (before any DB writes)
	testRate, err := avFetchCurrentRate(ctx, CurrencyUSD, code)
	if err != nil {
		return nil, &errs.Error{
			Code:    errs.FailedPrecondition,
			Message: fmt.Sprintf("cannot fetch FX rate for %s from Alpha Vantage: %v", req.Code, err),
		}
	}

	// Step 2: Fetch historical rates (before any DB writes)
	historicalRates, err := avFetchDailyRates(ctx, CurrencyUSD, code, 30)
	if err != nil {
		return nil, &errs.Error{
			Code:    errs.FailedPrecondition,
			Message: fmt.Sprintf("cannot fetch historical rates for %s: %v", req.Code, err),
		}
	}

	// Step 3: All external calls succeeded — now do atomic DB writes in a transaction
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, &errs.Error{Code: errs.Internal, Message: fmt.Sprintf("begin transaction: %v", err)}
	}
	defer tx.Rollback()

	// Insert currency
	var currencyRecord CurrencyRecord
	err = tx.QueryRow(ctx, `
		INSERT INTO currencies (code, name, is_base, active)
		VALUES ($1, $2, FALSE, TRUE)
		RETURNING code, name, is_base, active, created_at
	`, code, req.Name).Scan(
		&currencyRecord.Code,
		&currencyRecord.Name,
		&currencyRecord.IsBase,
		&currencyRecord.Active,
		&currencyRecord.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, &errs.Error{Code: errs.AlreadyExists, Message: fmt.Sprintf("currency %s already exists", req.Code)}
		}
		return nil, &errs.Error{Code: errs.Internal, Message: fmt.Sprintf("insert currency: %v", err)}
	}

	// Seed today's rate
	today := time.Now().UTC().Truncate(24 * time.Hour)
	seeded := 0

	_, err = tx.Exec(ctx, `
		INSERT INTO fx_rates (base_currency, quote_currency, rate, rate_date, source)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (base_currency, quote_currency, rate_date)
		DO UPDATE SET rate = EXCLUDED.rate, source = EXCLUDED.source, fetched_at = now()
	`, CurrencyUSD, code, testRate, today, "alphavantage")
	if err == nil {
		seeded++
	}

	// Seed historical rates
	for _, r := range historicalRates {
		_, err = tx.Exec(ctx, `
			INSERT INTO fx_rates (base_currency, quote_currency, rate, rate_date, source)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (base_currency, quote_currency, rate_date)
			DO UPDATE SET rate = EXCLUDED.rate, source = EXCLUDED.source, fetched_at = now()
		`, CurrencyUSD, code, r.rate, r.date, "alphavantage")
		if err == nil {
			seeded++
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, &errs.Error{Code: errs.Internal, Message: fmt.Sprintf("commit transaction: %v", err)}
	}

	return &AddCurrencyResponse{
		Currency:    currencyRecord,
		RatesSeeded: seeded,
	}, nil
}

// ListCurrencies returns all active currencies.
//
//encore:api public method=GET path=/currencies
func ListCurrencies(ctx context.Context) (*ListCurrenciesResponse, error) {
	currencies, err := listActiveCurrencies(ctx)
	if err != nil {
		return nil, &errs.Error{Code: errs.Internal, Message: err.Error()}
	}

	return &ListCurrenciesResponse{Currencies: currencies}, nil
}
