package fees

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const FeeTaskQueue = "fees-task-queue"

// Signal names used to communicate with the running workflow.
const (
	SignalAddLineItem    = "add_line_item"
	SignalCancelLineItem = "cancel_line_item"
	SignalCloseBill      = "close_bill"
)

// Query name to retrieve current workflow state.
const QueryBillState = "bill_state"

// FeePeriodWorkflowInput is passed when starting the workflow.
type FeePeriodWorkflowInput struct {
	BillID      int64     `json:"bill_id"`
	CustomerID  string    `json:"customer_id"`
	Currency    Currency  `json:"currency"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

// AddLineItemSignal is the payload sent via the AddLineItem signal.
type AddLineItemSignal struct {
	Description string   `json:"description"`
	AmountMinor int64    `json:"amount_minor"`
	Currency    Currency `json:"currency"`
	Date        string   `json:"date"` // YYYY-MM-DD for FX rate lookup
}

// CancelLineItemSignal is the payload sent via the CancelLineItem signal.
type CancelLineItemSignal struct {
	LineItemID int64 `json:"line_item_id"`
}

// CloseBillSignal is the payload sent via the CloseBill signal.
type CloseBillSignal struct {
	Reason string `json:"reason"`
}

// BillWorkflowState represents the workflow's in-memory state, queryable externally.
type BillWorkflowState struct {
	BillID           int64      `json:"bill_id"`
	CustomerID       string     `json:"customer_id"`
	Currency         Currency   `json:"currency"`
	Status           BillStatus `json:"status"`
	PeriodStart      time.Time  `json:"period_start"`
	PeriodEnd        time.Time  `json:"period_end"`
	TotalAmountMinor int64      `json:"total_amount_minor"`
	LineItems        []LineItem `json:"line_items"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	CloseReason      string     `json:"close_reason,omitempty"`
}

// FeePeriodWorkflowResult is returned when the workflow completes.
type FeePeriodWorkflowResult struct {
	BillID           int64  `json:"bill_id"`
	Closed           bool   `json:"closed"`
	TotalAmountMinor int64  `json:"total_amount_minor"`
	ItemCount        int    `json:"item_count"`
	CloseReason      string `json:"close_reason"`
}

// FeePeriodWorkflow models a billing period as a long-running workflow.
// It listens for signals to add line items or close the bill.
// If neither signal closes it, the bill auto-closes when the period ends.
func FeePeriodWorkflow(ctx workflow.Context, input FeePeriodWorkflowInput) (FeePeriodWorkflowResult, error) {
	// Activity options with retry policy.
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	// Internal state maintained by the workflow.
	state := BillWorkflowState{
		BillID:      input.BillID,
		CustomerID:  input.CustomerID,
		Currency:    input.Currency,
		Status:      BillStatusOpen,
		PeriodStart: input.PeriodStart,
		PeriodEnd:   input.PeriodEnd,
		LineItems:   []LineItem{},
	}

	// Register query handler so external callers can read workflow state.
	err := workflow.SetQueryHandler(ctx, QueryBillState, func() (BillWorkflowState, error) {
		return state, nil
	})
	if err != nil {
		return FeePeriodWorkflowResult{}, err
	}

	// Signal channels.
	addLineItemCh := workflow.GetSignalChannel(ctx, SignalAddLineItem)
	cancelLineItemCh := workflow.GetSignalChannel(ctx, SignalCancelLineItem)
	closeBillCh := workflow.GetSignalChannel(ctx, SignalCloseBill)

	// Calculate how long until the period ends.
	periodEndDuration := input.PeriodEnd.Sub(workflow.Now(ctx))

	// Use a timer for auto-close at period end.
	timerCtx, timerCancel := workflow.WithCancel(ctx)
	timerFired := workflow.NewChannel(ctx)

	// Start a goroutine that fires when the period ends.
	workflow.Go(timerCtx, func(gCtx workflow.Context) {
		if periodEndDuration > 0 {
			_ = workflow.Sleep(gCtx, periodEndDuration)
		}
		timerFired.Send(gCtx, true)
	})

	// Main loop: process signals until the bill is closed.
	for state.Status == BillStatusOpen {
		selector := workflow.NewSelector(ctx)

		// Handle add line item signal.
		selector.AddReceive(addLineItemCh, func(c workflow.ReceiveChannel, more bool) {
			var signal AddLineItemSignal
			c.Receive(ctx, &signal)

			if state.Status == BillStatusClosed {
				return // ignore if already closed
			}

			// Persist via activity.
			var output AddLineItemActivityOutput
			err := workflow.ExecuteActivity(ctx, AddLineItemActivity, AddLineItemActivityInput{
				BillID: input.BillID,
				Input: AddLineItemRequest{
					Description: signal.Description,
					AmountMinor: signal.AmountMinor,
					Currency:    signal.Currency,
					Date:        signal.Date,
				},
			}).Get(ctx, &output)

			if err == nil {
				state.LineItems = append(state.LineItems, output.LineItem)
				state.TotalAmountMinor += output.LineItem.BillAmountMinor
			}
		})

		// Handle cancel line item signal.
		selector.AddReceive(cancelLineItemCh, func(c workflow.ReceiveChannel, more bool) {
			var signal CancelLineItemSignal
			c.Receive(ctx, &signal)

			if state.Status == BillStatusClosed {
				return
			}

			var output CancelLineItemActivityOutput
			err := workflow.ExecuteActivity(ctx, CancelLineItemActivity, CancelLineItemActivityInput{
				BillID:     input.BillID,
				LineItemID: signal.LineItemID,
			}).Get(ctx, &output)

			if err == nil {
				// Remove from workflow state and decrement total
				for i, item := range state.LineItems {
					if item.ID == signal.LineItemID {
						state.TotalAmountMinor -= item.BillAmountMinor
						state.LineItems = append(state.LineItems[:i], state.LineItems[i+1:]...)
						break
					}
				}
			}
		})

		// Handle close bill signal.
		selector.AddReceive(closeBillCh, func(c workflow.ReceiveChannel, more bool) {
			var signal CloseBillSignal
			c.Receive(ctx, &signal)

			if state.Status == BillStatusClosed {
				return
			}

			state.CloseReason = signal.Reason
			doCloseBill(ctx, &state)
		})

		// Handle period-end timer.
		selector.AddReceive(timerFired, func(c workflow.ReceiveChannel, more bool) {
			var v bool
			c.Receive(ctx, &v)

			if state.Status == BillStatusClosed {
				return
			}

			state.CloseReason = "period_ended"
			doCloseBill(ctx, &state)
		})

		selector.Select(ctx)
	}

	// Cancel the timer goroutine if still running.
	timerCancel()

	// Drain any remaining signals that arrived after close.
	drainSignals(ctx, addLineItemCh, cancelLineItemCh, closeBillCh)

	return FeePeriodWorkflowResult{
		BillID:           state.BillID,
		Closed:           true,
		TotalAmountMinor: state.TotalAmountMinor,
		ItemCount:        len(state.LineItems),
		CloseReason:      state.CloseReason,
	}, nil
}

// doCloseBill is a helper called within the workflow to transition state to closed.
func doCloseBill(ctx workflow.Context, state *BillWorkflowState) {
	// Persist the close via activity.
	err := workflow.ExecuteActivity(ctx, CloseBillActivity, CloseBillActivityInput{
		BillID: state.BillID,
		Reason: state.CloseReason,
	}).Get(ctx, nil)

	if err == nil {
		now := workflow.Now(ctx)
		state.Status = BillStatusClosed
		state.ClosedAt = &now
	}
}

// drainSignals consumes any buffered signals so the workflow can complete cleanly.
func drainSignals(ctx workflow.Context, addCh, cancelCh, closeCh workflow.ReceiveChannel) {
	for {
		var signal AddLineItemSignal
		if ok := addCh.ReceiveAsync(&signal); !ok {
			break
		}
	}
	for {
		var signal CancelLineItemSignal
		if ok := cancelCh.ReceiveAsync(&signal); !ok {
			break
		}
	}
	for {
		var signal CloseBillSignal
		if ok := closeCh.ReceiveAsync(&signal); !ok {
			break
		}
	}
}
