package fees

import (
	"encore.dev/storage/sqldb"

	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrBillNotFound      = errors.New("bill not found")
	ErrBillAlreadyClosed = errors.New("bill already closed")
	ErrCurrencyMismatch  = errors.New("line item currency does not match bill currency")
	ErrInvalidCurrency   = errors.New("invalid currency")
	ErrInvalidAmount     = errors.New("invalid amount")
	ErrInvalidPeriod     = errors.New("invalid billing period")
)

func validateCurrency(c Currency) error {
	switch c {
	case CurrencyUSD, CurrencyGEL:
		return nil
	default:
		return ErrInvalidCurrency
	}
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
		SELECT id, bill_id, description, amount_minor, currency, created_at
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
			&item.AmountMinor,
			&item.Currency,
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

	if billCurrency != req.Currency {
		return LineItem{}, ErrCurrencyMismatch
	}

	var item LineItem

	err = tx.QueryRow(ctx, `
		INSERT INTO bill_line_items (
			bill_id,
			description,
			amount_minor,
			currency
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id, bill_id, description, amount_minor, currency, created_at
	`, billID, req.Description, req.AmountMinor, req.Currency).Scan(
		&item.ID,
		&item.BillID,
		&item.Description,
		&item.AmountMinor,
		&item.Currency,
		&item.CreatedAt,
	)

	if err != nil {
		return LineItem{}, fmt.Errorf("insert line item: %w", err)
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

	var total int64

	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_minor), 0)
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
		SELECT id, bill_id, description, amount_minor, currency, created_at
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
			&item.AmountMinor,
			&item.Currency,
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
