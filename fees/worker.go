package fees

import (
	"log"

	"go.temporal.io/sdk/worker"
)

func StartTemporalWorker() error {
	c, err := getTemporalClient()
	if err != nil {
		return err
	}

	// Fee period worker
	w := worker.New(c, FeeTaskQueue, worker.Options{})
	w.RegisterWorkflow(FeePeriodWorkflow)
	w.RegisterActivity(AddLineItemActivity)
	w.RegisterActivity(CloseBillActivity)

	// FX rate worker
	fxWorker := worker.New(c, FXTaskQueue, worker.Options{})
	fxWorker.RegisterWorkflow(FXRateCronWorkflow)
	fxWorker.RegisterActivity(FetchFXRateActivity)
	fxWorker.RegisterActivity(StoreFXRateActivity)
	fxWorker.RegisterActivity(ListActiveCurrencyCodesActivity)

	log.Println("starting temporal workers")

	// Start FX worker in background
	go func() {
		if err := fxWorker.Run(worker.InterruptCh()); err != nil {
			log.Printf("fx worker error: %v", err)
		}
	}()

	return w.Run(worker.InterruptCh())
}
