package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"serviceops360/api/internal/service"
)

type principalContextKey struct{}

func registerAuthRoutes(mux *http.ServeMux, auth *service.AuthService, accounts AccountMail, logger *slog.Logger) {
	mux.HandleFunc("/auth/login", postOnly(loginHandler(auth, logger)))
	mux.HandleFunc("/auth/me", getOnly(authenticated(auth, logger, func(w http.ResponseWriter, _ *http.Request, principal service.Principal) {
		writeJSON(w, http.StatusOK, principal.Identity)
	})))
	mux.HandleFunc("/auth/logout", postOnly(authenticated(auth, logger, func(w http.ResponseWriter, r *http.Request, principal service.Principal) {
		if err := auth.Logout(r.Context(), principal); err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("/auth/password-reset/request", postOnly(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Identifier string `json:"identifier"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		reset, retryAfter, err := auth.RequestPasswordReset(r.Context(), body.Identifier, directClientIP(r.RemoteAddr))
		switch {
		case errors.Is(err, service.ErrValidation):
			writeFieldError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Enter a valid email or mobile number.", map[string]string{"identifier": "must be an email address or a mobile number with 6 to 20 digits"})
			return
		case errors.Is(err, service.ErrRateLimited):
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter/time.Second))))
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many reset requests. Try again later.")
			return
		case err != nil:
			writeAuthError(w, err, 0, logger)
			return
		}
		if reset != nil {
			go accounts.sendResetLink(context.WithoutCancel(r.Context()), logger, *reset)
		}
		// Same answer whether or not an account matched.
		writeJSON(w, http.StatusAccepted, map[string]string{"message": "If an account matches, a link to reset the password has been sent."})
	}))
	mux.HandleFunc("/auth/password-reset/confirm", postOnly(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token       string `json:"token"`
			NewPassword string `json:"new_password"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		err := auth.ConfirmPasswordReset(r.Context(), body.Token, body.NewPassword)
		var fields service.FieldErrors
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.As(err, &fields):
			writeFieldError(w, http.StatusBadRequest, "VALIDATION_ERROR", "The password was not changed.", fields)
		case errors.Is(err, service.ErrResetTokenInvalid):
			writeError(w, http.StatusBadRequest, "RESET_TOKEN_INVALID", "This reset link is invalid or has expired. Request a new one.")
		default:
			writeAuthError(w, err, 0, logger)
		}
	}))
	mux.HandleFunc("/auth/change-password", postOnly(authenticated(auth, logger, func(w http.ResponseWriter, r *http.Request, principal service.Principal) {
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if err := auth.ChangePassword(r.Context(), principal, body.CurrentPassword, body.NewPassword); err != nil {
			var fields service.FieldErrors
			if errors.As(err, &fields) {
				writeFieldError(w, http.StatusBadRequest, "VALIDATION_ERROR", "The password was not changed.", fields)
				return
			}
			writeAuthError(w, err, 0, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
}

func loginHandler(auth *service.AuthService, logger *slog.Logger) http.HandlerFunc {
	// identifier is an email or phone; email is still accepted from older clients.
	type loginRequest struct {
		Identifier string `json:"identifier"`
		Email      string `json:"email"`
		Password   string `json:"password"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var request loginRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		identifier := request.Identifier
		if identifier == "" {
			identifier = request.Email
		}
		result, retryAfter, err := auth.Login(r.Context(), identifier, request.Password, directClientIP(r.RemoteAddr))
		if err != nil {
			writeAuthError(w, err, retryAfter, logger)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

type principalHandler func(http.ResponseWriter, *http.Request, service.Principal)

func authenticated(auth *service.AuthService, logger *slog.Logger, next principalHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			writeAuthError(w, err, 0, logger)
			return
		}
		// A temporary password only allows reading the identity, signing out
		// and choosing a new password (T28). Paths are relative to /api/v1.
		if principal.Identity.User.MustChangePassword {
			switch r.URL.Path {
			case "/auth/me", "/auth/logout", "/auth/change-password":
			default:
				writeAuthError(w, service.ErrPasswordChangeRequired, 0, logger)
				return
			}
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next(w, r.WithContext(ctx), principal)
	}
}

// authorizeCompany is the shared boundary for future company-scoped handlers.
// Call it with the roles permitted for that operation after authentication.
func authorizeCompany(auth *service.AuthService, principal service.Principal, companyID string, roles ...string) error {
	return auth.AuthorizeCompany(principal, companyID, roles...)
}

func writeAuthError(w http.ResponseWriter, err error, retryAfter time.Duration, logger *slog.Logger) {
	switch {
	case errors.Is(err, service.ErrValidation):
		writeFieldError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Enter a valid email or mobile number and your password.", map[string]string{
			"identifier": "must be an email address or a mobile number with 6 to 20 digits",
			"password":   "must contain 1 to 128 characters",
		})
	case errors.Is(err, service.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "Invalid email, mobile number or password")
	case errors.Is(err, service.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
	case errors.Is(err, service.ErrPasswordChangeRequired):
		writeError(w, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED", "Choose a new password to continue.")
	case errors.Is(err, service.ErrCompanySuspended):
		writeError(w, http.StatusForbidden, "COMPANY_SUSPENDED", "Company is suspended")
	case errors.Is(err, service.ErrForbidden):
		writeError(w, http.StatusForbidden, "FORBIDDEN", service.Message(err, "You do not have permission to do this."))
	case errors.Is(err, service.ErrRateLimited):
		seconds := max(1, int(retryAfter.Round(time.Second)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many login attempts")
	default:
		logger.Error("authentication operation failed", "error", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func directClientIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(remoteAddress) == "" {
		return "unknown"
	}
	return remoteAddress
}

func postOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
			return
		}
		next(w, r)
	}
}
