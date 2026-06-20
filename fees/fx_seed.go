package fees

import (
	"context"
	"fmt"
)

type SeedFXRatesRequest struct {
	Days int `json:"days"` // Number of past days to seed (default 30)
}

type SeedFXRatesResponse struct {
	Seeded  int    `json:"seeded"`
	Message string `json:"message"`
}

// SeedFXRates fetches historical FX rates from Alpha Vantage for all active non-USD currencies.
//
//encore:api private method=POST path=/fx/seed
func SeedFXRates(ctx context.Context, req *SeedFXRatesRequest) (*SeedFXRatesResponse, error) {
	days := req.Days
	if days <= 0 {
		days = 30
	}

	currencies, err := listActiveNonBaseCurrencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list currencies: %w", err)
	}

	totalSeeded := 0

	for _, curr := range currencies {
		rates, err := avFetchDailyRates(ctx, CurrencyUSD, Currency(curr.Code), days)
		if err != nil {
			return nil, fmt.Errorf("fetch historical rates for %s: %w", curr.Code, err)
		}

		for _, r := range rates {
			_, err := storeFXRate(ctx, CurrencyUSD, Currency(curr.Code), r.rate, r.date, "alphavantage")
			if err == nil {
				totalSeeded++
			}
		}
	}

	return &SeedFXRatesResponse{
		Seeded:  totalSeeded,
		Message: fmt.Sprintf("Seeded %d FX rates for %d currencies over the past %d days", totalSeeded, len(currencies), days),
	}, nil
}
