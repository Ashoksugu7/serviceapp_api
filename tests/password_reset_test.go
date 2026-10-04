package tests

import (
	"context"
	"errors"
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

func TestPasswordResetService(t *testing.T) {
	hash, err := service.HashPassword("correct horse battery 42")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAuthStore{identity: activeIdentity(), passwordHash: hash}
	auth := newTestAuth(t, store)
	ctx := context.Background()

	if reset, _, err := auth.RequestPasswordReset(ctx, "nobody@example.test", "192.0.2.1"); err != nil || reset != nil {
		t.Fatalf("unknown account: reset=%v err=%v", reset, err)
	}
	if _, _, err := auth.RequestPasswordReset(ctx, "not an email", "192.0.2.1"); !errors.Is(err, service.ErrValidation) {
		t.Fatalf("invalid identifier error = %v", err)
	}
	reset, _, err := auth.RequestPasswordReset(ctx, "ADMIN@example.test", "192.0.2.1")
	if err != nil || reset == nil || len(reset.Token) != 43 || reset.User.Email != "admin@example.test" {
		t.Fatalf("reset=%+v err=%v", reset, err)
	}

	var fields service.FieldErrors
	if err := auth.ConfirmPasswordReset(ctx, reset.Token, "onlyletters"); !errors.As(err, &fields) || fields["new_password"] == "" {
		t.Fatalf("weak password error = %v", err)
	}
	if err := auth.ConfirmPasswordReset(ctx, "not-a-token", "NewPassword9"); !errors.Is(err, service.ErrResetTokenInvalid) {
		t.Fatalf("malformed token error = %v", err)
	}
	if err := auth.ConfirmPasswordReset(ctx, reset.Token, "NewPassword9"); err != nil {
		t.Fatal(err)
	}
	if !service.VerifyPassword(store.passwordHash, "NewPassword9") {
		t.Fatal("new password not stored")
	}
	if err := auth.ConfirmPasswordReset(ctx, reset.Token, "AnotherPass9"); !errors.Is(err, service.ErrResetTokenInvalid) {
		t.Fatalf("reused token error = %v", err)
	}
}

func TestPasswordResetRateLimit(t *testing.T) {
	store := &fakeAuthStore{identity: activeIdentity()}
	auth := newTestAuth(t, store)
	for i := range 5 {
		if _, _, err := auth.RequestPasswordReset(context.Background(), "admin@example.test", "192.0.2.1"); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if _, retry, err := auth.RequestPasswordReset(context.Background(), "admin@example.test", "192.0.2.1"); !errors.Is(err, service.ErrRateLimited) || retry <= 0 {
		t.Fatalf("sixth request error=%v retry=%v", err, retry)
	}
}

// Known and unknown accounts get identical answers.
func TestPasswordResetRoutes(t *testing.T) {
	store := &handlerAuthStore{identity: service.Identity{User: service.User{ID: "10000000-0000-4000-8000-000000000001", Name: "Admin", Email: "admin@example.test", Role: "SUPER_ADMIN", Status: "ACTIVE"}}}
	auth, err := service.NewAuthService(store, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute, EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewApplicationHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second, auth, repository.NewCatalogStore(nil))
	post := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	known := post("/api/v1/auth/password-reset/request", `{"identifier":"admin@example.test"}`)
	unknown := post("/api/v1/auth/password-reset/request", `{"identifier":"nobody@example.test"}`)
	if known.Code != 202 || unknown.Code != 202 || known.Body.String() != unknown.Body.String() {
		t.Fatalf("known=%d %s unknown=%d %s", known.Code, known.Body.String(), unknown.Code, unknown.Body.String())
	}
	if store.resetHash == nil {
		t.Fatal("known account did not get a token")
	}
	if response := post("/api/v1/auth/password-reset/request", `{"identifier":"12"}`); response.Code != 400 || !strings.Contains(response.Body.String(), "identifier") {
		t.Fatalf("invalid identifier status=%d body=%s", response.Code, response.Body.String())
	}
	if response := post("/api/v1/auth/password-reset/confirm", `{"token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","new_password":"NewPassword9"}`); response.Code != 400 || !strings.Contains(response.Body.String(), "RESET_TOKEN_INVALID") {
		t.Fatalf("invalid token status=%d body=%s", response.Code, response.Body.String())
	}
}
