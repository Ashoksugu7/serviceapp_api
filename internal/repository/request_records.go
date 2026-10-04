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

type RequestListFilter struct {
	Page, PageSize                                                               int
	Sort, Order, Q, RequestNo, CustomerID, ProfileID, StatusID, DateFrom, DateTo string
	// T34 Records filters.
	Contact    string // mobile digits, matched anywhere in the customer's number
	State      string // "open" or "closed"
	StatusName string // status name across profiles, case-insensitive
	CreatedBy  string // user ID
	OutStore   string // "at_shop", "received" or "none"
	Overdue    bool   // at a shop past its expected-back date
}

// RequestListFacets counts the records matching the other filters, for filter chips.
type RequestListFacets struct {
	Statuses []map[string]any `json:"statuses"`
	Open     int              `json:"open"`
	Closed   int              `json:"closed"`
	Creators []map[string]any `json:"creators"`
}

type requestCondition struct {
	sql   string // uses ? for its single argument, if any
	arg   any
	facet string // conditions skipped when counting this facet
}

const requestListFrom = ` FROM service_requests r
 JOIN customers c ON c.company_id=r.company_id AND c.id=r.customer_id
 JOIN service_profiles p ON p.company_id=r.company_id AND p.id=r.profile_id
 JOIN service_profile_statuses st ON st.company_id=r.company_id AND st.id=r.status_id
 LEFT JOIN users u ON u.id=r.created_by
 LEFT JOIN LATERAL (SELECT e.status, e.due_date FROM out_store_entries e
   WHERE e.company_id=r.company_id AND e.service_request_id=r.id
   ORDER BY (e.status='SENT') DESC, e.sent_date DESC, e.id DESC LIMIT 1) os ON true`

func requestConditions(f RequestListFilter) []requestCondition {
	conditions := []requestCondition{}
	add := func(sql string, arg any, facet string) {
		conditions = append(conditions, requestCondition{sql: sql, arg: arg, facet: facet})
	}
	if f.RequestNo != "" {
		add("r.request_no=?", f.RequestNo, "")
	}
	if f.CustomerID != "" {
		add("r.customer_id=?::uuid", f.CustomerID, "")
	}
	if f.ProfileID != "" {
		add("r.profile_id=?::uuid", f.ProfileID, "")
	}
	if f.StatusID != "" {
		add("r.status_id=?::uuid", f.StatusID, "status")
	}
	if f.StatusName != "" {
		add("lower(st.name)=lower(?)", f.StatusName, "status")
	}
	switch f.State {
	case "open":
		add("NOT st.is_closed", nil, "status")
	case "closed":
		add("st.is_closed", nil, "status")
	}
	if f.DateFrom != "" {
		add("r.service_date>=?::date", f.DateFrom, "")
	}
	if f.DateTo != "" {
		add("r.service_date<=?::date", f.DateTo, "")
	}
	if f.CreatedBy != "" {
		add("r.created_by=?::uuid", f.CreatedBy, "creator")
	}
	if digits := onlyDigits(f.Contact); digits != "" {
		add("regexp_replace(c.contact,'[^0-9]','','g') LIKE ?", "%"+digits+"%", "")
	}
	switch f.OutStore {
	case "at_shop":
		add("os.status='SENT'", nil, "")
	case "received":
		add("os.status='RECEIVED_BACK'", nil, "")
	case "none":
		add("os.status IS NULL", nil, "")
	}
	if f.Overdue {
		add("os.status='SENT' AND os.due_date<current_date", nil, "")
	}
	if f.Q != "" {
		// Search number, customer, mobile (digits only when the text has 3+ digits) and details.
		search := "(r.request_no ILIKE ? OR c.name ILIKE ?1 OR c.contact ILIKE ?1 OR r.form_data::text ILIKE ?1"
		if digits := onlyDigits(f.Q); len(digits) >= 3 {
			search += " OR regexp_replace(c.contact,'[^0-9]','','g') LIKE '%" + digits + "%'"
		}
		add(search+")", "%"+f.Q+"%", "")
	}
	return conditions
}

// whereClause renders conditions (skipping one facet's own filters) with
// numbered arguments after companyID ($1).
func whereClause(conditions []requestCondition, skipFacet string) (string, []any) {
	parts := []string{"r.company_id=$1"}
	args := []any{nil}
	for _, c := range conditions {
		if skipFacet != "" && c.facet == skipFacet {
			continue
		}
		sql := c.sql
		if strings.Contains(sql, "?") {
			args = append(args, c.arg)
			n := fmt.Sprintf("$%d", len(args))
			sql = strings.ReplaceAll(strings.ReplaceAll(sql, "?1", n), "?", n)
		}
		parts = append(parts, sql)
	}
	return strings.Join(parts, " AND "), args
}

func (s *CatalogStore) ListServiceRequests(ctx context.Context, companyID string, f RequestListFilter) ([]map[string]any, int, error) {
	items, total, _, err := s.ListServiceRequestsWithFacets(ctx, companyID, f, false)
	return items, total, err
}

// ListServiceRequestsWithFacets lists one page of records and, when asked,
// counts per status, open/closed and creator for the filter chips (T34).
func (s *CatalogStore) ListServiceRequestsWithFacets(ctx context.Context, companyID string, f RequestListFilter, withFacets bool) ([]map[string]any, int, *RequestListFacets, error) {
	conditions := requestConditions(f)
	where, args := whereClause(conditions, "")
	args[0] = companyID
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+requestListFrom+` WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, nil, err
	}
	sortColumn := map[string]string{"service_date": "r.service_date", "updated_at": "r.updated_at", "request_no": "r.request_no"}[f.Sort]
	if sortColumn == "" {
		sortColumn = "r.created_at"
	}
	order := "DESC"
	if f.Order == "asc" {
		order = "ASC"
	}
	pageArgs := append(append([]any{}, args...), f.PageSize, (f.Page-1)*f.PageSize)
	query := `SELECT to_jsonb(r)||jsonb_build_object(
	    'status_name',st.name,'closed',st.is_closed,'customer_name',c.name,
	    'customer_contact',c.contact,'profile_name',p.name,'created_by_name',u.name,
	    'out_store_status',os.status,'out_store_due_date',os.due_date)` + requestListFrom + ` WHERE ` + where +
		fmt.Sprintf(" ORDER BY %s %s,r.id %s LIMIT $%d OFFSET $%d", sortColumn, order, order, len(pageArgs)-1, len(pageArgs))
	rows, err := s.pool.Query(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, nil, err
		}
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, 0, nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil || !withFacets {
		return items, total, nil, err
	}
	facets, err := s.requestFacets(ctx, companyID, conditions)
	return items, total, facets, err
}

func (s *CatalogStore) requestFacets(ctx context.Context, companyID string, conditions []requestCondition) (*RequestListFacets, error) {
	facets := &RequestListFacets{Statuses: []map[string]any{}, Creators: []map[string]any{}}
	where, args := whereClause(conditions, "status")
	args[0] = companyID
	rows, err := s.pool.Query(ctx, `SELECT st.name, bool_or(st.is_closed), count(*)`+requestListFrom+` WHERE `+where+` GROUP BY st.name ORDER BY min(st.sort_order), st.name`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var closed bool
		var count int
		if err := rows.Scan(&name, &closed, &count); err != nil {
			rows.Close()
			return nil, err
		}
		facets.Statuses = append(facets.Statuses, map[string]any{"status_name": name, "closed": closed, "count": count})
		if closed {
			facets.Closed += count
		} else {
			facets.Open += count
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	where, args = whereClause(conditions, "creator")
	args[0] = companyID
	rows, err = s.pool.Query(ctx, `SELECT r.created_by::text, COALESCE(min(u.name),'Unknown'), count(*)`+requestListFrom+` WHERE `+where+` GROUP BY r.created_by ORDER BY count(*) DESC, 2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var count int
		if err := rows.Scan(&id, &name, &count); err != nil {
			return nil, err
		}
		facets.Creators = append(facets.Creators, map[string]any{"id": id, "name": name, "count": count})
	}
	return facets, rows.Err()
}

func (s *CatalogStore) GetServiceRequest(ctx context.Context, companyID, id string) (map[string]any, error) {
	var requestRaw, customerRaw []byte
	var profileID string
	if err := s.pool.QueryRow(ctx, `SELECT to_jsonb(r)||jsonb_build_object('status_name',st.name),to_jsonb(c),r.profile_id::text FROM service_requests r JOIN customers c ON c.company_id=r.company_id AND c.id=r.customer_id JOIN service_profile_statuses st ON st.company_id=r.company_id AND st.id=r.status_id WHERE r.company_id=$1 AND r.id=$2`, companyID, id).Scan(&requestRaw, &customerRaw, &profileID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var request, customer map[string]any
	_ = json.Unmarshal(requestRaw, &request)
	_ = json.Unmarshal(customerRaw, &customer)
	form, err := s.GetForm(ctx, companyID, profileID)
	if err != nil {
		return nil, err
	}
	request["customer"] = customer
	request["form_definition"] = form
	linked, err := s.resolveLinkedValues(ctx, companyID, profileID, request["form_data"].(map[string]any))
	if err != nil {
		return nil, err
	}
	request["linked_values"] = linked
	return request, nil
}

func (s *CatalogStore) resolveLinkedValues(ctx context.Context, companyID, profileID string, formData map[string]any) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT field_key,field_type,config FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2 AND field_type IN ('linked_product','linked_charges','staff_role')`, companyID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]any{}
	for rows.Next() {
		var key, kind string
		var configRaw []byte
		if err := rows.Scan(&key, &kind, &configRaw); err != nil {
			return nil, err
		}
		value, present := formData[key]
		if !present || value == nil {
			continue
		}
		ids := []string{}
		if text, ok := value.(string); ok {
			ids = []string{text}
		} else if list, ok := value.([]any); ok {
			for _, entry := range list {
				if text, ok := entry.(string); ok {
					ids = append(ids, text)
				}
			}
		}
		displays := []map[string]any{}
		for _, id := range ids {
			var label, status string
			selectable := false
			switch kind {
			case "linked_product":
				err = s.pool.QueryRow(ctx, `SELECT name,status FROM products WHERE company_id=$1 AND profile_id=$2 AND id=$3`, companyID, profileID, id).Scan(&label, &status)
				selectable = status == "ACTIVE"
			case "linked_charges":
				err = s.pool.QueryRow(ctx, `SELECT name,status FROM charges WHERE company_id=$1 AND profile_id=$2 AND id=$3`, companyID, profileID, id).Scan(&label, &status)
				selectable = status == "ACTIVE"
			case "staff_role":
				var config map[string]any
				_ = json.Unmarshal(configRaw, &config)
				roleID, _ := config["role_id"].(string)
				err = s.pool.QueryRow(ctx, `SELECT s.name,s.status FROM staff s WHERE s.company_id=$1 AND s.id=$2`, companyID, id).Scan(&label, &status)
				if err == nil {
					var assigned bool
					_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_role_assignments WHERE company_id=$1 AND staff_id=$2 AND role_id=$3)`, companyID, id, roleID).Scan(&assigned)
					selectable = status == "ACTIVE" && assigned
				}
			}
			if errors.Is(err, pgx.ErrNoRows) {
				label = "Unavailable"
				selectable = false
				err = nil
			}
			if err != nil {
				return nil, err
			}
			displays = append(displays, map[string]any{"id": id, "label": label, "selectable": selectable})
		}
		result[key] = displays
	}
	return result, rows.Err()
}

type RequestPatch struct {
	ServiceDate, CustomerID *string
	FormData                map[string]any
	FormDataSet             bool
}

func (s *CatalogStore) UpdateServiceRequest(ctx context.Context, companyID, id, actorID, actorRole string, patch RequestPatch) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileID, oldDate, oldCustomer string
	var oldFormRaw []byte
	var closed bool
	if err := tx.QueryRow(ctx, `SELECT r.profile_id::text,r.service_date::text,r.customer_id::text,r.form_data,s.is_closed FROM service_requests r JOIN service_profile_statuses s ON s.company_id=r.company_id AND s.id=r.status_id WHERE r.company_id=$1 AND r.id=$2 FOR UPDATE OF r`, companyID, id).Scan(&profileID, &oldDate, &oldCustomer, &oldFormRaw, &closed); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if closed {
		return nil, service.InvalidState("Closed records cannot be edited. Move the record to an open status first.")
	}
	newDate, newCustomer := oldDate, oldCustomer
	if patch.ServiceDate != nil {
		newDate = *patch.ServiceDate
	}
	if patch.CustomerID != nil {
		newCustomer = *patch.CustomerID
		if newCustomer != oldCustomer {
			var blocked bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM out_store_entries WHERE company_id=$1 AND service_request_id=$2) OR EXISTS(SELECT 1 FROM standby_item_issues WHERE company_id=$1 AND service_request_id=$2)`, companyID, id).Scan(&blocked); err != nil {
				return nil, err
			}
			if blocked {
				return nil, service.InvalidState("The customer cannot change after the record has been sent to Out-Store or linked to a stand-by loan.")
			}
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customers WHERE company_id=$1 AND id=$2)`, companyID, newCustomer).Scan(&exists)
			if !exists {
				return nil, ErrNotFound
			}
		}
	}
	oldForm := map[string]any{}
	_ = json.Unmarshal(oldFormRaw, &oldForm)
	newForm := map[string]any{}
	for k, v := range oldForm {
		newForm[k] = v
	}
	if patch.FormDataSet {
		for k, v := range patch.FormData {
			if v == nil {
				delete(newForm, k)
			} else {
				newForm[k] = v
			}
		}
		fields, err := loadDynamicFields(ctx, tx, companyID, profileID)
		if err != nil {
			return nil, err
		}
		if validation := service.ValidateFormPatch(fields, newForm, patch.FormData); len(validation) > 0 {
			return nil, service.FieldErrors(validation)
		}
		if validation := service.ApplyFormulas(fields, newForm, patch.FormData); len(validation) > 0 {
			return nil, validation
		}
		if validation := validateLinkedValues(ctx, tx, companyID, profileID, fields, newForm); len(validation) > 0 {
			return nil, service.FieldErrors(validation)
		}
	}
	changes := map[string][2]any{}
	if newDate != oldDate {
		changes["core.service_date"] = [2]any{oldDate, newDate}
	}
	if newCustomer != oldCustomer {
		changes["core.customer_id"] = [2]any{oldCustomer, newCustomer}
	}
	for key := range unionKeys(oldForm, newForm) {
		oldValue, oldOK := oldForm[key]
		newValue, newOK := newForm[key]
		if !oldOK {
			oldValue = nil
		}
		if !newOK {
			newValue = nil
		}
		if !jsonEqual(oldValue, newValue) {
			changes[key] = [2]any{oldValue, newValue}
		}
	}
	if len(changes) > 0 {
		formJSON, _ := json.Marshal(newForm)
		if _, err := tx.Exec(ctx, `UPDATE service_requests SET service_date=$3,customer_id=$4,form_data=$5,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, id, newDate, newCustomer, formJSON); err != nil {
			return nil, err
		}
		for key, pair := range changes {
			oldJSON, _ := json.Marshal(pair[0])
			newJSON, _ := json.Marshal(pair[1])
			if _, err := tx.Exec(ctx, `INSERT INTO service_request_history(company_id,request_id,field_key,old_value,new_value,changed_by)VALUES($1,$2,$3,$4,$5,$6)`, companyID, id, key, oldJSON, newJSON, actorID); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.getServiceRequestBase(ctx, companyID, id)
}

func (s *CatalogStore) ChangeServiceRequestStatus(ctx context.Context, companyID, id, statusID, actorID, actorRole string) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileID, oldStatus string
	var currentClosed bool
	if err := tx.QueryRow(ctx, `SELECT r.profile_id::text,r.status_id::text,s.is_closed FROM service_requests r JOIN service_profile_statuses s ON s.company_id=r.company_id AND s.id=r.status_id WHERE r.company_id=$1 AND r.id=$2 FOR UPDATE OF r`, companyID, id).Scan(&profileID, &oldStatus, &currentClosed); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var targetClosed bool
	if err := tx.QueryRow(ctx, `SELECT is_closed FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id=$3 AND enabled`, companyID, profileID, statusID).Scan(&targetClosed); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if currentClosed && actorRole != "ADMIN" {
		return nil, service.Forbidden("Only an admin can reopen a closed record.")
	}
	if targetClosed {
		var outstanding bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM out_store_entries WHERE company_id=$1 AND service_request_id=$2 AND status='SENT') OR EXISTS(SELECT 1 FROM standby_item_issues WHERE company_id=$1 AND service_request_id=$2 AND returned_at IS NULL)`, companyID, id).Scan(&outstanding); err != nil {
			return nil, err
		}
		if outstanding {
			return nil, service.InvalidState("The record cannot be closed while it is at an Out-Store shop or has a stand-by item out.")
		}
	}
	if oldStatus != statusID {
		if _, err := tx.Exec(ctx, `UPDATE service_requests SET status_id=$3,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, id, statusID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_request_history(company_id,request_id,field_key,old_value,new_value,changed_by)VALUES($1,$2,'core.status',to_jsonb($3::text),to_jsonb($4::text),$5)`, companyID, id, oldStatus, statusID, actorID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.getServiceRequestBase(ctx, companyID, id)
}

func (s *CatalogStore) getServiceRequestBase(ctx context.Context, companyID, id string) (map[string]any, error) {
	return scanObject(s.pool.QueryRow(ctx, `SELECT to_jsonb(r)||jsonb_build_object('status_name',st.name) FROM service_requests r JOIN service_profile_statuses st ON st.company_id=r.company_id AND st.id=r.status_id WHERE r.company_id=$1 AND r.id=$2`, companyID, id))
}

func (s *CatalogStore) ListRequestHistory(ctx context.Context, companyID, id string, page, pageSize int, order string) ([]map[string]any, int, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_requests WHERE company_id=$1 AND id=$2)`, companyID, id).Scan(&exists); err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, 0, ErrNotFound
	}
	var total int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM service_request_history WHERE company_id=$1 AND request_id=$2`, companyID, id).Scan(&total)
	direction := "ASC"
	if order == "desc" {
		direction = "DESC"
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT to_jsonb(h)||jsonb_build_object('changed_by_name',u.name) FROM service_request_history h LEFT JOIN users u ON u.company_id=h.company_id AND u.id=h.changed_by WHERE h.company_id=$1 AND h.request_id=$2 ORDER BY h.created_at %s,h.id %s LIMIT $3 OFFSET $4`, direction, direction), companyID, id, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		items = append(items, item)
	}
	return items, total, rows.Err()
}
func unionKeys(a, b map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}
func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
