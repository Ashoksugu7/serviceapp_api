package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// PasswordResetTTL is how long an emailed reset link works (T29).
const PasswordResetTTL = 30 * time.Minute

// PasswordResetEmail is returned when a reset link should be emailed. Callers
// send it without waiting, so response timing does not reveal accounts.
type PasswordResetEmail struct {
	User  PasswordResetUser
	Token string
}

// RequestPasswordReset creates a single-use reset token for an active account.
// It returns nil (and no error) when no active account matches, so callers can
// answer every request the same way. Requests are limited per identifier and IP.
func (s *AuthService) RequestPasswordReset(ctx context.Context, identifier, clientIP string) (*PasswordResetEmail, time.Duration, error) {
	login, err := ParseLoginIdentifier(identifier)
	if err != nil {
		return nil, 0, ErrValidation
	}
	now := s.now().UTC()
	windowStart := now.Truncate(s.config.RateWindow)
	windowEnd := windowStart.Add(s.config.RateWindow)
	accountKey := s.rateKey("EMAIL", "reset\x00"+login.key())
	ipKey := s.rateKey("IP", "reset\x00"+clientIP)
	accountCount, ipCount, err := s.store.RateLimitCounts(ctx, accountKey, ipKey, windowStart)
	if err != nil {
		return nil, 0, err
	}
	if accountCount >= s.config.EmailLimit || ipCount >= s.config.IPLimit {
		return nil, windowEnd.Sub(now).Truncate(time.Second), ErrRateLimited
	}
	// Every request counts, whether or not the account exists.
	if _, _, err := s.store.RecordLoginFailure(ctx, accountKey, ipKey, windowStart, windowEnd); err != nil {
		return nil, 0, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, 0, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	user, found, err := s.store.CreatePasswordReset(ctx, login, hashResetToken(token), now.Add(PasswordResetTTL))
	if err != nil || !found {
		return nil, 0, err
	}
	return &PasswordResetEmail{User: user, Token: token}, 0, nil
}

// ConfirmPasswordReset sets a new password with a reset token, clears any
// temporary-password flag and signs the user out everywhere.
func (s *AuthService) ConfirmPasswordReset(ctx context.Context, token, newPassword string) error {
	if raw, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(raw) != 32 {
		return ErrResetTokenInvalid
	}
	if utf8.RuneCountInString(newPassword) > 128 {
		return FieldErrors{"new_password": "must contain 10 to 128 characters"}
	}
	hash, err := HashPassword(newPassword)
	if errors.Is(err, ErrValidation) {
		return FieldErrors{"new_password": strings.TrimSuffix(strings.TrimPrefix(Message(err, "is not allowed"), "Password "), ".")}
	} else if err != nil {
		return err
	}
	return s.store.ConfirmPasswordReset(ctx, hashResetToken(token), hash, s.now().UTC())
}

func hashResetToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
