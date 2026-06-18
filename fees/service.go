package fees

import "log"

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

	return &Service{}, nil
}
