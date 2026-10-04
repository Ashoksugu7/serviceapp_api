package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"serviceops360/api/internal/service"
)

type InlineOutStore struct {
	ShopID  string
	DueDate *string
	Price   *string
	Remarks *string
}

// NewCustomer is created together with a service request when the caller
// has no existing customer to select (T01 inline customer).
type NewCustomer struct {
	Name, Contact  string
	Email, Address *string
}

type ServiceRequestInput struct {
	CompanyID, ProfileID, ServiceDate, CustomerID, CreatedBy string
	FormData                                                 map[string]any
	OutStore                                                 *InlineOutStore
	Customer                                                 *NewCustomer
}

// validateNewCustomer checks an inline customer and rejects a mobile number
// that already belongs to a customer, so the caller selects that customer instead.
func validateNewCustomer(ctx context.Context, tx pgx.Tx, companyID string, customer NewCustomer) (map[string]any, map[string]string, error) {
	errs := map[string]string{}
	name, contact := strings.TrimSpace(customer.Name), strings.TrimSpace(customer.Contact)
	if name == "" || utf8.RuneCountInString(name) > 200 {
		errs["customer.name"] = "is required and can be at most 200 characters"
	}
	if contact == "" || utf8.RuneCountInString(contact) > 50 || onlyDigits(contact) == "" {
		errs["customer.contact"] = "must be a phone number of at most 50 characters"
	} else if owner, found, err := contactOwner(ctx, tx, companyID, contact, ""); err != nil {
		return nil, nil, err
	} else if found {
		errs["customer.contact"] = "already belongs to " + owner + "; select that customer instead"
	}
	values := map[string]any{"name": name, "contact": contact}
	if customer.Email != nil && strings.TrimSpace(*customer.Email) != "" {
		if email, err := service.NormalizeEmail(*customer.Email); err != nil {
			errs["customer.email"] = "must be a valid email address"
		} else {
			values["email"] = email
		}
	}
	if customer.Address != nil && strings.TrimSpace(*customer.Address) != "" {
		if utf8.RuneCountInString(*customer.Address) > 4000 {
			errs["customer.address"] = "can be at most 4000 characters"
		}
		values["address"] = strings.TrimSpace(*customer.Address)
	}
	return values, errs, nil
}

func (s *CatalogStore) CreateServiceRequest(ctx context.Context, input ServiceRequestInput) (map[string]any, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var prefix string
	var nextNumber int64
	var outStoreEnabled bool
	var sentStatusID *string
	if err := tx.QueryRow(ctx, `SELECT prefix,next_number,out_store_enabled,sent_status_id::text FROM service_profiles WHERE company_id=$1 AND id=$2 AND is_active FOR UPDATE`, input.CompanyID, input.ProfileID).Scan(&prefix, &nextNumber, &outStoreEnabled, &sentStatusID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var initialStatusID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND enabled AND is_initial`, input.CompanyID, input.ProfileID).Scan(&initialStatusID); errors.Is(err, pgx.ErrNoRows) {
		return nil, service.InvalidState("The profile has no enabled initial status. Ask an admin to set one in Service Profiles.")
	} else if err != nil {
		return nil, err
	}
	var newCustomer map[string]any
	customerErrors := map[string]string{}
	switch {
	case input.Customer != nil && input.CustomerID != "":
		return nil, service.FieldErrors{"customer_id": "send customer_id or customer, not both"}
	case input.Customer != nil:
		values, errs, err := validateNewCustomer(ctx, tx, input.CompanyID, *input.Customer)
		if err != nil {
			return nil, err
		}
		newCustomer, customerErrors = values, errs
	case input.CustomerID == "":
		return nil, service.FieldErrors{"customer_id": "select a customer or enter a new one"}
	default:
		var customerExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customers WHERE company_id=$1 AND id::text=$2)`, input.CompanyID, input.CustomerID).Scan(&customerExists); err != nil {
			return nil, err
		}
		if !customerExists {
			// Tenant rule (T01): IDs outside the authorized company are 404.
			return nil, notFound("Customer not found in this company.")
		}
	}
	fields, err := loadDynamicFields(ctx, tx, input.CompanyID, input.ProfileID)
	if err != nil {
		return nil, err
	}
	fieldErrors := service.ApplyFormulas(fields, input.FormData, input.FormData)
	for key, message := range service.ValidateFormData(fields, input.FormData) {
		fieldErrors[key] = message
	}
	if len(fieldErrors) == 0 {
		fieldErrors = validateLinkedValues(ctx, tx, input.CompanyID, input.ProfileID, fields, input.FormData)
	}
	// Report customer and form problems together.
	for key, message := range customerErrors {
		fieldErrors[key] = message
	}
	if len(fieldErrors) > 0 {
		return nil, service.FieldErrors(fieldErrors)
	}
	statusID := initialStatusID
	if input.OutStore != nil {
		if !outStoreEnabled || sentStatusID == nil {
			return nil, service.InvalidState("Out-Store is not set up for this profile.")
		}
		var shopOK bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM out_store_shops WHERE company_id=$1 AND id=$2 AND status='ACTIVE' AND (profile_id IS NULL OR profile_id=$3))`, input.CompanyID, input.OutStore.ShopID, input.ProfileID).Scan(&shopOK); err != nil {
			return nil, err
		}
		if !shopOK {
			return nil, ErrNotFound
		}
		statusID = *sentStatusID
	}
	var createdCustomer map[string]any
	if newCustomer != nil {
		created, err := insertCustomer(ctx, tx, input.CompanyID, newCustomer)
		if err != nil {
			return nil, err
		}
		createdCustomer = created
		input.CustomerID = created["id"].(string)
	}
	requestNo := fmt.Sprintf("%s%d", prefix, nextNumber)
	if _, err := tx.Exec(ctx, `UPDATE service_profiles SET next_number=next_number+1 WHERE company_id=$1 AND id=$2`, input.CompanyID, input.ProfileID); err != nil {
		return nil, err
	}
	formJSON, err := json.Marshal(input.FormData)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `INSERT INTO service_requests(company_id,profile_id,request_no,service_date,customer_id,status_id,form_data,created_by)VALUES($1,$2,$3,$4,$5,$6,$7,$8)RETURNING to_jsonb(service_requests)`, input.CompanyID, input.ProfileID, requestNo, input.ServiceDate, input.CustomerID, statusID, formJSON, input.CreatedBy).Scan(&raw); err != nil {
		return nil, err
	}
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	var requestID string = request["id"].(string)
	if _, err := tx.Exec(ctx, `INSERT INTO service_request_history(company_id,request_id,field_key,new_value,changed_by)VALUES($1,$2,'core.status',to_jsonb($3::text),$4)`, input.CompanyID, requestID, initialStatusID, input.CreatedBy); err != nil {
		return nil, err
	}
	request["out_store_entry"] = nil
	if input.OutStore != nil {
		if initialStatusID != statusID {
			if _, err := tx.Exec(ctx, `INSERT INTO service_request_history(company_id,request_id,field_key,old_value,new_value,changed_by)VALUES($1,$2,'core.status',to_jsonb($3::text),to_jsonb($4::text),$5)`, input.CompanyID, requestID, initialStatusID, statusID, input.CreatedBy); err != nil {
				return nil, err
			}
		}
		var outRaw []byte
		if err := tx.QueryRow(ctx, `INSERT INTO out_store_entries(company_id,service_request_id,shop_id,sent_date,due_date,price,remarks)VALUES($1,$2,$3,$4,$5,$6,$7)RETURNING to_jsonb(out_store_entries)||jsonb_build_object('price',price::text)`, input.CompanyID, requestID, input.OutStore.ShopID, input.ServiceDate, input.OutStore.DueDate, input.OutStore.Price, input.OutStore.Remarks).Scan(&outRaw); err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(outRaw, &out); err != nil {
			return nil, err
		}
		if err := recordOutStore(ctx, tx, input.CompanyID, out["id"].(string), "sent", input.CreatedBy); err != nil {
			return nil, err
		}
		request["out_store_entry"] = out
	}
	var statusName string
	if err := tx.QueryRow(ctx, `SELECT name FROM service_profile_statuses WHERE company_id=$1 AND id=$2`, input.CompanyID, statusID).Scan(&statusName); err != nil {
		return nil, err
	}
	request["status_name"] = statusName
	// The customer created with this request, or null when an existing one was used.
	request["customer"] = createdCustomer
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return request, nil
}

func loadDynamicFields(ctx context.Context, tx pgx.Tx, companyID, profileID string) ([]service.DynamicField, error) {
	rows, err := tx.Query(ctx, `SELECT field_key,field_type,required,enabled,is_system,config FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2`, companyID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := []service.DynamicField{}
	for rows.Next() {
		var field service.DynamicField
		var config []byte
		if err := rows.Scan(&field.Key, &field.Type, &field.Required, &field.Enabled, &field.IsSystem, &config); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(config, &field.Config); err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, rows.Err()
}
func validateLinkedValues(ctx context.Context, tx pgx.Tx, companyID, profileID string, fields []service.DynamicField, values map[string]any) map[string]string {
	result := map[string]string{}
	for _, field := range fields {
		value, ok := values[field.Key]
		if !ok || value == nil {
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
		for _, id := range ids {
			var valid bool
			switch field.Type {
			case "linked_product":
				_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE company_id=$1 AND profile_id=$2 AND id=$3 AND status='ACTIVE')`, companyID, profileID, id).Scan(&valid)
			case "linked_charges":
				_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM charges WHERE company_id=$1 AND profile_id=$2 AND id=$3 AND status='ACTIVE')`, companyID, profileID, id).Scan(&valid)
			case "staff_role":
				roleID, _ := field.Config["role_id"].(string)
				_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff s JOIN staff_role_assignments a ON a.company_id=s.company_id AND a.staff_id=s.id WHERE s.company_id=$1 AND s.id=$2 AND s.status='ACTIVE' AND a.role_id=$3)`, companyID, id, roleID).Scan(&valid)
			default:
				valid = true
			}
			if !valid {
				result[field.Key] = "reference is not eligible"
			}
		}
	}
	return result
}
