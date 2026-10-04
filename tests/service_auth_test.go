package tests

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "serviceops360/api/internal/service"
)

const (
	testUserID    = "10000000-0000-4000-8000-000000000001"
	testCompanyID = "20000000-0000-4000-8000-000000000002"
)

type fakeAuthStore struct {
	identity            Identity
	passwordHash        string
	emailCount, ipCount int
	loadErr             error
	revoked             bool
	loginEmail          string
	loginPhone          string
	resetHash           []byte
	emailKey, ipKey     []byte
}

func (f *fakeAuthStore) AuthenticateAndCreateSession(_ context.Context, login LoginIdentifier, password string, verify PasswordVerifier, _ string, _, _ time.Time) (Identity, error) {
	f.loginEmail = login.Email
	f.loginPhone = login.Phone
	if !verify(f.passwordHash, password) || f.identity.User.Status != "ACTIVE" {
		return Identity{}, ErrInvalidCredentials
	}
	return f.identity, nil
}

func (f *fakeAuthStore) LoadSessionIdentity(_ context.Context, _, _ string, _ time.Time) (Identity, error) {
	if f.loadErr != nil {
		return Identity{}, f.loadErr
	}
	if f.revoked {
		return Identity{}, ErrUnauthenticated
	}
	return f.identity, nil
}

func (f *fakeAuthStore) RevokeSession(_ context.Context, _, _ string, _ time.Time) error {
	f.revoked = true
	return nil
}

func (f *fakeAuthStore) RateLimitCounts(_ context.Context, emailKey, ipKey []byte, _ time.Time) (int, int, error) {
	f.emailKey, f.ipKey = emailKey, ipKey
	return f.emailCount, f.ipCount, nil
}

func (f *fakeAuthStore) ChangePassword(_ context.Context, _, _, current string, verify PasswordVerifier, newHash string) error {
	if !verify(f.passwordHash, current) {
		return ErrInvalidCredentials
	}
	f.passwordHash, f.identity.User.MustChangePassword = newHash, false
	return nil
}

func (f *fakeAuthStore) CreatePasswordReset(_ context.Context, login LoginIdentifier, tokenHash []byte, _ time.Time) (PasswordResetUser, bool, error) {
	if login.Email != f.identity.User.Email {
		return PasswordResetUser{}, false, nil
	}
	f.resetHash = tokenHash
	return PasswordResetUser{Name: f.identity.User.Name, Email: f.identity.User.Email, Company: "Company"}, true, nil
}

func (f *fakeAuthStore) ConfirmPasswordReset(_ context.Context, tokenHash []byte, newHash string, _ time.Time) error {
	if f.resetHash == nil || string(tokenHash) != string(f.resetHash) {
		return ErrResetTokenInvalid
	}
	f.resetHash, f.passwordHash = nil, newHash
	return nil
}

func (f *fakeAuthStore) RecordLoginFailure(_ context.Context, emailKey, ipKey []byte, _, _ time.Time) (int, int, error) {
	f.emailKey, f.ipKey = emailKey, ipKey
	f.emailCount++
	f.ipCount++
	return f.emailCount, f.ipCount, nil
}

func newTestAuth(t *testing.T, store *fakeAuthStore) *AuthService {
	t.Helper()
	auth, err := NewAuthService(store, AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360",
		Audience: "serviceops360-api", TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second,
		RateWindow: 15 * time.Minute, EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func activeIdentity() Identity {
	companyID := testCompanyID
	return Identity{
		User:    User{ID: testUserID, CompanyID: &companyID, Name: "Admin", Email: "admin@example.test", Role: "ADMIN", Status: "ACTIVE"},
		Company: &Company{ID: companyID, Name: "Company", Status: "ACTIVE"},
	}
}

func TestLoginAuthenticateLogoutAndClaimRecheck(t *testing.T) {
	hash, err := HashPassword("correct horse battery 42")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAuthStore{identity: activeIdentity(), passwordHash: hash}
	auth := newTestAuth(t, store)
	result, _, err := auth.Login(context.Background(), " ADMIN@EXAMPLE.TEST ", "correct horse battery 42", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if store.loginEmail != "admin@example.test" || result.TokenType != "Bearer" || result.ExpiresIn != 1800 {
		t.Fatalf("unexpected login result: %+v email=%q", result, store.loginEmail)
	}
	if strings.Contains(result.AccessToken, "correct horse") || len(store.emailKey) != 32 || len(store.ipKey) != 32 {
		t.Fatal("login leaked credentials or did not HMAC rate-limit keys")
	}
	principal, err := auth.Authenticate(context.Background(), "Bearer "+result.AccessToken)
	if err != nil || principal.Identity.User.ID != testUserID {
		t.Fatalf("authenticate failed: %+v %v", principal, err)
	}
	store.identity.User.Role = "USER"
	if _, err := auth.Authenticate(context.Background(), "Bearer "+result.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("stale role claim accepted: %v", err)
	}
	store.identity.User.Role = "ADMIN"
	if err := auth.Logout(context.Background(), principal); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(context.Background(), "Bearer "+result.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session accepted: %v", err)
	}
}

func TestLoginFailureAndRateLimit(t *testing.T) {
	hash, err := HashPassword("correct horse battery 42")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAuthStore{identity: activeIdentity(), passwordHash: hash}
	auth := newTestAuth(t, store)
	for attempt := 1; attempt <= 5; attempt++ {
		_, _, err := auth.Login(context.Background(), "admin@example.test", "wrong password", "192.0.2.1")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d returned %v", attempt, err)
		}
	}
	_, retry, err := auth.Login(context.Background(), "admin@example.test", "correct horse battery 42", "192.0.2.1")
	if !errors.Is(err, ErrRateLimited) || retry <= 0 || retry > 15*time.Minute {
		t.Fatalf("rate limit = %v retry=%v", err, retry)
	}
}

func TestTokenTamperingSuspensionAndTenantAuthorization(t *testing.T) {
	hash, _ := HashPassword("correct horse battery 42")
	store := &fakeAuthStore{identity: activeIdentity(), passwordHash: hash}
	auth := newTestAuth(t, store)
	result, _, err := auth.Login(context.Background(), "admin@example.test", "correct horse battery 42", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tampered := result.AccessToken[:len(result.AccessToken)-1] + "x"
	if _, err := auth.Authenticate(context.Background(), "Bearer "+tampered); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("tampered token accepted: %v", err)
	}
	store.loadErr = ErrCompanySuspended
	if _, err := auth.Authenticate(context.Background(), "Bearer "+result.AccessToken); !errors.Is(err, ErrCompanySuspended) {
		t.Fatalf("suspension not enforced: %v", err)
	}
	store.loadErr = nil
	principal, _ := auth.Authenticate(context.Background(), "Bearer "+result.AccessToken)
	if err := auth.AuthorizeCompany(principal, testCompanyID, "ADMIN"); err != nil {
		t.Fatal(err)
	}
	if err := auth.AuthorizeCompany(principal, "30000000-0000-4000-8000-000000000003", "ADMIN"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant access accepted: %v", err)
	}
	if err := auth.AuthorizeCompany(principal, testCompanyID, "USER"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong role accepted: %v", err)
	}
}

func TestPasswordPolicyAndArgon2id(t *testing.T) {
	password := "päss phrase with spaces 5"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateNewPassword("abc1234567"); err != nil {
		t.Fatalf("10-character letter+number password rejected: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || !VerifyPassword(hash, password) || VerifyPassword(hash, password+"x") {
		t.Fatal("Argon2id hash verification failed")
	}
	for _, invalid := range []string{"short1", "abcdefghij", "1234567890", "password123", strings.Repeat("a1", 65)} {
		if err := ValidateNewPassword(invalid); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid password %q accepted", invalid)
		}
	}
}
