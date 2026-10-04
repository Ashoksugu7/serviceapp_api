package repository

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"serviceops360/api/internal/service"
)

// ListQuery is a page of a company list with search, filters and sorting (T37).
type ListQuery struct {
	Page, PageSize int
	Q, Sort, Order string
	// Filters holds the documented query parameters (status, role, profile_id,
	// is_active, enabled) that were sent; unknown ones are rejected.
	Filters map[string]string
}

type listFilter struct {
	column string
	values []string // allowed values; nil for a UUID; {"true","false"} for booleans
	// orNull also returns rows that apply to every profile (shops with no profile).
	orNull bool
}

type listSpec struct {
	search  []string // columns matched with ILIKE
	digits  string   // column also matched by digits only (mobile numbers)
	sorts   map[string]string
	deflt   string
	filters map[string]listFilter
}

var (
	activeInactive = []string{"ACTIVE", "INACTIVE"}
	booleans       = []string{"true", "false"}
	byName         = map[string]string{"name": "lower(t.name)"}
	bySortOrder    = map[string]string{"sort_order": "t.sort_order"}
)

var listSpecs = map[string]listSpec{
	"users":       {search: []string{"t.name", "t.email", "t.phone"}, digits: "t.phone", sorts: byName, deflt: "name", filters: map[string]listFilter{"status": {column: "t.status", values: activeInactive}, "role": {column: "t.role", values: []string{"ADMIN", "USER"}}}},
	"customers":   {search: []string{"t.name", "t.contact", "t.email"}, digits: "t.contact", sorts: byName, deflt: "name"},
	"staff":       {search: []string{"t.name", "t.contact", "t.email", "t.specialization"}, digits: "t.contact", sorts: byName, deflt: "name", filters: map[string]listFilter{"status": {column: "t.status", values: activeInactive}}},
	"staff-roles": {search: []string{"t.name"}, sorts: byName, deflt: "name"},
	"products": {search: []string{"t.name", "t.brand", "t.category"}, sorts: byName, deflt: "name", filters: map[string]listFilter{
		"status": {column: "t.status", values: []string{"ACTIVE", "DISCONTINUED"}}, "profile_id": {column: "t.profile_id"}}},
	"charges": {search: []string{"t.name", "t.description"}, sorts: byName, deflt: "name", filters: map[string]listFilter{
		"status": {column: "t.status", values: activeInactive}, "profile_id": {column: "t.profile_id"}}},
	"out-store-shops": {search: []string{"t.shop_name", "t.contact_person", "t.contact", "t.address"}, digits: "t.contact", sorts: map[string]string{"shop_name": "lower(t.shop_name)"}, deflt: "shop_name", filters: map[string]listFilter{
		"status": {column: "t.status", values: activeInactive}, "profile_id": {column: "t.profile_id", orNull: true}}},
	"standby-items": {search: []string{"t.name", "t.serial_no", "t.category"}, sorts: byName, deflt: "name", filters: map[string]listFilter{
		"status": {column: "t.status", values: []string{"AVAILABLE", "ISSUED", "UNDER_MAINTENANCE"}}}},
	"service-profiles": {search: []string{"t.name", "t.prefix"}, sorts: byName, deflt: "name", filters: map[string]listFilter{"is_active": {column: "t.is_active", values: booleans}}},
	"fields":           {sorts: bySortOrder, deflt: "sort_order", filters: map[string]listFilter{"enabled": {column: "t.enabled", values: booleans}}},
	"statuses":         {sorts: bySortOrder, deflt: "sort_order", filters: map[string]listFilter{"enabled": {column: "t.enabled", values: booleans}}},
	"companies": {search: []string{"t.name", "t.email", "t.contact"}, sorts: map[string]string{"name": "lower(t.name)", "created_at": "t.created_at"}, deflt: "name", filters: map[string]listFilter{
		"status": {column: "t.status", values: []string{"ACTIVE", "SUSPENDED"}}}},
}

var uuidText = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// likeContains escapes LIKE wildcards so "50%" or "a_b" match literally.
func likeContains(text string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text) + "%"
}

// ValidateListQuery checks sort, order and filter values against the
// resource's documented parameters and returns field errors for bad ones.
func ValidateListQuery(resource string, q ListQuery) error {
	spec, ok := listSpecs[resource]
	if !ok {
		return fmt.Errorf("unknown list %q", resource)
	}
	errs := service.FieldErrors{}
	if q.Sort != "" && spec.sorts[q.Sort] == "" {
		errs["sort"] = "must be one of " + strings.Join(keys(spec.sorts), ", ")
	}
	if q.Order != "" && q.Order != "asc" && q.Order != "desc" {
		errs["order"] = "must be asc or desc"
	}
	if q.Q != "" && len(spec.search) == 0 {
		errs["q"] = "is not supported for this list"
	}
	for name, value := range q.Filters {
		filter, known := spec.filters[name]
		switch {
		case !known:
			errs[name] = "is not a filter for this list"
		case filter.values == nil && !uuidText.MatchString(value):
			errs[name] = "must be a valid ID"
		case filter.values != nil && !contains(filter.values, value):
			errs[name] = "must be one of " + strings.Join(filter.values, ", ")
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// ListPage returns one page of a company list and the total matching rows.
// companyID is empty for the companies list (SUPER_ADMIN only).
func (s *CatalogStore) ListPage(ctx context.Context, resource, companyID, profileID string, q ListQuery) ([]map[string]any, int, error) {
	if err := ValidateListQuery(resource, q); err != nil {
		return nil, 0, err
	}
	spec := listSpecs[resource]
	table, read := "companies", "to_jsonb(t)"
	if catalog, ok := catalogSpecs[resource]; ok {
		table, read = catalog.table, catalog.read
	}
	where, args := []string{"true"}, []any{}
	arg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if resource != "companies" {
		where = append(where, "t.company_id="+arg(companyID))
		if catalogSpecs[resource].profile {
			where = append(where, "t.profile_id="+arg(profileID))
		}
	}
	if text := strings.TrimSpace(q.Q); text != "" {
		pattern := arg(likeContains(text))
		parts := []string{}
		for _, column := range spec.search {
			parts = append(parts, column+" ILIKE "+pattern)
		}
		// "98765 43210" and "9876543210" find the same mobile number.
		if digits := onlyDigits(text); spec.digits != "" && len(digits) >= 3 {
			parts = append(parts, "regexp_replace("+spec.digits+",'[^0-9]','','g') LIKE "+arg("%"+digits+"%"))
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	for _, name := range keys(q.Filters) {
		filter := spec.filters[name]
		condition := filter.column + "=" + arg(q.Filters[name])
		if filter.orNull {
			condition = "(" + condition + " OR " + filter.column + " IS NULL)"
		}
		where = append(where, condition)
	}
	clause := " FROM " + table + " t WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*)"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	orderBy := spec.sorts[q.Sort]
	if orderBy == "" {
		orderBy = spec.sorts[spec.deflt]
	}
	direction := "ASC"
	if q.Order == "desc" {
		direction = "DESC"
	}
	limit, offset := arg(q.PageSize), arg((q.Page-1)*q.PageSize)
	rows, err := s.pool.Query(ctx, "SELECT "+read+clause+fmt.Sprintf(" ORDER BY %s %s,t.id %s LIMIT %s OFFSET %s", orderBy, direction, direction, limit, offset), args...)
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

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
