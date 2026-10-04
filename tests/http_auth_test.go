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
	"serviceops360/api/internal/service"
)

type handlerAuthStore struct {
	identity     service.Identity
	passwordHash string
	revoked      bool
	emailCount   int
	ipCount      int
	resetHash    []byte
}

func (s *handlerAuthStore) AuthenticateAndCreateSession(_ context.Context, _ service.LoginIdentifier, password string, verify service.PasswordVerifier, _ string, _, _ time.Time) (service.Identity, error) {
	if !verify(s.passwordHash, password) {
		return service.Identity{}, service.ErrInvalidCredentials
	}
	return s.identity, nil
}

func (s *handlerAuthStore) LoadSessionIdentity(_ context.Context, _, _ string, _ time.Time) (service.Identity, error) {
	if s.revoked {
		return service.Identity{}, service.ErrUnauthenticated
	}
	return s.identity, nil
}

func (s *handlerAuthStore) RevokeSession(_ context.Context, _, _ string, _ time.Time) error {
	s.revoked = true
	return nil
}

func (s *handlerAuthStore) RateLimitCounts(context.Context, []byte, []byte, time.Time) (int, int, error) {
	return s.emailCount, s.ipCount, nil
}

func (s *handlerAuthStore) ChangePassword(_ context.Context, _, _, current string, verify service.PasswordVerifier, newHash string) error {
	if !verify(s.passwordHash, current) {
		return service.ErrInvalidCredentials
	}
	s.passwordHash, s.identity.User.MustChangePassword = newHash, false
	return nil
}

func (s *handlerAuthStore) CreatePasswordReset(_ context.Context, login service.LoginIdentifier, tokenHash []byte, _ time.Time) (service.PasswordResetUser, bool, error) {
	if login.Email != s.identity.User.Email {
		return service.PasswordResetUser{}, false, nil
	}
	s.resetHash = tokenHash
	return service.PasswordResetUser{Name: s.identity.User.Name, Email: s.identity.User.Email, Company: "Company"}, true, nil
}

func (s *handlerAuthStore) ConfirmPasswordReset(_ context.Context, tokenHash []byte, newHash string, _ time.Time) error {
	if s.resetHash == nil || string(tokenHash) != string(s.resetHash) {
		return service.ErrResetTokenInvalid
	}
	s.resetHash, s.passwordHash, s.revoked = nil, newHash, true
	return nil
}

func (s *handlerAuthStore) RecordLoginFailure(context.Context, []byte, []byte, time.Time, time.Time) (int, int, error) {
	s.emailCount++
	s.ipCount++
	return s.emailCount, s.ipCount, nil
}

func TestAuthenticationRoutes(t *testing.T) {
	passwordHash, err := service.HashPassword("correct horse battery 42")
	if err != nil {
		t.Fatal(err)
	}
	companyID := "20000000-0000-4000-8000-000000000002"
	store := &handlerAuthStore{
		passwordHash: passwordHash,
		identity: service.Identity{
			User:    service.User{ID: "10000000-0000-4000-8000-000000000001", CompanyID: &companyID, Name: "Admin", Email: "admin@example.test", Role: "ADMIN", Status: "ACTIVE"},
			Company: &service.Company{ID: companyID, Name: "Company", Status: "ACTIVE"},
		},
	}
	auth, err := service.NewAuthService(store, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute,
		EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, auth)

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"ADMIN@example.test","password":"correct horse battery 42"}`))
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "192.0.2.10:1234"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var result service.LoginResult
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &result); err != nil || result.AccessToken == "" {
		t.Fatalf("invalid login response: %+v %v", result, err)
	}

	me := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	me.Header.Set("Authorization", "Bearer "+result.AccessToken)
	meResponse := httptest.NewRecorder()
	handler.ServeHTTP(meResponse, me)
	// Identity must never expose the hash (must_change_password is an allowed flag).
	if meResponse.Code != http.StatusOK || strings.Contains(meResponse.Body.String(), "password_hash") || strings.Contains(meResponse.Body.String(), "$argon2") {
		t.Fatalf("me status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.Header.Set("Authorization", "Bearer "+result.AccessToken)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusNoContent || logoutResponse.Body.Len() != 0 {
		t.Fatalf("logout status=%d body=%q", logoutResponse.Code, logoutResponse.Body.String())
	}
	meResponse = httptest.NewRecorder()
	handler.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusUnauthorized || !strings.Contains(meResponse.Body.String(), "UNAUTHENTICATED") {
		t.Fatalf("revoked token status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}
}

func TestAuthenticationRouteMethodsAndErrors(t *testing.T) {
	store := &handlerAuthStore{}
	auth, err := service.NewAuthService(store, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute,
		EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, auth)

	for _, test := range []struct {
		method, path, body, authorization string
		status                            int
		code                              string
	}{
		{http.MethodGet, "/api/v1/auth/login", "", "", 405, "METHOD_NOT_ALLOWED"},
		{http.MethodGet, "/api/v1/auth/me", "", "", 401, "UNAUTHENTICATED"},
		{http.MethodPost, "/api/v1/auth/login", `{"email":"bad","password":"x"}`, "", 400, "VALIDATION_ERROR"},
		{http.MethodPost, "/api/v1/auth/login", `{"identifier":"12","password":"x"}`, "", 400, "identifier"},
		{http.MethodPost, "/api/v1/auth/login", `{"identifier":"98765 43210","password":"wrong"}`, "", 401, "AUTHENTICATION_FAILED"},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Authorization", test.authorization)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	store.emailCount = 5
	rateLimited := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"password"}`))
	rateLimited.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rateLimited)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" || !strings.Contains(response.Body.String(), "RATE_LIMITED") {
		t.Fatalf("rate limit status=%d retry=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
}
