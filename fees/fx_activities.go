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

var secrets struct {
	AlphaVantageAPIKey string
}

// FetchFXRateActivityInput defines the input for fetching an FX rate.
type FetchFXRateActivityInput struct {
	BaseCurrency  Currency `json:"base_currency"`
	QuoteCurrency Currency `json:"quote_currency"`
}

// FetchFXRateActivityOutput is the result of fetching an FX rate.
type FetchFXRateActivityOutput struct {
	Rate     float64   `json:"rate"`
	RateDate time.Time `json:"rate_date"`
	Source   string    `json:"source"`
}

// StoreFXRateActivityInput defines the input for storing an FX rate.
type StoreFXRateActivityInput struct {
	BaseCurrency  Currency  `json:"base_currency"`
	QuoteCurrency Currency  `json:"quote_currency"`
	Rate          float64   `json:"rate"`
	RateDate      time.Time `json:"rate_date"`
	Source        string    `json:"source"`
}

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

// FetchFXRateActivity fetches the current FX rate from Alpha Vantage.
func FetchFXRateActivity(ctx context.Context, input FetchFXRateActivityInput) (FetchFXRateActivityOutput, error) {
	apiKey := secrets.AlphaVantageAPIKey

	url := fmt.Sprintf(
		"https://www.alphavantage.co/query?function=CURRENCY_EXCHANGE_RATE&from_currency=%s&to_currency=%s&apikey=%s",
		input.BaseCurrency, input.QuoteCurrency, apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FetchFXRateActivityOutput{}, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return FetchFXRateActivityOutput{}, fmt.Errorf("fetch fx rate from alpha vantage: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return FetchFXRateActivityOutput{}, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return FetchFXRateActivityOutput{}, fmt.Errorf("alpha vantage returned status %d: %s", resp.StatusCode, string(body))
	}

	var avResp alphaVantageExchangeRateResponse
	if err := json.Unmarshal(body, &avResp); err != nil {
		return FetchFXRateActivityOutput{}, fmt.Errorf("parse alpha vantage response: %w", err)
	}

	if avResp.ErrorMessage != "" {
		return FetchFXRateActivityOutput{}, fmt.Errorf("alpha vantage error: %s", avResp.ErrorMessage)
	}
	if avResp.Note != "" {
		return FetchFXRateActivityOutput{}, fmt.Errorf("alpha vantage rate limit: %s", avResp.Note)
	}

	rateStr := avResp.RealtimeCurrencyExchangeRate.ExchangeRate
	if rateStr == "" {
		return FetchFXRateActivityOutput{}, fmt.Errorf("no exchange rate in alpha vantage response")
	}

	rate, err := strconv.ParseFloat(rateStr, 64)
	if err != nil {
		return FetchFXRateActivityOutput{}, fmt.Errorf("parse rate %q: %w", rateStr, err)
	}

	if rate <= 0 {
		return FetchFXRateActivityOutput{}, fmt.Errorf("invalid rate from alpha vantage: %f", rate)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)

	return FetchFXRateActivityOutput{
		Rate:     rate,
		RateDate: today,
		Source:   "alphavantage",
	}, nil
}

// StoreFXRateActivity persists the FX rate to the database.
func StoreFXRateActivity(ctx context.Context, input StoreFXRateActivityInput) error {
	_, err := storeFXRate(ctx, input.BaseCurrency, input.QuoteCurrency, input.Rate, input.RateDate, input.Source)
	return err
}
