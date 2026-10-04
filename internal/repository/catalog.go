package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"serviceops360/api/internal/service"
)

var ErrNotFound = errors.New("resource not found")

// notFoundError names what was missing while still matching ErrNotFound.
type notFoundError struct{ message string }

func (e *notFoundError) Error() string { return ErrNotFound.Error() + ": " + e.message }
func (e *notFoundError) Unwrap() error { return ErrNotFound }

func notFound(message string) error { return &notFoundError{message: message} }

// NotFoundMessage returns the client-facing text for a not-found error.
func NotFoundMessage(err error) string {
	var named *notFoundError
	if errors.As(err, &named) {
		return named.message
	}
	return "Not found. It may have been removed, or it belongs to another company."
}

var ErrLastAdmin = errors.New("the last active admin cannot be deactivated or made a user; make another user an admin first")

type CatalogStore struct{ pool *pgxpool.Pool }

func NewCatalogStore(pool *pgxpool.Pool) *CatalogStore { return &CatalogStore{pool: pool} }

type resourceSpec struct {
	table, read    string
	create, update map[string]string
	profile        bool
}

var catalogSpecs = map[string]resourceSpec{
	"users":            {"users", `to_jsonb(t)-'password_hash'`, cols("name,email,phone,password_hash,must_change_password,role"), cols("name,email,phone,role,status"), false},
	"customers":        {"customers", "to_jsonb(t)", cols("name,contact,email,address"), cols("name,contact,email,address"), false},
	"staff":            {"staff", `to_jsonb(t)||jsonb_build_object('role_ids',COALESCE((SELECT jsonb_agg(a.role_id ORDER BY a.role_id) FROM staff_role_assignments a WHERE a.company_id=t.company_id AND a.staff_id=t.id),'[]'::jsonb))`, cols("name,contact,email,specialization,status,role_ids"), cols("name,contact,email,specialization,status,role_ids"), false},
	"staff-roles":      {"staff_roles", "to_jsonb(t)", cols("name"), cols("name"), false},
	"products":         {"products", "to_jsonb(t)", cols("profile_id,name,brand,category,status"), cols("name,brand,category,status"), false},
	"charges":          {"charges", "to_jsonb(t)", cols("profile_id,name,description,status"), cols("name,description,status"), false},
	"out-store-shops":  {"out_store_shops", "to_jsonb(t)", cols("profile_id,shop_name,contact_person,contact,address,status"), cols("shop_name,contact_person,contact,address,status"), false},
	"standby-items":    {"standby_items", `to_jsonb(t)||jsonb_build_object('price',t.price::text)`, cols("name,serial_no,category,price"), cols("name,serial_no,category,price,status"), false},
	"service-profiles": {"service_profiles", "to_jsonb(t)", cols("name,prefix"), cols("name,prefix,out_store_enabled,is_active,core_labels"), false},
	"fields":           {"service_profile_fields", `to_jsonb(t)-'field_key'-'field_type'||jsonb_build_object('key',t.field_key,'type',t.field_type)`, cols("key:field_key,label,type:field_type,required,enabled,sort_order,config"), cols("label,type:field_type,required,enabled,sort_order,config"), true},
	"statuses":         {"service_profile_statuses", `to_jsonb(t)-'is_initial'-'is_closed'||jsonb_build_object('initial',t.is_initial,'closed',t.is_closed)`, cols("name,sort_order,initial:is_initial,closed:is_closed,enabled"), cols("name,sort_order,initial:is_initial,closed:is_closed,enabled"), true},
}

func cols(value string) map[string]string {
	result := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		parts := strings.SplitN(item, ":", 2)
		result[parts[0]] = parts[len(parts)-1]
	}
	return result
}

func (s *CatalogStore) List(ctx context.Context, resource, companyID, profileID string) ([]map[string]any, error) {
	spec, ok := catalogSpecs[resource]
	if !ok {
		return nil, errors.New("unknown resource")
	}
	query := fmt.Sprintf("SELECT %s FROM %s t WHERE company_id=$1", spec.read, spec.table)
	args := []any{companyID}
	if spec.profile {
		query += " AND profile_id=$2"
		args = append(args, profileID)
	}
	query += " ORDER BY id"
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CatalogStore) Get(ctx context.Context, resource, companyID, profileID, id string) (map[string]any, error) {
	spec := catalogSpecs[resource]
	query := fmt.Sprintf("SELECT %s FROM %s t WHERE company_id=$1 AND id=$2", spec.read, spec.table)
	args := []any{companyID, id}
	if spec.profile {
		query += " AND profile_id=$3"
		args = append(args, profileID)
	}
	return scanObject(s.pool.QueryRow(ctx, query, args...))
}

func (s *CatalogStore) Create(ctx context.Context, resource, companyID, profileID string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs[resource]
	if err := validateCatalogValues(resource, values); err != nil {
		return nil, err
	}
	if resource == "customers" {
		return s.createCustomer(ctx, companyID, values)
	}
	if resource == "staff" {
		return s.saveStaff(ctx, companyID, "", values, true)
	}
	if resource == "service-profiles" {
		return s.createProfile(ctx, companyID, values)
	}
	if resource == "fields" {
		return s.createField(ctx, companyID, profileID, values)
	}
	if resource == "statuses" {
		return s.createStatus(ctx, companyID, profileID, values)
	}
	return s.insert(ctx, spec, companyID, profileID, values)
}

func (s *CatalogStore) insert(ctx context.Context, spec resourceSpec, companyID, profileID string, values map[string]any) (map[string]any, error) {
	keys := sortedAllowed(values, spec.create)
	if len(keys) != len(values) {
		return nil, fieldsError(values, spec.create)
	}
	columns := []string{"company_id"}
	args := []any{companyID}
	placeholders := []string{"$1"}
	if spec.profile {
		columns = append(columns, "profile_id")
		args = append(args, profileID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	for _, key := range keys {
		columns = append(columns, spec.create[key])
		args = append(args, values[key])
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	query := fmt.Sprintf("INSERT INTO %s AS t(%s) VALUES (%s) RETURNING %s", spec.table, strings.Join(columns, ","), strings.Join(placeholders, ","), spec.read)
	return scanObject(s.pool.QueryRow(ctx, query, args...))
}

func (s *CatalogStore) Update(ctx context.Context, resource, companyID, profileID, id string, values map[string]any) (map[string]any, error) {
	if err := validateCatalogValues(resource, values); err != nil {
		return nil, err
	}
	if resource == "standby-items" {
		return s.updateStandbyItem(ctx, companyID, id, values)
	}
	if resource == "statuses" {
		return s.updateStatus(ctx, companyID, profileID, id, values)
	}
	if resource == "fields" {
		return s.updateField(ctx, companyID, profileID, id, values)
	}
	if resource == "users" {
		return s.updateUser(ctx, companyID, id, values)
	}
	if resource == "staff" {
		return s.saveStaff(ctx, companyID, id, values, false)
	}
	if resource == "customers" {
		if contact, ok := values["contact"].(string); ok {
			if onlyDigits(contact) == "" {
				return nil, service.FieldErrors{"contact": "must be a mobile number"}
			}
			if owner, found, err := contactOwner(ctx, s.pool, companyID, contact, id); err != nil {
				return nil, err
			} else if found {
				return nil, service.FieldErrors{"contact": "already belongs to " + owner}
			}
		}
	}
	if resource == "service-profiles" {
		if raw, changes := values["core_labels"]; changes {
			labels, ok := raw.(map[string]any)
			if !ok || len(labels) == 0 {
				return nil, service.Invalid("core_labels must be an object with at least one label.")
			}
			allowed := map[string]bool{"service_date": true, "customer_name": true, "customer_contact": true}
			for key, rawLabel := range labels {
				label, ok := rawLabel.(string)
				if !allowed[key] || !ok || strings.TrimSpace(label) == "" || len(label) > 200 {
					return nil, service.FieldErrors{"core_labels": "labels must be service_date, customer_name or customer_contact, with 1 to 200 characters"}
				}
			}
		}
		if prefix, changesPrefix := values["prefix"]; changesPrefix {
			// Resending the current prefix (e.g. from a full edit form) is not a change.
			var current string
			var used bool
			if err := s.pool.QueryRow(ctx, `SELECT prefix,EXISTS(SELECT 1 FROM service_requests WHERE company_id=$1 AND profile_id=$2) FROM service_profiles WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&current, &used); errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			} else if err != nil {
				return nil, err
			}
			if prefix == current {
				delete(values, "prefix")
			} else if used {
				return nil, service.InvalidState("The prefix cannot change after the first record has been created.")
			}
		}
		if enabled, changes := values["out_store_enabled"].(bool); changes && !enabled {
			blocked, err := s.profileHasOpenDispatch(ctx, companyID, id)
			if err != nil {
				return nil, err
			}
			if blocked {
				return nil, service.InvalidState("Out-Store cannot be turned off while records are still at an Out-Store shop.")
			}
		}
		if enabled, changes := values["out_store_enabled"].(bool); changes && enabled {
			var ready bool
			if err := s.pool.QueryRow(ctx, `SELECT p.sent_status_id IS NOT NULL AND p.received_status_id IS NOT NULL AND EXISTS(SELECT 1 FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id=p.sent_status_id AND enabled) AND EXISTS(SELECT 1 FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id=p.received_status_id AND enabled) FROM service_profiles p WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&ready); errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			} else if err != nil {
				return nil, err
			}
			if !ready {
				return nil, service.InvalidState("Set both Out-Store statuses (sent and received back) before turning Out-Store on.")
			}
		}
		if active, changes := values["is_active"].(bool); changes && !active {
			blocked, err := s.profileHasOpenDispatch(ctx, companyID, id)
			if err != nil {
				return nil, err
			}
			if blocked {
				return nil, service.InvalidState("The profile cannot be archived while records are still at an Out-Store shop.")
			}
			var otherActive bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_profiles WHERE company_id=$1 AND id<>$2 AND is_active)`, companyID, id).Scan(&otherActive); err != nil {
				return nil, err
			}
			if !otherActive {
				return nil, service.InvalidState("At least one service profile must stay active.")
			}
		}
	}
	spec := catalogSpecs[resource]
	if resource == "service-profiles" && len(values) == 0 {
		// Only unchanged values were sent: nothing to update.
		return s.Get(ctx, resource, companyID, profileID, id)
	}
	keys := sortedAllowed(values, spec.update)
	if len(keys) == 0 || len(keys) != len(values) {
		return nil, fieldsError(values, spec.update)
	}
	args := []any{}
	sets := []string{}
	for _, key := range keys {
		args = append(args, values[key])
		if resource == "service-profiles" && key == "core_labels" {
			sets = append(sets, fmt.Sprintf("core_labels=core_labels||$%d::jsonb", len(args)))
		} else {
			sets = append(sets, fmt.Sprintf("%s=$%d", spec.update[key], len(args)))
		}
	}
	args = append(args, companyID, id)
	query := fmt.Sprintf("UPDATE %s t SET %s WHERE company_id=$%d AND id=$%d", spec.table, strings.Join(sets, ","), len(args)-1, len(args))
	if spec.profile {
		args = append(args, profileID)
		query += fmt.Sprintf(" AND profile_id=$%d", len(args))
	}
	query += " RETURNING " + spec.read
	return scanObject(s.pool.QueryRow(ctx, query, args...))
}

func validateCatalogValues(resource string, values map[string]any) error {
	if resource != "products" {
		return nil
	}
	if raw, supplied := values["status"]; supplied {
		status, ok := raw.(string)
		if !ok || (status != "ACTIVE" && status != "DISCONTINUED") {
			return service.FieldErrors{"status": "must be ACTIVE or DISCONTINUED"}
		}
	}
	return nil
}

func (s *CatalogStore) saveStaff(ctx context.Context, companyID, id string, values map[string]any, creating bool) (map[string]any, error) {
	spec := catalogSpecs["staff"]
	allowed := spec.update
	if creating {
		allowed = spec.create
	}
	keys := sortedAllowed(values, allowed)
	if len(keys) != len(values) {
		return nil, fieldsError(values, allowed)
	}
	var roleIDs []any
	if raw, supplied := values["role_ids"]; supplied {
		var ok bool
		roleIDs, ok = raw.([]any)
		if !ok {
			return nil, errors.New("role_ids must be an array")
		}
		delete(values, "role_ids")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if creating {
		keys = sortedAllowed(values, allowed)
		columns, placeholders, args := []string{"company_id"}, []string{"$1"}, []any{companyID}
		for _, key := range keys {
			columns = append(columns, allowed[key])
			args = append(args, values[key])
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		if err := tx.QueryRow(ctx, fmt.Sprintf("INSERT INTO staff(%s)VALUES(%s)RETURNING id::text", strings.Join(columns, ","), strings.Join(placeholders, ",")), args...).Scan(&id); err != nil {
			return nil, err
		}
	} else if len(values) > 0 {
		keys = sortedAllowed(values, allowed)
		args, sets := []any{}, []string{}
		for _, key := range keys {
			args = append(args, values[key])
			sets = append(sets, fmt.Sprintf("%s=$%d", allowed[key], len(args)))
		}
		args = append(args, companyID, id)
		result, err := tx.Exec(ctx, fmt.Sprintf("UPDATE staff SET %s WHERE company_id=$%d AND id=$%d", strings.Join(sets, ","), len(args)-1, len(args)), args...)
		if err != nil {
			return nil, err
		}
		if result.RowsAffected() != 1 {
			return nil, ErrNotFound
		}
	}
	if roleIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM staff_role_assignments WHERE company_id=$1 AND staff_id=$2`, companyID, id); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, raw := range roleIDs {
			roleID, ok := raw.(string)
			if !ok || seen[roleID] {
				return nil, errors.New("role_ids must contain distinct UUID strings")
			}
			seen[roleID] = true
			result, err := tx.Exec(ctx, `INSERT INTO staff_role_assignments(company_id,staff_id,role_id) SELECT $1,$2,id FROM staff_roles WHERE company_id=$1 AND id=$3`, companyID, id, roleID)
			if err != nil {
				return nil, err
			}
			if result.RowsAffected() != 1 {
				return nil, errors.New("role_ids contains an unknown role")
			}
		}
	}
	item, err := scanObject(tx.QueryRow(ctx, `SELECT `+spec.read+` FROM staff t WHERE company_id=$1 AND id=$2`, companyID, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *CatalogStore) updateUser(ctx context.Context, companyID, id string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs["users"]
	keys := sortedAllowed(values, spec.update)
	if len(keys) == 0 || len(keys) != len(values) {
		return nil, fieldsError(values, spec.update)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM companies WHERE id=$1 FOR UPDATE`, companyID); err != nil {
		return nil, err
	}
	var currentRole, currentStatus string
	if err := tx.QueryRow(ctx, `SELECT role,status FROM users WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&currentRole, &currentStatus); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	nextRole, nextStatus := currentRole, currentStatus
	if v, ok := values["role"].(string); ok {
		nextRole = v
	}
	if v, ok := values["status"].(string); ok {
		nextStatus = v
	}
	if currentRole == "ADMIN" && currentStatus == "ACTIVE" && (nextRole != "ADMIN" || nextStatus != "ACTIVE") {
		var others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE company_id=$1 AND id<>$2 AND role='ADMIN' AND status='ACTIVE'`, companyID, id).Scan(&others); err != nil {
			return nil, err
		}
		if others == 0 {
			return nil, ErrLastAdmin
		}
	}
	args := []any{}
	sets := []string{}
	for _, k := range keys {
		args = append(args, values[k])
		sets = append(sets, fmt.Sprintf("%s=$%d", spec.update[k], len(args)))
	}
	args = append(args, companyID, id)
	item, err := scanObject(tx.QueryRow(ctx, fmt.Sprintf("UPDATE users t SET %s WHERE company_id=$%d AND id=$%d RETURNING %s", strings.Join(sets, ","), len(args)-1, len(args), spec.read), args...))
	if err != nil {
		return nil, err
	}
	if _, password := values["password_hash"]; password || nextStatus != "ACTIVE" || nextRole != currentRole {
		if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *CatalogStore) createField(ctx context.Context, companyID, profileID string, values map[string]any) (map[string]any, error) {
	if err := service.ValidateFieldDefinition(values, true); err != nil {
		return nil, err
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2`, companyID, profileID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 200 {
		return nil, service.InvalidState("A profile can have at most 200 fields.")
	}
	field := service.DynamicField{Key: values["key"].(string), Type: values["type"].(string), Enabled: true, Config: values["config"].(map[string]any)}
	if required, ok := values["required"].(bool); ok {
		field.Required = required
	}
	if enabled, ok := values["enabled"].(bool); ok {
		field.Enabled = enabled
	}
	if err := s.validateFieldRole(ctx, companyID, field); err != nil {
		return nil, err
	}
	fields, err := s.catalogFields(ctx, companyID, profileID)
	if err != nil {
		return nil, err
	}
	fields = append(fields, field)
	if err := validateFieldGraph(fields); err != nil {
		return nil, err
	}
	return s.insert(ctx, catalogSpecs["fields"], companyID, profileID, values)
}

func (s *CatalogStore) updateField(ctx context.Context, companyID, profileID, id string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs["fields"]
	keys := sortedAllowed(values, spec.update)
	if len(keys) == 0 || len(keys) != len(values) {
		return nil, fieldsError(values, spec.update)
	}
	var key, label, kind string
	var required, enabled, system bool
	var sortOrder int
	var configRaw []byte
	if err := s.pool.QueryRow(ctx, `SELECT field_key,label,field_type,required,enabled,is_system,sort_order,config FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2 AND id=$3`, companyID, profileID, id).Scan(&key, &label, &kind, &required, &enabled, &system, &sortOrder, &configRaw); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	config := map[string]any{}
	_ = json.Unmarshal(configRaw, &config)
	effective := map[string]any{"key": key, "label": label, "type": kind, "required": required, "enabled": enabled, "sort_order": sortOrder, "config": config}
	for name, value := range values {
		effective[name] = value
	}
	if err := service.ValidateFieldDefinition(effective, false); err != nil {
		return nil, err
	}
	nextKind, kindOK := effective["type"].(string)
	nextEnabled, enabledOK := effective["enabled"].(bool)
	if !kindOK || !enabledOK {
		return nil, service.Invalid("type must be text and enabled must be true or false.")
	}
	if system && (nextKind != kind || !nextEnabled) {
		return nil, service.InvalidState("Built-in fields cannot change type or be hidden.")
	}
	if nextKind != kind {
		var used bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_requests WHERE company_id=$1 AND profile_id=$2)`, companyID, profileID).Scan(&used); err != nil {
			return nil, err
		}
		if used {
			return nil, service.InvalidState("The field type cannot change after the profile has records.")
		}
	}
	nextConfig, ok := effective["config"].(map[string]any)
	if !ok {
		return nil, service.FieldErrors{"config": "must be an object"}
	}
	field := service.DynamicField{Key: key, Type: nextKind, Required: effective["required"].(bool), Enabled: nextEnabled, IsSystem: system, Config: nextConfig}
	if err := s.validateFieldRole(ctx, companyID, field); err != nil {
		return nil, err
	}
	fields, err := s.catalogFields(ctx, companyID, profileID)
	if err != nil {
		return nil, err
	}
	for index := range fields {
		if fields[index].Key == key {
			fields[index] = field
		}
	}
	if err := validateFieldGraph(fields); err != nil {
		return nil, err
	}
	args, sets := []any{}, []string{}
	for _, name := range keys {
		args = append(args, values[name])
		sets = append(sets, fmt.Sprintf("%s=$%d", spec.update[name], len(args)))
	}
	args = append(args, companyID, id, profileID)
	return scanObject(s.pool.QueryRow(ctx, fmt.Sprintf("UPDATE service_profile_fields t SET %s WHERE company_id=$%d AND id=$%d AND profile_id=$%d RETURNING %s", strings.Join(sets, ","), len(args)-2, len(args)-1, len(args), spec.read), args...))
}

func (s *CatalogStore) catalogFields(ctx context.Context, companyID, profileID string) ([]service.DynamicField, error) {
	rows, err := s.pool.Query(ctx, `SELECT field_key,field_type,required,enabled,is_system,config FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2`, companyID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := []service.DynamicField{}
	for rows.Next() {
		var field service.DynamicField
		var raw []byte
		if err := rows.Scan(&field.Key, &field.Type, &field.Required, &field.Enabled, &field.IsSystem, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &field.Config)
		fields = append(fields, field)
	}
	return fields, rows.Err()
}

func (s *CatalogStore) validateFieldRole(ctx context.Context, companyID string, field service.DynamicField) error {
	if field.Type != "staff_role" {
		return nil
	}
	roleID, _ := field.Config["role_id"].(string)
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_roles WHERE company_id=$1 AND id=$2)`, companyID, roleID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func validateFieldGraph(fields []service.DynamicField) error {
	values := map[string]any{}
	for _, field := range fields {
		if field.Type == "number" {
			values[field.Key] = json.Number("0")
		}
	}
	if validation := service.ApplyFormulas(fields, values, map[string]any{}); len(validation) > 0 {
		return validation
	}
	return nil
}

func (s *CatalogStore) createStatus(ctx context.Context, companyID, profileID string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs["statuses"]
	keys := sortedAllowed(values, spec.create)
	if len(keys) != len(values) {
		return nil, fieldsError(values, spec.create)
	}
	initial, _ := values["initial"].(bool)
	closed, _ := values["closed"].(bool)
	enabled := true
	if raw, present := values["enabled"]; present {
		var ok bool
		enabled, ok = raw.(bool)
		if !ok {
			return nil, service.FieldErrors{"enabled": "must be true or false"}
		}
	}
	if initial && (!enabled || closed) {
		return nil, service.InvalidState("An initial status must be enabled and cannot be closed.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileExists int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM service_profiles WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, profileID).Scan(&profileExists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if initial {
		if _, err := tx.Exec(ctx, `UPDATE service_profile_statuses SET is_initial=false WHERE company_id=$1 AND profile_id=$2 AND is_initial`, companyID, profileID); err != nil {
			return nil, err
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2`, companyID, profileID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 100 {
		return nil, service.InvalidState("A profile can have at most 100 statuses.")
	}
	columns := []string{"company_id", "profile_id"}
	args := []any{companyID, profileID}
	placeholders := []string{"$1", "$2"}
	for _, key := range keys {
		columns = append(columns, spec.create[key])
		args = append(args, values[key])
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	item, err := scanObject(tx.QueryRow(ctx, fmt.Sprintf("INSERT INTO service_profile_statuses AS t(%s) VALUES(%s) RETURNING %s", strings.Join(columns, ","), strings.Join(placeholders, ","), spec.read), args...))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *CatalogStore) updateStatus(ctx context.Context, companyID, profileID, id string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs["statuses"]
	keys := sortedAllowed(values, spec.update)
	if len(keys) == 0 || len(keys) != len(values) {
		return nil, fieldsError(values, spec.update)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileExists int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM service_profiles WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, profileID).Scan(&profileExists); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var currentInitial, currentEnabled, currentClosed bool
	if err := tx.QueryRow(ctx, `SELECT is_initial,enabled,is_closed FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id=$3 FOR UPDATE`, companyID, profileID, id).Scan(&currentInitial, &currentEnabled, &currentClosed); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	nextInitial, nextEnabled, nextClosed := currentInitial, currentEnabled, currentClosed
	if raw, present := values["initial"]; present {
		value, ok := raw.(bool)
		if !ok {
			return nil, service.FieldErrors{"initial": "must be true or false"}
		}
		nextInitial = value
	}
	if raw, present := values["enabled"]; present {
		value, ok := raw.(bool)
		if !ok {
			return nil, service.FieldErrors{"enabled": "must be true or false"}
		}
		nextEnabled = value
	}
	if raw, present := values["closed"]; present {
		value, ok := raw.(bool)
		if !ok {
			return nil, service.FieldErrors{"closed": "must be true or false"}
		}
		nextClosed = value
	}
	if nextInitial && nextEnabled && nextClosed {
		return nil, service.InvalidState("An initial status cannot also be closed.")
	}
	if currentInitial && currentEnabled && (!nextInitial || !nextEnabled) {
		return nil, service.InvalidState("The profile needs an initial status. Mark another status as initial first.")
	}
	if currentEnabled && !nextEnabled {
		var mapped bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sent_status_id=$3 OR received_status_id=$3,false) FROM service_profiles WHERE company_id=$1 AND id=$2`, companyID, profileID, id).Scan(&mapped); err != nil {
			return nil, err
		}
		if mapped {
			return nil, service.InvalidState("This status is used by the Out-Store mapping. Change the mapping before disabling it.")
		}
	}
	if nextInitial && nextEnabled {
		if _, err := tx.Exec(ctx, `UPDATE service_profile_statuses SET is_initial=false WHERE company_id=$1 AND profile_id=$2 AND id<>$3 AND is_initial`, companyID, profileID, id); err != nil {
			return nil, err
		}
	}
	args, sets := []any{}, []string{}
	for _, key := range keys {
		args = append(args, values[key])
		sets = append(sets, fmt.Sprintf("%s=$%d", spec.update[key], len(args)))
	}
	args = append(args, companyID, id, profileID)
	item, err := scanObject(tx.QueryRow(ctx, fmt.Sprintf("UPDATE service_profile_statuses t SET %s WHERE company_id=$%d AND id=$%d AND profile_id=$%d RETURNING %s", strings.Join(sets, ","), len(args)-2, len(args)-1, len(args), spec.read), args...))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *CatalogStore) Disable(ctx context.Context, resource, companyID, profileID, id string) error {
	spec := catalogSpecs[resource]
	column := "enabled"
	if resource == "service-profiles" {
		blocked, err := s.profileHasOpenDispatch(ctx, companyID, id)
		if err != nil {
			return err
		}
		if blocked {
			return service.InvalidState("The profile cannot be archived while records are still at an Out-Store shop.")
		}
		var otherActive bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_profiles WHERE company_id=$1 AND id<>$2 AND is_active)`, companyID, id).Scan(&otherActive); err != nil {
			return err
		}
		if !otherActive {
			return service.InvalidState("At least one service profile must stay active.")
		}
		column = "is_active"
	}
	if resource == "statuses" {
		var initial bool
		if err := s.pool.QueryRow(ctx, `SELECT is_initial AND enabled FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id=$3`, companyID, profileID, id).Scan(&initial); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if initial {
			return service.InvalidState("The initial status cannot be disabled. Mark another status as initial first.")
		}
		var mapped bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_profiles WHERE company_id=$1 AND id=$2 AND (sent_status_id=$3 OR received_status_id=$3))`, companyID, profileID, id).Scan(&mapped); err != nil {
			return err
		}
		if mapped {
			return service.InvalidState("This status is used by the Out-Store mapping. Change the mapping before disabling it.")
		}
	}
	if resource == "fields" {
		var system bool
		if err := s.pool.QueryRow(ctx, `SELECT is_system FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2 AND id=$3`, companyID, profileID, id).Scan(&system); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if system {
			return service.InvalidState("Built-in fields cannot be removed.")
		}
	}
	query := fmt.Sprintf("UPDATE %s SET %s=false WHERE company_id=$1 AND id=$2", spec.table, column)
	args := []any{companyID, id}
	if spec.profile {
		query += " AND profile_id=$3"
		args = append(args, profileID)
	}
	if resource == "service-profiles" {
		query += " AND (SELECT count(*) FROM service_profiles WHERE company_id=$1 AND is_active)>1"
	}
	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *CatalogStore) createProfile(ctx context.Context, companyID string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs["service-profiles"]
	keys := sortedAllowed(values, spec.create)
	if len(keys) != len(values) {
		return nil, fieldsError(values, spec.create)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	columns, placeholders, args := []string{"company_id"}, []string{"$1"}, []any{companyID}
	for _, key := range keys {
		columns = append(columns, spec.create[key])
		args = append(args, values[key])
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	var id string
	if err := tx.QueryRow(ctx, fmt.Sprintf("INSERT INTO service_profiles(%s)VALUES(%s)RETURNING id::text", strings.Join(columns, ","), strings.Join(placeholders, ",")), args...).Scan(&id); err != nil {
		return nil, err
	}
	statuses := []string{"Pending", "Assigned", "In-Progress", "Completed", "Sent", "Delivered", "Returned Not Repaired"}
	for order, name := range statuses {
		closed := name == "Delivered" || name == "Returned Not Repaired"
		if _, err := tx.Exec(ctx, `INSERT INTO service_profile_statuses(company_id,profile_id,name,sort_order,is_initial,is_closed)VALUES($1,$2,$3,$4,$5,$6)`, companyID, id, name, order, name == "Pending", closed); err != nil {
			return nil, err
		}
	}
	item, err := scanObject(tx.QueryRow(ctx, `SELECT to_jsonb(p) FROM service_profiles p WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

// DeleteStatus removes a status that no record uses now or in its history
// (T32). Used, initial and Out-Store mapped statuses return 409 explaining why;
// those can be disabled instead.
func (s *CatalogStore) DeleteStatus(ctx context.Context, companyID, profileID, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var name string
	var initial, enabled, mapped bool
	err = tx.QueryRow(ctx, `SELECT st.name,st.is_initial,st.enabled,COALESCE(p.sent_status_id=st.id OR p.received_status_id=st.id,false) FROM service_profile_statuses st JOIN service_profiles p ON p.company_id=st.company_id AND p.id=st.profile_id WHERE st.company_id=$1 AND st.profile_id=$2 AND st.id=$3 FOR UPDATE OF st,p`, companyID, profileID, id).Scan(&name, &initial, &enabled, &mapped)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var current, past int
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM service_requests WHERE company_id=$1 AND status_id=$2),
		(SELECT count(DISTINCT request_id) FROM service_request_history WHERE company_id=$1 AND field_key='core.status' AND (old_value=to_jsonb($2::text) OR new_value=to_jsonb($2::text)))`, companyID, id).Scan(&current, &past); err != nil {
		return err
	}
	switch {
	case current > 0:
		return service.InvalidState(fmt.Sprintf("%q is the current status of %s, so it cannot be deleted. Disable it instead to stop using it for new changes.", name, records(current)))
	case past > 0:
		return service.InvalidState(fmt.Sprintf("%q appears in the history of %s, so it cannot be deleted. Disable it instead to stop using it for new changes.", name, records(past)))
	case initial && enabled:
		return service.InvalidState(fmt.Sprintf("%q is the initial status. Mark another status as initial before deleting it.", name))
	case mapped:
		return service.InvalidState(fmt.Sprintf("%q is used by the Out-Store mapping. Change the mapping before deleting it.", name))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id=$3`, companyID, profileID, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func records(n int) string {
	if n == 1 {
		return "1 record"
	}
	return fmt.Sprintf("%d records", n)
}

func (s *CatalogStore) DeleteRole(ctx context.Context, companyID, id string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM staff_roles WHERE company_id=$1 AND id=$2 AND is_system=false`, companyID, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *CatalogStore) createCustomer(ctx context.Context, companyID string, values map[string]any) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if contact, ok := values["contact"].(string); ok {
		if onlyDigits(contact) == "" {
			return nil, service.FieldErrors{"contact": "must be a mobile number"}
		}
		if owner, found, err := contactOwner(ctx, tx, companyID, contact, ""); err != nil {
			return nil, err
		} else if found {
			return nil, service.FieldErrors{"contact": "already belongs to " + owner}
		}
	}
	obj, err := insertCustomer(ctx, tx, companyID, values)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return obj, nil
}

// insertCustomer inserts the customer inside the caller's transaction.
// Customers are identified by mobile number (unique per company, T33).
func insertCustomer(ctx context.Context, tx pgx.Tx, companyID string, values map[string]any) (map[string]any, error) {
	spec := catalogSpecs["customers"]
	allowed := map[string]string{}
	for key, column := range spec.create {
		allowed[key] = column
	}
	keys := sortedAllowed(values, allowed)
	columns := []string{"company_id"}
	args := []any{companyID}
	ph := []string{"$1"}
	for _, k := range keys {
		columns = append(columns, allowed[k])
		args = append(args, values[k])
		ph = append(ph, fmt.Sprintf("$%d", len(args)))
	}
	return scanObject(tx.QueryRow(ctx, fmt.Sprintf("INSERT INTO customers(%s) VALUES(%s) RETURNING to_jsonb(customers)", strings.Join(columns, ","), strings.Join(ph, ",")), args...))
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// contactOwner names the company customer with the same mobile number,
// comparing digits only so "98765 43210" and "9876543210" match.
func contactOwner(ctx context.Context, q rowQuerier, companyID, contact, excludeID string) (string, bool, error) {
	digits := onlyDigits(contact)
	if digits == "" {
		return "", false, nil
	}
	var name string
	err := q.QueryRow(ctx, `SELECT name FROM customers WHERE company_id=$1 AND regexp_replace(contact,'[^0-9]','','g')=$2 AND ($3='' OR id::text<>$3) LIMIT 1`, companyID, digits, excludeID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return name, err == nil, err
}

func onlyDigits(value string) string {
	var digits strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	return digits.String()
}

// fieldsError names submitted fields that cannot be used, or reports that none were sent.
func fieldsError(values map[string]any, allowed map[string]string) error {
	fields := service.FieldErrors{}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			fields[key] = "cannot be set here"
		}
	}
	if len(fields) > 0 {
		return fields
	}
	return service.Invalid("Send at least one field to change.")
}

func sortedAllowed(values map[string]any, allowed map[string]string) []string {
	keys := []string{}
	for key := range values {
		if _, ok := allowed[key]; ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func scanObject(row interface{ Scan(...any) error }) (map[string]any, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *CatalogStore) ListCompanies(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT to_jsonb(c) FROM companies c ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *CatalogStore) ListServiceRecordProfileOptions(ctx context.Context, companyID string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT jsonb_build_object('id',id,'name',name,'prefix',prefix,'is_active',true) FROM service_profiles WHERE company_id=$1 AND is_active ORDER BY name,id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		item, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *CatalogStore) GetCompany(ctx context.Context, id string) (map[string]any, error) {
	return scanObject(s.pool.QueryRow(ctx, `SELECT to_jsonb(c) FROM companies c WHERE id=$1`, id))
}
func (s *CatalogStore) UpdateCompany(ctx context.Context, id string, values map[string]any) (map[string]any, error) {
	allowed := cols("name,email,contact,address,status")
	keys := sortedAllowed(values, allowed)
	if len(keys) == 0 || len(keys) != len(values) {
		return nil, fieldsError(values, allowed)
	}
	args := []any{}
	sets := []string{}
	for _, k := range keys {
		args = append(args, values[k])
		sets = append(sets, fmt.Sprintf("%s=$%d", k, len(args)))
	}
	args = append(args, id)
	return scanObject(s.pool.QueryRow(ctx, fmt.Sprintf("UPDATE companies c SET %s,updated_at=now() WHERE id=$%d RETURNING to_jsonb(c)", strings.Join(sets, ","), len(args)), args...))
}

type CompanyOnboarding struct {
	Name                                string
	Email, Contact, Address             any
	AdminName, AdminEmail, PasswordHash string
	AdminPhone                          *string
}

func (s *CatalogStore) OnboardCompany(ctx context.Context, in CompanyOnboarding) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var companyID string
	if err := tx.QueryRow(ctx, `INSERT INTO companies(name,email,contact,address)VALUES($1,$2,$3,$4)RETURNING id::text`, in.Name, in.Email, in.Contact, in.Address).Scan(&companyID); err != nil {
		return nil, err
	}
	admin, err := scanObject(tx.QueryRow(ctx, `INSERT INTO users AS t(company_id,name,email,phone,password_hash,role,must_change_password)VALUES($1,$2,$3,$4,$5,'ADMIN',true)RETURNING to_jsonb(t)-'password_hash'`, companyID, in.AdminName, in.AdminEmail, in.AdminPhone, in.PasswordHash))
	if err != nil {
		return nil, err
	}
	roles := map[string]string{}
	for _, name := range []string{"Attended By", "Service Engineer", "Delivered By"} {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO staff_roles(company_id,name,is_system)VALUES($1,$2,$3)RETURNING id::text`, companyID, name, name != "Delivered By").Scan(&id); err != nil {
			return nil, err
		}
		roles[name] = id
	}
	profiles := []struct {
		name, prefix string
		out          bool
		statuses     []string
		initial      string
	}{{"Job Card", "A", true, []string{"Pending", "Assigned", "In-Progress", "Completed", "Sent to Out-Store", "Delivered", "Returned Not Repaired"}, "Pending"}, {"Refill", "RF", false, []string{"Received", "In-Progress", "Completed", "Sent", "Delivered", "Returned Not Repaired"}, "Received"}}
	for _, p := range profiles {
		var pid string
		if err := tx.QueryRow(ctx, `INSERT INTO service_profiles(company_id,name,prefix,out_store_enabled)VALUES($1,$2,$3,$4)RETURNING id::text`, companyID, p.name, p.prefix, p.out).Scan(&pid); err != nil {
			return nil, err
		}
		statusIDs := map[string]string{}
		for i, n := range p.statuses {
			var sid string
			closed := n == "Delivered" || n == "Returned Not Repaired"
			if err := tx.QueryRow(ctx, `INSERT INTO service_profile_statuses(company_id,profile_id,name,sort_order,is_initial,is_closed)VALUES($1,$2,$3,$4,$5,$6)RETURNING id::text`, companyID, pid, n, i, n == p.initial, closed).Scan(&sid); err != nil {
				return nil, err
			}
			statusIDs[n] = sid
		}
		if p.out {
			_, err = tx.Exec(ctx, `UPDATE service_profiles SET sent_status_id=$2,received_status_id=$3 WHERE id=$1`, pid, statusIDs["Sent to Out-Store"], statusIDs["Completed"])
			if err != nil {
				return nil, err
			}
		}
		prefix := "jc_"
		if p.prefix == "RF" {
			prefix = "rf_"
		}
		for i, f := range defaultFields(prefix, roles) {
			if _, err := tx.Exec(ctx, `INSERT INTO service_profile_fields(company_id,profile_id,field_key,label,field_type,required,sort_order,config)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, companyID, pid, f[0], f[1], f[2], f[3] == "true", i, json.RawMessage(f[4])); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	company, err := s.GetCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	// CompanyOnboarded: the company and its first admin (callers add delivery).
	return map[string]any{"company": company, "admin": admin}, nil
}
func defaultFields(prefix string, roles map[string]string) [][]string {
	if prefix == "rf_" {
		return [][]string{{"rf_attendedby", "Attended By", "staff_role", "false", fmt.Sprintf(`{"role_id":%q}`, roles["Attended By"])}, {"rf_toner", "Toner Model", "text", "true", `{}`}, {"rf_engineer", "Assigned Engineer", "staff_role", "false", fmt.Sprintf(`{"role_id":%q}`, roles["Service Engineer"])}, {"rf_charges", "Charges / Refilling", "linked_charges", "false", `{}`}, {"rf_totalamount", "Total Amount", "number", "false", `{"currency":true}`}, {"rf_deliverydate", "Delivery Date", "date", "false", `{"quick_pick":true}`}, {"rf_deliveredby", "Delivered By", "staff_role", "false", fmt.Sprintf(`{"role_id":%q}`, roles["Delivered By"])}, {"rf_remarks", "Remarks", "text", "false", `{"multiline":true}`}}
	}
	return [][]string{{"jc_attendedby", "Attended By", "staff_role", "false", fmt.Sprintf(`{"role_id":%q}`, roles["Attended By"])}, {"jc_servicetype", "Service Type", "choice", "true", `{"options":["In-Person","In-Store"]}`}, {"jc_duedate", "Due Date", "date", "false", `{"quick_pick":true}`}, {"jc_product", "Product", "linked_product", "false", `{}`}, {"jc_serial", "Serial No", "text", "false", `{}`}, {"jc_complaint", "Complaint", "text", "true", `{"multiline":true}`}, {"jc_accessories", "Accessories", "text", "false", `{}`}, {"jc_address", "Address", "text", "false", `{}`}, {"jc_advance", "Advance Amount", "number", "false", `{"currency":true}`}, {"jc_totalamount", "Total Amount", "number", "false", `{"currency":true}`}, {"jc_balance", "Balance Due", "number", "false", `{"currency":true,"formula":{"a":"jc_totalamount","op":"-","b":"jc_advance"}}`}, {"jc_services", "Services Provided", "linked_charges", "false", `{}`}, {"jc_engineer", "Service Engineer", "staff_role", "false", fmt.Sprintf(`{"role_id":%q}`, roles["Service Engineer"])}, {"jc_deliverydate", "Delivery Date", "date", "false", `{"quick_pick":true}`}, {"jc_deliveredby", "Delivered By", "staff_role", "false", fmt.Sprintf(`{"role_id":%q}`, roles["Delivered By"])}, {"jc_reminder", "Reminder", "date", "false", `{"toggle_based":true}`}}
}

func (s *CatalogStore) SetOutStoreMapping(ctx context.Context, companyID, profileID string, sentID, receivedID *string) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT out_store_enabled FROM service_profiles WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, profileID).Scan(&enabled); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var blocked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM out_store_entries e JOIN service_requests r ON r.company_id=e.company_id AND r.id=e.service_request_id WHERE e.company_id=$1 AND r.profile_id=$2 AND e.status='SENT')`, companyID, profileID).Scan(&blocked); err != nil {
		return nil, err
	}
	if blocked {
		return nil, service.InvalidState("The Out-Store mapping cannot change while records are still at an Out-Store shop.")
	}
	if sentID == nil {
		if enabled {
			return nil, service.InvalidState("Turn Out-Store off before clearing the status mapping.")
		}
	} else {
		var validCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id IN ($3,$4) AND enabled`, companyID, profileID, *sentID, *receivedID).Scan(&validCount); err != nil {
			return nil, err
		}
		if validCount != 2 || *sentID == *receivedID {
			return nil, service.InvalidState("Choose two different enabled statuses of this profile for sent and received back.")
		}
	}
	item, err := scanObject(tx.QueryRow(ctx, `UPDATE service_profiles p SET sent_status_id=$3,received_status_id=$4 WHERE company_id=$1 AND id=$2 RETURNING to_jsonb(p)`, companyID, profileID, sentID, receivedID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return item, nil
}
func (s *CatalogStore) profileHasOpenDispatch(ctx context.Context, companyID, profileID string) (bool, error) {
	var blocked bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM out_store_entries e JOIN service_requests r ON r.company_id=e.company_id AND r.id=e.service_request_id WHERE e.company_id=$1 AND r.profile_id=$2 AND e.status='SENT')`, companyID, profileID).Scan(&blocked)
	return blocked, err
}
func (s *CatalogStore) GetForm(ctx context.Context, companyID, profileID string) (map[string]any, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT jsonb_build_object('id',p.id,'name',p.name,'prefix',p.prefix,'core_fields',jsonb_build_array(jsonb_build_object('key','service_date','label',p.core_labels->>'service_date','read_only',false),jsonb_build_object('key','customer_contact','label',p.core_labels->>'customer_contact','read_only',false),jsonb_build_object('key','customer_name','label',p.core_labels->>'customer_name','read_only',true)),'fields',COALESCE((SELECT jsonb_agg(to_jsonb(f)-'field_key'-'field_type'||jsonb_build_object('key',f.field_key,'type',f.field_type)ORDER BY f.sort_order,f.id)FROM service_profile_fields f WHERE f.company_id=p.company_id AND f.profile_id=p.id AND f.enabled),'[]'),'statuses',COALESCE((SELECT jsonb_agg(to_jsonb(s)-'is_initial'-'is_closed'||jsonb_build_object('initial',s.is_initial,'closed',s.is_closed)ORDER BY s.sort_order,s.id)FROM service_profile_statuses s WHERE s.company_id=p.company_id AND s.profile_id=p.id AND s.enabled),'[]'),'staff_roles',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id,'name',r.name,'is_system',r.is_system)ORDER BY r.name,r.id)FROM staff_roles r WHERE r.company_id=p.company_id),'[]'))FROM service_profiles p WHERE p.company_id=$1 AND p.id=$2`, companyID, profileID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}

// ResetUserPassword sets a new temporary password for a company user, forces a
// change at next sign-in and signs the user out everywhere (T28).
func (s *CatalogStore) ResetUserPassword(ctx context.Context, companyID, userID, passwordHash string) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := scanObject(tx.QueryRow(ctx, `UPDATE users t SET password_hash=$3,must_change_password=true WHERE company_id=$1 AND id=$2 RETURNING to_jsonb(t)-'password_hash'`, companyID, userID, passwordHash))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound("User not found in this company.")
	} else if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}
