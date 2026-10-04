package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

func TestAuthStoreLifecycle(t *testing.T) {
	databaseURL := os.Getenv("SERVICEOPS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SERVICEOPS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "serviceops_auth_test_" + hex.EncodeToString(random)
	if _, err := base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = base.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`) }()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",pg_catalog"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, name := range []string{"000001_company_master_data.up.sql", "000002_service_transactions.up.sql", "000003_authentication.up.sql", "000004_user_phone.up.sql", "000005_must_change_password.up.sql", "000006_password_reset_tokens.up.sql", "000007_record_history_events.up.sql", "000008_customer_mobile_identity.up.sql", "000009_drop_company_code.up.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	passwordHash, err := service.HashPassword("correct horse battery 42")
	if err != nil {
		t.Fatal(err)
	}
	var companyID string
	if err := pool.QueryRow(ctx, `INSERT INTO companies(name) VALUES ('Company') RETURNING id::text`).Scan(&companyID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
        INSERT INTO users(company_id,name,email,password_hash,role)
        VALUES ($1,'Admin','admin@example.test',$2,'ADMIN')`, companyID, passwordHash); err != nil {
		t.Fatal(err)
	}
	store := repository.NewAuthStore(pool)
	if err := store.BootstrapSuperAdmin(ctx, "Root", "root@example.test", passwordHash); err != nil {
		t.Fatalf("bootstrap SUPER_ADMIN: %v", err)
	}
	if err := store.BootstrapSuperAdmin(ctx, "Second Root", "root2@example.test", passwordHash); err == nil {
		t.Fatal("second SUPER_ADMIN bootstrap unexpectedly succeeded")
	}
	auth, err := service.NewAuthService(store, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute,
		EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := auth.Login(ctx, "admin@example.test", "correct horse battery 42", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.Authenticate(ctx, "Bearer "+result.AccessToken)
	if err != nil || principal.Identity.User.CompanyID == nil || *principal.Identity.User.CompanyID != companyID {
		t.Fatalf("database-backed authentication failed: %+v %v", principal, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE companies SET status='SUSPENDED' WHERE id=$1`, companyID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, "Bearer "+result.AccessToken); !errors.Is(err, service.ErrCompanySuspended) {
		t.Fatalf("suspended company session accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE companies SET status='ACTIVE' WHERE id=$1`, companyID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='INACTIVE' WHERE email='admin@example.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, "Bearer "+result.AccessToken); !errors.Is(err, service.ErrUnauthenticated) {
		t.Fatalf("inactive user session accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='ACTIVE' WHERE email='admin@example.test'`); err != nil {
		t.Fatal(err)
	}
	if err := auth.Logout(ctx, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, "Bearer "+result.AccessToken); !errors.Is(err, service.ErrUnauthenticated) {
		t.Fatalf("revoked database session accepted: %v", err)
	}

	catalog := repository.NewCatalogStore(pool)
	onboarded, err := catalog.OnboardCompany(ctx, repository.CompanyOnboarding{
		Name: "Second Company", AdminName: "Owner", AdminEmail: "owner@example.test", PasswordHash: passwordHash,
	})
	if err != nil {
		t.Fatalf("onboard company: %v", err)
	}
	secondCompanyID := onboarded["company"].(map[string]any)["id"].(string)
	// T30: one blank "Service" profile with the default statuses, no preset
	// fields and no staff roles.
	profiles, err := catalog.List(ctx, "service-profiles", secondCompanyID, "")
	if err != nil || len(profiles) != 1 || profiles[0]["name"] != "Service" || profiles[0]["prefix"] != "S" || profiles[0]["out_store_enabled"] != false {
		t.Fatalf("default profiles=%+v err=%v", profiles, err)
	}
	serviceID := profiles[0]["id"].(string)
	fields, err := catalog.List(ctx, "fields", secondCompanyID, serviceID)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Service fields=%d err=%v", len(fields), err)
	}
	form, err := catalog.GetForm(ctx, secondCompanyID, serviceID)
	if err != nil || len(form["core_fields"].([]any)) != 3 || len(form["staff_roles"].([]any)) != 0 {
		t.Fatalf("form contract invalid: %+v err=%v", form, err)
	}
	assertDefaultStatuses(t, catalog, secondCompanyID, profiles[0])
	customer, err := catalog.Create(ctx, "customers", secondCompanyID, "", map[string]any{"name": "Customer", "contact": "123"})
	if _, numbered := customer["customer_no"]; err != nil || numbered {
		t.Fatalf("customer without number: %+v err=%v", customer, err)
	}
	roles, err := catalog.List(ctx, "staff-roles", secondCompanyID, "")
	if err != nil || len(roles) != 0 {
		t.Fatalf("staff roles=%d err=%v", len(roles), err)
	}
	role, err := catalog.Create(ctx, "staff-roles", secondCompanyID, "", map[string]any{"name": "Engineer"})
	if err != nil {
		t.Fatal(err)
	}
	staff, err := catalog.Create(ctx, "staff", secondCompanyID, "", map[string]any{
		"name": "Engineer", "contact": "555", "role_ids": []any{role["id"]},
	})
	if err != nil || len(staff["role_ids"].([]any)) != 1 {
		t.Fatalf("staff roles not assigned: %+v err=%v", staff, err)
	}
	custom, err := catalog.Create(ctx, "service-profiles", secondCompanyID, "", map[string]any{"name": "Custom", "prefix": "CU"})
	if err != nil {
		t.Fatal(err)
	}
	custom, err = catalog.Get(ctx, "service-profiles", secondCompanyID, "", custom["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	assertDefaultStatuses(t, catalog, secondCompanyID, custom)

	// T29: a reset link works once, sets the password, clears the temporary flag
	// and signs the user out; expired links are refused.
	reset, _, err := auth.RequestPasswordReset(ctx, "admin@example.test", "192.0.2.50")
	if err != nil || reset == nil {
		t.Fatalf("request reset: %+v %v", reset, err)
	}
	stale, _, err := auth.RequestPasswordReset(ctx, "admin@example.test", "192.0.2.50")
	if err != nil || stale == nil {
		t.Fatalf("second reset: %v", err)
	}
	if err := auth.ConfirmPasswordReset(ctx, reset.Token, "Reset password 77"); !errors.Is(err, service.ErrResetTokenInvalid) {
		t.Fatalf("earlier link should be cancelled by a newer request: %v", err)
	}
	if err := auth.ConfirmPasswordReset(ctx, stale.Token, "Reset password 77"); err != nil {
		t.Fatalf("confirm reset: %v", err)
	}
	if err := auth.ConfirmPasswordReset(ctx, stale.Token, "Reset password 78"); !errors.Is(err, service.ErrResetTokenInvalid) {
		t.Fatalf("used link accepted: %v", err)
	}
	if _, _, err := auth.Login(ctx, "admin@example.test", "Reset password 77", "192.0.2.51"); err != nil {
		t.Fatalf("login with reset password: %v", err)
	}
	expired, _, err := auth.RequestPasswordReset(ctx, "admin@example.test", "192.0.2.50")
	if err != nil || expired == nil {
		t.Fatalf("third reset: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE password_reset_tokens SET created_at=now()-interval '2 hours', expires_at=now()-interval '1 hour' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ConfirmPasswordReset(ctx, expired.Token, "Reset password 79"); !errors.Is(err, service.ErrResetTokenInvalid) {
		t.Fatalf("expired link accepted: %v", err)
	}
}

// assertDefaultStatuses checks the T31 statuses, their order, flags and the
// Out-Store mapping of a newly created profile.
func assertDefaultStatuses(t *testing.T, catalog *repository.CatalogStore, companyID string, profile map[string]any) {
	t.Helper()
	statuses, err := catalog.List(context.Background(), "statuses", companyID, profile["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name            string
		initial, closed bool
	}{{"Open", true, false}, {"In Progress", false, false}, {"Sent to Out-Store", false, false}, {"Received from Out-Store", false, false}, {"Closed", false, true}, {"Returned Not Repaired", false, true}}
	if len(statuses) != len(want) {
		t.Fatalf("default statuses=%+v", statuses)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i]["sort_order"].(float64) < statuses[j]["sort_order"].(float64) })
	byName := map[string]string{}
	for i, w := range want {
		got := statuses[i]
		if got["name"] != w.name || got["initial"] != w.initial || got["closed"] != w.closed || got["sort_order"] != float64(i) {
			t.Fatalf("status %d = %+v, want %+v", i, got, w)
		}
		byName[w.name] = got["id"].(string)
	}
	if profile["sent_status_id"] != byName["Sent to Out-Store"] || profile["received_status_id"] != byName["Received from Out-Store"] {
		t.Fatalf("Out-Store mapping %v/%v", profile["sent_status_id"], profile["received_status_id"])
	}
}
