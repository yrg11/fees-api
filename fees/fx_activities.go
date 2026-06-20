package fees

import (
	"context"
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

// FetchFXRateActivity fetches the current FX rate from Alpha Vantage.
func FetchFXRateActivity(ctx context.Context, input FetchFXRateActivityInput) (FetchFXRateActivityOutput, error) {
	rate, err := avFetchCurrentRate(ctx, input.BaseCurrency, input.QuoteCurrency)
	if err != nil {
		return FetchFXRateActivityOutput{}, err
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

// ListActiveCurrencyCodesActivity returns all active non-USD currency codes.
func ListActiveCurrencyCodesActivity(ctx context.Context) ([]string, error) {
	currencies, err := listActiveNonBaseCurrencies(ctx)
	if err != nil {
		return nil, err
	}

	codes := make([]string, len(currencies))
	for i, c := range currencies {
		codes[i] = c.Code
	}
	return codes, nil
}
