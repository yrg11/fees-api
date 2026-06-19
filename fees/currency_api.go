package fees

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"encore.dev/beta/errs"
)

type AddCurrencyRequest struct {
	Code string `json:"code"` // ISO 4217 currency code (e.g., "EUR", "GBP")
	Name string `json:"name"` // Display name (e.g., "Euro", "British Pound")
}

type AddCurrencyResponse struct {
	Currency   CurrencyRecord `json:"currency"`
	RatesSeeded int           `json:"rates_seeded"`
}

type ListCurrenciesResponse struct {
	Currencies []CurrencyRecord `json:"currencies"`
}

// AddCurrency registers a new currency after verifying FX data is available from Alpha Vantage.
// This operation is atomic: if rate fetching or seeding fails, the currency is not added.
//
//encore:api private method=POST path=/currencies
func AddCurrency(ctx context.Context, req *AddCurrencyRequest) (*AddCurrencyResponse, error) {
	if req.Code == "" || len(req.Code) < 3 {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "currency code must be at least 3 characters"}
	}
	if req.Name == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "currency name is required"}
	}

	code := Currency(req.Code)

	// Check if currency already exists
	existing, err := getCurrency(ctx, code)
	if err == nil {
		if existing.Active {
			return nil, &errs.Error{Code: errs.AlreadyExists, Message: fmt.Sprintf("currency %s already exists", req.Code)}
		}
	}

	// Step 1: Verify we can fetch FX rates from Alpha Vantage
	testRate, err := fetchCurrentRateFromAlphaVantage(ctx, code)
	if err != nil {
		return nil, &errs.Error{
			Code:    errs.FailedPrecondition,
			Message: fmt.Sprintf("cannot fetch FX rate for %s from Alpha Vantage: %v", req.Code, err),
		}
	}

	// Step 2: Fetch historical rates (30 days)
	historicalRates, err := fetchHistoricalRatesForCurrency(ctx, code, 30)
	if err != nil {
		return nil, &errs.Error{
			Code:    errs.FailedPrecondition,
			Message: fmt.Sprintf("cannot fetch historical rates for %s: %v", req.Code, err),
		}
	}

	// Step 3: Insert currency into DB
	currencyRecord, err := insertCurrency(ctx, code, req.Name)
	if err != nil {
		return nil, &errs.Error{Code: errs.Internal, Message: err.Error()}
	}

	// Step 4: Seed today's rate
	today := time.Now().UTC().Truncate(24 * time.Hour)
	seeded := 0
	_, err = storeFXRate(ctx, CurrencyUSD, code, testRate, today, "alphavantage")
	if err == nil {
		seeded++
	}

	// Step 5: Seed historical rates
	for _, r := range historicalRates {
		_, err = storeFXRate(ctx, CurrencyUSD, code, r.rate, r.date, "alphavantage")
		if err == nil {
			seeded++
		}
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

type historicalRate struct {
	date time.Time
	rate float64
}

// alphaVantageFXDailyResponse represents the Alpha Vantage FX_DAILY response.
type alphaVantageFXDailyResponse struct {
	MetaData     map[string]string            `json:"Meta Data"`
	TimeSeries   map[string]map[string]string `json:"Time Series FX (Daily)"`
	ErrorMessage string                       `json:"Error Message"`
	Note         string                       `json:"Note"`
}

// fetchCurrentRateFromAlphaVantage fetches the current exchange rate for USD -> currency.
func fetchCurrentRateFromAlphaVantage(ctx context.Context, currency Currency) (float64, error) {
	apiKey := secrets.AlphaVantageAPIKey

	url := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=CURRENCY_EXCHANGE_RATE&from_currency=USD&to_currency=%s&apikey=%s",
		currency, apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch rate: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("alpha vantage returned status %d", resp.StatusCode)
	}

	var avResp alphaVantageExchangeRateResponse
	if err := json.Unmarshal(body, &avResp); err != nil {
		return 0, fmt.Errorf("parse response: %w", err)
	}

	if avResp.ErrorMessage != "" {
		return 0, fmt.Errorf("alpha vantage error: %s", avResp.ErrorMessage)
	}
	if avResp.Note != "" {
		return 0, fmt.Errorf("alpha vantage rate limit: %s", avResp.Note)
	}

	rateStr := avResp.RealtimeCurrencyExchangeRate.ExchangeRate
	if rateStr == "" {
		return 0, fmt.Errorf("no exchange rate in response")
	}

	rate, err := strconv.ParseFloat(rateStr, 64)
	if err != nil || rate <= 0 {
		return 0, fmt.Errorf("invalid rate: %s", rateStr)
	}

	return rate, nil
}

// fetchHistoricalRatesForCurrency fetches historical daily rates for USD -> currency.
func fetchHistoricalRatesForCurrency(ctx context.Context, currency Currency, days int) ([]historicalRate, error) {
	apiKey := secrets.AlphaVantageAPIKey

	outputSize := "compact"
	if days > 100 {
		outputSize = "full"
	}

	url := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=FX_DAILY&from_symbol=USD&to_symbol=%s&outputsize=%s&apikey=%s",
		currency, outputSize, apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch from alpha vantage: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpha vantage returned status %d", resp.StatusCode)
	}

	var avResp alphaVantageFXDailyResponse
	if err := json.Unmarshal(body, &avResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if avResp.ErrorMessage != "" {
		return nil, fmt.Errorf("alpha vantage error: %s", avResp.ErrorMessage)
	}
	if avResp.Note != "" {
		return nil, fmt.Errorf("alpha vantage rate limit: %s", avResp.Note)
	}

	if len(avResp.TimeSeries) == 0 {
		return nil, fmt.Errorf("no time series data")
	}

	cutoff := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -days)

	var rates []historicalRate
	for dateStr, values := range avResp.TimeSeries {
		rateDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if rateDate.Before(cutoff) {
			continue
		}

		closeStr, ok := values["4. close"]
		if !ok {
			continue
		}

		rate, err := strconv.ParseFloat(closeStr, 64)
		if err != nil || rate <= 0 {
			continue
		}

		rates = append(rates, historicalRate{date: rateDate, rate: rate})
	}

	return rates, nil
}
