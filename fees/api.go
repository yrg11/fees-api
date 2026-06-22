package fees

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"encore.dev/beta/errs"
)

type CreateBillRequest struct {
	Currency    Currency  `json:"currency"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

//encore:api auth method=POST path=/bills
func CreateBill(ctx context.Context, req *CreateBillRequest) (*CreateBillResponse, error) {
	customerID := getAuthCustomerID()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  customerID,
		Currency:    req.Currency,
		PeriodStart: req.PeriodStart,
		PeriodEnd:   req.PeriodEnd,
	})
	if err != nil {
		return nil, mapError(err)
	}

	workflowID, err := startFeeWorkflow(ctx, bill)
	if err != nil {
		// Workflow failed to start — delete the orphaned bill so it doesn't sit OPEN forever.
		if delErr := deleteBill(ctx, bill.ID); delErr != nil {
			log.Printf("failed to clean up bill %d after workflow start failure: %v", bill.ID, delErr)
		}
		return nil, mapError(err)
	}

	if err := setBillWorkflowID(ctx, bill.ID, workflowID); err != nil {
		return nil, mapError(err)
	}

	bill.WorkflowID = workflowID

	return &CreateBillResponse{
		Bill: bill,
	}, nil
}

// AddLineItem sends a signal to the bill's Temporal workflow to add a line item.
//
//encore:api auth method=POST path=/bills/:billID/line-items
func AddLineItem(ctx context.Context, billID int64, req *AddLineItemRequest) (*AddLineItemResponse, error) {
	// Validate input before signalling.
	if req.Description == "" {
		return nil, mapError(fmt.Errorf("description is required"))
	}
	if req.AmountMinor <= 0 {
		return nil, mapError(ErrInvalidAmount)
	}
	if err := validateCurrencyExists(ctx, req.Currency); err != nil {
		return nil, mapError(err)
	}
	if req.Date == "" {
		return nil, mapError(ErrInvalidDate)
	}
	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		return nil, mapError(ErrInvalidDate)
	}

	// Check bill exists, is open, and belongs to this customer.
	bill, err := authorizeBillAccess(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}
	if bill.Status == BillStatusClosed {
		return nil, mapError(ErrBillAlreadyClosed)
	}

	// Send signal to the workflow — the workflow will persist via activity.
	err = signalAddLineItem(ctx, billID, AddLineItemSignal{
		IdempotencyKey: req.IdempotencyKey,
		Description:    req.Description,
		AmountMinor:    req.AmountMinor,
		Currency:       req.Currency,
		Date:           req.Date,
	})
	if err != nil {
		return nil, mapError(err)
	}

	// Return an acknowledgement. The line item will be persisted asynchronously by the workflow.
	return &AddLineItemResponse{
		Accepted: true,
		BillID:   billID,
		Note:     "line item accepted for processing; query the bill or workflow-state to confirm persistence",
	}, nil
}

type CancelLineItemResponse struct {
	Accepted   bool  `json:"accepted"`
	BillID     int64 `json:"bill_id"`
	LineItemID int64 `json:"line_item_id"`
}

// CancelLineItem sends a signal to the bill's Temporal workflow to cancel a line item.
//
//encore:api auth method=DELETE path=/bills/:billID/line-items/:lineItemID
func CancelLineItem(ctx context.Context, billID int64, lineItemID int64) (*CancelLineItemResponse, error) {
	// Check bill exists, is open, and belongs to this customer.
	bill, err := authorizeBillAccess(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}
	if bill.Status == BillStatusClosed {
		return nil, mapError(ErrBillAlreadyClosed)
	}

	// Send cancel signal to the workflow.
	err = signalCancelLineItem(ctx, billID, lineItemID)
	if err != nil {
		return nil, mapError(err)
	}

	return &CancelLineItemResponse{
		Accepted:   true,
		BillID:     billID,
		LineItemID: lineItemID,
	}, nil
}

// CloseBill sends a signal to the bill's Temporal workflow to close it,
// then waits for the close to complete and returns the final bill with all line items.
//
//encore:api auth method=POST path=/bills/:billID/close
func CloseBill(ctx context.Context, billID int64) (*CloseBillResponse, error) {
	// Check bill exists, is open, and belongs to this customer.
	bill, err := authorizeBillAccess(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}
	if bill.Status == BillStatusClosed {
		// Already closed — return current state.
		items, err := listLineItems(ctx, billID)
		if err != nil {
			return nil, mapError(err)
		}
		return &CloseBillResponse{Bill: bill, LineItems: items}, nil
	}

	// Send close signal to the workflow.
	err = signalCloseBill(ctx, billID, "manual_close")
	if err != nil {
		return nil, mapError(err)
	}

	// Wait for the workflow to process the close signal by polling until status changes.
	closedBill, items, err := waitForBillClose(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}

	return &CloseBillResponse{Bill: closedBill, LineItems: items}, nil
}

//encore:api auth method=GET path=/bills/:billID
func GetBill(ctx context.Context, billID int64) (*GetBillResponse, error) {
	bill, err := authorizeBillAccess(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}

	items, err := listLineItems(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}

	return &GetBillResponse{
		Bill:      bill,
		LineItems: items,
	}, nil
}

// GetBillWorkflowState queries the Temporal workflow for real-time bill state.
//
//encore:api auth method=GET path=/bills/:billID/workflow-state
func GetBillWorkflowState(ctx context.Context, billID int64) (*BillWorkflowState, error) {
	_, err := authorizeBillAccess(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}

	state, err := queryBillWorkflowState(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}
	return state, nil
}

type ListBillsParams struct {
	Status string `query:"status"`
}

//encore:api auth method=GET path=/bills
func ListBills(ctx context.Context, params *ListBillsParams) (*ListBillsResponse, error) {
	customerID := getAuthCustomerID()

	var statusFilter *BillStatus

	if params.Status != "" {
		s := BillStatus(params.Status)
		switch s {
		case BillStatusOpen, BillStatusClosed:
			statusFilter = &s
		default:
			return nil, mapError(fmt.Errorf("invalid status"))
		}
	}

	bills, err := listBillsByCustomer(ctx, customerID, statusFilter)
	if err != nil {
		return nil, mapError(err)
	}

	return &ListBillsResponse{
		Bills: bills,
	}, nil
}

// authorizeBillAccess checks that the bill exists and belongs to the authenticated customer.
func authorizeBillAccess(ctx context.Context, billID int64) (Bill, error) {
	bill, err := getBill(ctx, billID)
	if err != nil {
		return Bill{}, err
	}
	if bill.CustomerID != getAuthCustomerID() {
		return Bill{}, ErrBillNotFound // don't reveal existence to other customers
	}
	return bill, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, ErrBillNotFound):
		return &errs.Error{Code: errs.NotFound, Message: err.Error()}
	case errors.Is(err, ErrBillAlreadyClosed):
		return &errs.Error{Code: errs.FailedPrecondition, Message: err.Error()}
	case errors.Is(err, ErrLineItemNotFound):
		return &errs.Error{Code: errs.NotFound, Message: err.Error()}
	case errors.Is(err, ErrCurrencyMismatch):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case errors.Is(err, ErrInvalidCurrency):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case errors.Is(err, ErrInvalidAmount):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case errors.Is(err, ErrInvalidPeriod):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case errors.Is(err, ErrInvalidDate):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case errors.Is(err, ErrFXRateNotFound):
		return &errs.Error{Code: errs.FailedPrecondition, Message: err.Error()}
	default:
		log.Printf("internal error: %v", err)
		return &errs.Error{Code: errs.Internal, Message: "internal error"}
	}
}
