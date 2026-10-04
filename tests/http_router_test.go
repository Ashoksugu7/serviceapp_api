package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"serviceops360/api/internal/httpapi"
)

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestOperationalRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
		code         string
	}{
		{"GET", "/api/v1/health/live", 200, ""},
		{"HEAD", "/api/v1/health/live", 200, ""},
		{"GET", "/api/v1/health/ready", 503, "NOT_READY"},
		{"POST", "/api/v1/health/live", 405, "METHOD_NOT_ALLOWED"},
		{"GET", "/api/v1/auth/me", 404, "NOT_FOUND"},
		{"GET", "/health/live", 404, "NOT_FOUND"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return errors.New("private database details") }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if w.Header().Get("Content-Type") != "application/json" {
				t.Fatal("response is not JSON")
			}
			if strings.Contains(w.Body.String(), "private database") {
				t.Fatal("database details leaked")
			}
			if tc.method == http.MethodHead {
				if w.Body.Len() != 0 {
					t.Fatalf("HEAD response contained a body: %q", w.Body.String())
				}
				return
			}
			var body struct {
				Error struct {
					Code, Message string
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.code {
				t.Fatalf("error code = %s, want %s", body.Error.Code, tc.code)
			}
			if tc.status == http.StatusMethodNotAllowed && w.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal("missing allowed methods")
			}
		})
	}
}

func TestRejectsKnownOversizedBodyBeforeRouting(t *testing.T) {
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/unknown", strings.NewReader("{}"))
	req.ContentLength = (1 << 20) + 1
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "PAYLOAD_TOO_LARGE") {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

func TestFixedEnumsEndpoint(t *testing.T) {
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/enums", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var body struct {
		FieldTypes  []string            `json:"field_types"`
		TextFormats []string            `json:"text_formats"`
		UserRoles   []string            `json:"user_roles"`
		Statuses    map[string][]string `json:"statuses"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !contains(body.FieldTypes, "linked_product") || !contains(body.TextFormats, "email") || !contains(body.UserRoles, "ADMIN") || !contains(body.Statuses["product"], "DISCONTINUED") {
		t.Fatalf("unexpected enum response: %s", w.Body.String())
	}
}

func TestMCPInitialize(t *testing.T) {
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"serviceops360-api"`) {
		t.Fatalf("MCP initialize = %d %s", w.Code, w.Body.String())
	}
}

func TestMCPToolCallsAPI(t *testing.T) {
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"serviceops_api_request","arguments":{"method":"GET","path":"/api/v1/enums"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"field_types"`) || !strings.Contains(w.Body.String(), `"status":200`) {
		t.Fatalf("MCP tool call = %d %s", w.Code, w.Body.String())
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAccessLogRecordsRequestWithoutQuery(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), logger, time.Second)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/health/live?token=secret", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	output := logs.String()
	for _, want := range []string{"msg=\"HTTP request completed\"", "method=GET", "path=/api/v1/health/live", "status=200", "duration="} {
		if !strings.Contains(output, want) {
			t.Fatalf("access log %q does not contain %q", output, want)
		}
	}
	if strings.Contains(output, "token=secret") {
		t.Fatalf("access log leaked query string: %q", output)
	}
}

func TestAccessLogRecordsAPIErrorDetails(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := httpapi.NewHandler(pingFunc(func(context.Context) error { return nil }), logger, time.Second)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	output := logs.String()
	for _, want := range []string{"level=WARN", "msg=\"HTTP request failed\"", "status=404", "error_code=NOT_FOUND", "error_message=\"Route not found\""} {
		if !strings.Contains(output, want) {
			t.Fatalf("access log %q does not contain %q", output, want)
		}
	}
}

func TestReadinessTimeoutCancelsDatabaseWork(t *testing.T) {
	cancelled := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	handler := httpapi.NewHandler(pingFunc(func(ctx context.Context) error {
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	}), slog.New(slog.NewTextHandler(io.Discard, nil)), 20*time.Millisecond)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/health/ready", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "REQUEST_TIMEOUT") {
		t.Fatalf("unexpected timeout response: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("database work did not receive cancellation")
	}
}
