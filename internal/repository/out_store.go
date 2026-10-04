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

type OutStoreInput struct {
	ServiceRequestID, ShopID, SentDate string
	DueDate, Price, Remarks            *string
}
type OutStorePatch struct {
	ShopID, SentDate, DueDate, Price, Remarks, Status *string
	DueDateSet, PriceSet, RemarksSet                  bool
}
type OutStoreFilter struct {
	Page, PageSize                          int
	Order, ServiceRequestID, Status, ShopID string
	Q                                       string // record number, customer, mobile or shop
}

const outStoreJSON = `to_jsonb(e)||jsonb_build_object('price',e.price::text)`

// outStoreViewJSON adds what the Out-Store screen shows next to each entry
// (T38): record number and profile, customer name and mobile, shop name and
// contact, and whether the entry is overdue at the shop.
const outStoreViewJSON = outStoreJSON + `||jsonb_build_object('request_no',r.request_no,'profile_id',r.profile_id,'profile_name',p.name,'customer_id',c.id,'customer_name',c.name,'customer_contact',c.contact,'shop_name',sh.shop_name,'shop_contact',sh.contact,'overdue',COALESCE(e.status='SENT' AND e.due_date<current_date,false))`

const outStoreViewFrom = ` FROM out_store_entries e
 JOIN service_requests r ON r.company_id=e.company_id AND r.id=e.service_request_id
 JOIN service_profiles p ON p.company_id=r.company_id AND p.id=r.profile_id
 JOIN customers c ON c.company_id=r.company_id AND c.id=r.customer_id
 JOIN out_store_shops sh ON sh.company_id=e.company_id AND sh.id=e.shop_id`

func (s *CatalogStore) ListOutStore(ctx context.Context, companyID string, f OutStoreFilter) ([]map[string]any, int, error) {
	where := []string{"e.company_id=$1"}
	args := []any{companyID}
	for _, filter := range []struct{ column, value string }{{"e.service_request_id", f.ServiceRequestID}, {"e.status", f.Status}, {"e.shop_id", f.ShopID}} {
		if filter.value != "" {
			args = append(args, filter.value)
			where = append(where, fmt.Sprintf("%s=$%d", filter.column, len(args)))
		}
	}
	if text := strings.TrimSpace(f.Q); text != "" {
		args = append(args, likeContains(text))
		n := len(args)
		search := fmt.Sprintf("(r.request_no ILIKE $%d OR c.name ILIKE $%d OR c.contact ILIKE $%d OR sh.shop_name ILIKE $%d", n, n, n, n)
		if digits := onlyDigits(text); len(digits) >= 3 {
			args = append(args, "%"+digits+"%")
			search += fmt.Sprintf(" OR regexp_replace(c.contact,'[^0-9]','','g') LIKE $%d", len(args))
		}
		where = append(where, search+")")
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+outStoreViewFrom+` WHERE `+strings.Join(where, " AND "), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	direction := "DESC"
	if f.Order == "asc" {
		direction = "ASC"
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := s.pool.Query(ctx, `SELECT `+outStoreViewJSON+outStoreViewFrom+` WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY e.sent_date %s,e.id %s LIMIT $%d OFFSET $%d", direction, direction, len(args)-1, len(args)), args...)
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
func (s *CatalogStore) GetOutStore(ctx context.Context, companyID, id string) (map[string]any, error) {
	return scanObject(s.pool.QueryRow(ctx, `SELECT `+outStoreViewJSON+outStoreViewFrom+` WHERE e.company_id=$1 AND e.id=$2`, companyID, id))
}

func (s *CatalogStore) CreateOutStore(ctx context.Context, companyID, actorID string, input OutStoreInput) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileID, oldStatus string
	var sentStatus, receivedStatus *string
	var enabled, currentClosed bool
	if err := tx.QueryRow(ctx, `SELECT r.profile_id::text,r.status_id::text,p.out_store_enabled,p.sent_status_id::text,p.received_status_id::text,st.is_closed FROM service_requests r JOIN service_profiles p ON p.company_id=r.company_id AND p.id=r.profile_id JOIN service_profile_statuses st ON st.company_id=r.company_id AND st.id=r.status_id WHERE r.company_id=$1 AND r.id=$2 FOR UPDATE OF r,p`, companyID, input.ServiceRequestID).Scan(&profileID, &oldStatus, &enabled, &sentStatus, &receivedStatus, &currentClosed); errors.Is(err, pgx.ErrNoRows) {
		return nil, service.FieldErrors{"service_request_id": "must reference an existing service request in this company"}
	} else if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, service.FieldErrors{"service_request_id": "the request profile does not have Out-Store enabled"}
	}
	if currentClosed {
		return nil, service.FieldErrors{"service_request_id": "cannot be sent out because the service request is closed"}
	}
	if sentStatus == nil || receivedStatus == nil {
		return nil, service.FieldErrors{"service_request_id": "the request profile must configure both sent and received-back statuses"}
	}
	if err := validateShop(ctx, tx, companyID, input.ShopID, profileID); err != nil {
		return nil, err
	}
	var alreadySent bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM out_store_entries WHERE company_id=$1 AND service_request_id=$2 AND status='SENT')`, companyID, input.ServiceRequestID).Scan(&alreadySent); err != nil {
		return nil, err
	}
	if alreadySent {
		return nil, service.InvalidState("This record is already at an Out-Store shop. Receive it back before sending it again.")
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `INSERT INTO out_store_entries AS e(company_id,service_request_id,shop_id,sent_date,due_date,price,remarks)VALUES($1,$2,$3,$4,$5,$6,$7)RETURNING `+outStoreJSON, companyID, input.ServiceRequestID, input.ShopID, input.SentDate, input.DueDate, input.Price, input.Remarks).Scan(&raw); err != nil {
		return nil, err
	}
	if oldStatus != *sentStatus {
		if _, err := tx.Exec(ctx, `UPDATE service_requests SET status_id=$3,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, input.ServiceRequestID, *sentStatus); err != nil {
			return nil, err
		}
		if err := insertStatusHistory(ctx, tx, companyID, input.ServiceRequestID, oldStatus, *sentStatus, actorID); err != nil {
			return nil, err
		}
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if err := recordOutStore(ctx, tx, companyID, result["id"].(string), "sent", actorID); err != nil {
		return nil, err
	}
	result["service_status_id"] = *sentStatus
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CatalogStore) UpdateOutStore(ctx context.Context, companyID, id, actorID string, patch OutStorePatch) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileID, currentShop, currentSent string
	var currentDue, currentPrice, currentRemarks *string
	var status string
	if err := tx.QueryRow(ctx, `SELECT r.profile_id::text,e.shop_id::text,e.sent_date::text,e.due_date::text,e.price::text,e.remarks,e.status FROM out_store_entries e JOIN service_requests r ON r.company_id=e.company_id AND r.id=e.service_request_id WHERE e.company_id=$1 AND e.id=$2 FOR UPDATE OF e`, companyID, id).Scan(&profileID, &currentShop, &currentSent, &currentDue, &currentPrice, &currentRemarks, &status); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if status != "SENT" {
		return nil, service.InvalidState("Only entries still at the shop can be changed.")
	}
	if patch.Status != nil && *patch.Status != "SENT" {
		return nil, service.InvalidState("Use receive back to mark an entry as received.")
	}
	shop, sent, due, price, remarks := currentShop, currentSent, currentDue, currentPrice, currentRemarks
	if patch.ShopID != nil {
		shop = *patch.ShopID
	}
	if patch.SentDate != nil {
		sent = *patch.SentDate
	}
	if patch.DueDateSet {
		due = patch.DueDate
	}
	if patch.PriceSet {
		price = patch.Price
	}
	if patch.RemarksSet {
		remarks = patch.Remarks
	}
	if err := validateShop(ctx, tx, companyID, shop, profileID); err != nil {
		return nil, err
	}
	before, _, err := outStoreSnapshot(ctx, tx, companyID, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE out_store_entries SET shop_id=$3,sent_date=$4,due_date=$5,price=$6,remarks=$7 WHERE company_id=$1 AND id=$2`, companyID, id, shop, sent, due, price, remarks); err != nil {
		return nil, err
	}
	if err := recordOutStoreChange(ctx, tx, companyID, id, before, actorID); err != nil {
		return nil, err
	}
	item, err := scanObject(tx.QueryRow(ctx, `SELECT `+outStoreJSON+` FROM out_store_entries e WHERE company_id=$1 AND id=$2`, companyID, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *CatalogStore) ReceiveOutStore(ctx context.Context, companyID, id, actorID string) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var requestID, status, currentStatus string
	var receivedStatus *string
	if err := tx.QueryRow(ctx, `SELECT e.service_request_id::text,e.status,r.status_id::text,p.received_status_id::text FROM out_store_entries e JOIN service_requests r ON r.company_id=e.company_id AND r.id=e.service_request_id JOIN service_profiles p ON p.company_id=r.company_id AND p.id=r.profile_id WHERE e.company_id=$1 AND e.id=$2 FOR UPDATE OF e,r,p`, companyID, id).Scan(&requestID, &status, &currentStatus, &receivedStatus); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if status == "SENT" {
		if receivedStatus == nil {
			return nil, service.InvalidState("Set the profile's received-back status before receiving items back.")
		}
		var targetEnabled bool
		if err := tx.QueryRow(ctx, `SELECT enabled FROM service_profile_statuses WHERE company_id=$1 AND id=$2`, companyID, *receivedStatus).Scan(&targetEnabled); err != nil {
			return nil, err
		}
		if !targetEnabled {
			return nil, service.InvalidState("The profile's received-back status is disabled. Update the Out-Store mapping first.")
		}
		if _, err := tx.Exec(ctx, `UPDATE out_store_entries SET status='RECEIVED_BACK',received_back_at=now() WHERE company_id=$1 AND id=$2`, companyID, id); err != nil {
			return nil, err
		}
		if currentStatus != *receivedStatus {
			if _, err := tx.Exec(ctx, `UPDATE service_requests SET status_id=$3,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, requestID, *receivedStatus); err != nil {
				return nil, err
			}
			if err := insertStatusHistory(ctx, tx, companyID, requestID, currentStatus, *receivedStatus, actorID); err != nil {
				return nil, err
			}
			currentStatus = *receivedStatus
		}
		if err := recordOutStore(ctx, tx, companyID, id, "received", actorID); err != nil {
			return nil, err
		}
	}
	item, err := scanObject(tx.QueryRow(ctx, `SELECT `+outStoreJSON+` FROM out_store_entries e WHERE company_id=$1 AND id=$2`, companyID, id))
	if err != nil {
		return nil, err
	}
	item["service_status_id"] = currentStatus
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}
func validateShop(ctx context.Context, tx pgx.Tx, companyID, shopID, profileID string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM out_store_shops WHERE company_id=$1 AND id=$2 AND status='ACTIVE' AND (profile_id IS NULL OR profile_id=$3))`, companyID, shopID, profileID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return service.FieldErrors{"shop_id": "must reference an active shop for this service profile (or a shop available to all profiles)"}
	}
	return nil
}
func insertStatusHistory(ctx context.Context, tx pgx.Tx, companyID, requestID, oldStatus, newStatus, actorID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO service_request_history(company_id,request_id,field_key,old_value,new_value,changed_by)VALUES($1,$2,'core.status',to_jsonb($3::text),to_jsonb($4::text),$5)`, companyID, requestID, oldStatus, newStatus, actorID)
	return err
}
