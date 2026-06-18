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

	w := worker.New(c, FeeTaskQueue, worker.Options{})

	w.RegisterWorkflow(FeePeriodWorkflow)
	w.RegisterActivity(AddLineItemActivity)
	w.RegisterActivity(CloseBillActivity)

	log.Println("starting temporal worker")

	return w.Run(worker.InterruptCh())
}
