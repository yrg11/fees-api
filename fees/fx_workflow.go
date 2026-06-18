package fees

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const FXTaskQueue = "fx-task-queue"

// FXRateCronWorkflowInput is passed when starting the FX cron workflow.
type FXRateCronWorkflowInput struct {
	BaseCurrency  Currency `json:"base_currency"`
	QuoteCurrency Currency `json:"quote_currency"`
}

// FXRateCronWorkflow is a Temporal cron workflow that fetches FX rates daily.
// It is scheduled with CronSchedule: "0 9 * * *" (daily at 9am UTC).
func FXRateCronWorkflow(ctx workflow.Context, input FXRateCronWorkflowInput) error {
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	// Fetch the FX rate from yfinance
	var fetchOutput FetchFXRateActivityOutput
	err := workflow.ExecuteActivity(ctx, FetchFXRateActivity, FetchFXRateActivityInput{
		BaseCurrency:  input.BaseCurrency,
		QuoteCurrency: input.QuoteCurrency,
	}).Get(ctx, &fetchOutput)
	if err != nil {
		return err
	}

	// Store the rate in the database
	err = workflow.ExecuteActivity(ctx, StoreFXRateActivity, StoreFXRateActivityInput{
		BaseCurrency:  input.BaseCurrency,
		QuoteCurrency: input.QuoteCurrency,
		Rate:          fetchOutput.Rate,
		RateDate:      fetchOutput.RateDate,
		Source:        fetchOutput.Source,
	}).Get(ctx, nil)

	return err
}
