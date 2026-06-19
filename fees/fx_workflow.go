package fees

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const FXTaskQueue = "fx-task-queue"

// FXRateCronWorkflow is a Temporal cron workflow that fetches FX rates daily
// for all active non-USD currencies.
// It is scheduled with CronSchedule: "0 9 * * *" (daily at 9am UTC).
func FXRateCronWorkflow(ctx workflow.Context) error {
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	// Get list of active non-USD currencies
	var currencies []string
	err := workflow.ExecuteActivity(ctx, ListActiveCurrencyCodesActivity).Get(ctx, &currencies)
	if err != nil {
		return err
	}

	// Fetch and store rate for each currency
	for _, currencyCode := range currencies {
		var fetchOutput FetchFXRateActivityOutput
		err := workflow.ExecuteActivity(ctx, FetchFXRateActivity, FetchFXRateActivityInput{
			BaseCurrency:  CurrencyUSD,
			QuoteCurrency: Currency(currencyCode),
		}).Get(ctx, &fetchOutput)
		if err != nil {
			// Log but continue with other currencies
			workflow.GetLogger(ctx).Error("failed to fetch rate", "currency", currencyCode, "error", err)
			continue
		}

		err = workflow.ExecuteActivity(ctx, StoreFXRateActivity, StoreFXRateActivityInput{
			BaseCurrency:  CurrencyUSD,
			QuoteCurrency: Currency(currencyCode),
			Rate:          fetchOutput.Rate,
			RateDate:      fetchOutput.RateDate,
			Source:        fetchOutput.Source,
		}).Get(ctx, nil)
		if err != nil {
			workflow.GetLogger(ctx).Error("failed to store rate", "currency", currencyCode, "error", err)
		}
	}

	return nil
}
