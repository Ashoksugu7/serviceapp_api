package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"serviceops360/api/internal/config"
	"serviceops360/api/internal/database"
)

func TestDatabaseConnectionErrorsAreUsefulAndRedacted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := database.Open(ctx, config.Config{
		DatabaseURL: "postgres://secret-user:secret-password@127.0.0.1:1/secret-db?sslmode=disable",
		DBMaxConns:  1, DBConnectTimeout: 100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("unreachable PostgreSQL server unexpectedly connected")
	}
	if !strings.Contains(err.Error(), "connection refused") && !strings.Contains(err.Error(), "could not connect") {
		t.Fatalf("unexpected connection error: %v", err)
	}
	for _, secret := range []string{"secret-user", "secret-password", "secret-db"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("connection detail leaked: %v", err)
		}
	}
	if _, err := database.Open(ctx, config.Config{DatabaseURL: "://invalid", DBMaxConns: 1, DBConnectTimeout: time.Second}); err == nil || err.Error() != "invalid DATABASE_URL" {
		t.Fatalf("malformed URL error=%v", err)
	}
}
