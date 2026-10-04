package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"serviceops360/api/internal/service"
)

type StandbyIssueInput struct {
	CustomerID, IssuedDate                      string
	ServiceRequestID, DueDate, Notes, ProductID *string
}

const standbyItemJSON = `to_jsonb(i)||jsonb_build_object('price',i.price::text)`

// StandbyIssueFilter selects lending history across items (T36).
type StandbyIssueFilter struct {
	Page, PageSize                              int
	Order, Contact, CustomerID, ItemID, Request string
	State                                       string // open or returned
}

// standbyIssueFrom adds item, customer, record and user names, and days out
// (to today while the item is still out) to each issue.
const standbyIssueFrom = ` FROM standby_item_issues si
 JOIN standby_items i ON i.company_id=si.company_id AND i.id=si.standby_item_id
 JOIN customers c ON c.company_id=si.company_id AND c.id=si.customer_id
 LEFT JOIN service_requests r ON r.company_id=si.company_id AND r.id=si.service_request_id
 LEFT JOIN users ib ON ib.company_id=si.company_id AND ib.id=si.issued_by
 LEFT JOIN users rb ON rb.company_id=si.company_id AND rb.id=si.returned_by`

const standbyIssueJSON = `to_jsonb(si)||jsonb_build_object('item_name',i.name,'serial_no',i.serial_no,'customer_name',c.name,'customer_contact',c.contact,'request_no',r.request_no,'issued_by_name',ib.name,'returned_by_name',rb.name,'days_out',GREATEST(COALESCE(si.returned_at::date,current_date)-si.issued_date,0),'overdue',si.returned_at IS NULL AND si.due_date<current_date)`

func (s *CatalogStore) ListStandbyIssues(ctx context.Context, companyID, itemID string) ([]map[string]any, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM standby_items WHERE company_id=$1 AND id=$2)`, companyID, itemID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	items, _, err := s.ListStandbyIssueHistory(ctx, companyID, StandbyIssueFilter{Page: 1, PageSize: 500, ItemID: itemID})
	return items, err
}

// ListStandbyIssueHistory returns lending history, newest first by default,
// filtered by customer mobile digits, customer, item, record or state.
func (s *CatalogStore) ListStandbyIssueHistory(ctx context.Context, companyID string, f StandbyIssueFilter) ([]map[string]any, int, error) {
	where := []string{"si.company_id=$1"}
	args := []any{companyID}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if digits := onlyDigits(f.Contact); digits != "" {
		add("regexp_replace(c.contact,'[^0-9]','','g') LIKE $%d", "%"+digits+"%")
	}
	for _, filter := range []struct{ column, value string }{{"si.customer_id", f.CustomerID}, {"si.standby_item_id", f.ItemID}, {"si.service_request_id", f.Request}} {
		if filter.value != "" {
			add(filter.column+"=$%d", filter.value)
		}
	}
	switch f.State {
	case "open":
		where = append(where, "si.returned_at IS NULL")
	case "returned":
		where = append(where, "si.returned_at IS NOT NULL")
	}
	condition := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+standbyIssueFrom+condition, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	direction := "DESC"
	if f.Order == "asc" {
		direction = "ASC"
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := s.pool.Query(ctx, `SELECT `+standbyIssueJSON+standbyIssueFrom+condition+fmt.Sprintf(" ORDER BY si.issued_date %s,si.id %s LIMIT $%d OFFSET $%d", direction, direction, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		item, err := scanObject(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *CatalogStore) IssueStandbyItem(ctx context.Context, companyID, itemID, actorID string, input StandbyIssueInput) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var itemStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM standby_items WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, itemID).Scan(&itemStatus); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	switch itemStatus {
	case "ISSUED":
		return nil, service.InvalidState("This item is already issued. Return it before issuing it again.")
	case "UNDER_MAINTENANCE":
		return nil, service.InvalidState("This item is under maintenance. Mark it available before issuing it.")
	}

	var customerExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customers WHERE company_id=$1 AND id=$2)`, companyID, input.CustomerID).Scan(&customerExists); err != nil {
		return nil, err
	}
	if !customerExists {
		return nil, notFound("Customer not found in this company.")
	}

	var requestProfileID string
	if input.ServiceRequestID != nil {
		var requestCustomerID string
		var closed bool
		err := tx.QueryRow(ctx, `SELECT r.customer_id::text,r.profile_id::text,st.is_closed FROM service_requests r JOIN service_profile_statuses st ON st.company_id=r.company_id AND st.id=r.status_id WHERE r.company_id=$1 AND r.id=$2 FOR SHARE OF r`, companyID, *input.ServiceRequestID).Scan(&requestCustomerID, &requestProfileID, &closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound("Linked service record not found in this company.")
		}
		if err != nil {
			return nil, err
		}
		if requestCustomerID != input.CustomerID {
			return nil, service.InvalidState("The linked record belongs to a different customer.")
		}
		if closed {
			return nil, service.InvalidState("The linked record is closed. Link an open record or none.")
		}
	}

	if input.ProductID != nil {
		var productProfileID string
		err := tx.QueryRow(ctx, `SELECT profile_id::text FROM products WHERE company_id=$1 AND id=$2 FOR SHARE`, companyID, *input.ProductID).Scan(&productProfileID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if input.ServiceRequestID != nil && productProfileID != requestProfileID {
			return nil, service.InvalidState("The product received must belong to the linked record's profile.")
		}
	}

	var issueRaw []byte
	err = tx.QueryRow(ctx, `INSERT INTO standby_item_issues AS si(company_id,standby_item_id,customer_id,service_request_id,issued_date,due_date,notes,received_product_id,issued_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING to_jsonb(si)`, companyID, itemID, input.CustomerID, input.ServiceRequestID, input.IssuedDate, input.DueDate, input.Notes, input.ProductID, actorID).Scan(&issueRaw)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE standby_items SET status='ISSUED' WHERE company_id=$1 AND id=$2`, companyID, itemID); err != nil {
		return nil, err
	}
	var issueID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(issueRaw, &issueID); err != nil {
		return nil, err
	}
	if err := recordStandby(ctx, tx, companyID, issueID.ID, "issued", actorID); err != nil {
		return nil, err
	}
	result, err := standbyResult(ctx, tx, companyID, itemID, issueRaw)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CatalogStore) ReturnStandbyItem(ctx context.Context, companyID, itemID, issueID, actorID string) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var itemStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM standby_items WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, itemID).Scan(&itemStatus); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var issueRaw []byte
	var returned bool
	err = tx.QueryRow(ctx, `SELECT to_jsonb(si),returned_at IS NOT NULL FROM standby_item_issues si WHERE company_id=$1 AND standby_item_id=$2 AND id=$3 FOR UPDATE`, companyID, itemID, issueID).Scan(&issueRaw, &returned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !returned {
		if itemStatus != "ISSUED" {
			return nil, service.InvalidState("This item is not currently issued.")
		}
		if err := tx.QueryRow(ctx, `UPDATE standby_item_issues AS si SET returned_at=now(),returned_by=$4 WHERE company_id=$1 AND standby_item_id=$2 AND id=$3 RETURNING to_jsonb(si)`, companyID, itemID, issueID, actorID).Scan(&issueRaw); err != nil {
			return nil, err
		}
		if err := recordStandby(ctx, tx, companyID, issueID, "returned", actorID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE standby_items SET status='AVAILABLE' WHERE company_id=$1 AND id=$2`, companyID, itemID); err != nil {
			return nil, err
		}
	}
	result, err := standbyResult(ctx, tx, companyID, itemID, issueRaw)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func standbyResult(ctx context.Context, tx pgx.Tx, companyID, itemID string, issueRaw []byte) (map[string]any, error) {
	item, err := scanObject(tx.QueryRow(ctx, `SELECT `+standbyItemJSON+` FROM standby_items i WHERE company_id=$1 AND id=$2`, companyID, itemID))
	if err != nil {
		return nil, err
	}
	var issue map[string]any
	if err := json.Unmarshal(issueRaw, &issue); err != nil {
		return nil, err
	}
	return map[string]any{"item": item, "issue": issue}, nil
}

func (s *CatalogStore) updateStandbyItem(ctx context.Context, companyID, id string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs["standby-items"]
	keys := sortedAllowed(values, spec.update)
	if len(keys) == 0 || len(keys) != len(values) {
		return nil, fieldsError(values, spec.update)
	}
	if raw, changes := values["status"]; changes {
		status, ok := raw.(string)
		if !ok || (status != "AVAILABLE" && status != "UNDER_MAINTENANCE") {
			return nil, service.InvalidState("Use Issue and Return on the Stand-by screen to change whether an item is issued. Here the status can only be AVAILABLE or UNDER_MAINTENANCE.")
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM standby_items WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&currentStatus); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if _, changes := values["status"]; changes && currentStatus == "ISSUED" {
		return nil, service.InvalidState("Return the item before changing its status.")
	}
	args, sets := []any{}, []string{}
	for _, key := range keys {
		args = append(args, values[key])
		sets = append(sets, fmt.Sprintf("%s=$%d", spec.update[key], len(args)))
	}
	args = append(args, companyID, id)
	query := fmt.Sprintf("UPDATE standby_items i SET %s WHERE company_id=$%d AND id=$%d RETURNING %s", strings.Join(sets, ","), len(args)-1, len(args), standbyItemJSON)
	item, err := scanObject(tx.QueryRow(ctx, query, args...))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}
