package fees

import "context"

type AddLineItemActivityInput struct {
	BillID int64              `json:"bill_id"`
	Input  AddLineItemRequest `json:"input"`
}

type AddLineItemActivityOutput struct {
	LineItem LineItem `json:"line_item"`
}

type CloseBillActivityInput struct {
	BillID int64  `json:"bill_id"`
	Reason string `json:"reason"`
}

type CloseBillActivityOutput struct {
	Bill      Bill       `json:"bill"`
	LineItems []LineItem `json:"line_items"`
}

func AddLineItemActivity(ctx context.Context, input AddLineItemActivityInput) (AddLineItemActivityOutput, error) {
	item, err := addLineItem(ctx, input.BillID, input.Input)
	if err != nil {
		return AddLineItemActivityOutput{}, err
	}

	return AddLineItemActivityOutput{
		LineItem: item,
	}, nil
}

type CancelLineItemActivityInput struct {
	BillID     int64 `json:"bill_id"`
	LineItemID int64 `json:"line_item_id"`
}

type CancelLineItemActivityOutput struct {
	CancelledItem LineItem `json:"cancelled_item"`
}

func CancelLineItemActivity(ctx context.Context, input CancelLineItemActivityInput) (CancelLineItemActivityOutput, error) {
	item, err := cancelLineItem(ctx, input.BillID, input.LineItemID)
	if err != nil {
		return CancelLineItemActivityOutput{}, err
	}

	return CancelLineItemActivityOutput{
		CancelledItem: item,
	}, nil
}

func CloseBillActivity(ctx context.Context, input CloseBillActivityInput) (CloseBillActivityOutput, error) {
	bill, items, err := closeBill(ctx, input.BillID)
	if err != nil {
		return CloseBillActivityOutput{}, err
	}

	return CloseBillActivityOutput{
		Bill:      bill,
		LineItems: items,
	}, nil
}
