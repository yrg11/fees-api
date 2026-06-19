package fees

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type SeedFXRatesRequest struct {
	Days int `json:"days"` // Number of past days to seed (default 30)
}

type SeedFXRatesResponse struct {
	Seeded  int    `json:"seeded"`
	Message string `json:"message"`
}

// SeedFXRates fetches historical FX rates from Alpha Vantage and stores them.
//
//encore:api private method=POST path=/fx/seed
func SeedFXRates(ctx context.Context, req *SeedFXRatesRequest) (*SeedFXRatesResponse, error) {
	days := req.Days
	if days <= 0 {
		days = 30
	}

	rates, err := fetchHistoricalRates(ctx, CurrencyUSD, CurrencyGEL, days)
	if err != nil {
		return nil, fmt.Errorf("fetch historical rates: %w", err)
	}

	seeded := 0
	for _, r := range rates {
		_, err := storeFXRate(ctx, CurrencyUSD, CurrencyGEL, r.rate, r.date, "alphavantage")
		if err != nil {
			return nil, fmt.Errorf("store rate for %s: %w", r.date.Format("2006-01-02"), err)
		}
		seeded++
	}

	return &SeedFXRatesResponse{
		Seeded:  seeded,
		Message: fmt.Sprintf("Seeded %d FX rates for USD/GEL over the past %d days", seeded, days),
	}, nil
}

type historicalRate struct {
	date time.Time
	rate float64
}

// alphaVantageFXDailyResponse represents the Alpha Vantage FX_DAILY response.
type alphaVantageFXDailyResponse struct {
	MetaData   map[string]string            `json:"Meta Data"`
	TimeSeries map[string]map[string]string `json:"Time Series FX (Daily)"`
	ErrorMessage string                     `json:"Error Message"`
	Note         string                     `json:"Note"`
}

// fetchHistoricalRates fetches historical daily rates from Alpha Vantage FX_DAILY endpoint.
func fetchHistoricalRates(ctx context.Context, base, quote Currency, days int) ([]historicalRate, error) {
	apiKey := secrets.AlphaVantageAPIKey

	outputSize := "compact" // last 100 data points
	if days > 100 {
		outputSize = "full"
	}

	url := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=FX_DAILY&from_symbol=%s&to_symbol=%s&outputsize=%s&apikey=%s",
		base, quote, outputSize, apiKey,
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
		return nil, fmt.Errorf("alpha vantage returned status %d: %s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("no time series data from alpha vantage")
	}

	cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days)

	var rates []historicalRate
	for dateStr, values := range avResp.TimeSeries {
		rateDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		// Only include dates within the requested range
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

	if len(rates) == 0 {
		return nil, fmt.Errorf("no valid rates found in alpha vantage response")
	}

	return rates, nil
}
