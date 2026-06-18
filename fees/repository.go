package fees

import (
	"encore.dev/storage/sqldb"

	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	ErrBillNotFound      = errors.New("bill not found")
	ErrBillAlreadyClosed = errors.New("bill already closed")
	ErrCurrencyMismatch  = errors.New("line item currency does not match bill currency")
	ErrInvalidCurrency   = errors.New("invalid currency")
	ErrInvalidAmount     = errors.New("invalid amount")
	ErrInvalidPeriod     = errors.New("invalid billing period")
	ErrFXRateNotFound    = errors.New("fx rate not found for the given date or previous day")
	ErrInvalidDate       = errors.New("invalid date format, expected YYYY-MM-DD")
)

func validateCurrency(c Currency) error {
	switch c {
	case CurrencyUSD, CurrencyGEL:
		return nil
	default:
		return ErrInvalidCurrency
	}
}

// convertAmount converts baseAmount from baseCurrency to billCurrency using the given rate.
// Rate is stored as 1 USD = rate GEL.
// If baseCurrency == USD and billCurrency == GEL: billAmount = baseAmount * rate
// If baseCurrency == GEL and billCurrency == USD: billAmount = baseAmount / rate
func convertAmount(baseAmountMinor int64, baseCurrency, billCurrency Currency, rate float64) int64 {
	if baseCurrency == billCurrency {
		return baseAmountMinor
	}
	// Rate is always USD/GEL (1 USD = rate GEL)
	if baseCurrency == CurrencyUSD && billCurrency == CurrencyGEL {
		return int64(math.Round(float64(baseAmountMinor) * rate))
	}
	// baseCurrency == GEL, billCurrency == USD
	return int64(math.Round(float64(baseAmountMinor) / rate))
}

// getFXRate retrieves the FX rate for the given date, falling back to the previous day.
// Returns error if neither date has a rate.
func getFXRate(ctx context.Context, baseCurrency, quoteCurrency Currency, rateDate time.Time) (FXRate, error) {
	const query = `
		SELECT id, base_currency, quote_currency, rate, rate_date, source, fetched_at
		FROM fx_rates
		WHERE base_currency = $1 AND quote_currency = $2 AND rate_date <= $3
		ORDER BY rate_date DESC
		LIMIT 1
	`

	var r FXRate
	err := db.QueryRow(ctx, query, baseCurrency, quoteCurrency, rateDate).Scan(
		&r.ID,
		&r.BaseCurrency,
		&r.QuoteCurrency,
		&r.Rate,
		&r.RateDate,
		&r.Source,
		&r.FetchedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return FXRate{}, ErrFXRateNotFound
	}
	if err != nil {
		return FXRate{}, fmt.Errorf("get fx rate: %w", err)
	}

	// Only allow the exact date or previous day (not older)
	dayBefore := rateDate.AddDate(0, 0, -1)
	if r.RateDate.Before(dayBefore) {
		return FXRate{}, ErrFXRateNotFound
	}

	return r, nil
}

// storeFXRate upserts an FX rate for a given date.
func storeFXRate(ctx context.Context, baseCurrency, quoteCurrency Currency, rate float64, rateDate time.Time, source string) (FXRate, error) {
	const query = `
		INSERT INTO fx_rates (base_currency, quote_currency, rate, rate_date, source)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (base_currency, quote_currency, rate_date)
		DO UPDATE SET rate = EXCLUDED.rate, source = EXCLUDED.source, fetched_at = now()
		RETURNING id, base_currency, quote_currency, rate, rate_date, source, fetched_at
	`

	var r FXRate
	err := db.QueryRow(ctx, query, baseCurrency, quoteCurrency, rate, rateDate, source).Scan(
		&r.ID,
		&r.BaseCurrency,
		&r.QuoteCurrency,
		&r.Rate,
		&r.RateDate,
		&r.Source,
		&r.FetchedAt,
	)
	if err != nil {
		return FXRate{}, fmt.Errorf("store fx rate: %w", err)
	}

	return r, nil
}

// lookupFXRateForConversion gets the rate needed to convert between two currencies.
// Always looks up USD/GEL pair regardless of direction.
func lookupFXRateForConversion(ctx context.Context, baseCurrency, billCurrency Currency, rateDate time.Time) (float64, time.Time, error) {
	// Always store/lookup as USD/GEL
	fxRate, err := getFXRate(ctx, CurrencyUSD, CurrencyGEL, rateDate)
	if err != nil {
		return 0, time.Time{}, err
	}
	return fxRate.Rate, fxRate.RateDate, nil
}

func createBill(ctx context.Context, req CreateBillRequest) (Bill, error) {
	if req.CustomerID == "" {
		return Bill{}, fmt.Errorf("customer_id is required")
	}
	if err := validateCurrency(req.Currency); err != nil {
		return Bill{}, err
	}
	if !req.PeriodEnd.After(req.PeriodStart) {
		return Bill{}, ErrInvalidPeriod
	}

	const query = `
		INSERT INTO bills (
			customer_id,
			currency,
			status,
			period_start,
			period_end
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING
			id,
			customer_id,
			currency,
			status,
			period_start,
			period_end,
			total_amount_minor,
			COALESCE(temporal_workflow_id, ''),
			created_at,
			closed_at
	`

	var b Bill

	err := db.QueryRow(ctx, query,
		req.CustomerID,
		req.Currency,
		BillStatusOpen,
		req.PeriodStart,
		req.PeriodEnd,
	).Scan(
		&b.ID,
		&b.CustomerID,
		&b.Currency,
		&b.Status,
		&b.PeriodStart,
		&b.PeriodEnd,
		&b.TotalAmountMinor,
		&b.WorkflowID,
		&b.CreatedAt,
		&b.ClosedAt,
	)

	if err != nil {
		return Bill{}, fmt.Errorf("create bill: %w", err)
	}

	return b, nil
}

func setBillWorkflowID(ctx context.Context, billID int64, workflowID string) error {

	const query = `
		UPDATE bills
		SET temporal_workflow_id = $2
		WHERE id = $1
	`
	result, err := db.Exec(ctx, query, billID, workflowID)
	if err != nil {
		return fmt.Errorf("set workflow id: %w", err)
	}
	rows := result.RowsAffected()
	if rows == 0 {
		return ErrBillNotFound
	}
	return nil

}

func getBill(ctx context.Context, billID int64) (Bill, error) {
	const query = `
		SELECT
			id,
			customer_id,
			currency,
			status,
			period_start,
			period_end,
			total_amount_minor,
			COALESCE(temporal_workflow_id, ''),
			created_at,
			closed_at
		FROM bills
		WHERE id = $1
	`

	var b Bill

	err := db.QueryRow(ctx, query, billID).Scan(
		&b.ID,
		&b.CustomerID,
		&b.Currency,
		&b.Status,
		&b.PeriodStart,
		&b.PeriodEnd,
		&b.TotalAmountMinor,
		&b.WorkflowID,
		&b.CreatedAt,
		&b.ClosedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return Bill{}, ErrBillNotFound
	}

	if err != nil {
		return Bill{}, fmt.Errorf("get bill: %w", err)
	}

	return b, nil
}

func listBills(ctx context.Context, status *BillStatus) ([]Bill, error) {
	query := `
		SELECT
			id,
			customer_id,
			currency,
			status,
			period_start,
			period_end,
			total_amount_minor,
			COALESCE(temporal_workflow_id, ''),
			created_at,
			closed_at
		FROM bills
	`

	var rows *sqldb.Rows
	var err error

	if status != nil {
		query += ` WHERE status = $1 ORDER BY id DESC`
		rows, err = db.Query(ctx, query, *status)
	} else {
		query += ` ORDER BY id DESC`
		rows, err = db.Query(ctx, query)
	}

	if err != nil {
		return nil, fmt.Errorf("list bills: %w", err)
	}
	defer rows.Close()

	var bills []Bill

	for rows.Next() {
		var b Bill
		if err := rows.Scan(
			&b.ID,
			&b.CustomerID,
			&b.Currency,
			&b.Status,
			&b.PeriodStart,
			&b.PeriodEnd,
			&b.TotalAmountMinor,
			&b.WorkflowID,
			&b.CreatedAt,
			&b.ClosedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bill: %w", err)
		}

		bills = append(bills, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bills: %w", err)
	}

	return bills, nil
}

func listLineItems(ctx context.Context, billID int64) ([]LineItem, error) {
	const query = `
		SELECT id, bill_id, description, base_currency, base_amount_minor,
		       bill_currency, bill_amount_minor, fx_rate, fx_rate_date, created_at
		FROM bill_line_items
		WHERE bill_id = $1
		ORDER BY id
	`

	rows, err := db.Query(ctx, query, billID)
	if err != nil {
		return nil, fmt.Errorf("list line items: %w", err)
	}
	defer rows.Close()

	var items []LineItem

	for rows.Next() {
		var item LineItem

		if err := rows.Scan(
			&item.ID,
			&item.BillID,
			&item.Description,
			&item.BaseCurrency,
			&item.BaseAmountMinor,
			&item.BillCurrency,
			&item.BillAmountMinor,
			&item.FXRate,
			&item.FXRateDate,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan line item: %w", err)
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate line items: %w", err)
	}

	return items, nil
}

func addLineItem(ctx context.Context, billID int64, req AddLineItemRequest) (LineItem, error) {
	if req.Description == "" {
		return LineItem{}, fmt.Errorf("description is required")
	}
	if req.AmountMinor <= 0 {
		return LineItem{}, ErrInvalidAmount
	}
	if err := validateCurrency(req.Currency); err != nil {
		return LineItem{}, err
	}
	if req.Date == "" {
		return LineItem{}, ErrInvalidDate
	}

	rateDate, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		return LineItem{}, ErrInvalidDate
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return LineItem{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var billStatus BillStatus
	var billCurrency Currency

	err = tx.QueryRow(ctx, `
		SELECT status, currency
		FROM bills
		WHERE id = $1
		FOR UPDATE
	`, billID).Scan(&billStatus, &billCurrency)

	if errors.Is(err, sql.ErrNoRows) {
		return LineItem{}, ErrBillNotFound
	}

	if err != nil {
		return LineItem{}, fmt.Errorf("lock bill: %w", err)
	}

	if billStatus == BillStatusClosed {
		return LineItem{}, ErrBillAlreadyClosed
	}

	// Determine bill_amount_minor and fx_rate
	var billAmountMinor int64
	var fxRate *float64
	var fxRateDate *time.Time

	if req.Currency == billCurrency {
		// Same currency, no conversion needed
		billAmountMinor = req.AmountMinor
	} else {
		// Cross-currency: look up FX rate
		rate, rateDateVal, err := lookupFXRateForConversion(ctx, req.Currency, billCurrency, rateDate)
		if err != nil {
			return LineItem{}, err
		}
		billAmountMinor = convertAmount(req.AmountMinor, req.Currency, billCurrency, rate)
		fxRate = &rate
		fxRateDate = &rateDateVal
	}

	var item LineItem

	err = tx.QueryRow(ctx, `
		INSERT INTO bill_line_items (
			bill_id,
			description,
			base_currency,
			base_amount_minor,
			bill_currency,
			bill_amount_minor,
			fx_rate,
			fx_rate_date
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, bill_id, description, base_currency, base_amount_minor,
		          bill_currency, bill_amount_minor, fx_rate, fx_rate_date, created_at
	`, billID, req.Description, req.Currency, req.AmountMinor, billCurrency, billAmountMinor, fxRate, fxRateDate).Scan(
		&item.ID,
		&item.BillID,
		&item.Description,
		&item.BaseCurrency,
		&item.BaseAmountMinor,
		&item.BillCurrency,
		&item.BillAmountMinor,
		&item.FXRate,
		&item.FXRateDate,
		&item.CreatedAt,
	)

	if err != nil {
		return LineItem{}, fmt.Errorf("insert line item: %w", err)
	}

	// Update the bill's running total
	_, err = tx.Exec(ctx, `
		UPDATE bills SET total_amount_minor = total_amount_minor + $2 WHERE id = $1
	`, billID, billAmountMinor)
	if err != nil {
		return LineItem{}, fmt.Errorf("update bill total: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return LineItem{}, fmt.Errorf("commit add line item: %w", err)
	}

	return item, nil
}

func closeBill(ctx context.Context, billID int64) (Bill, []LineItem, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return Bill{}, nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var status BillStatus

	err = tx.QueryRow(ctx, `
		SELECT status
		FROM bills
		WHERE id = $1
		FOR UPDATE
	`, billID).Scan(&status)

	if errors.Is(err, sql.ErrNoRows) {
		return Bill{}, nil, ErrBillNotFound
	}

	if err != nil {
		return Bill{}, nil, fmt.Errorf("lock bill: %w", err)
	}

	if status == BillStatusClosed {
		b, err := getBill(ctx, billID)
		if err != nil {
			return Bill{}, nil, err
		}

		items, err := listLineItems(ctx, billID)
		if err != nil {
			return Bill{}, nil, err
		}

		return b, items, nil
	}

	// Sum bill_amount_minor (always in bill's currency)
	var total int64

	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(bill_amount_minor), 0)
		FROM bill_line_items
		WHERE bill_id = $1
	`, billID).Scan(&total)

	if err != nil {
		return Bill{}, nil, fmt.Errorf("sum line items: %w", err)
	}

	var b Bill

	err = tx.QueryRow(ctx, `
		UPDATE bills
		SET
			status = $2,
			total_amount_minor = $3,
			closed_at = $4
		WHERE id = $1
		RETURNING
			id,
			customer_id,
			currency,
			status,
			period_start,
			period_end,
			total_amount_minor,
			COALESCE(temporal_workflow_id, ''),
			created_at,
			closed_at
	`, billID, BillStatusClosed, total, time.Now().UTC()).Scan(
		&b.ID,
		&b.CustomerID,
		&b.Currency,
		&b.Status,
		&b.PeriodStart,
		&b.PeriodEnd,
		&b.TotalAmountMinor,
		&b.WorkflowID,
		&b.CreatedAt,
		&b.ClosedAt,
	)

	if err != nil {
		return Bill{}, nil, fmt.Errorf("update bill closed: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT id, bill_id, description, base_currency, base_amount_minor,
		       bill_currency, bill_amount_minor, fx_rate, fx_rate_date, created_at
		FROM bill_line_items
		WHERE bill_id = $1
		ORDER BY id
	`, billID)

	if err != nil {
		return Bill{}, nil, fmt.Errorf("list line items: %w", err)
	}
	defer rows.Close()

	var items []LineItem

	for rows.Next() {
		var item LineItem

		if err := rows.Scan(
			&item.ID,
			&item.BillID,
			&item.Description,
			&item.BaseCurrency,
			&item.BaseAmountMinor,
			&item.BillCurrency,
			&item.BillAmountMinor,
			&item.FXRate,
			&item.FXRateDate,
			&item.CreatedAt,
		); err != nil {
			return Bill{}, nil, fmt.Errorf("scan line item: %w", err)
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return Bill{}, nil, fmt.Errorf("iterate line items: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Bill{}, nil, fmt.Errorf("commit close bill: %w", err)
	}

	return b, items, nil
}
