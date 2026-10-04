package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"serviceops360/api/internal/httpapi"
	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

func TestManagementHTTPWorkflow(t *testing.T) {
	url := os.Getenv("SERVICEOPS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SERVICEOPS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	schema := "serviceops_management_test_" + hex.EncodeToString(random)
	if _, err := base.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = base.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",pg_catalog"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, name := range []string{"000001_company_master_data.up.sql", "000002_service_transactions.up.sql", "000003_authentication.up.sql", "000004_user_phone.up.sql", "000005_must_change_password.up.sql", "000006_password_reset_tokens.up.sql", "000007_record_history_events.up.sql", "000008_customer_mobile_identity.up.sql", "000009_drop_company_code.up.sql"} {
		sql, err := os.ReadFile(filepath.Join("..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	hash, _ := service.HashPassword("root password is secure 1")
	if _, err := pool.Exec(ctx, `INSERT INTO users(name,email,password_hash,role)VALUES('Root','root@example.test',$1,'SUPER_ADMIN')`, hash); err != nil {
		t.Fatal(err)
	}
	auth, err := service.NewAuthService(repository.NewAuthStore(pool), service.AuthConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api", TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute, EmailLimit: 5, IPLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewApplicationHandler(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second, auth, repository.NewCatalogStore(pool))
	rootToken := httpLogin(t, handler, "root@example.test", "root password is secure 1")
	onboard := httpJSON(t, handler, http.MethodPost, "/api/v1/companies", rootToken, `{"name":"Acme","admin":{"name":"Owner","email":"owner@example.test"}}`)
	if onboard.Code != http.StatusCreated {
		t.Fatalf("onboard %d %s", onboard.Code, onboard.Body.String())
	}
	// CompanyOnboarded: { company, admin, delivery }.
	var onboarded struct {
		Company map[string]any `json:"company"`
		Admin   map[string]any `json:"admin"`
	}
	_ = json.Unmarshal(onboard.Body.Bytes(), &onboarded)
	if onboarded.Admin["email"] != "owner@example.test" || onboarded.Admin["must_change_password"] != true || onboarded.Admin["password_hash"] != nil {
		t.Fatalf("onboarded admin %s", onboard.Body.String())
	}
	company := onboarded.Company
	companyID := company["id"].(string)
	ownerToken := activateAccount(t, handler, "owner@example.test", onboard, "owner password secure 2")
	profiles := httpJSON(t, handler, http.MethodGet, "/api/v1/companies/"+companyID+"/service-profiles", ownerToken, "")
	if profiles.Code != 200 {
		t.Fatalf("profiles %d %s", profiles.Code, profiles.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(profiles.Body.Bytes(), &list)
	if len(list.Items) != 2 {
		t.Fatalf("profiles=%d", len(list.Items))
	}
	var jobCardID, refillID string
	for _, profile := range list.Items {
		if profile["prefix"] == "A" {
			jobCardID = profile["id"].(string)
		} else if profile["prefix"] == "RF" {
			refillID = profile["id"].(string)
		}
	}
	form := httpJSON(t, handler, http.MethodGet, "/api/v1/companies/"+companyID+"/service-profiles/"+jobCardID+"/form", ownerToken, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), "core_fields") {
		t.Fatalf("form %d %s", form.Code, form.Body.String())
	}
	var formBody map[string]any
	_ = json.Unmarshal(form.Body.Bytes(), &formBody)
	statuses := formBody["statuses"].([]any)
	var pendingID, assignedID, completedID, deliveredID string
	for _, raw := range statuses {
		status := raw.(map[string]any)
		if status["name"] == "Pending" {
			pendingID = status["id"].(string)
		}
		if status["name"] == "Assigned" {
			assignedID = status["id"].(string)
		}
		if status["name"] == "Delivered" {
			deliveredID = status["id"].(string)
		}
		if status["name"] == "Completed" {
			completedID = status["id"].(string)
		}
	}
	var productFieldID string
	for _, raw := range formBody["fields"].([]any) {
		field := raw.(map[string]any)
		if field["key"] == "jc_product" {
			productFieldID = field["id"].(string)
		}
	}
	statusPath := "/api/v1/companies/" + companyID + "/service-profiles/" + jobCardID + "/statuses/"
	initialDelete := httpJSON(t, handler, http.MethodDelete, statusPath+pendingID, ownerToken, "")
	if initialDelete.Code != 409 {
		t.Fatalf("initial status disabled=%d %s", initialDelete.Code, initialDelete.Body.String())
	}
	mappedDisable := httpJSON(t, handler, http.MethodPatch, statusPath+completedID, ownerToken, `{"enabled":false}`)
	if mappedDisable.Code != 409 {
		t.Fatalf("mapped status disabled=%d %s", mappedDisable.Code, mappedDisable.Body.String())
	}
	newInitial := httpJSON(t, handler, http.MethodPatch, statusPath+assignedID, ownerToken, `{"initial":true}`)
	if newInitial.Code != 200 {
		t.Fatalf("replace initial status=%d %s", newInitial.Code, newInitial.Body.String())
	}
	restoreInitial := httpJSON(t, handler, http.MethodPatch, statusPath+pendingID, ownerToken, `{"initial":true}`)
	if restoreInitial.Code != 200 {
		t.Fatalf("restore initial status=%d %s", restoreInitial.Code, restoreInitial.Body.String())
	}
	customProfile := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/service-profiles", ownerToken, `{"name":"Archive Test","prefix":"AT"}`)
	if customProfile.Code != 201 {
		t.Fatalf("custom profile %d %s", customProfile.Code, customProfile.Body.String())
	}
	var customProfileBody map[string]any
	_ = json.Unmarshal(customProfile.Body.Bytes(), &customProfileBody)
	archivedProfile := httpJSON(t, handler, http.MethodDelete, "/api/v1/companies/"+companyID+"/service-profiles/"+customProfileBody["id"].(string), ownerToken, "")
	if archivedProfile.Code != 204 {
		t.Fatalf("profile archive route %d %s", archivedProfile.Code, archivedProfile.Body.String())
	}
	profilePath := "/api/v1/companies/" + companyID + "/service-profiles/" + jobCardID
	coreRelabel := httpJSON(t, handler, http.MethodPatch, profilePath, ownerToken, `{"core_labels":{"customer_contact":"Client Mobile"}}`)
	if coreRelabel.Code != 200 || !strings.Contains(coreRelabel.Body.String(), `"customer_contact":"Client Mobile"`) || !strings.Contains(coreRelabel.Body.String(), `"service_date":"Date"`) {
		t.Fatalf("core label merge %d %s", coreRelabel.Code, coreRelabel.Body.String())
	}
	invalidCore := httpJSON(t, handler, http.MethodPatch, profilePath, ownerToken, `{"core_labels":{"unknown":"Bad"}}`)
	if invalidCore.Code != 400 {
		t.Fatalf("unknown core label=%d %s", invalidCore.Code, invalidCore.Body.String())
	}
	reservedField := httpJSON(t, handler, http.MethodPost, profilePath+"/fields", ownerToken, `{"key":"service_date","label":"Duplicate","type":"date","config":{}}`)
	if reservedField.Code != 400 {
		t.Fatalf("reserved core field=%d %s", reservedField.Code, reservedField.Body.String())
	}
	refillEnable := httpJSON(t, handler, http.MethodPatch, "/api/v1/companies/"+companyID+"/service-profiles/"+refillID, ownerToken, `{"out_store_enabled":true}`)
	if refillEnable.Code != 409 {
		t.Fatalf("profile enabled without mapping=%d %s", refillEnable.Code, refillEnable.Body.String())
	}
	mappingPath := profilePath + "/out-store-mapping"
	invalidMapping := httpJSON(t, handler, http.MethodPut, mappingPath, ownerToken, `{"sent_status_id":"`+pendingID+`","received_status_id":"`+pendingID+`"}`)
	if invalidMapping.Code != 409 {
		t.Fatalf("duplicate mapping statuses=%d %s", invalidMapping.Code, invalidMapping.Body.String())
	}
	clearEnabledMapping := httpJSON(t, handler, http.MethodPut, mappingPath, ownerToken, `{"sent_status_id":null,"received_status_id":null}`)
	if clearEnabledMapping.Code != 409 {
		t.Fatalf("enabled profile mapping cleared=%d %s", clearEnabledMapping.Code, clearEnabledMapping.Body.String())
	}
	customer := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/customers", ownerToken, `{"name":"Customer","contact":"98765 00555"}`)
	if customer.Code != 201 || strings.Contains(customer.Body.String(), "customer_no") {
		t.Fatalf("customer %d %s", customer.Code, customer.Body.String())
	}
	// Email and address are optional for customers; null clears them.
	if optional := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/customers", ownerToken, `{"name":"No email","contact":"90000 11111","email":null,"address":null}`); optional.Code != 201 {
		t.Fatalf("customer without email %d %s", optional.Code, optional.Body.String())
	}
	// T33: one customer per mobile number, compared by digits.
	for body, want := range map[string]string{
		`{"name":"Copy","contact":"9876500555"}`: "already belongs to Customer",
		`{"name":"Copy","contact":"no number"}`:  "must be a mobile number",
	} {
		if dup := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/customers", ownerToken, body); dup.Code != 400 || !strings.Contains(dup.Body.String(), want) {
			t.Fatalf("customer %s=%d %s", body, dup.Code, dup.Body.String())
		}
	}
	var customerBody map[string]any
	_ = json.Unmarshal(customer.Body.Bytes(), &customerBody)
	customerID := customerBody["id"].(string)
	requestPath := "/api/v1/companies/" + companyID + "/service-requests"
	created := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-16","customer_id":"`+customerID+`","form_data":{"jc_servicetype":"In-Person","jc_complaint":"No power","jc_advance":20,"jc_totalamount":100}}`)
	if created.Code != 201 || !strings.Contains(created.Body.String(), `"request_no":"A1001"`) || !strings.Contains(created.Body.String(), `"jc_balance":80`) {
		t.Fatalf("request %d %s", created.Code, created.Body.String())
	}
	var createdBody map[string]any
	_ = json.Unmarshal(created.Body.Bytes(), &createdBody)
	requestID := createdBody["id"].(string)
	// T32: unused statuses can be deleted; used ones explain why not.
	statusesPath := profilePath + "/statuses"
	spare := httpJSON(t, handler, http.MethodPost, statusesPath, ownerToken, `{"name":"Spare","sort_order":20}`)
	var spareBody map[string]any
	_ = json.Unmarshal(spare.Body.Bytes(), &spareBody)
	if deleted := httpJSON(t, handler, http.MethodDelete, statusesPath+"/"+spareBody["id"].(string), ownerToken, ""); spare.Code != 201 || deleted.Code != 204 {
		t.Fatalf("delete unused status %d %d %s", spare.Code, deleted.Code, deleted.Body.String())
	}
	if again := httpJSON(t, handler, http.MethodDelete, statusesPath+"/"+spareBody["id"].(string), ownerToken, ""); again.Code != 404 {
		t.Fatalf("deleted status still found=%d", again.Code)
	}
	quoted := httpJSON(t, handler, http.MethodPost, statusesPath, ownerToken, `{"name":"Quoted","sort_order":21}`)
	var quotedBody map[string]any
	_ = json.Unmarshal(quoted.Body.Bytes(), &quotedBody)
	quotedID := quotedBody["id"].(string)
	requestStatusPath := requestPath + "/" + requestID + "/status"
	if moved := httpJSON(t, handler, http.MethodPatch, requestStatusPath, ownerToken, `{"status_id":"`+quotedID+`"}`); moved.Code != 200 {
		t.Fatalf("move to quoted %d %s", moved.Code, moved.Body.String())
	}
	if inUse := httpJSON(t, handler, http.MethodDelete, statusesPath+"/"+quotedID, ownerToken, ""); inUse.Code != 409 || !strings.Contains(inUse.Body.String(), `\"Quoted\" is the current status of 1 record`) {
		t.Fatalf("delete current status=%d %s", inUse.Code, inUse.Body.String())
	}
	if moved := httpJSON(t, handler, http.MethodPatch, requestStatusPath, ownerToken, `{"status_id":"`+pendingID+`"}`); moved.Code != 200 {
		t.Fatalf("move back to pending %d %s", moved.Code, moved.Body.String())
	}
	if inHistory := httpJSON(t, handler, http.MethodDelete, statusesPath+"/"+quotedID, ownerToken, ""); inHistory.Code != 409 || !strings.Contains(inHistory.Body.String(), "appears in the history of 1 record") {
		t.Fatalf("delete status in history=%d %s", inHistory.Code, inHistory.Body.String())
	}
	if userDelete := httpJSON(t, handler, http.MethodDelete, statusesPath+"/"+quotedID, "", ""); userDelete.Code != 401 {
		t.Fatalf("anonymous status delete=%d", userDelete.Code)
	}
	invalid := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-16","customer_id":"`+customerID+`","form_data":{"jc_servicetype":"In-Person"}}`)
	if invalid.Code != 400 || !strings.Contains(invalid.Body.String(), "jc_complaint") {
		t.Fatalf("validation %d %s", invalid.Code, invalid.Body.String())
	}
	invalidChoice := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-16","customer_id":"`+customerID+`","form_data":{"jc_servicetype":"Remote","jc_complaint":"No power"}}`)
	if invalidChoice.Code != 400 || !strings.Contains(invalidChoice.Body.String(), "jc_servicetype") {
		t.Fatalf("choice validation %d %s", invalidChoice.Code, invalidChoice.Body.String())
	}
	refillRequest := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+refillID+`","service_date":"2026-09-16","customer_id":"`+customerID+`","form_data":{"rf_toner":"TN-100"}}`)
	if refillRequest.Code != 201 || !strings.Contains(refillRequest.Body.String(), `"request_no":"RF1001"`) {
		t.Fatalf("independent profile numbering %d %s", refillRequest.Code, refillRequest.Body.String())
	}
	shop := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/out-store-shops", ownerToken, `{"profile_id":"`+jobCardID+`","shop_name":"Vendor"}`)
	if shop.Code != 201 {
		t.Fatalf("shop %d %s", shop.Code, shop.Body.String())
	}
	var shopBody map[string]any
	_ = json.Unmarshal(shop.Body.Bytes(), &shopBody)
	var nextBeforeRollback int
	if err := pool.QueryRow(ctx, `SELECT next_number FROM service_profiles WHERE id=$1`, jobCardID).Scan(&nextBeforeRollback); err != nil {
		t.Fatal(err)
	}
	failedInline := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-17","customer_id":"`+customerID+`","form_data":{"jc_servicetype":"In-Store","jc_complaint":"Rollback"},"out_store":{"shop_id":"`+shopBody["id"].(string)+`","due_date":"2026-09-16"}}`)
	if failedInline.Code != 400 {
		t.Fatalf("invalid inline dispatch=%d %s", failedInline.Code, failedInline.Body.String())
	}
	var nextAfterRollback int
	if err := pool.QueryRow(ctx, `SELECT next_number FROM service_profiles WHERE id=$1`, jobCardID).Scan(&nextAfterRollback); err != nil {
		t.Fatal(err)
	}
	if nextAfterRollback != nextBeforeRollback {
		t.Fatalf("failed transaction consumed request number: %d -> %d", nextBeforeRollback, nextAfterRollback)
	}
	inline := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-17","customer_id":"`+customerID+`","form_data":{"jc_servicetype":"In-Store","jc_complaint":"Board issue"},"out_store":{"shop_id":"`+shopBody["id"].(string)+`","due_date":"2026-09-20","price":"100.00"}}`)
	if inline.Code != 201 || !strings.Contains(inline.Body.String(), `"request_no":"A1002"`) || !strings.Contains(inline.Body.String(), "out_store_entry") {
		t.Fatalf("inline %d %s", inline.Code, inline.Body.String())
	}
	var inlineBody map[string]any
	_ = json.Unmarshal(inline.Body.Bytes(), &inlineBody)
	inlineID := inlineBody["id"].(string)
	listed := httpJSON(t, handler, http.MethodGet, requestPath+"?q=Customer&sort=service_date&order=asc", ownerToken, "")
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), "A1001") {
		t.Fatalf("list %d %s", listed.Code, listed.Body.String())
	}
	// T34: mobile search, Out-Store state, overdue, open/closed and facets.
	for _, tc := range []struct{ query, want, notWant string }{
		{"?q=9876500555", `"customer_contact":"98765 00555"`, ""},
		{"?contact=98765-00555&sort=request_no&order=asc", `"request_no":"A1001"`, ""},
		{"?out_store=at_shop", `"request_no":"A1002"`, `"request_no":"A1001"`},
		{"?out_store=none", `"request_no":"A1001"`, `"request_no":"A1002"`},
		{"?overdue=true", `"out_store_status":"SENT"`, `"request_no":"A1001"`},
		{"?state=open&facets=true", `"facets":`, ""},
		{"?status_name=sent%20to%20out-store", `"request_no":"A1002"`, `"request_no":"A1001"`},
	} {
		response := httpJSON(t, handler, http.MethodGet, requestPath+tc.query, ownerToken, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), tc.want) || (tc.notWant != "" && strings.Contains(response.Body.String(), tc.notWant)) {
			t.Fatalf("records %s: %d %s", tc.query, response.Code, response.Body.String())
		}
	}
	var faceted struct {
		Facets struct {
			Statuses []map[string]any `json:"statuses"`
			Open     int              `json:"open"`
			Creators []map[string]any `json:"creators"`
		} `json:"facets"`
	}
	facetResponse := httpJSON(t, handler, http.MethodGet, requestPath+"?facets=true&status_name=Pending", ownerToken, "")
	_ = json.Unmarshal(facetResponse.Body.Bytes(), &faceted)
	// Status counts ignore the status filter so other chips still show totals.
	if faceted.Facets.Open != 3 || len(faceted.Facets.Statuses) != 3 || len(faceted.Facets.Creators) != 1 || faceted.Facets.Creators[0]["name"] != "Owner" {
		t.Fatalf("facets: %s", facetResponse.Body.String())
	}
	for _, query := range []string{"?state=done", "?out_store=lost", "?created_by=nope", "?overdue=maybe", "?sort=customer"} {
		if response := httpJSON(t, handler, http.MethodGet, requestPath+query, ownerToken, ""); response.Code != 400 {
			t.Fatalf("records %s accepted: %d %s", query, response.Code, response.Body.String())
		}
	}
	detailPath := requestPath + "/" + requestID
	detail := httpJSON(t, handler, http.MethodGet, detailPath, ownerToken, "")
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "form_definition") || !strings.Contains(detail.Body.String(), "customer") {
		t.Fatalf("detail %d %s", detail.Code, detail.Body.String())
	}
	edited := httpJSON(t, handler, http.MethodPatch, detailPath, ownerToken, `{"service_date":"2026-09-19","form_data":{"jc_complaint":"Power supply replaced","jc_totalamount":10}}`)
	if edited.Code != 200 || !strings.Contains(edited.Body.String(), "Power supply replaced") || !strings.Contains(edited.Body.String(), `"jc_balance":-10`) {
		t.Fatalf("edit %d %s", edited.Code, edited.Body.String())
	}
	formulaOverride := httpJSON(t, handler, http.MethodPatch, detailPath, ownerToken, `{"form_data":{"jc_balance":999}}`)
	if formulaOverride.Code != 400 || !strings.Contains(formulaOverride.Body.String(), "jc_balance") {
		t.Fatalf("formula override=%d %s", formulaOverride.Code, formulaOverride.Body.String())
	}
	product := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/products", ownerToken, `{"profile_id":"`+jobCardID+`","name":"Laptop"}`)
	if product.Code != 201 {
		t.Fatalf("product %d %s", product.Code, product.Body.String())
	}
	var productBody map[string]any
	_ = json.Unmarshal(product.Body.Bytes(), &productBody)
	linkedEdit := httpJSON(t, handler, http.MethodPatch, detailPath, ownerToken, `{"form_data":{"jc_product":"`+productBody["id"].(string)+`"}}`)
	if linkedEdit.Code != 200 {
		t.Fatalf("linked edit %d %s", linkedEdit.Code, linkedEdit.Body.String())
	}
	linkedDetail := httpJSON(t, handler, http.MethodGet, detailPath, ownerToken, "")
	if linkedDetail.Code != 200 || !strings.Contains(linkedDetail.Body.String(), `"label":"Laptop"`) {
		t.Fatalf("linked display %d %s", linkedDetail.Code, linkedDetail.Body.String())
	}
	operator := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/users", ownerToken, `{"name":"Operator","email":"operator@example.test","role":"USER"}`)
	if operator.Code != 201 {
		t.Fatalf("operator create %d %s", operator.Code, operator.Body.String())
	}
	var operatorBody map[string]any
	_ = json.Unmarshal(operator.Body.Bytes(), &operatorBody)
	operatorID := operatorBody["id"].(string)
	operatorToken := activateAccount(t, handler, "operator@example.test", operator, "operator password secure 3")
	userMasterRead := httpJSON(t, handler, http.MethodGet, "/api/v1/companies/"+companyID+"/products", operatorToken, "")
	if userMasterRead.Code != 200 {
		t.Fatalf("USER master read %d %s", userMasterRead.Code, userMasterRead.Body.String())
	}
	userMasterWrite := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/charges", operatorToken, `{"profile_id":"`+jobCardID+`","name":"Unauthorized"}`)
	if userMasterWrite.Code != 403 {
		t.Fatalf("USER master write=%d %s", userMasterWrite.Code, userMasterWrite.Body.String())
	}
	userTransaction := httpJSON(t, handler, http.MethodPost, requestPath, operatorToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-20","customer_id":"`+customerID+`","form_data":{"jc_servicetype":"In-Person","jc_complaint":"USER transaction"}}`)
	if userTransaction.Code != 201 {
		t.Fatalf("USER transaction %d %s", userTransaction.Code, userTransaction.Body.String())
	}
	rootTransaction := httpJSON(t, handler, http.MethodGet, requestPath, rootToken, "")
	if rootTransaction.Code != 403 {
		t.Fatalf("SUPER_ADMIN transaction access=%d %s", rootTransaction.Code, rootTransaction.Body.String())
	}
	secondCompany := httpJSON(t, handler, http.MethodPost, "/api/v1/companies", rootToken, `{"name":"Beta","admin":{"name":"Beta Owner","email":"beta@example.test"}}`)
	if secondCompany.Code != 201 {
		t.Fatalf("second company %d %s", secondCompany.Code, secondCompany.Body.String())
	}
	var secondCompanyBody map[string]any
	_ = json.Unmarshal(secondCompany.Body.Bytes(), &secondCompanyBody)
	secondCompanyID := secondCompanyBody["company"].(map[string]any)["id"].(string)
	betaToken := activateAccount(t, handler, "beta@example.test", secondCompany, "beta owner password 4")
	betaProfiles := httpJSON(t, handler, http.MethodGet, "/api/v1/companies/"+secondCompanyID+"/service-profiles", betaToken, "")
	var betaProfileList struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(betaProfiles.Body.Bytes(), &betaProfileList)
	var betaJobCardID string
	for _, profile := range betaProfileList.Items {
		if profile["prefix"] == "A" {
			betaJobCardID = profile["id"].(string)
		}
	}
	betaCustomer := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+secondCompanyID+"/customers", betaToken, `{"name":"Beta Customer","contact":"777"}`)
	var betaCustomerBody map[string]any
	_ = json.Unmarshal(betaCustomer.Body.Bytes(), &betaCustomerBody)
	betaCustomerID := betaCustomerBody["id"].(string)
	betaProduct := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+secondCompanyID+"/products", betaToken, `{"profile_id":"`+betaJobCardID+`","name":"Beta Laptop"}`)
	var betaProductBody map[string]any
	_ = json.Unmarshal(betaProduct.Body.Bytes(), &betaProductBody)
	betaRequest := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+secondCompanyID+"/service-requests", betaToken, `{"profile_id":"`+betaJobCardID+`","service_date":"2026-09-20","customer_id":"`+betaCustomerID+`","form_data":{"jc_servicetype":"In-Person","jc_complaint":"Beta request"}}`)
	if betaRequest.Code != 201 || !strings.Contains(betaRequest.Body.String(), `"request_no":"A1001"`) {
		t.Fatalf("tenant-independent numbering %d %s", betaRequest.Code, betaRequest.Body.String())
	}
	crossPath := httpJSON(t, handler, http.MethodGet, "/api/v1/companies/"+secondCompanyID+"/customers", ownerToken, "")
	if crossPath.Code != 403 {
		t.Fatalf("cross-tenant path access=%d %s", crossPath.Code, crossPath.Body.String())
	}
	crossBody := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-20","customer_id":"`+betaCustomerID+`","form_data":{"jc_servicetype":"In-Person","jc_complaint":"Cross tenant"}}`)
	if crossBody.Code != 404 {
		t.Fatalf("cross-tenant body reference=%d %s", crossBody.Code, crossBody.Body.String())
	}
	crossQuery := httpJSON(t, handler, http.MethodGet, requestPath+"?customer_id="+betaCustomerID, ownerToken, "")
	if crossQuery.Code != 200 || !strings.Contains(crossQuery.Body.String(), `"total":0`) {
		t.Fatalf("cross-tenant query=%d %s", crossQuery.Code, crossQuery.Body.String())
	}
	historyBeforeInvalid := httpJSON(t, handler, http.MethodGet, detailPath+"/history", ownerToken, "")
	var invalidBefore struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(historyBeforeInvalid.Body.Bytes(), &invalidBefore)
	crossDynamic := httpJSON(t, handler, http.MethodPatch, detailPath, ownerToken, `{"form_data":{"jc_product":"`+betaProductBody["id"].(string)+`"}}`)
	if crossDynamic.Code != 400 || !strings.Contains(crossDynamic.Body.String(), "jc_product") {
		t.Fatalf("cross-tenant dynamic reference=%d %s", crossDynamic.Code, crossDynamic.Body.String())
	}
	historyAfterInvalid := httpJSON(t, handler, http.MethodGet, detailPath+"/history", ownerToken, "")
	var invalidAfter struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(historyAfterInvalid.Body.Bytes(), &invalidAfter)
	if invalidAfter.Total != invalidBefore.Total {
		t.Fatalf("invalid edit wrote history: %d -> %d", invalidBefore.Total, invalidAfter.Total)
	}
	productRelabel := httpJSON(t, handler, http.MethodPatch, "/api/v1/companies/"+companyID+"/products/"+productBody["id"].(string), ownerToken, `{"name":"Laptop Renamed","status":"DISCONTINUED"}`)
	if productRelabel.Code != 200 {
		t.Fatalf("product relabel/archive %d %s", productRelabel.Code, productRelabel.Body.String())
	}
	fieldPath := "/api/v1/companies/" + companyID + "/service-profiles/" + jobCardID + "/fields/" + productFieldID
	historicalTypeChange := httpJSON(t, handler, http.MethodPatch, fieldPath, ownerToken, `{"type":"text","config":{}}`)
	if historicalTypeChange.Code != 409 {
		t.Fatalf("historical field retyped=%d %s", historicalTypeChange.Code, historicalTypeChange.Body.String())
	}
	immutableKey := httpJSON(t, handler, http.MethodPatch, fieldPath, ownerToken, `{"key":"changed"}`)
	if immutableKey.Code != 400 {
		t.Fatalf("immutable field key=%d %s", immutableKey.Code, immutableKey.Body.String())
	}
	disableField := httpJSON(t, handler, http.MethodDelete, fieldPath, ownerToken, "")
	if disableField.Code != 204 {
		t.Fatalf("disable field=%d %s", disableField.Code, disableField.Body.String())
	}
	historicalDetail := httpJSON(t, handler, http.MethodGet, detailPath, ownerToken, "")
	if historicalDetail.Code != 200 || !strings.Contains(historicalDetail.Body.String(), `"label":"Laptop Renamed"`) || !strings.Contains(historicalDetail.Body.String(), `"selectable":false`) {
		t.Fatalf("historical linked value %d %s", historicalDetail.Code, historicalDetail.Body.String())
	}
	disabledFieldEdit := httpJSON(t, handler, http.MethodPatch, detailPath, ownerToken, `{"form_data":{"jc_product":"`+productBody["id"].(string)+`"}}`)
	if disabledFieldEdit.Code != 400 || !strings.Contains(disabledFieldEdit.Body.String(), "jc_product") {
		t.Fatalf("disabled field accepted=%d %s", disabledFieldEdit.Code, disabledFieldEdit.Body.String())
	}
	standbyPath := "/api/v1/companies/" + companyID + "/standby-items"
	standby := httpJSON(t, handler, http.MethodPost, standbyPath, ownerToken, `{"name":"Loaner Laptop","serial_no":"SL-1","price":"500.00"}`)
	if standby.Code != 201 || !strings.Contains(standby.Body.String(), `"price":"500.00"`) {
		t.Fatalf("standby create %d %s", standby.Code, standby.Body.String())
	}
	var standbyBody map[string]any
	_ = json.Unmarshal(standby.Body.Bytes(), &standbyBody)
	standbyID := standbyBody["id"].(string)
	issuedMaster := httpJSON(t, handler, http.MethodPatch, standbyPath+"/"+standbyID, ownerToken, `{"status":"ISSUED"}`)
	if issuedMaster.Code != 409 {
		t.Fatalf("master set issued=%d %s", issuedMaster.Code, issuedMaster.Body.String())
	}
	mismatchProduct := httpJSON(t, handler, http.MethodPost, "/api/v1/companies/"+companyID+"/products", ownerToken, `{"profile_id":"`+refillID+`","name":"Toner"}`)
	if mismatchProduct.Code != 201 {
		t.Fatalf("mismatch product %d %s", mismatchProduct.Code, mismatchProduct.Body.String())
	}
	var mismatchProductBody map[string]any
	_ = json.Unmarshal(mismatchProduct.Body.Bytes(), &mismatchProductBody)
	badIssue := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/issue", ownerToken, `{"customer_id":"`+customerID+`","service_request_id":"`+requestID+`","issued_date":"2026-09-20","received_product_id":"`+mismatchProductBody["id"].(string)+`"}`)
	if badIssue.Code != 409 {
		t.Fatalf("profile-mismatched standby issue=%d %s", badIssue.Code, badIssue.Body.String())
	}
	issued := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/issue", ownerToken, `{"customer_id":"`+customerID+`","service_request_id":"`+requestID+`","issued_date":"2026-09-20","due_date":"2026-09-25","notes":"Temporary device","received_product_id":"`+productBody["id"].(string)+`"}`)
	if issued.Code != 201 || !strings.Contains(issued.Body.String(), `"status":"ISSUED"`) || !strings.Contains(issued.Body.String(), `"notes":"Temporary device"`) {
		t.Fatalf("standby issue %d %s", issued.Code, issued.Body.String())
	}
	var issuedBody struct {
		Issue map[string]any `json:"issue"`
	}
	_ = json.Unmarshal(issued.Body.Bytes(), &issuedBody)
	firstIssueID := issuedBody.Issue["id"].(string)
	duplicateIssue := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/issue", ownerToken, `{"customer_id":"`+customerID+`","issued_date":"2026-09-21"}`)
	if duplicateIssue.Code != 409 {
		t.Fatalf("duplicate standby issue=%d %s", duplicateIssue.Code, duplicateIssue.Body.String())
	}
	standbyBlockedClose := httpJSON(t, handler, http.MethodPatch, detailPath+"/status", ownerToken, `{"status_id":"`+deliveredID+`"}`)
	if standbyBlockedClose.Code != 409 {
		t.Fatalf("standby-linked request close=%d %s", standbyBlockedClose.Code, standbyBlockedClose.Body.String())
	}
	// T36: a different user receives the item back.
	returned := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/return", operatorToken, `{"issue_id":"`+firstIssueID+`"}`)
	if returned.Code != 200 || !strings.Contains(returned.Body.String(), `"status":"AVAILABLE"`) || !strings.Contains(returned.Body.String(), `"returned_at":`) {
		t.Fatalf("standby return %d %s", returned.Code, returned.Body.String())
	}
	retryReturn := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/return", ownerToken, `{"issue_id":"`+firstIssueID+`"}`)
	if retryReturn.Code != 200 || !strings.Contains(retryReturn.Body.String(), `"status":"AVAILABLE"`) {
		t.Fatalf("standby return retry %d %s", retryReturn.Code, retryReturn.Body.String())
	}
	reissued := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/issue", ownerToken, `{"customer_id":"`+customerID+`","issued_date":"2026-09-26"}`)
	if reissued.Code != 201 {
		t.Fatalf("standby reissue %d %s", reissued.Code, reissued.Body.String())
	}
	var reissuedBody struct {
		Issue map[string]any `json:"issue"`
	}
	_ = json.Unmarshal(reissued.Body.Bytes(), &reissuedBody)
	secondIssueID := reissuedBody.Issue["id"].(string)
	oldReturnRetry := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/return", ownerToken, `{"issue_id":"`+firstIssueID+`"}`)
	if oldReturnRetry.Code != 200 || !strings.Contains(oldReturnRetry.Body.String(), `"status":"ISSUED"`) {
		t.Fatalf("old return changed newer issue %d %s", oldReturnRetry.Code, oldReturnRetry.Body.String())
	}
	finalReturn := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+standbyID+"/return", ownerToken, `{"issue_id":"`+secondIssueID+`"}`)
	if finalReturn.Code != 200 || !strings.Contains(finalReturn.Body.String(), `"status":"AVAILABLE"`) {
		t.Fatalf("final standby return %d %s", finalReturn.Code, finalReturn.Body.String())
	}
	// T36: who lent and received each item, days out, and history on the linked record.
	itemIssues := httpJSON(t, handler, http.MethodGet, standbyPath+"/"+standbyID+"/issues", operatorToken, "")
	var itemIssuesBody struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(itemIssues.Body.Bytes(), &itemIssuesBody)
	if itemIssues.Code != 200 || len(itemIssuesBody.Items) != 2 {
		t.Fatalf("item issues %d %s", itemIssues.Code, itemIssues.Body.String())
	}
	first := itemIssuesBody.Items[1]
	if first["id"] != firstIssueID || first["issued_by"] == nil || first["returned_by"] != operatorID || first["returned_by_name"] != "Operator" || first["customer_contact"] != "98765 00555" || first["item_name"] != "Loaner Laptop" || first["request_no"] == nil || first["days_out"] == nil {
		t.Fatalf("issue history row %v", first)
	}
	if missingItem := httpJSON(t, handler, http.MethodGet, standbyPath+"/"+operatorID+"/issues", ownerToken, ""); missingItem.Code != 404 {
		t.Fatalf("issues for unknown item=%d", missingItem.Code)
	}
	issuesPath := "/api/v1/companies/" + companyID + "/standby-issues"
	for query, want := range map[string]int{"?contact=00555": 2, "?contact=9876500555&state=returned": 2, "?contact=00555&state=open": 0, "?contact=11111": 0, "?service_request_id=" + requestID: 1, "?standby_item_id=" + standbyID + "&order=asc": 2} {
		list := httpJSON(t, handler, http.MethodGet, issuesPath+query, operatorToken, "")
		var listBody struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		_ = json.Unmarshal(list.Body.Bytes(), &listBody)
		if list.Code != 200 || listBody.Total != want || len(listBody.Items) != want {
			t.Fatalf("standby issues %s: %d %s", query, list.Code, list.Body.String())
		}
	}
	for _, query := range []string{"?state=lost", "?customer_id=nope", "?order=sideways"} {
		if bad := httpJSON(t, handler, http.MethodGet, issuesPath+query, ownerToken, ""); bad.Code != 400 {
			t.Fatalf("standby issues %s=%d %s", query, bad.Code, bad.Body.String())
		}
	}
	standbyHistory := httpJSON(t, handler, http.MethodGet, detailPath+"/history?order=asc", operatorToken, "")
	var standbyHistoryBody struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(standbyHistory.Body.Bytes(), &standbyHistoryBody)
	standbyEvents := []map[string]any{}
	for _, entry := range standbyHistoryBody.Items {
		if entry["changed_by_name"] == nil {
			t.Fatalf("history without user name %v", entry)
		}
		if entry["field_key"] == "core.standby" {
			standbyEvents = append(standbyEvents, entry)
		}
	}
	// Only the first issue was linked to the record; the reissue was not.
	if len(standbyEvents) != 2 {
		t.Fatalf("standby history events %s", standbyHistory.Body.String())
	}
	lent, back := standbyEvents[0]["new_value"].(map[string]any), standbyEvents[1]["new_value"].(map[string]any)
	if lent["event"] != "issued" || lent["days_out"] != nil || lent["customer_contact"] != "98765 00555" || back["event"] != "returned" || back["issue_id"] != firstIssueID || back["days_out"] == nil || standbyEvents[1]["changed_by"] != operatorID || standbyEvents[1]["changed_by_name"] != "Operator" {
		t.Fatalf("standby events %v", standbyEvents)
	}
	maintenance := httpJSON(t, handler, http.MethodPost, standbyPath, ownerToken, `{"name":"Maintenance Loaner"}`)
	if maintenance.Code != 201 {
		t.Fatalf("maintenance standby create %d %s", maintenance.Code, maintenance.Body.String())
	}
	var maintenanceBody map[string]any
	_ = json.Unmarshal(maintenance.Body.Bytes(), &maintenanceBody)
	maintenanceID := maintenanceBody["id"].(string)
	maintenanceSet := httpJSON(t, handler, http.MethodPatch, standbyPath+"/"+maintenanceID, ownerToken, `{"status":"UNDER_MAINTENANCE"}`)
	if maintenanceSet.Code != 200 {
		t.Fatalf("standby maintenance %d %s", maintenanceSet.Code, maintenanceSet.Body.String())
	}
	maintenanceIssue := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+maintenanceID+"/issue", ownerToken, `{"customer_id":"`+customerID+`","issued_date":"2026-09-20"}`)
	if maintenanceIssue.Code != 409 {
		t.Fatalf("maintenance standby issued=%d %s", maintenanceIssue.Code, maintenanceIssue.Body.String())
	}
	concurrentItem := httpJSON(t, handler, http.MethodPost, standbyPath, ownerToken, `{"name":"Concurrent Loaner"}`)
	var concurrentItemBody map[string]any
	_ = json.Unmarshal(concurrentItem.Body.Bytes(), &concurrentItemBody)
	concurrentItemID := concurrentItemBody["id"].(string)
	issueCodes := make(chan int, 2)
	var issueWG sync.WaitGroup
	for range 2 {
		issueWG.Add(1)
		go func() {
			defer issueWG.Done()
			response := httpJSON(t, handler, http.MethodPost, standbyPath+"/"+concurrentItemID+"/issue", ownerToken, `{"customer_id":"`+customerID+`","issued_date":"2026-09-27"}`)
			issueCodes <- response.Code
		}()
	}
	issueWG.Wait()
	close(issueCodes)
	createdIssues, rejectedIssues := 0, 0
	for code := range issueCodes {
		if code == http.StatusCreated {
			createdIssues++
		} else if code == http.StatusConflict {
			rejectedIssues++
		}
	}
	if createdIssues != 1 || rejectedIssues != 1 {
		t.Fatalf("concurrent standby issue results: created=%d conflict=%d", createdIssues, rejectedIssues)
	}
	history := httpJSON(t, handler, http.MethodGet, detailPath+"/history?order=asc", ownerToken, "")
	if history.Code != 200 || !strings.Contains(history.Body.String(), "core.service_date") || !strings.Contains(history.Body.String(), "jc_complaint") {
		t.Fatalf("history %d %s", history.Code, history.Body.String())
	}
	closed := httpJSON(t, handler, http.MethodPatch, detailPath+"/status", ownerToken, `{"status_id":"`+deliveredID+`"}`)
	if closed.Code != 200 {
		t.Fatalf("close %d %s", closed.Code, closed.Body.String())
	}
	closedEdit := httpJSON(t, handler, http.MethodPatch, detailPath, ownerToken, `{"form_data":{"jc_complaint":"Forbidden edit"}}`)
	if closedEdit.Code != 409 {
		t.Fatalf("closed edit=%d %s", closedEdit.Code, closedEdit.Body.String())
	}
	userReopen := httpJSON(t, handler, http.MethodPatch, detailPath+"/status", operatorToken, `{"status_id":"`+pendingID+`"}`)
	if userReopen.Code != 403 {
		t.Fatalf("USER reopened closed request=%d %s", userReopen.Code, userReopen.Body.String())
	}
	reopened := httpJSON(t, handler, http.MethodPatch, detailPath+"/status", ownerToken, `{"status_id":"`+pendingID+`"}`)
	if reopened.Code != 200 {
		t.Fatalf("admin reopen %d %s", reopened.Code, reopened.Body.String())
	}
	blockedClose := httpJSON(t, handler, http.MethodPatch, requestPath+"/"+inlineID+"/status", ownerToken, `{"status_id":"`+deliveredID+`"}`)
	if blockedClose.Code != 409 {
		t.Fatalf("outstanding dispatch close=%d %s", blockedClose.Code, blockedClose.Body.String())
	}
	outEntry := inlineBody["out_store_entry"].(map[string]any)
	outID := outEntry["id"].(string)
	outPath := "/api/v1/companies/" + companyID + "/out-store-entries"
	outList := httpJSON(t, handler, http.MethodGet, outPath+"?status=SENT&order=asc", ownerToken, "")
	if outList.Code != 200 || !strings.Contains(outList.Body.String(), outID) {
		t.Fatalf("out list %d %s", outList.Code, outList.Body.String())
	}
	outEdit := httpJSON(t, handler, http.MethodPatch, outPath+"/"+outID, ownerToken, `{"due_date":"2026-09-21","price":"125.50","remarks":"Vendor diagnosing"}`)
	if outEdit.Code != 200 || !strings.Contains(outEdit.Body.String(), "Vendor diagnosing") {
		t.Fatalf("out edit %d %s", outEdit.Code, outEdit.Body.String())
	}
	received := httpJSON(t, handler, http.MethodPost, outPath+"/"+outID+"/receive-back", ownerToken, `{}`)
	if received.Code != 200 || !strings.Contains(received.Body.String(), "RECEIVED_BACK") {
		t.Fatalf("receive %d %s", received.Code, received.Body.String())
	}
	historyBefore := httpJSON(t, handler, http.MethodGet, requestPath+"/"+inlineID+"/history", ownerToken, "")
	var before struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(historyBefore.Body.Bytes(), &before)
	retry := httpJSON(t, handler, http.MethodPost, outPath+"/"+outID+"/receive-back", ownerToken, `{}`)
	if retry.Code != 200 {
		t.Fatalf("receive retry %d %s", retry.Code, retry.Body.String())
	}
	historyAfter := httpJSON(t, handler, http.MethodGet, requestPath+"/"+inlineID+"/history", ownerToken, "")
	var after struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(historyAfter.Body.Bytes(), &after)
	if after.Total != before.Total {
		t.Fatalf("receive retry added history: %d -> %d", before.Total, after.Total)
	}
	// T35: dispatch, edit and receive-back are in the record history, after the status changes.
	outHistory := httpJSON(t, handler, http.MethodGet, requestPath+"/"+inlineID+"/history?order=asc", ownerToken, "")
	var outHistoryBody struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(outHistory.Body.Bytes(), &outHistoryBody)
	outEvents := []map[string]any{}
	for _, entry := range outHistoryBody.Items {
		if entry["field_key"] == "core.out_store" {
			outEvents = append(outEvents, entry)
		}
	}
	if first := outHistoryBody.Items[0]; first["field_key"] != "core.status" || first["old_value"] != nil {
		t.Fatalf("record creation should be the first history row, got %v", first)
	}
	if len(outEvents) != 3 {
		t.Fatalf("out-store history events %s", outHistory.Body.String())
	}
	sentEvent, changedEvent, receivedEvent := outEvents[0]["new_value"].(map[string]any), outEvents[1], outEvents[2]["new_value"].(map[string]any)
	changedNew, changedOld := changedEvent["new_value"].(map[string]any), changedEvent["old_value"].(map[string]any)
	if sentEvent["event"] != "sent" || sentEvent["entry_id"] != outID || sentEvent["shop_name"] == nil ||
		changedNew["event"] != "changed" || changedNew["remarks"] != "Vendor diagnosing" || changedNew["price"] != "125.50" || changedOld["remarks"] == "Vendor diagnosing" || changedOld["event"] != nil ||
		receivedEvent["event"] != "received" || receivedEvent["received_back_at"] == nil {
		t.Fatalf("out-store events %v", outEvents)
	}
	if last := outHistoryBody.Items[len(outHistoryBody.Items)-1]; last["field_key"] != "core.out_store" {
		t.Fatalf("receive event should follow the status change, got %v", last)
	}
	completedEdit := httpJSON(t, handler, http.MethodPatch, outPath+"/"+outID, ownerToken, `{"remarks":"late edit"}`)
	if completedEdit.Code != 409 {
		t.Fatalf("completed entry edit=%d", completedEdit.Code)
	}
	redispatch := httpJSON(t, handler, http.MethodPost, outPath, ownerToken, `{"service_request_id":"`+inlineID+`","shop_id":"`+shopBody["id"].(string)+`","sent_date":"2026-09-22"}`)
	if redispatch.Code != 201 {
		t.Fatalf("redispatch %d %s", redispatch.Code, redispatch.Body.String())
	}
	var redispatchBody map[string]any
	_ = json.Unmarshal(redispatch.Body.Bytes(), &redispatchBody)
	historyCount := func() int {
		var body struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(httpJSON(t, handler, http.MethodGet, requestPath+"/"+inlineID+"/history", ownerToken, "").Body.Bytes(), &body)
		return body.Total
	}
	beforeNoop := historyCount()
	if noop := httpJSON(t, handler, http.MethodPatch, outPath+"/"+redispatchBody["id"].(string), ownerToken, `{"remarks":null}`); noop.Code != 200 {
		t.Fatalf("no-op out-store edit %d %s", noop.Code, noop.Body.String())
	}
	if after := historyCount(); after != beforeNoop {
		t.Fatalf("no-op out-store edit added history: %d -> %d", beforeNoop, after)
	}
	duplicate := httpJSON(t, handler, http.MethodPost, outPath, ownerToken, `{"service_request_id":"`+inlineID+`","shop_id":"`+shopBody["id"].(string)+`","sent_date":"2026-09-23"}`)
	if duplicate.Code != 409 {
		t.Fatalf("duplicate dispatch=%d %s", duplicate.Code, duplicate.Body.String())
	}
	mappingBlocked := httpJSON(t, handler, http.MethodPut, "/api/v1/companies/"+companyID+"/service-profiles/"+jobCardID+"/out-store-mapping", ownerToken, `{"sent_status_id":"`+pendingID+`","received_status_id":"`+deliveredID+`"}`)
	if mappingBlocked.Code != 409 {
		t.Fatalf("mapping changed with open dispatch=%d %s", mappingBlocked.Code, mappingBlocked.Body.String())
	}
	disableBlocked := httpJSON(t, handler, http.MethodPatch, "/api/v1/companies/"+companyID+"/service-profiles/"+jobCardID, ownerToken, `{"out_store_enabled":false}`)
	if disableBlocked.Code != 409 {
		t.Fatalf("Out-Store disabled with open dispatch=%d %s", disableBlocked.Code, disableBlocked.Body.String())
	}
	var wg sync.WaitGroup
	numbers := make(chan string, 10)
	failures := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := httpJSON(t, handler, http.MethodPost, requestPath, ownerToken, `{"profile_id":"`+jobCardID+`","service_date":"2026-09-18","customer_id":"`+customerID+`","form_data":{"jc_servicetype":"In-Person","jc_complaint":"Concurrent"}}`)
			if response.Code != 201 {
				failures <- response.Body.String()
				return
			}
			var body map[string]any
			_ = json.Unmarshal(response.Body.Bytes(), &body)
			numbers <- body["request_no"].(string)
		}()
	}
	wg.Wait()
	close(numbers)
	close(failures)
	if failure := <-failures; failure != "" {
		t.Fatalf("concurrent create failed: %s", failure)
	}
	seen := map[string]bool{}
	for number := range numbers {
		if seen[number] {
			t.Fatalf("duplicate request number %s", number)
		}
		seen[number] = true
	}
	if len(seen) != 10 {
		t.Fatalf("created %d concurrent requests", len(seen))
	}
	forbidden := httpJSON(t, handler, http.MethodPatch, "/api/v1/companies/"+companyID, ownerToken, `{"status":"SUSPENDED"}`)
	if forbidden.Code != 403 {
		t.Fatalf("admin changed status: %d", forbidden.Code)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='INACTIVE' WHERE id=$1`, operatorID); err != nil {
		t.Fatal(err)
	}
	inactiveUser := httpJSON(t, handler, http.MethodGet, "/api/v1/companies/"+companyID+"/products", operatorToken, "")
	if inactiveUser.Code != 401 {
		t.Fatalf("inactive USER access=%d %s", inactiveUser.Code, inactiveUser.Body.String())
	}
}

// activateAccount signs in with the temporary password returned when no email
// was sent (T28), sets the user's own password and returns the session token.
func activateAccount(t *testing.T, h http.Handler, email string, created *httptest.ResponseRecorder, newPassword string) string {
	t.Helper()
	var body struct {
		Delivery struct {
			EmailSent         bool   `json:"email_sent"`
			TemporaryPassword string `json:"temporary_password"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || body.Delivery.EmailSent || body.Delivery.TemporaryPassword == "" {
		t.Fatalf("expected an undelivered temporary password: %s", created.Body.String())
	}
	token := httpLogin(t, h, email, body.Delivery.TemporaryPassword)
	if r := httpJSON(t, h, http.MethodGet, "/api/v1/dashboard", token, ""); r.Code != http.StatusForbidden {
		t.Fatalf("temporary password session was not limited: %d %s", r.Code, r.Body.String())
	}
	change := httpJSON(t, h, http.MethodPost, "/api/v1/auth/change-password", token, `{"current_password":"`+body.Delivery.TemporaryPassword+`","new_password":"`+newPassword+`"}`)
	if change.Code != http.StatusNoContent {
		t.Fatalf("change password %d %s", change.Code, change.Body.String())
	}
	return token
}

func httpLogin(t *testing.T, h http.Handler, email, password string) string {
	t.Helper()
	r := httpJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", `{"email":"`+email+`","password":"`+password+`"}`)
	if r.Code != 200 {
		t.Fatalf("login %d %s", r.Code, r.Body.String())
	}
	var body service.LoginResult
	_ = json.Unmarshal(r.Body.Bytes(), &body)
	return body.AccessToken
}
func httpJSON(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}
