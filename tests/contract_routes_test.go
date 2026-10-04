package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"serviceops360/api/internal/httpapi"
	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

func TestOpenAPIOperationsAreRegistered(t *testing.T) {
	raw, err := os.ReadFile("../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	auth, err := service.NewAuthService(&handlerAuthStore{}, service.AuthConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: 30 * time.Minute, ClockSkew: 30 * time.Second, RateWindow: 15 * time.Minute, EmailLimit: 5, IPLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewApplicationHandler(pingFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, auth, repository.NewCatalogStore(nil))
	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	operationCount := 0
	for path, operations := range contract.Paths {
		for method := range operations {
			operationCount++
			url := "/api/v1" + placeholder.ReplaceAllString(path, "11111111-1111-4111-8111-111111111111")
			// Public routes: sign-in and password reset (T29) answer without a token.
			public := path == "/auth/login" || strings.HasPrefix(path, "/auth/password-reset/")
			body := ""
			if public {
				body = `{}`
			}
			request := httptest.NewRequest(strings.ToUpper(method), url, strings.NewReader(body))
			if body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			expected := http.StatusUnauthorized
			if strings.HasPrefix(path, "/health/") {
				expected = http.StatusOK
			} else if public {
				expected = http.StatusBadRequest
			}
			if response.Code != expected {
				t.Errorf("%s %s: status=%d, want %d; body=%s", method, path, response.Code, expected, response.Body.String())
			}
			if expected >= 400 {
				var envelope struct {
					Error struct {
						Code, Message string
					} `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error.Code == "" || envelope.Error.Message == "" {
					t.Errorf("%s %s: invalid error envelope: %s", method, path, response.Body.String())
				}
			}
		}
	}
	if operationCount != 72 {
		t.Fatalf("verified %d OpenAPI operations, want 72", operationCount)
	}
}
