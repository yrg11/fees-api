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
	ID              int64      `json:"id"`
	BillID          int64      `json:"bill_id"`
	Description     string     `json:"description"`
	BaseCurrency    Currency   `json:"base_currency"`
	BaseAmountMinor int64      `json:"base_amount_minor"`
	BillCurrency    Currency   `json:"bill_currency"`
	BillAmountMinor int64      `json:"bill_amount_minor"`
	FXRate          *float64   `json:"fx_rate,omitempty"`
	FXRateDate      *time.Time `json:"fx_rate_date,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type FXRate struct {
	ID            int64     `json:"id"`
	BaseCurrency  Currency  `json:"base_currency"`
	QuoteCurrency Currency  `json:"quote_currency"`
	Rate          float64   `json:"rate"`
	RateDate      time.Time `json:"rate_date"`
	Source        string    `json:"source"`
	FetchedAt     time.Time `json:"fetched_at"`
}

// createBillInput is the internal input for creating a bill (customer_id comes from auth).
type createBillInput struct {
	CustomerID  string
	Currency    Currency
	PeriodStart time.Time
	PeriodEnd   time.Time
}

type CreateBillResponse struct {
	Bill Bill `json:"bill"`
}

type AddLineItemRequest struct {
	IdempotencyKey string   `json:"idempotency_key,omitempty"` // Optional client-provided key to prevent duplicates
	Description    string   `json:"description"`
	AmountMinor    int64    `json:"amount_minor"`
	Currency       Currency `json:"currency"`
	Date           string   `json:"date"` // YYYY-MM-DD, used for FX rate lookup
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
