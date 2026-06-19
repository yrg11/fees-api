package fees

import (
	"context"
	"log"

	"go.temporal.io/sdk/client"
)

//encore:service
type Service struct{}

func initService() (*Service, error) {
	// Start the Temporal worker in the background.
	// It polls the task queue and executes workflows/activities.
	go func() {
		if err := StartTemporalWorker(); err != nil {
			log.Fatalf("temporal worker failed: %v", err)
		}
	}()

	// Start the FX rate cron workflow (idempotent — uses fixed workflow ID).
	go func() {
		if err := startFXCronWorkflow(); err != nil {
			log.Printf("failed to start FX cron workflow: %v", err)
		}
	}()

	return &Service{}, nil
}

func startFXCronWorkflow() error {
	c, err := getTemporalClient()
	if err != nil {
		return err
	}

	workflowID := "fx-rate-cron-usd-gel"

	_, err = c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:           workflowID,
		TaskQueue:    FXTaskQueue,
		CronSchedule: "0 9 * * *", // Daily at 9am UTC
	}, FXRateCronWorkflow, FXRateCronWorkflowInput{
		BaseCurrency:  CurrencyUSD,
		QuoteCurrency: CurrencyGEL,
	})

	if err != nil {
		// If workflow already running, that's fine (idempotent).
		log.Printf("FX cron workflow start: %v", err)
	}

	return nil
}
