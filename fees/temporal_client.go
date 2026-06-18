package fees

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"go.temporal.io/sdk/client"
)

var (
	temporalClientOnce sync.Once
	temporalClient     client.Client
	temporalClientErr  error
)

func getTemporalClient() (client.Client, error) {
	temporalClientOnce.Do(func() {
		temporalClient, temporalClientErr = client.Dial(client.Options{
			HostPort: "localhost:7233",
		})
	})

	return temporalClient, temporalClientErr
}

func workflowIDForBill(billID int64) string {
	return fmt.Sprintf("fee-period-bill-%d", billID)
}

func startFeeWorkflow(ctx context.Context, bill Bill) (string, error) {
	c, err := getTemporalClient()
	if err != nil {
		return "", fmt.Errorf("connect temporal: %w", err)
	}

	workflowID := workflowIDForBill(bill.ID)

	_, err = c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: FeeTaskQueue,
	}, FeePeriodWorkflow, FeePeriodWorkflowInput{
		BillID:      bill.ID,
		CustomerID:  bill.CustomerID,
		Currency:    bill.Currency,
		PeriodStart: bill.PeriodStart,
		PeriodEnd:   bill.PeriodEnd,
	})

	if err != nil {
		return "", fmt.Errorf("start fee workflow: %w", err)
	}

	return workflowID, nil
}

// signalAddLineItem sends an AddLineItem signal to the bill's workflow.
func signalAddLineItem(ctx context.Context, billID int64, signal AddLineItemSignal) error {
	c, err := getTemporalClient()
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}

	workflowID := workflowIDForBill(billID)
	err = c.SignalWorkflow(ctx, workflowID, "", SignalAddLineItem, signal)
	if err != nil {
		return fmt.Errorf("signal add line item: %w", err)
	}

	return nil
}

// signalCloseBill sends a CloseBill signal to the bill's workflow.
func signalCloseBill(ctx context.Context, billID int64, reason string) error {
	c, err := getTemporalClient()
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}

	workflowID := workflowIDForBill(billID)
	err = c.SignalWorkflow(ctx, workflowID, "", SignalCloseBill, CloseBillSignal{
		Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("signal close bill: %w", err)
	}

	return nil
}

// queryBillWorkflowState queries the running workflow for its current state.
func queryBillWorkflowState(ctx context.Context, billID int64) (*BillWorkflowState, error) {
	c, err := getTemporalClient()
	if err != nil {
		return nil, fmt.Errorf("connect temporal: %w", err)
	}

	workflowID := workflowIDForBill(billID)
	resp, err := c.QueryWorkflow(ctx, workflowID, "", QueryBillState)
	if err != nil {
		return nil, fmt.Errorf("query workflow state: %w", err)
	}

	var state BillWorkflowState
	if err := resp.Get(&state); err != nil {
		// Try raw JSON decode as fallback.
		raw, rawErr := json.Marshal(resp)
		if rawErr != nil {
			return nil, fmt.Errorf("decode workflow state: %w", err)
		}
		if jsonErr := json.Unmarshal(raw, &state); jsonErr != nil {
			return nil, fmt.Errorf("decode workflow state: %w", err)
		}
	}

	return &state, nil
}
