package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"serviceops360/api/internal/httpapi"
	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

func TestGenerateTemporaryPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		password, err := service.GenerateTemporaryPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(password) != 12 || service.ValidateNewPassword(password) != nil || strings.ContainsAny(password, "0O1lI") {
			t.Fatalf("unexpected temporary password %q", password)
		}
		if seen[password] {
			t.Fatalf("temporary password repeated: %q", password)
		}
		seen[password] = true
	}
}

// A temporary-password session can only read the identity, sign out and change
// the password; changing it restores normal access.
func TestPasswordChangeRequired(t *testing.T) {
	hash, err := service.HashPassword("Temporary123")
	if err != nil {
		t.Fatal(err)
	}
	companyID := "20000000-0000-4000-8000-000000000002"
	store := &handlerAuthStore{passwordHash: hash, identity: service.Identity{
		User:    service.User{ID: "10000000-0000-4000-8000-000000000001", CompanyID: &companyID, Name: "New", Email: "new@example.test", Role: "ADMIN", Status: "ACTIVE", MustChangePassword: true},
		Company: &service.Company{ID: companyID, Name: "Company", Status: "ACTIVE"},
	}}
	auth, err := service.NewAuthService(store, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute, EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewApplicationHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second, auth, repository.NewCatalogStore(nil))
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	login := call(http.MethodPost, "/api/v1/auth/login", "", `{"identifier":"new@example.test","password":"Temporary123"}`)
	var result struct {
		AccessToken string       `json:"access_token"`
		User        service.User `json:"user"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &result); err != nil || login.Code != 200 || !result.User.MustChangePassword {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	token := result.AccessToken

	if response := call(http.MethodGet, "/api/v1/companies", token, ""); response.Code != 403 || !strings.Contains(response.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatalf("blocked route status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/api/v1/auth/me", token, ""); response.Code != 200 || !strings.Contains(response.Body.String(), `"must_change_password":true`) {
		t.Fatalf("me status=%d body=%s", response.Code, response.Body.String())
	}
	for _, tc := range []struct{ body, field string }{
		{`{"current_password":"wrong","new_password":"NewPassword9"}`, "current_password"},
		{`{"current_password":"Temporary123","new_password":"Temporary123"}`, "new_password"},
		{`{"current_password":"Temporary123","new_password":"onlyletters"}`, "new_password"},
	} {
		if response := call(http.MethodPost, "/api/v1/auth/change-password", token, tc.body); response.Code != 400 || !strings.Contains(response.Body.String(), tc.field) {
			t.Fatalf("change-password %s status=%d body=%s", tc.body, response.Code, response.Body.String())
		}
	}
	if response := call(http.MethodPost, "/api/v1/auth/change-password", token, `{"current_password":"Temporary123","new_password":"MyOwnPassword7"}`); response.Code != 204 {
		t.Fatalf("change-password status=%d body=%s", response.Code, response.Body.String())
	}
	// Normal authorization applies again: an ADMIN may not list companies.
	if response := call(http.MethodGet, "/api/v1/companies", token, ""); response.Code != 403 || strings.Contains(response.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatalf("after change status=%d body=%s", response.Code, response.Body.String())
	}
	if !service.VerifyPassword(store.passwordHash, "MyOwnPassword7") {
		t.Fatal("new password was not stored")
	}
}
