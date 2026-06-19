package fees

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBill_Success(t *testing.T) {
	ctx := context.Background()

	req := createBillInput{
		CustomerID:  "cust_test_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	}

	bill, err := createBill(ctx, req)
	require.NoError(t, err)

	assert.NotZero(t, bill.ID)
	assert.Equal(t, "cust_test_1", bill.CustomerID)
	assert.Equal(t, CurrencyUSD, bill.Currency)
	assert.Equal(t, BillStatusOpen, bill.Status)
	assert.Equal(t, int64(0), bill.TotalAmountMinor)
	assert.Empty(t, bill.WorkflowID)
	assert.NotZero(t, bill.CreatedAt)
	assert.Nil(t, bill.ClosedAt)
}

func TestCreateBill_InvalidCurrency(t *testing.T) {
	ctx := context.Background()

	req := createBillInput{
		CustomerID:  "cust_1",
		Currency:    "EUR",
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	}

	_, err := createBill(ctx, req)
	assert.ErrorIs(t, err, ErrInvalidCurrency)
}

func TestCreateBill_EmptyCustomerID(t *testing.T) {
	ctx := context.Background()

	req := createBillInput{
		CustomerID:  "",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	}

	_, err := createBill(ctx, req)
	assert.Error(t, err)
}

func TestCreateBill_InvalidPeriod(t *testing.T) {
	ctx := context.Background()

	req := createBillInput{
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), // end before start
	}

	_, err := createBill(ctx, req)
	assert.ErrorIs(t, err, ErrInvalidPeriod)
}

func TestAddLineItem_SameCurrency_Success(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_li_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	item, err := addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "API calls - June",
		AmountMinor: 2999,
		Currency:    CurrencyUSD,
		Date:        "2026-06-15",
	})
	require.NoError(t, err)

	assert.NotZero(t, item.ID)
	assert.Equal(t, bill.ID, item.BillID)
	assert.Equal(t, "API calls - June", item.Description)
	assert.Equal(t, int64(2999), item.BaseAmountMinor)
	assert.Equal(t, CurrencyUSD, item.BaseCurrency)
	assert.Equal(t, int64(2999), item.BillAmountMinor)
	assert.Equal(t, CurrencyUSD, item.BillCurrency)
	assert.Nil(t, item.FXRate)
	assert.Nil(t, item.FXRateDate)
	assert.NotZero(t, item.CreatedAt)
}

func TestAddLineItem_CrossCurrency_Success(t *testing.T) {
	ctx := context.Background()

	// First, store an FX rate for the date
	rateDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	_, err := storeFXRate(ctx, CurrencyUSD, CurrencyGEL, 2.7, rateDate, "test")
	require.NoError(t, err)

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_cross_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Add a GEL line item to a USD bill
	item, err := addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "GEL expense",
		AmountMinor: 2700, // 27.00 GEL
		Currency:    CurrencyGEL,
		Date:        "2026-06-15",
	})
	require.NoError(t, err)

	assert.Equal(t, CurrencyGEL, item.BaseCurrency)
	assert.Equal(t, int64(2700), item.BaseAmountMinor)
	assert.Equal(t, CurrencyUSD, item.BillCurrency)
	assert.Equal(t, int64(1000), item.BillAmountMinor) // 2700 / 2.7 = 1000
	assert.NotNil(t, item.FXRate)
	assert.InDelta(t, 2.7, *item.FXRate, 0.0001)
	assert.NotNil(t, item.FXRateDate)
}

func TestAddLineItem_CrossCurrency_FallbackToPreviousDay(t *testing.T) {
	ctx := context.Background()

	// Store rate for June 14 only
	rateDate := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	_, err := storeFXRate(ctx, CurrencyUSD, CurrencyGEL, 2.8, rateDate, "test")
	require.NoError(t, err)

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_cross_fb_1",
		Currency:    CurrencyGEL,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Add a USD line item for June 15 — should fall back to June 14 rate
	item, err := addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "USD expense",
		AmountMinor: 1000, // $10.00
		Currency:    CurrencyUSD,
		Date:        "2026-06-15",
	})
	require.NoError(t, err)

	assert.Equal(t, CurrencyUSD, item.BaseCurrency)
	assert.Equal(t, int64(1000), item.BaseAmountMinor)
	assert.Equal(t, CurrencyGEL, item.BillCurrency)
	assert.Equal(t, int64(2800), item.BillAmountMinor) // 1000 * 2.8 = 2800
	assert.NotNil(t, item.FXRate)
	assert.InDelta(t, 2.8, *item.FXRate, 0.0001)
}

func TestAddLineItem_CrossCurrency_NoRate_Error(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_cross_noratex",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// No FX rate stored for Jan 2026
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "No rate available",
		AmountMinor: 1000,
		Currency:    CurrencyGEL,
		Date:        "2026-01-10",
	})
	assert.ErrorIs(t, err, ErrFXRateNotFound)
}

func TestAddLineItem_BillNotFound(t *testing.T) {
	ctx := context.Background()

	_, err := addLineItem(ctx, 999999, AddLineItemRequest{
		Description: "Test",
		AmountMinor: 100,
		Currency:    CurrencyUSD,
		Date:        "2026-06-15",
	})
	assert.ErrorIs(t, err, ErrBillNotFound)
}

func TestAddLineItem_BillAlreadyClosed(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_closed_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Close the bill first.
	_, _, err = closeBill(ctx, bill.ID)
	require.NoError(t, err)

	// Try to add a line item to the closed bill.
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Should fail",
		AmountMinor: 100,
		Currency:    CurrencyUSD,
		Date:        "2026-06-15",
	})
	assert.ErrorIs(t, err, ErrBillAlreadyClosed)
}

func TestAddLineItem_InvalidAmount(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_amt_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Zero amount",
		AmountMinor: 0,
		Currency:    CurrencyUSD,
		Date:        "2026-06-15",
	})
	assert.ErrorIs(t, err, ErrInvalidAmount)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Negative amount",
		AmountMinor: -100,
		Currency:    CurrencyUSD,
		Date:        "2026-06-15",
	})
	assert.ErrorIs(t, err, ErrInvalidAmount)
}

func TestAddLineItem_InvalidDate(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_date_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "No date",
		AmountMinor: 100,
		Currency:    CurrencyUSD,
		Date:        "",
	})
	assert.ErrorIs(t, err, ErrInvalidDate)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Bad date",
		AmountMinor: 100,
		Currency:    CurrencyUSD,
		Date:        "not-a-date",
	})
	assert.ErrorIs(t, err, ErrInvalidDate)
}

func TestCloseBill_Success(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_close_1",
		Currency:    CurrencyGEL,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Add some line items (same currency).
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Item 1",
		AmountMinor: 1000,
		Currency:    CurrencyGEL,
		Date:        "2026-06-10",
	})
	require.NoError(t, err)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Item 2",
		AmountMinor: 2500,
		Currency:    CurrencyGEL,
		Date:        "2026-06-11",
	})
	require.NoError(t, err)

	// Close the bill.
	closedBill, items, err := closeBill(ctx, bill.ID)
	require.NoError(t, err)

	assert.Equal(t, BillStatusClosed, closedBill.Status)
	assert.Equal(t, int64(3500), closedBill.TotalAmountMinor)
	assert.NotNil(t, closedBill.ClosedAt)
	assert.Len(t, items, 2)
}

func TestCloseBill_Idempotent(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_idem_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Close twice — should not error.
	_, _, err = closeBill(ctx, bill.ID)
	require.NoError(t, err)

	closedBill, _, err := closeBill(ctx, bill.ID)
	require.NoError(t, err)
	assert.Equal(t, BillStatusClosed, closedBill.Status)
}

func TestCloseBill_NotFound(t *testing.T) {
	ctx := context.Background()

	_, _, err := closeBill(ctx, 999999)
	assert.ErrorIs(t, err, ErrBillNotFound)
}

func TestCloseBill_EmptyBill(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_empty_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	closedBill, items, err := closeBill(ctx, bill.ID)
	require.NoError(t, err)

	assert.Equal(t, BillStatusClosed, closedBill.Status)
	assert.Equal(t, int64(0), closedBill.TotalAmountMinor)
	assert.Empty(t, items)
}

func TestGetBill_Success(t *testing.T) {
	ctx := context.Background()

	created, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_get_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	fetched, err := getBill(ctx, created.ID)
	require.NoError(t, err)

	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, created.CustomerID, fetched.CustomerID)
	assert.Equal(t, created.Currency, fetched.Currency)
}

func TestGetBill_NotFound(t *testing.T) {
	ctx := context.Background()

	_, err := getBill(ctx, 999999)
	assert.ErrorIs(t, err, ErrBillNotFound)
}

func TestListBills_FilterByStatus(t *testing.T) {
	ctx := context.Background()

	// Create an open bill.
	_, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_list_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Create and close another bill.
	closedBill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_list_2",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)
	_, _, err = closeBill(ctx, closedBill.ID)
	require.NoError(t, err)

	// List open bills.
	openStatus := BillStatusOpen
	openBills, err := listBills(ctx, &openStatus)
	require.NoError(t, err)
	for _, b := range openBills {
		assert.Equal(t, BillStatusOpen, b.Status)
	}

	// List closed bills.
	closedStatus := BillStatusClosed
	closedBills, err := listBills(ctx, &closedStatus)
	require.NoError(t, err)
	for _, b := range closedBills {
		assert.Equal(t, BillStatusClosed, b.Status)
	}

	// List all bills (no filter).
	allBills, err := listBills(ctx, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(allBills), 2)
}

func TestListLineItems_ReturnsItemsInOrder(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_items_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{Description: "First", AmountMinor: 100, Currency: CurrencyUSD, Date: "2026-06-10"})
	require.NoError(t, err)
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{Description: "Second", AmountMinor: 200, Currency: CurrencyUSD, Date: "2026-06-11"})
	require.NoError(t, err)
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{Description: "Third", AmountMinor: 300, Currency: CurrencyUSD, Date: "2026-06-12"})
	require.NoError(t, err)

	items, err := listLineItems(ctx, bill.ID)
	require.NoError(t, err)

	assert.Len(t, items, 3)
	assert.Equal(t, "First", items[0].Description)
	assert.Equal(t, "Second", items[1].Description)
	assert.Equal(t, "Third", items[2].Description)
}

func TestSetBillWorkflowID(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, createBillInput{
		CustomerID:  "cust_wf_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	err = setBillWorkflowID(ctx, bill.ID, "fee-period-bill-42")
	require.NoError(t, err)

	fetched, err := getBill(ctx, bill.ID)
	require.NoError(t, err)
	assert.Equal(t, "fee-period-bill-42", fetched.WorkflowID)
}

func TestSetBillWorkflowID_NotFound(t *testing.T) {
	ctx := context.Background()

	err := setBillWorkflowID(ctx, 999999, "wf-id")
	assert.ErrorIs(t, err, ErrBillNotFound)
}

func TestStoreFXRate_Success(t *testing.T) {
	ctx := context.Background()

	rateDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rate, err := storeFXRate(ctx, CurrencyUSD, CurrencyGEL, 2.75, rateDate, "yfinance")
	require.NoError(t, err)

	assert.NotZero(t, rate.ID)
	assert.Equal(t, CurrencyUSD, rate.BaseCurrency)
	assert.Equal(t, CurrencyGEL, rate.QuoteCurrency)
	assert.InDelta(t, 2.75, rate.Rate, 0.0001)
	assert.Equal(t, "yfinance", rate.Source)
}

func TestGetFXRate_ExactDate(t *testing.T) {
	ctx := context.Background()

	rateDate := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	_, err := storeFXRate(ctx, CurrencyUSD, CurrencyGEL, 2.65, rateDate, "test")
	require.NoError(t, err)

	rate, err := getFXRate(ctx, CurrencyUSD, CurrencyGEL, rateDate)
	require.NoError(t, err)
	assert.InDelta(t, 2.65, rate.Rate, 0.0001)
}

func TestGetFXRate_FallbackPreviousDay(t *testing.T) {
	ctx := context.Background()

	// Store rate for July 3 only
	rateDate := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	_, err := storeFXRate(ctx, CurrencyUSD, CurrencyGEL, 2.6, rateDate, "test")
	require.NoError(t, err)

	// Query for July 4 — should fall back to July 3
	queryDate := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)
	rate, err := getFXRate(ctx, CurrencyUSD, CurrencyGEL, queryDate)
	require.NoError(t, err)
	assert.InDelta(t, 2.6, rate.Rate, 0.0001)
}

func TestGetFXRate_TooOld_Error(t *testing.T) {
	ctx := context.Background()

	// Store rate for July 1
	rateDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	_, err := storeFXRate(ctx, CurrencyUSD, CurrencyGEL, 2.5, rateDate, "test")
	require.NoError(t, err)

	// Query for July 5 — July 1 is more than 1 day before, should error
	queryDate := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	_, err = getFXRate(ctx, CurrencyUSD, CurrencyGEL, queryDate)
	assert.ErrorIs(t, err, ErrFXRateNotFound)
}

func TestConvertAmountViaUSD(t *testing.T) {
	// Same currency — rateBase and rateBill don't matter
	assert.Equal(t, int64(1000), convertAmountViaUSD(1000, CurrencyUSD, CurrencyUSD, 1.0, 1.0))

	// USD to GEL: 1000 * 2.7 = 2700 (rateBill=2.7)
	assert.Equal(t, int64(2700), convertAmountViaUSD(1000, CurrencyUSD, CurrencyGEL, 1.0, 2.7))

	// GEL to USD: 2700 / 2.7 = 1000 (rateBase=2.7)
	assert.Equal(t, int64(1000), convertAmountViaUSD(2700, CurrencyGEL, CurrencyUSD, 2.7, 1.0))

	// GEL to EUR (triangulation): 2700 GEL / 2.7 (GEL rate) * 0.9 (EUR rate) = 900
	assert.Equal(t, int64(900), convertAmountViaUSD(2700, CurrencyGEL, "EUR", 2.7, 0.9))
}
