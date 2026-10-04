package tests

import (
	"strings"
	"testing"
	"time"

	"serviceops360/api/internal/config"
)

func setValidEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_ADDR", "DB_MAX_CONNS", "DB_CONNECT_TIMEOUT", "HTTP_REQUEST_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT", "AUTH_RATE_WINDOW", "AUTH_EMAIL_FAILURE_LIMIT", "AUTH_IP_FAILURE_LIMIT"} {
		t.Setenv(key, "")
	}
	t.Setenv("DATABASE_URL", "postgresql://serviceops:secret@localhost/serviceops360")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
}

func TestLoadDefaults(t *testing.T) {
	setValidEnvironment(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.DBMaxConns != 10 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DBConnectTimeout != 5*time.Second || cfg.RequestTimeout != 10*time.Second || cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("unexpected timeout defaults: %+v", cfg)
	}
	if cfg.AuthTokenTTL != 30*time.Minute || cfg.AuthClockSkew != 30*time.Second || cfg.AuthRateWindow != 15*time.Minute || cfg.AuthEmailLimit != 5 || cfg.AuthIPLimit != 20 {
		t.Fatalf("unexpected authentication defaults: %+v", cfg)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"missing database", "DATABASE_URL", ""}, {"short JWT secret", "JWT_SECRET", "short"},
		{"python database scheme", "DATABASE_URL", "postgresql+asyncpg://example/db"}, {"keyword database string", "DATABASE_URL", "host=localhost dbname=serviceops"},
		{"invalid address", "HTTP_ADDR", "localhost"}, {"invalid port", "HTTP_ADDR", "localhost:70000"},
		{"zero connections", "DB_MAX_CONNS", "0"}, {"overflow connections", "DB_MAX_CONNS", "2147483648"},
		{"negative timeout", "HTTP_REQUEST_TIMEOUT", "-1s"}, {"malformed timeout", "DB_CONNECT_TIMEOUT", "five seconds"},
		{"zero shutdown", "HTTP_SHUTDOWN_TIMEOUT", "0s"}, {"excessive shutdown", "HTTP_SHUTDOWN_TIMEOUT", "2h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("expected error identifying %s, got %v", tc.key, err)
			}
		})
	}
}
