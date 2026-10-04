package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

type Pinger interface{ Ping(context.Context) error }

func NewHandler(db Pinger, logger *slog.Logger, timeout time.Duration, authService ...*service.AuthService) http.Handler {
	var auth *service.AuthService
	if len(authService) > 0 {
		auth = authService[0]
	}
	return newHandler(db, logger, timeout, auth, nil, AccountMail{})
}

// NewApplicationHandler serves the full API. accounts (optional) sends welcome
// and reset emails; without it temporary passwords are returned to the admin.
func NewApplicationHandler(db Pinger, logger *slog.Logger, timeout time.Duration, auth *service.AuthService, catalog *repository.CatalogStore, accounts ...AccountMail) http.Handler {
	mailer := AccountMail{}
	if len(accounts) > 0 {
		mailer = accounts[0]
	}
	return newHandler(db, logger, timeout, auth, catalog, mailer)
}

func newHandler(db Pinger, logger *slog.Logger, timeout time.Duration, auth *service.AuthService, catalog *repository.CatalogStore, accounts AccountMail) http.Handler {
	v1 := http.NewServeMux()
	v1.HandleFunc("/health/live", getOnly(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	v1.HandleFunc("/health/ready", getOnly(func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "NOT_READY", "Service unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}))
	v1.HandleFunc("/enums", getOnly(fixedEnums))
	if auth != nil {
		registerAuthRoutes(v1, auth, accounts, logger)
		if catalog != nil {
			registerManagementRoutes(v1, auth, catalog, accounts, logger)
		}
	}
	v1.HandleFunc("/", notFound)
	root := http.NewServeMux()
	root.Handle("/api/v1/", http.StripPrefix("/api/v1", v1))
	root.HandleFunc("/", notFound)
	// TimeoutHandler cancels request context and buffers the response so an
	// expired handler cannot write a late success response. Handlers must pass
	// r.Context() to every database call.
	timed := http.TimeoutHandler(recoverPanics(root, logger), timeout,
		`{"error":{"code":"REQUEST_TIMEOUT","message":"Request timed out"}}`)
	api := accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			w = headWriter{w}
		}
		if r.ContentLength > maxBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Request body exceeds 1 MiB")
			return
		}
		timed.ServeHTTP(w, r)
	}), logger)
	root.Handle("/mcp", newMCPHandler(api, logger))
	return api
}

// accessLog records request metadata without logging headers, bodies, or query
// strings, which may contain credentials or other sensitive values.
func accessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		attributes := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration", time.Since(started),
		}
		if recorder.status >= http.StatusBadRequest {
			if code, message := recorder.errorDetails(); code != "" {
				attributes = append(attributes, "error_code", code, "error_message", message)
			}
			if recorder.status >= http.StatusInternalServerError {
				logger.Error("HTTP request failed", attributes...)
			} else {
				logger.Warn("HTTP request failed", attributes...)
			}
			return
		}
		logger.Info("HTTP request completed", attributes...)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        []byte
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	// Error responses are intentionally tiny JSON documents. Keep a bounded
	// copy only for structured logging; never retain normal response bodies.
	if w.status >= http.StatusBadRequest && len(w.body) < 4096 {
		remaining := 4096 - len(w.body)
		logged := p
		if len(logged) > remaining {
			logged = logged[:remaining]
		}
		w.body = append(w.body, logged...)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusRecorder) errorDetails() (code, message string) {
	var payload struct {
		Error errorDetail `json:"error"`
	}
	if err := json.Unmarshal(w.body, &payload); err != nil {
		return "", ""
	}
	return payload.Error.Code, payload.Error.Message
}

type headWriter struct{ http.ResponseWriter }

func (w headWriter) Write(p []byte) (int, error) { return len(p), nil }

func getOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
			return
		}
		next(w, r)
	}
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
}

func recoverPanics(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				if value == http.ErrAbortHandler {
					panic(value)
				}
				logger.Error("request panic recovered", "method", r.Method)
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
