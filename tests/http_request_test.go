package tests

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"serviceops360/api/internal/httpapi"
	"serviceops360/api/internal/service"
)

func TestJSONRequestBoundary(t *testing.T) {
	hash, err := service.HashPassword("correct horse battery 42")
	if err != nil {
		t.Fatal(err)
	}
	companyID := "20000000-0000-4000-8000-000000000002"
	store := &handlerAuthStore{passwordHash: hash, identity: service.Identity{
		User:    service.User{ID: "10000000-0000-4000-8000-000000000001", CompanyID: &companyID, Role: "ADMIN", Status: "ACTIVE"},
		Company: &service.Company{ID: companyID, Name: "Company", Status: "ACTIVE"},
	}}
	auth, err := service.NewAuthService(store, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute, EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, auth)
	for _, tc := range []struct {
		name, contentType, body, code string
		status                        int
	}{
		{"valid", "application/json; charset=utf-8", `{"email":"admin@example.test","password":"correct horse battery 42"}`, "", 200},
		{"wrong content type", "text/plain", `{}`, "VALIDATION_ERROR", 400},
		{"empty", "application/json", ``, "INVALID_JSON", 400},
		{"array", "application/json", `[]`, "INVALID_JSON", 400},
		{"unknown field", "application/json", `{"email":"admin@example.test","password":"correct horse battery 42","company_id":"foreign"}`, "INVALID_JSON", 400},
		{"multiple objects", "application/json", `{} {}`, "INVALID_JSON", 400},
		{"malformed", "application/json", `{"email":`, "INVALID_JSON", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || (tc.code != "" && !strings.Contains(response.Body.String(), tc.code)) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestJSONRequestBoundaryRejectsStreamOverOneMiB(t *testing.T) {
	auth, err := service.NewAuthService(&handlerAuthStore{}, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute, EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, auth)
	body := `{"email":"admin@example.test","password":"` + strings.Repeat("x", 1<<20) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "PAYLOAD_TOO_LARGE") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}
