package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

func registerManagementRoutes(m *http.ServeMux, auth *service.AuthService, store *repository.CatalogStore, accounts AccountMail, logger *slog.Logger) {
	m.HandleFunc("/dashboard", authenticated(auth, logger, dashboardHandler(store, logger)))
	m.HandleFunc("/companies", authenticated(auth, logger, companiesHandler(auth, store, accounts, logger)))
	m.HandleFunc("/companies/{companyId}/users/{id}/reset-password", authenticated(auth, logger, resetPasswordHandler(auth, store, accounts, logger)))
	m.HandleFunc("/companies/{companyId}", authenticated(auth, logger, companyHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/service-record-options", authenticated(auth, logger, serviceRecordOptionsHandler(auth, store, logger)))
	resources := []string{"users", "customers", "staff", "staff-roles", "products", "charges", "out-store-shops", "standby-items", "service-profiles"}
	for _, resource := range resources {
		r := resource
		m.HandleFunc("/companies/{companyId}/"+r, authenticated(auth, logger, collectionHandler(auth, store, accounts, logger, r)))
		m.HandleFunc("/companies/{companyId}/"+r+"/{id}", authenticated(auth, logger, itemHandler(auth, store, logger, r)))
	}
	for _, resource := range []string{"fields", "statuses"} {
		r := resource
		m.HandleFunc("/companies/{companyId}/service-profiles/{profileId}/"+r, authenticated(auth, logger, nestedCollectionHandler(auth, store, logger, r)))
		m.HandleFunc("/companies/{companyId}/service-profiles/{profileId}/"+r+"/{id}", authenticated(auth, logger, nestedItemHandler(auth, store, logger, r)))
	}
	m.HandleFunc("/companies/{companyId}/service-profiles/{profileId}/out-store-mapping", authenticated(auth, logger, mappingHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/service-profiles/{profileId}/form", authenticated(auth, logger, formHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/service-requests", authenticated(auth, logger, serviceRequestCollectionHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/service-requests/{id}", authenticated(auth, logger, serviceRequestItemHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/service-requests/{id}/status", authenticated(auth, logger, serviceRequestStatusHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/service-requests/{id}/history", authenticated(auth, logger, serviceRequestHistoryHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/out-store-entries", authenticated(auth, logger, outStoreCollectionHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/out-store-entries/{id}", authenticated(auth, logger, outStoreItemHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/out-store-entries/{id}/receive-back", authenticated(auth, logger, outStoreReceiveHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/standby-items/{id}/issue", authenticated(auth, logger, standbyIssueHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/standby-items/{id}/return", authenticated(auth, logger, standbyReturnHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/standby-items/{id}/issues", authenticated(auth, logger, standbyIssueListHandler(auth, store, logger)))
	m.HandleFunc("/companies/{companyId}/standby-issues", authenticated(auth, logger, standbyIssueHistoryHandler(auth, store, logger)))
}

// serviceRecordOptionsHandler exposes only the active profile identity needed
// to start a service record. Profile configuration remains administrator-only.
func serviceRecordOptionsHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodGet {
			methodError(w, "GET")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		items, err := store.ListServiceRecordProfileOptions(r.Context(), companyID)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeList(w, items)
	}
}

func dashboardHandler(store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodGet {
			methodError(w, "GET")
			return
		}
		companyID := ""
		if p.Identity.User.Role != "SUPER_ADMIN" {
			if p.Identity.Company == nil {
				writeAuthError(w, service.ErrForbidden, 0, logger)
				return
			}
			companyID = p.Identity.Company.ID
		}
		dashboard, err := store.Dashboard(r.Context(), companyID)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, dashboard)
	}
}

func companiesHandler(auth *service.AuthService, store *repository.CatalogStore, accounts AccountMail, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if p.Identity.User.Role != "SUPER_ADMIN" {
			writeAuthError(w, service.ErrForbidden, 0, logger)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writePage(w, r, logger, func(q repository.ListQuery) ([]map[string]any, int, error) {
				return store.ListPage(r.Context(), "companies", "", "", q)
			})
		case http.MethodPost:
			var body struct {
				Name    string `json:"name"`
				Email   any    `json:"email"`
				Contact any    `json:"contact"`
				Address any    `json:"address"`
				Admin   struct {
					Name     string  `json:"name"`
					Email    string  `json:"email"`
					Phone    *string `json:"phone"`
					Password string  `json:"password"`
				} `json:"admin"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			email, err := service.NormalizeEmail(body.Admin.Email)
			if err != nil {
				writeManagementError(w, service.FieldErrors{"admin.email": "must be a valid email address"}, logger)
				return
			}
			var adminPhone *string
			if body.Admin.Phone != nil && strings.TrimSpace(*body.Admin.Phone) != "" {
				phone, err := service.NormalizePhone(*body.Admin.Phone)
				if err != nil {
					writeManagementError(w, service.FieldErrors{"admin.phone": "must be a mobile number with 6 to 20 digits"}, logger)
					return
				}
				adminPhone = &phone
			}
			if body.Admin.Password != "" {
				writeManagementError(w, service.FieldErrors{"admin.password": "is generated by the server; leave it out"}, logger)
				return
			}
			temporary, hash, err := temporaryCredentials()
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			item, err := store.OnboardCompany(r.Context(), repository.CompanyOnboarding{Name: body.Name, Email: body.Email, Contact: body.Contact, Address: body.Address, AdminName: body.Admin.Name, AdminEmail: email, AdminPhone: adminPhone, PasswordHash: hash})
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			item["delivery"] = accounts.deliver(r.Context(), logger, credentialEmail{
				name: body.Admin.Name, email: email, phone: adminPhone, company: body.Name, temporaryPassword: temporary,
			})
			writeJSON(w, http.StatusCreated, item)
		default:
			methodError(w, "GET, POST")
		}
	}
}
func companyHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		cid := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, cid, "SUPER_ADMIN", "ADMIN"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		switch r.Method {
		case http.MethodGet:
			item, err := store.GetCompany(r.Context(), cid)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
		case http.MethodPatch:
			var v map[string]any
			if !decodeJSON(w, r, &v) {
				return
			}
			if _, changesStatus := v["status"]; changesStatus && p.Identity.User.Role == "ADMIN" {
				writeAuthError(w, service.ErrForbidden, 0, logger)
				return
			}
			item, err := store.UpdateCompany(r.Context(), cid, v)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
		default:
			methodError(w, "GET, PATCH")
		}
	}
}

func collectionHandler(auth *service.AuthService, store *repository.CatalogStore, accounts AccountMail, logger *slog.Logger, resource string) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		cid := r.PathValue("companyId")
		readRoles, writeRoles := roles(resource)
		allowed := readRoles
		if r.Method == http.MethodPost {
			allowed = writeRoles
		}
		if err := auth.AuthorizeCompany(p, cid, allowed...); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writePage(w, r, logger, func(q repository.ListQuery) ([]map[string]any, int, error) {
				return store.ListPage(r.Context(), resource, cid, "", q)
			})
		case http.MethodPost:
			var v map[string]any
			if !decodeJSON(w, r, &v) {
				return
			}
			var temporary string
			if resource == "users" {
				// New users get a server-generated temporary password (T28).
				if _, sent := v["password"]; sent {
					writeManagementError(w, service.FieldErrors{"password": "is generated by the server; leave it out"}, logger)
					return
				}
				generated, err := service.GenerateTemporaryPassword()
				if err != nil {
					writeManagementError(w, err, logger)
					return
				}
				temporary, v["password"], v["must_change_password"] = generated, generated, true
			}
			if err := prepareCredentials(v, resource == "users"); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			item, err := store.Create(r.Context(), resource, cid, "", v)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			if resource == "users" {
				item["delivery"] = accounts.deliver(r.Context(), logger, userCredentialEmail(r.Context(), store, cid, item, temporary, false))
			}
			writeJSON(w, 201, item)
		default:
			methodError(w, "GET, POST")
		}
	}
}
func itemHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger, resource string) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		cid := r.PathValue("companyId")
		read, write := roles(resource)
		allowed := write
		if r.Method == http.MethodGet {
			allowed = read
		}
		if err := auth.AuthorizeCompany(p, cid, allowed...); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		id := r.PathValue("id")
		switch r.Method {
		case http.MethodGet:
			if resource != "customers" && resource != "staff" {
				methodError(w, "PATCH")
				return
			}
			item, err := store.Get(r.Context(), resource, cid, "", id)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
		case http.MethodPatch:
			var v map[string]any
			if !decodeJSON(w, r, &v) {
				return
			}
			if _, sent := v["password"]; sent && resource == "users" {
				writeManagementError(w, service.FieldErrors{"password": "cannot be set here; use reset password"}, logger)
				return
			}
			if err := prepareCredentials(v, resource == "users"); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			item, err := store.Update(r.Context(), resource, cid, "", id, v)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
		case http.MethodDelete:
			if resource != "staff-roles" && resource != "service-profiles" {
				methodError(w, "PATCH")
				return
			}
			var err error
			if resource == "staff-roles" {
				err = store.DeleteRole(r.Context(), cid, id)
			} else {
				err = store.Disable(r.Context(), resource, cid, "", id)
			}
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			w.WriteHeader(204)
		default:
			methodError(w, "GET, PATCH, DELETE")
		}
	}
}
func nestedCollectionHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger, resource string) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		cid, pid := r.PathValue("companyId"), r.PathValue("profileId")
		roles := []string{"SUPER_ADMIN", "ADMIN"}
		if err := auth.AuthorizeCompany(p, cid, roles...); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		if r.Method == http.MethodGet {
			if _, err := store.Get(r.Context(), "service-profiles", cid, "", pid); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writePage(w, r, logger, func(q repository.ListQuery) ([]map[string]any, int, error) {
				return store.ListPage(r.Context(), resource, cid, pid, q)
			})
			return
		}
		if r.Method == http.MethodPost {
			var v map[string]any
			if !decodeJSON(w, r, &v) {
				return
			}
			if resource == "fields" {
				if err := service.ValidateFieldDefinition(v, true); err != nil {
					writeManagementError(w, err, logger)
					return
				}
			}
			item, err := store.Create(r.Context(), resource, cid, pid, v)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 201, item)
			return
		}
		methodError(w, "GET, POST")
	}
}
func nestedItemHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger, resource string) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		cid, pid, id := r.PathValue("companyId"), r.PathValue("profileId"), r.PathValue("id")
		if err := auth.AuthorizeCompany(p, cid, "SUPER_ADMIN", "ADMIN"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		if r.Method == http.MethodPatch {
			var v map[string]any
			if !decodeJSON(w, r, &v) {
				return
			}
			if resource == "fields" {
				if err := service.ValidateFieldDefinition(v, false); err != nil {
					writeManagementError(w, err, logger)
					return
				}
			}
			item, err := store.Update(r.Context(), resource, cid, pid, id, v)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
			return
		}
		if r.Method == http.MethodDelete {
			// Statuses are deleted when unused (T32); fields are disabled.
			remove := store.Disable
			if resource == "statuses" {
				remove = func(ctx context.Context, _ string, cid, pid, id string) error {
					return store.DeleteStatus(ctx, cid, pid, id)
				}
			}
			if err := remove(r.Context(), resource, cid, pid, id); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			w.WriteHeader(204)
			return
		}
		methodError(w, "PATCH, DELETE")
	}
}
func mappingHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodPut {
			methodError(w, "PUT")
			return
		}
		cid := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, cid, "SUPER_ADMIN", "ADMIN"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		var v map[string]any
		if !decodeJSON(w, r, &v) {
			return
		}
		sent, sentSet, err := optionalString(v, "sent_status_id", true)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		received, receivedSet, err := optionalString(v, "received_status_id", true)
		if err != nil || !sentSet || !receivedSet || (sent == nil) != (received == nil) || len(v) != 2 {
			writeManagementError(w, service.Invalid("Send sent_status_id and received_status_id together: two status IDs, or both null."), logger)
			return
		}
		item, err := store.SetOutStoreMapping(r.Context(), cid, r.PathValue("profileId"), sent, received)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, 200, item)
	}
}
func formHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodError(w, "GET, HEAD")
			return
		}
		cid := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, cid, "SUPER_ADMIN", "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		item, err := store.GetForm(r.Context(), cid, r.PathValue("profileId"))
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, 200, item)
	}
}

func serviceRequestCollectionHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	type inlineOutStore struct {
		ShopID  string  `json:"shop_id"`
		DueDate *string `json:"due_date"`
		Price   *string `json:"price"`
		Remarks *string `json:"remarks"`
	}
	type inlineCustomer struct {
		Name    string  `json:"name"`
		Contact string  `json:"contact"`
		Email   *string `json:"email"`
		Address *string `json:"address"`
	}
	type createRequest struct {
		ProfileID   string          `json:"profile_id"`
		ServiceDate string          `json:"service_date"`
		CustomerID  string          `json:"customer_id"`
		Customer    *inlineCustomer `json:"customer"`
		FormData    map[string]any  `json:"form_data"`
		OutStore    *inlineOutStore `json:"out_store"`
	}
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		if r.Method == http.MethodGet {
			filter, err := parseRequestFilter(r)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			withFacets := r.URL.Query().Get("facets") == "true"
			items, total, facets, err := store.ListServiceRequestsWithFacets(r.Context(), companyID, filter, withFacets)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			response := map[string]any{"items": items, "page": filter.Page, "page_size": filter.PageSize, "total": total}
			if facets != nil {
				response["facets"] = facets
			}
			writeJSON(w, 200, response)
			return
		}
		if r.Method != http.MethodPost {
			methodError(w, "GET, POST")
			return
		}
		var body createRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		if _, err := time.Parse("2006-01-02", body.ServiceDate); err != nil {
			writeFieldError(w, 400, "VALIDATION_ERROR", "Invalid service request", map[string]string{"service_date": "must use YYYY-MM-DD"})
			return
		}
		input := repository.ServiceRequestInput{CompanyID: companyID, ProfileID: body.ProfileID, ServiceDate: body.ServiceDate, CustomerID: body.CustomerID, CreatedBy: p.Identity.User.ID, FormData: body.FormData}
		if body.Customer != nil {
			input.Customer = &repository.NewCustomer{Name: body.Customer.Name, Contact: body.Customer.Contact, Email: body.Customer.Email, Address: body.Customer.Address}
		}
		if body.OutStore != nil {
			input.OutStore = &repository.InlineOutStore{ShopID: body.OutStore.ShopID, DueDate: body.OutStore.DueDate, Price: body.OutStore.Price, Remarks: body.OutStore.Remarks}
		}
		item, err := store.CreateServiceRequest(r.Context(), input)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func serviceRequestItemHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		companyID, id := r.PathValue("companyId"), r.PathValue("id")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		if r.Method == http.MethodGet {
			item, err := store.GetServiceRequest(r.Context(), companyID, id)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
			return
		}
		if r.Method == http.MethodPatch {
			var body struct {
				ServiceDate *string         `json:"service_date"`
				CustomerID  *string         `json:"customer_id"`
				FormData    *map[string]any `json:"form_data"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			if body.ServiceDate == nil && body.CustomerID == nil && body.FormData == nil {
				writeError(w, 400, "VALIDATION_ERROR", "At least one field is required")
				return
			}
			if body.ServiceDate != nil {
				if _, err := time.Parse("2006-01-02", *body.ServiceDate); err != nil {
					writeFieldError(w, 400, "VALIDATION_ERROR", "Invalid request", map[string]string{"service_date": "must use YYYY-MM-DD"})
					return
				}
			}
			patch := repository.RequestPatch{ServiceDate: body.ServiceDate, CustomerID: body.CustomerID}
			if body.FormData != nil {
				patch.FormData = *body.FormData
				patch.FormDataSet = true
			}
			item, err := store.UpdateServiceRequest(r.Context(), companyID, id, p.Identity.User.ID, p.Identity.User.Role, patch)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
			return
		}
		methodError(w, "GET, PATCH")
	}
}
func serviceRequestStatusHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodPatch {
			methodError(w, "PATCH")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		var body struct {
			StatusID string `json:"status_id"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		item, err := store.ChangeServiceRequestStatus(r.Context(), companyID, r.PathValue("id"), body.StatusID, p.Identity.User.ID, p.Identity.User.Role)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, 200, item)
	}
}
func serviceRequestHistoryHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodError(w, "GET, HEAD")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		page, pageSize, err := parsePage(r)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		order := r.URL.Query().Get("order")
		if order == "" {
			order = "asc"
		}
		if order != "asc" && order != "desc" {
			writeManagementError(w, service.Invalid("order must be asc or desc."), logger)
			return
		}
		items, total, err := store.ListRequestHistory(r.Context(), companyID, r.PathValue("id"), page, pageSize, order)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "page": page, "page_size": pageSize, "total": total})
	}
}
func parseRequestFilter(r *http.Request) (repository.RequestListFilter, error) {
	page, size, err := parsePage(r)
	if err != nil {
		return repository.RequestListFilter{}, err
	}
	q := r.URL.Query()
	f := repository.RequestListFilter{Page: page, PageSize: size, Sort: q.Get("sort"), Order: q.Get("order"), Q: q.Get("q"), RequestNo: q.Get("request_no"), CustomerID: q.Get("customer_id"), ProfileID: q.Get("profile_id"), StatusID: q.Get("status_id"), DateFrom: q.Get("date_from"), DateTo: q.Get("date_to"),
		Contact: q.Get("contact"), State: q.Get("state"), StatusName: q.Get("status_name"), CreatedBy: q.Get("created_by"), OutStore: q.Get("out_store")}
	for _, id := range []struct{ name, value string }{{"customer_id", f.CustomerID}, {"profile_id", f.ProfileID}, {"status_id", f.StatusID}, {"created_by", f.CreatedBy}} {
		if id.value != "" && !uuidPattern.MatchString(id.value) {
			return f, service.Invalid(id.name + " must be a UUID.")
		}
	}
	if f.State != "" && f.State != "open" && f.State != "closed" {
		return f, service.Invalid("state must be open or closed.")
	}
	if f.OutStore != "" && f.OutStore != "at_shop" && f.OutStore != "received" && f.OutStore != "none" {
		return f, service.Invalid("out_store must be at_shop, received or none.")
	}
	if raw := q.Get("overdue"); raw != "" {
		overdue, err := strconv.ParseBool(raw)
		if err != nil {
			return f, service.Invalid("overdue must be true or false.")
		}
		f.Overdue = overdue
	}
	if len(f.Contact) > 50 || len(f.StatusName) > 200 {
		return f, service.Invalid("contact or status_name is too long.")
	}
	if f.Sort == "" {
		f.Sort = "created_at"
	}
	if f.Order == "" {
		f.Order = "desc"
	}
	switch f.Sort {
	case "created_at", "service_date", "updated_at", "request_no":
	default:
		return f, service.Invalid("sort must be created_at, service_date, updated_at or request_no.")
	}
	if f.Order != "asc" && f.Order != "desc" {
		return f, service.Invalid("order must be asc or desc.")
	}
	if len(f.Q) > 200 {
		return f, service.Invalid("Search text can be at most 200 characters.")
	}
	for _, date := range []*string{&f.DateFrom, &f.DateTo} {
		if *date != "" {
			if _, err := time.Parse("2006-01-02", *date); err != nil {
				return f, service.Invalid("date_from and date_to must be dates in YYYY-MM-DD format.")
			}
		}
	}
	if f.DateFrom != "" && f.DateTo != "" && f.DateFrom > f.DateTo {
		return f, service.Invalid("date_from cannot be after date_to.")
	}
	return f, nil
}
func parsePage(r *http.Request) (int, int, error) {
	page, size := 1, 25
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			return 0, 0, service.Invalid("page must be a whole number of 1 or more.")
		}
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil || size < 1 || size > 100 {
			return 0, 0, service.Invalid("page_size must be a whole number from 1 to 100.")
		}
	}
	return page, size, nil
}

func outStoreCollectionHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	type create struct {
		ServiceRequestID string  `json:"service_request_id"`
		ShopID           string  `json:"shop_id"`
		SentDate         string  `json:"sent_date"`
		DueDate          *string `json:"due_date"`
		Price            *string `json:"price"`
		Remarks          *string `json:"remarks"`
	}
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		if r.Method == http.MethodGet {
			page, size, err := parsePage(r)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			q := r.URL.Query()
			order, status := q.Get("order"), q.Get("status")
			if order == "" {
				order = "desc"
			}
			if order != "asc" && order != "desc" {
				writeManagementError(w, service.Invalid("order must be asc or desc."), logger)
				return
			}
			if status != "" && status != "SENT" && status != "RECEIVED_BACK" {
				writeManagementError(w, service.Invalid("status must be SENT or RECEIVED_BACK."), logger)
				return
			}
			filter := repository.OutStoreFilter{Page: page, PageSize: size, Order: order, ServiceRequestID: q.Get("service_request_id"), Status: status, ShopID: q.Get("shop_id"), Q: q.Get("q")}
			items, total, err := store.ListOutStore(r.Context(), companyID, filter)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
			return
		}
		if r.Method == http.MethodPost {
			var body create
			if !decodeJSON(w, r, &body) {
				return
			}
			fieldErrors := service.FieldErrors{}
			if strings.TrimSpace(body.ServiceRequestID) == "" {
				fieldErrors["service_request_id"] = "is required"
			}
			if strings.TrimSpace(body.ShopID) == "" {
				fieldErrors["shop_id"] = "is required"
			}
			if _, err := time.Parse("2006-01-02", body.SentDate); err != nil {
				fieldErrors["sent_date"] = "must use YYYY-MM-DD"
			}
			if body.DueDate != nil {
				if _, err := time.Parse("2006-01-02", *body.DueDate); err != nil {
					fieldErrors["due_date"] = "must use YYYY-MM-DD"
				} else if body.SentDate != "" && *body.DueDate < body.SentDate {
					fieldErrors["due_date"] = "must be on or after sent_date"
				}
			}
			if body.Price != nil {
				price, err := strconv.ParseFloat(*body.Price, 64)
				if err != nil || price < 0 {
					fieldErrors["price"] = "must be a non-negative number"
				}
			}
			if len(fieldErrors) > 0 {
				writeManagementError(w, fieldErrors, logger)
				return
			}
			item, err := store.CreateOutStore(r.Context(), companyID, p.Identity.User.ID, repository.OutStoreInput{ServiceRequestID: body.ServiceRequestID, ShopID: body.ShopID, SentDate: body.SentDate, DueDate: body.DueDate, Price: body.Price, Remarks: body.Remarks})
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 201, item)
			return
		}
		methodError(w, "GET, POST")
	}
}
func outStoreItemHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		companyID, id := r.PathValue("companyId"), r.PathValue("id")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		if r.Method == http.MethodGet {
			item, err := store.GetOutStore(r.Context(), companyID, id)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
			return
		}
		if r.Method == http.MethodPatch {
			var values map[string]any
			if !decodeJSON(w, r, &values) {
				return
			}
			allowed := map[string]bool{"shop_id": true, "sent_date": true, "due_date": true, "price": true, "remarks": true, "status": true}
			if len(values) == 0 {
				writeManagementError(w, service.Invalid("Send at least one field to change."), logger)
				return
			}
			for key := range values {
				if !allowed[key] {
					writeManagementError(w, service.FieldErrors{key: "cannot be changed on an Out-Store entry"}, logger)
					return
				}
			}
			patch := repository.OutStorePatch{}
			var err error
			if patch.ShopID, _, err = optionalString(values, "shop_id", false); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			if patch.SentDate, _, err = optionalString(values, "sent_date", false); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			if patch.DueDate, patch.DueDateSet, err = optionalString(values, "due_date", true); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			if patch.Price, patch.PriceSet, err = optionalString(values, "price", true); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			if patch.Remarks, patch.RemarksSet, err = optionalString(values, "remarks", true); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			if patch.Status, _, err = optionalString(values, "status", false); err != nil {
				writeManagementError(w, err, logger)
				return
			}
			for _, date := range []*string{patch.SentDate, patch.DueDate} {
				if date != nil {
					if _, err := time.Parse("2006-01-02", *date); err != nil {
						writeManagementError(w, service.Invalid("Dates must be in YYYY-MM-DD format."), logger)
						return
					}
				}
			}
			item, err := store.UpdateOutStore(r.Context(), companyID, id, p.Identity.User.ID, patch)
			if err != nil {
				writeManagementError(w, err, logger)
				return
			}
			writeJSON(w, 200, item)
			return
		}
		methodError(w, "GET, PATCH")
	}
}
func outStoreReceiveHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodPost {
			methodError(w, "POST")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		var empty struct{}
		if !decodeJSON(w, r, &empty) {
			return
		}
		item, err := store.ReceiveOutStore(r.Context(), companyID, r.PathValue("id"), p.Identity.User.ID)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, 200, item)
	}
}

func standbyIssueHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	type issueRequest struct {
		CustomerID       string  `json:"customer_id"`
		ServiceRequestID *string `json:"service_request_id"`
		IssuedDate       string  `json:"issued_date"`
		DueDate          *string `json:"due_date"`
		Notes            *string `json:"notes"`
		ProductID        *string `json:"received_product_id"`
	}
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodPost {
			methodError(w, "POST")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		var body issueRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		fields := service.FieldErrors{}
		if body.CustomerID == "" {
			fields["customer_id"] = "is required"
		}
		if _, err := time.Parse("2006-01-02", body.IssuedDate); err != nil {
			fields["issued_date"] = "must be a date in YYYY-MM-DD format"
		}
		if body.DueDate != nil {
			if _, err := time.Parse("2006-01-02", *body.DueDate); err != nil || *body.DueDate < body.IssuedDate {
				fields["due_date"] = "must be a date on or after the issued date"
			}
		}
		if body.Notes != nil && len(*body.Notes) > 4000 {
			fields["notes"] = "can be at most 4000 characters"
		}
		if len(fields) > 0 {
			writeManagementError(w, fields, logger)
			return
		}
		result, err := store.IssueStandbyItem(r.Context(), companyID, r.PathValue("id"), p.Identity.User.ID, repository.StandbyIssueInput{CustomerID: body.CustomerID, ServiceRequestID: body.ServiceRequestID, IssuedDate: body.IssuedDate, DueDate: body.DueDate, Notes: body.Notes, ProductID: body.ProductID})
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func standbyReturnHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodPost {
			methodError(w, "POST")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		var body struct {
			IssueID string `json:"issue_id"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.IssueID == "" {
			writeManagementError(w, service.FieldErrors{"issue_id": "is required"}, logger)
			return
		}
		result, err := store.ReturnStandbyItem(r.Context(), companyID, r.PathValue("id"), body.IssueID, p.Identity.User.ID)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func standbyIssueListHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodError(w, "GET, HEAD")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		items, err := store.ListStandbyIssues(r.Context(), companyID, r.PathValue("id"))
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// standbyIssueHistoryHandler lists lending history across items, for example
// everything a customer has borrowed (?contact=98765…) (T36).
func standbyIssueHistoryHandler(auth *service.AuthService, store *repository.CatalogStore, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodError(w, "GET, HEAD")
			return
		}
		companyID := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, companyID, "ADMIN", "USER"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		page, size, err := parsePage(r)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		q := r.URL.Query()
		f := repository.StandbyIssueFilter{Page: page, PageSize: size, Order: q.Get("order"), Contact: q.Get("contact"), CustomerID: q.Get("customer_id"), ItemID: q.Get("standby_item_id"), Request: q.Get("service_request_id"), State: q.Get("state")}
		fields := service.FieldErrors{}
		for key, value := range map[string]string{"customer_id": f.CustomerID, "standby_item_id": f.ItemID, "service_request_id": f.Request} {
			if value != "" && !uuidPattern.MatchString(value) {
				fields[key] = "must be a valid ID"
			}
		}
		if f.State != "" && f.State != "open" && f.State != "returned" {
			fields["state"] = "must be open or returned"
		}
		if f.Order != "" && f.Order != "asc" && f.Order != "desc" {
			fields["order"] = "must be asc or desc"
		}
		if len(fields) > 0 {
			writeManagementError(w, fields, logger)
			return
		}
		items, total, err := store.ListStandbyIssueHistory(r.Context(), companyID, f)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
	}
}

func optionalString(values map[string]any, key string, nullable bool) (*string, bool, error) {
	raw, present := values[key]
	if !present {
		return nil, false, nil
	}
	if raw == nil {
		if nullable {
			return nil, true, nil
		}
		return nil, true, service.FieldErrors{key: "cannot be null"}
	}
	value, ok := raw.(string)
	if !ok {
		return nil, true, service.FieldErrors{key: "must be text"}
	}
	return &value, true, nil
}

func roles(resource string) ([]string, []string) {
	if resource == "users" {
		return []string{"SUPER_ADMIN", "ADMIN"}, []string{"SUPER_ADMIN", "ADMIN"}
	}
	if resource == "service-profiles" {
		return []string{"SUPER_ADMIN", "ADMIN"}, []string{"SUPER_ADMIN", "ADMIN"}
	}
	return []string{"ADMIN", "USER"}, []string{"ADMIN"}
}

// prepareCredentials normalizes email and phone. Email is required for user
// accounts; for customers and staff it is optional and empty clears it.
func prepareCredentials(v map[string]any, emailRequired bool) error {
	if raw, ok := v["email"]; ok && !emailRequired && (raw == nil || raw == "") {
		v["email"] = nil
	} else if ok {
		email, ok := raw.(string)
		if !ok {
			return service.FieldErrors{"email": "must be a valid email address"}
		}
		normalized, err := service.NormalizeEmail(email)
		if err != nil {
			return service.FieldErrors{"email": "must be a valid email address"}
		}
		v["email"] = normalized
	}
	if raw, ok := v["phone"]; ok {
		// Empty or null clears the phone; otherwise store digits only.
		if raw == nil {
			v["phone"] = nil
		} else if text, isText := raw.(string); !isText {
			return service.FieldErrors{"phone": "must be a mobile number with 6 to 20 digits"}
		} else if strings.TrimSpace(text) == "" {
			v["phone"] = nil
		} else if phone, err := service.NormalizePhone(text); err != nil {
			return service.FieldErrors{"phone": "must be a mobile number with 6 to 20 digits"}
		} else {
			v["phone"] = phone
		}
	}
	if role, ok := v["role"].(string); ok && role == "SUPER_ADMIN" {
		return service.Forbidden("Company users cannot be given the SUPER_ADMIN role.")
	}
	raw, ok := v["password"]
	if !ok {
		return nil
	}
	password, ok := raw.(string)
	if !ok {
		return service.FieldErrors{"password": "must be text"}
	}
	hash, err := service.HashPassword(password)
	if errors.Is(err, service.ErrValidation) {
		return service.FieldErrors{"password": passwordReason(err)}
	} else if err != nil {
		return err
	}
	v["password_hash"] = hash
	delete(v, "password")
	return nil
}

// writePage serves a paged list: page, page_size, q, sort and order, with any
// other query parameter treated as a filter and validated by the list (T37).
func writePage(w http.ResponseWriter, r *http.Request, logger *slog.Logger, list func(repository.ListQuery) ([]map[string]any, int, error)) {
	page, size, err := parsePage(r)
	if err != nil {
		writeManagementError(w, err, logger)
		return
	}
	query := repository.ListQuery{Page: page, PageSize: size, Filters: map[string]string{}}
	for name, values := range r.URL.Query() {
		value := values[len(values)-1]
		switch name {
		case "page", "page_size":
		case "q":
			query.Q = value
		case "sort":
			query.Sort = value
		case "order":
			query.Order = value
		default:
			if value != "" {
				query.Filters[name] = value
			}
		}
	}
	items, total, err := list(query)
	if err != nil {
		writeManagementError(w, err, logger)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
}

func writeList(w http.ResponseWriter, items []map[string]any) {
	writeJSON(w, 200, map[string]any{"items": items, "page": 1, "page_size": 25, "total": len(items)})
}
func methodError(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
}
func writeManagementError(w http.ResponseWriter, err error, logger *slog.Logger) {
	var pg *pgconn.PgError
	var fieldErrors service.FieldErrors
	switch {
	case errors.Is(err, repository.ErrNotFound):
		logger.Warn("management request rejected", "error", err)
		writeError(w, 404, "NOT_FOUND", repository.NotFoundMessage(err))
	case errors.Is(err, repository.ErrLastAdmin):
		logger.Warn("management request rejected", "error", err)
		writeError(w, 409, "LAST_ACTIVE_ADMIN", "The last active admin cannot be deactivated or made a user. Make another user an admin first.")
	case errors.Is(err, service.ErrValidation):
		logger.Warn("management request rejected", "error", err)
		writeError(w, 400, "VALIDATION_ERROR", service.Message(err, "The request is not valid."))
	case errors.As(err, &fieldErrors):
		logger.Warn("management request rejected", "field_errors", fieldErrors)
		writeFieldError(w, 400, "VALIDATION_ERROR", "Invalid form data", fieldErrors)
	case errors.Is(err, service.ErrInvalidState):
		logger.Warn("management request rejected", "error", err)
		writeError(w, 409, "INVALID_STATE", service.Message(err, "This action is not allowed in the current state."))
	case errors.Is(err, service.ErrForbidden):
		logger.Warn("management request rejected", "error", err)
		writeError(w, 403, "FORBIDDEN", service.Message(err, "You do not have permission to do this."))
	case errors.As(err, &pg) && pg.Code == "23505":
		logPostgresRejection(logger, pg)
		if message, fields := uniqueConflict(pg); fields != nil {
			writeFieldError(w, 409, "CONFLICT", message, fields)
		} else {
			writeError(w, 409, "CONFLICT", message)
		}
	case errors.As(err, &pg) && pg.Code == "23503":
		logPostgresRejection(logger, pg)
		if fields := postgresFieldErrors(pg); fields != nil {
			writeFieldError(w, 400, "VALIDATION_ERROR", "Invalid field value", fields)
			return
		}
		writeError(w, 409, "RESOURCE_IN_USE", "It is still used by other records, or it belongs to another company.")
	case errors.As(err, &pg) && (pg.Code == "23514" || pg.Code == "23502" || pg.Code == "22P02"):
		logPostgresRejection(logger, pg)
		if fields := postgresFieldErrors(pg); fields != nil {
			writeFieldError(w, 400, "VALIDATION_ERROR", "Invalid field value", fields)
			return
		}
		writeError(w, 400, "VALIDATION_ERROR", "A value is missing, too long or not allowed.")
	default:
		if strings.Contains(err.Error(), "field") {
			logger.Warn("management request rejected", "error", err)
			writeError(w, 400, "VALIDATION_ERROR", err.Error())
			return
		}
		logger.Error("management operation failed", "error", err)
		writeError(w, 500, "INTERNAL_ERROR", "Internal server error")
	}
}

func logPostgresRejection(logger *slog.Logger, err *pgconn.PgError) {
	// Do not log PostgreSQL Detail: it can echo submitted values. The SQLSTATE,
	// constraint and message identify the failing rule without recording input.
	logger.Warn("management request rejected", "sqlstate", err.Code, "constraint", err.ConstraintName, "error", err.Message)
}

// uniqueConflict explains a unique-constraint violation, naming the field when
// the client can fix it.
func uniqueConflict(err *pgconn.PgError) (string, service.FieldErrors) {
	switch err.ConstraintName {
	case "service_profiles_company_id_prefix_key":
		return "This prefix is already used by another profile.", service.FieldErrors{"prefix": "is already used by another profile"}
	case "users_email_ci_key":
		return "This email is already used by another account.", service.FieldErrors{"email": "is already used by another account"}
	case "users_phone_key":
		return "This mobile number is already used by another account.", service.FieldErrors{"phone": "is already used by another account"}
	case "service_profile_fields_profile_id_field_key_key":
		return "This key is already used by another field in the profile.", service.FieldErrors{"key": "is already used in this profile"}
	case "customers_company_mobile_key":
		return "This mobile number already belongs to another customer.", service.FieldErrors{"contact": "already belongs to another customer"}
	case "service_requests_company_id_request_no_key":
		return "That request number was just taken. Try again.", nil
	case "staff_role_assignments_company_id_staff_id_role_id_key":
		return "A role is listed more than once.", service.FieldErrors{"role_ids": "must not repeat a role"}
	case "service_profile_statuses_one_initial_idx":
		return "The profile already has an initial status.", service.FieldErrors{"initial": "another status is already initial"}
	case "out_store_entries_one_open_idx":
		return "This record is already at an Out-Store shop. Receive it back before sending it again.", nil
	case "standby_item_issues_one_open_idx":
		return "This item is already issued. Return it before issuing it again.", nil
	}
	return "This already exists.", nil
}

func postgresFieldErrors(err *pgconn.PgError) service.FieldErrors {
	switch err.ConstraintName {
	case "products_status_check":
		return service.FieldErrors{"status": "must be ACTIVE or DISCONTINUED"}
	case "products_company_id_profile_id_fkey":
		return service.FieldErrors{"profile_id": "must reference a service profile in this company"}
	}
	if err.ColumnName != "" {
		return service.FieldErrors{err.ColumnName: "contains an invalid value"}
	}
	return nil
}

// passwordReason turns "Password must …." into the field message "must …".
func passwordReason(err error) string {
	return strings.TrimSuffix(strings.TrimPrefix(service.Message(err, "is not allowed"), "Password "), ".")
}

// temporaryCredentials returns a new temporary password and its hash.
func temporaryCredentials() (string, string, error) {
	temporary, err := service.GenerateTemporaryPassword()
	if err != nil {
		return "", "", err
	}
	hash, err := service.HashPassword(temporary)
	return temporary, hash, err
}

// userCredentialEmail collects what the welcome or reset email needs.
func userCredentialEmail(ctx context.Context, store *repository.CatalogStore, companyID string, user map[string]any, temporary string, reset bool) credentialEmail {
	email := credentialEmail{reset: reset, temporaryPassword: temporary, company: "your company"}
	email.name, _ = user["name"].(string)
	email.email, _ = user["email"].(string)
	if phone, ok := user["phone"].(string); ok {
		email.phone = &phone
	}
	if company, err := store.GetCompany(ctx, companyID); err == nil {
		if name, ok := company["name"].(string); ok {
			email.company = name
		}
	}
	return email
}

// resetPasswordHandler gives a company user a new temporary password, signs
// them out everywhere and emails the password (T28).
func resetPasswordHandler(auth *service.AuthService, store *repository.CatalogStore, accounts AccountMail, logger *slog.Logger) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p service.Principal) {
		if r.Method != http.MethodPost {
			methodError(w, "POST")
			return
		}
		cid := r.PathValue("companyId")
		if err := auth.AuthorizeCompany(p, cid, "SUPER_ADMIN", "ADMIN"); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		temporary, hash, err := temporaryCredentials()
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		user, err := store.ResetUserPassword(r.Context(), cid, r.PathValue("id"), hash)
		if err != nil {
			writeManagementError(w, err, logger)
			return
		}
		delivery := accounts.deliver(r.Context(), logger, userCredentialEmail(r.Context(), store, cid, user, temporary, true))
		writeJSON(w, http.StatusOK, map[string]any{"user": user, "delivery": delivery})
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
