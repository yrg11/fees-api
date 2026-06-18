package fees

import "time"

type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyGEL Currency = "GEL"
)

type BillStatus string

const (
	BillStatusOpen   BillStatus = "OPEN"
	BillStatusClosed BillStatus = "CLOSED"
)

type Bill struct {
	ID               int64      `json:"id"`
	CustomerID       string     `json:"customer_id"`
	Currency         Currency   `json:"currency"`
	Status           BillStatus `json:"status"`
	PeriodStart      time.Time  `json:"period_start"`
	PeriodEnd        time.Time  `json:"period_end"`
	TotalAmountMinor int64      `json:"total_amount_minor"`
	WorkflowID       string     `json:"workflow_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
}

type LineItem struct {
	ID          int64     `json:"id"`
	BillID      int64     `json:"bill_id"`
	Description string    `json:"description"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    Currency  `json:"currency"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateBillRequest struct {
	CustomerID  string    `json:"customer_id"`
	Currency    Currency  `json:"currency"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

type CreateBillResponse struct {
	Bill Bill `json:"bill"`
}

type AddLineItemRequest struct {
	Description string   `json:"description"`
	AmountMinor int64    `json:"amount_minor"`
	Currency    Currency `json:"currency"`
}

type AddLineItemResponse struct {
	Accepted bool  `json:"accepted"`
	BillID   int64 `json:"bill_id"`
}

type CloseBillResponse struct {
	Accepted bool  `json:"accepted"`
	BillID   int64 `json:"bill_id"`
}

type GetBillResponse struct {
	Bill      Bill       `json:"bill"`
	LineItems []LineItem `json:"line_items"`
}

type ListBillsResponse struct {
	Bills []Bill `json:"bills"`
}

