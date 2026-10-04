package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrCompanySuspended   = errors.New("company suspended")
	ErrForbidden          = errors.New("forbidden")
	ErrRateLimited        = errors.New("rate limited")
	ErrValidation         = errors.New("validation failed")
	ErrInvalidState       = errors.New("invalid state")
	// ErrPasswordChangeRequired blocks a temporary-password session from
	// everything except changing the password (T28).
	ErrPasswordChangeRequired = errors.New("password change required")
	// ErrResetTokenInvalid covers unknown, used and expired reset links (T29).
	ErrResetTokenInvalid = errors.New("password reset token invalid")
)

type FieldErrors map[string]string

func (e FieldErrors) Error() string { return "field validation failed" }

type User struct {
	ID        string  `json:"id"`
	CompanyID *string `json:"company_id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	Phone     *string `json:"phone"`
	Role      string  `json:"role"`
	Status    string  `json:"status"`
	// MustChangePassword is true while the user signs in with a temporary password.
	MustChangePassword bool `json:"must_change_password"`
}

type Company struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     *string   `json:"email"`
	Contact   *string   `json:"contact"`
	Address   *string   `json:"address"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Identity struct {
	User    User     `json:"user"`
	Company *Company `json:"company"`
}

type Principal struct {
	Identity  Identity
	SessionID string
}

type LoginResult struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int64    `json:"expires_in"`
	User        User     `json:"user"`
	Company     *Company `json:"company"`
}

type PasswordVerifier func(encodedHash, password string) bool

type AuthStore interface {
	AuthenticateAndCreateSession(context.Context, LoginIdentifier, string, PasswordVerifier, string, time.Time, time.Time) (Identity, error)
	LoadSessionIdentity(context.Context, string, string, time.Time) (Identity, error)
	RevokeSession(context.Context, string, string, time.Time) error
	RateLimitCounts(context.Context, []byte, []byte, time.Time) (int, int, error)
	RecordLoginFailure(context.Context, []byte, []byte, time.Time, time.Time) (int, int, error)
	ChangePassword(ctx context.Context, userID, sessionID, currentPassword string, verify PasswordVerifier, newHash string) error
	CreatePasswordReset(ctx context.Context, login LoginIdentifier, tokenHash []byte, expiresAt time.Time) (PasswordResetUser, bool, error)
	ConfirmPasswordReset(ctx context.Context, tokenHash []byte, newHash string, now time.Time) error
}

// PasswordResetUser is who a reset link is emailed to.
type PasswordResetUser struct {
	Name, Email, Company string
}

type AuthConfig struct {
	Secret     []byte
	Issuer     string
	Audience   string
	TokenTTL   time.Duration
	ClockSkew  time.Duration
	RateWindow time.Duration
	EmailLimit int
	IPLimit    int
}

type AuthService struct {
	store     AuthStore
	config    AuthConfig
	dummyHash string
	now       func() time.Time
}

func NewAuthService(store AuthStore, config AuthConfig) (*AuthService, error) {
	if store == nil || len(config.Secret) < 32 || config.Issuer == "" || config.Audience == "" ||
		config.TokenTTL <= 0 || config.ClockSkew < 0 || config.RateWindow <= 0 || config.EmailLimit < 1 || config.IPLimit < 1 {
		return nil, errors.New("invalid authentication configuration")
	}
	// Hashed once so unknown accounts take as long to reject as wrong passwords.
	dummy, err := HashPassword("invalid-password-placeholder-0")
	if err != nil {
		return nil, err
	}
	return &AuthService{store: store, config: config, dummyHash: dummy, now: time.Now}, nil
}

// Login accepts an email or a phone number as the identifier.
func (s *AuthService) Login(ctx context.Context, identifier, password, clientIP string) (LoginResult, time.Duration, error) {
	login, err := ParseLoginIdentifier(identifier)
	if err != nil || password == "" || utf8.RuneCountInString(password) > 128 {
		return LoginResult{}, 0, ErrValidation
	}
	now := s.now().UTC()
	windowStart := now.Truncate(s.config.RateWindow)
	windowEnd := windowStart.Add(s.config.RateWindow)
	// Account limits share the EMAIL scope; the key includes the identifier kind.
	emailKey := s.rateKey("EMAIL", login.key())
	ipKey := s.rateKey("IP", clientIP)
	emailCount, ipCount, err := s.store.RateLimitCounts(ctx, emailKey, ipKey, windowStart)
	if err != nil {
		return LoginResult{}, 0, err
	}
	if emailCount >= s.config.EmailLimit || ipCount >= s.config.IPLimit {
		return LoginResult{}, windowEnd.Sub(now).Truncate(time.Second), ErrRateLimited
	}

	sessionID, err := newUUID()
	if err != nil {
		return LoginResult{}, 0, err
	}
	expiresAt := now.Add(s.config.TokenTTL)
	verify := func(encoded, candidate string) bool {
		if encoded == "" {
			encoded = s.dummyHash
		}
		return VerifyPassword(encoded, candidate)
	}
	identity, err := s.store.AuthenticateAndCreateSession(ctx, login, password, verify, sessionID, now, expiresAt)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			emailCount, ipCount, recordErr := s.store.RecordLoginFailure(ctx, emailKey, ipKey, windowStart, windowEnd)
			if recordErr != nil {
				return LoginResult{}, 0, recordErr
			}
			if emailCount > s.config.EmailLimit || ipCount > s.config.IPLimit {
				return LoginResult{}, windowEnd.Sub(now).Truncate(time.Second), ErrRateLimited
			}
		}
		return LoginResult{}, 0, err
	}
	token, err := s.sign(identity, sessionID, now, expiresAt)
	if err != nil {
		return LoginResult{}, 0, err
	}
	return LoginResult{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(s.config.TokenTTL / time.Second),
		User:        identity.User,
		Company:     identity.Company,
	}, 0, nil
}

func (s *AuthService) Authenticate(ctx context.Context, authorization string) (Principal, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) || strings.TrimSpace(strings.TrimPrefix(authorization, prefix)) == "" {
		return Principal{}, ErrUnauthenticated
	}
	claims, err := s.parse(strings.TrimSpace(strings.TrimPrefix(authorization, prefix)), s.now().UTC())
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	identity, err := s.store.LoadSessionIdentity(ctx, claims.ID, claims.Subject, s.now().UTC())
	if err != nil {
		return Principal{}, err
	}
	if identity.User.Role != claims.Role || pointerValue(identity.User.CompanyID) != claims.CompanyID {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{Identity: identity, SessionID: claims.ID}, nil
}

func (s *AuthService) Logout(ctx context.Context, principal Principal) error {
	return s.store.RevokeSession(ctx, principal.SessionID, principal.Identity.User.ID, s.now().UTC())
}

func (s *AuthService) AuthorizeCompany(principal Principal, companyID string, roles ...string) error {
	allowed := false
	for _, role := range roles {
		if principal.Identity.User.Role == role {
			allowed = true
			break
		}
	}
	if !allowed {
		return ErrForbidden
	}
	if principal.Identity.User.Role == "SUPER_ADMIN" {
		return nil
	}
	if principal.Identity.User.CompanyID == nil || *principal.Identity.User.CompanyID != companyID {
		return ErrForbidden
	}
	return nil
}

func (s *AuthService) rateKey(scope, value string) []byte {
	mac := hmac.New(sha256.New, s.config.Secret)
	_, _ = mac.Write([]byte(scope + "\x00" + value))
	return mac.Sum(nil)
}

type tokenClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	ID        string `json:"jti"`
	Role      string `json:"role"`
	CompanyID string `json:"company_id,omitempty"`
}

func (s *AuthService) sign(identity Identity, sessionID string, issuedAt, expiresAt time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims := tokenClaims{
		Issuer: s.config.Issuer, Audience: s.config.Audience, Subject: identity.User.ID,
		ExpiresAt: expiresAt.Unix(), IssuedAt: issuedAt.Unix(), ID: sessionID,
		Role: identity.User.Role, CompanyID: pointerValue(identity.User.CompanyID),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.config.Secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *AuthService) parse(token string, now time.Time) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return tokenClaims{}, ErrUnauthenticated
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(headerBytes) != parts[0] || string(headerBytes) != `{"alg":"HS256","typ":"JWT"}` {
		return tokenClaims{}, ErrUnauthenticated
	}
	mac := hmac.New(sha256.New, s.config.Secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[2] || !hmac.Equal(signature, mac.Sum(nil)) {
		return tokenClaims{}, ErrUnauthenticated
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(payload) != parts[1] {
		return tokenClaims{}, ErrUnauthenticated
	}
	var claims tokenClaims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return tokenClaims{}, ErrUnauthenticated
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return tokenClaims{}, ErrUnauthenticated
	}
	skew := s.config.ClockSkew
	if claims.Issuer != s.config.Issuer || claims.Audience != s.config.Audience ||
		!validUUID(claims.Subject) || !validUUID(claims.ID) ||
		claims.ExpiresAt == 0 || now.After(time.Unix(claims.ExpiresAt, 0).Add(skew)) ||
		time.Unix(claims.IssuedAt, 0).After(now.Add(skew)) || !validRole(claims.Role) {
		return tokenClaims{}, ErrUnauthenticated
	}
	if claims.Role == "SUPER_ADMIN" && claims.CompanyID != "" {
		return tokenClaims{}, ErrUnauthenticated
	}
	if claims.Role != "SUPER_ADMIN" && !validUUID(claims.CompanyID) {
		return tokenClaims{}, ErrUnauthenticated
	}
	return claims, nil
}

func validRole(role string) bool {
	return role == "SUPER_ADMIN" || role == "ADMIN" || role == "USER"
}

func NormalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return "", ErrValidation
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email || !strings.Contains(email, "@") {
		return "", ErrValidation
	}
	return email, nil
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// ChangePassword replaces the signed-in user's password after checking the
// current one, clears must_change_password and signs out the user's other
// sessions. A wrong current password returns FieldErrors for current_password.
func (s *AuthService) ChangePassword(ctx context.Context, principal Principal, currentPassword, newPassword string) error {
	if currentPassword == "" || utf8.RuneCountInString(currentPassword) > 128 {
		return FieldErrors{"current_password": "is required"}
	}
	if newPassword == currentPassword {
		return FieldErrors{"new_password": "must be different from the current password"}
	}
	hash, err := HashPassword(newPassword)
	if errors.Is(err, ErrValidation) {
		return FieldErrors{"new_password": strings.TrimSuffix(strings.TrimPrefix(Message(err, "is not allowed"), "Password "), ".")}
	} else if err != nil {
		return err
	}
	err = s.store.ChangePassword(ctx, principal.Identity.User.ID, principal.SessionID, currentPassword, VerifyPassword, hash)
	if errors.Is(err, ErrInvalidCredentials) {
		return FieldErrors{"current_password": "is incorrect"}
	}
	return err
}
