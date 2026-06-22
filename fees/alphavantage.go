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

// alphaVantageExchangeRateResponse represents the Alpha Vantage currency exchange rate response.
type alphaVantageExchangeRateResponse struct {
	RealtimeCurrencyExchangeRate struct {
		FromCurrencyCode string `json:"1. From_Currency Code"`
		ToCurrencyCode   string `json:"3. To_Currency Code"`
		ExchangeRate     string `json:"5. Exchange Rate"`
		LastRefreshed    string `json:"6. Last Refreshed"`
	} `json:"Realtime Currency Exchange Rate"`
	ErrorMessage string `json:"Error Message"`
	Note         string `json:"Note"`
}

// alphaVantageFXDailyResponse represents the Alpha Vantage FX_DAILY response.
type alphaVantageFXDailyResponse struct {
	MetaData     map[string]string            `json:"Meta Data"`
	TimeSeries   map[string]map[string]string `json:"Time Series FX (Daily)"`
	ErrorMessage string                       `json:"Error Message"`
	Note         string                       `json:"Note"`
}

type historicalRate struct {
	date time.Time
	rate float64
}

// avFetchCurrentRate fetches the current exchange rate for a currency pair from Alpha Vantage.
func avFetchCurrentRate(ctx context.Context, fromCurrency, toCurrency Currency) (float64, error) {
	apiKey := secrets.AlphaVantageAPIKey

	url := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=CURRENCY_EXCHANGE_RATE&from_currency=%s&to_currency=%s&apikey=%s",
		fromCurrency, toCurrency, apiKey,
	)

	body, err := avDoRequest(ctx, url)
	if err != nil {
		return 0, err
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

// avFetchDailyRates fetches historical daily rates for a currency pair from Alpha Vantage.
// Returns rates within the last `days` calendar days.
func avFetchDailyRates(ctx context.Context, fromCurrency, toCurrency Currency, days int) ([]historicalRate, error) {
	apiKey := secrets.AlphaVantageAPIKey

	outputSize := "compact"
	if days > 100 {
		outputSize = "full"
	}

	url := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=FX_DAILY&from_symbol=%s&to_symbol=%s&outputsize=%s&apikey=%s",
		fromCurrency, toCurrency, outputSize, apiKey,
	)

	body, err := avDoRequest(ctx, url)
	if err != nil {
		return nil, err
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

	if len(rates) == 0 {
		return nil, fmt.Errorf("no valid rates found for %s/%s", fromCurrency, toCurrency)
	}

	return rates, nil
}

var avHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

// avDoRequest performs an HTTP GET to the given URL and returns the response body.
func avDoRequest(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := avHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alpha vantage request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpha vantage returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
