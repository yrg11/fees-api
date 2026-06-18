package fees

import (
	"context"
	"fmt"
	"time"

	"encore.dev/beta/errs"
)

//encore:api public method=POST path=/bills
func CreateBill(ctx context.Context, req *CreateBillRequest) (*CreateBillResponse, error) {
	bill, err := createBill(ctx, *req)
	if err != nil {
		return nil, mapError(err)
	}

	workflowID, err := startFeeWorkflow(ctx, bill)
	if err != nil {
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
//encore:api public method=POST path=/bills/:billID/line-items
func AddLineItem(ctx context.Context, billID int64, req *AddLineItemRequest) (*AddLineItemResponse, error) {
	// Validate input before signalling.
	if req.Description == "" {
		return nil, mapError(fmt.Errorf("description is required"))
	}
	if req.AmountMinor <= 0 {
		return nil, mapError(ErrInvalidAmount)
	}
	if err := validateCurrency(req.Currency); err != nil {
		return nil, mapError(err)
	}
	if req.Date == "" {
		return nil, mapError(ErrInvalidDate)
	}
	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		return nil, mapError(ErrInvalidDate)
	}

	// Check bill exists and is open (fast-fail before sending signal).
	bill, err := getBill(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}
	if bill.Status == BillStatusClosed {
		return nil, mapError(ErrBillAlreadyClosed)
	}

	// Send signal to the workflow — the workflow will persist via activity.
	err = signalAddLineItem(ctx, billID, AddLineItemSignal{
		Description: req.Description,
		AmountMinor: req.AmountMinor,
		Currency:    req.Currency,
		Date:        req.Date,
	})
	if err != nil {
		return nil, mapError(err)
	}

	// Return an acknowledgement. The line item will be persisted asynchronously by the workflow.
	return &AddLineItemResponse{
		Accepted: true,
		BillID:   billID,
	}, nil
}

// CloseBill sends a signal to the bill's Temporal workflow to close it.
//
//encore:api public method=POST path=/bills/:billID/close
func CloseBill(ctx context.Context, billID int64) (*CloseBillResponse, error) {
	// Check bill exists and is open.
	bill, err := getBill(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}
	if bill.Status == BillStatusClosed {
		return nil, mapError(ErrBillAlreadyClosed)
	}

	// Send close signal to the workflow.
	err = signalCloseBill(ctx, billID, "manual_close")
	if err != nil {
		return nil, mapError(err)
	}

	return &CloseBillResponse{
		Accepted: true,
		BillID:   billID,
	}, nil
}

//encore:api public method=GET path=/bills/:billID
func GetBill(ctx context.Context, billID int64) (*GetBillResponse, error) {
	bill, err := getBill(ctx, billID)
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
//encore:api public method=GET path=/bills/:billID/workflow-state
func GetBillWorkflowState(ctx context.Context, billID int64) (*BillWorkflowState, error) {
	state, err := queryBillWorkflowState(ctx, billID)
	if err != nil {
		return nil, mapError(err)
	}
	return state, nil
}

type ListBillsParams struct {
	Status string `query:"status"`
}

//encore:api public method=GET path=/bills
func ListBills(ctx context.Context, params *ListBillsParams) (*ListBillsResponse, error) {
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

	bills, err := listBills(ctx, statusFilter)
	if err != nil {
		return nil, mapError(err)
	}

	return &ListBillsResponse{
		Bills: bills,
	}, nil
}

func mapError(err error) error {
	switch {
	case isErr(err, ErrBillNotFound):
		return &errs.Error{Code: errs.NotFound, Message: err.Error()}
	case isErr(err, ErrBillAlreadyClosed):
		return &errs.Error{Code: errs.FailedPrecondition, Message: err.Error()}
	case isErr(err, ErrCurrencyMismatch):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case isErr(err, ErrInvalidCurrency):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case isErr(err, ErrInvalidAmount):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case isErr(err, ErrInvalidPeriod):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case isErr(err, ErrInvalidDate):
		return &errs.Error{Code: errs.InvalidArgument, Message: err.Error()}
	case isErr(err, ErrFXRateNotFound):
		return &errs.Error{Code: errs.FailedPrecondition, Message: err.Error()}
	default:
		return &errs.Error{Code: errs.Internal, Message: err.Error()}
	}
}

func isErr(err, target error) bool {
	return err == target || (err != nil && err.Error() == target.Error())
}
