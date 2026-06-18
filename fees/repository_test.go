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

	req := CreateBillRequest{
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

	req := CreateBillRequest{
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

	req := CreateBillRequest{
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

	req := CreateBillRequest{
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), // end before start
	}

	_, err := createBill(ctx, req)
	assert.ErrorIs(t, err, ErrInvalidPeriod)
}

func TestAddLineItem_Success(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, CreateBillRequest{
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
	})
	require.NoError(t, err)

	assert.NotZero(t, item.ID)
	assert.Equal(t, bill.ID, item.BillID)
	assert.Equal(t, "API calls - June", item.Description)
	assert.Equal(t, int64(2999), item.AmountMinor)
	assert.Equal(t, CurrencyUSD, item.Currency)
	assert.NotZero(t, item.CreatedAt)
}

func TestAddLineItem_BillNotFound(t *testing.T) {
	ctx := context.Background()

	_, err := addLineItem(ctx, 999999, AddLineItemRequest{
		Description: "Test",
		AmountMinor: 100,
		Currency:    CurrencyUSD,
	})
	assert.ErrorIs(t, err, ErrBillNotFound)
}

func TestAddLineItem_BillAlreadyClosed(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, CreateBillRequest{
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
	})
	assert.ErrorIs(t, err, ErrBillAlreadyClosed)
}

func TestAddLineItem_CurrencyMismatch(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, CreateBillRequest{
		CustomerID:  "cust_curr_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Wrong currency",
		AmountMinor: 100,
		Currency:    CurrencyGEL, // bill is USD
	})
	assert.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestAddLineItem_InvalidAmount(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, CreateBillRequest{
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
	})
	assert.ErrorIs(t, err, ErrInvalidAmount)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Negative amount",
		AmountMinor: -100,
		Currency:    CurrencyUSD,
	})
	assert.ErrorIs(t, err, ErrInvalidAmount)
}

func TestCloseBill_Success(t *testing.T) {
	ctx := context.Background()

	bill, err := createBill(ctx, CreateBillRequest{
		CustomerID:  "cust_close_1",
		Currency:    CurrencyGEL,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Add some line items.
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Item 1",
		AmountMinor: 1000,
		Currency:    CurrencyGEL,
	})
	require.NoError(t, err)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{
		Description: "Item 2",
		AmountMinor: 2500,
		Currency:    CurrencyGEL,
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

	bill, err := createBill(ctx, CreateBillRequest{
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

	bill, err := createBill(ctx, CreateBillRequest{
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

	created, err := createBill(ctx, CreateBillRequest{
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
	_, err := createBill(ctx, CreateBillRequest{
		CustomerID:  "cust_list_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	// Create and close another bill.
	closedBill, err := createBill(ctx, CreateBillRequest{
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

	bill, err := createBill(ctx, CreateBillRequest{
		CustomerID:  "cust_items_1",
		Currency:    CurrencyUSD,
		PeriodStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{Description: "First", AmountMinor: 100, Currency: CurrencyUSD})
	require.NoError(t, err)
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{Description: "Second", AmountMinor: 200, Currency: CurrencyUSD})
	require.NoError(t, err)
	_, err = addLineItem(ctx, bill.ID, AddLineItemRequest{Description: "Third", AmountMinor: 300, Currency: CurrencyUSD})
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

	bill, err := createBill(ctx, CreateBillRequest{
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
