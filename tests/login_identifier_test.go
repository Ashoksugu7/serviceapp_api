package tests

import (
	"context"
	"errors"
	"testing"

	. "serviceops360/api/internal/service"
)

func TestLoginWithPhoneNumber(t *testing.T) {
	hash, err := HashPassword("correct horse battery 42")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAuthStore{identity: activeIdentity(), passwordHash: hash}
	auth := newTestAuth(t, store)
	if _, _, err := auth.Login(context.Background(), "+91 98765-43210", "correct horse battery 42", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if store.loginPhone != "919876543210" || store.loginEmail != "" {
		t.Fatalf("phone login looked up email=%q phone=%q", store.loginEmail, store.loginPhone)
	}
	if _, _, err := auth.Login(context.Background(), "12345", "correct horse battery 42", "127.0.0.1"); !errors.Is(err, ErrValidation) {
		t.Fatalf("short phone error = %v, want ErrValidation", err)
	}
}

func TestParseLoginIdentifier(t *testing.T) {
	cases := []struct {
		raw          string
		email, phone string
		valid        bool
	}{
		{" Admin@Example.Test ", "admin@example.test", "", true},
		{"98765 43210", "", "9876543210", true},
		{"(044) 2345-6789", "", "04423456789", true},
		{"98765abc", "", "", false},
		{"12345", "", "", false},
		{"not-an-email@", "", "", false},
	}
	for _, tc := range cases {
		got, err := ParseLoginIdentifier(tc.raw)
		if (err == nil) != tc.valid {
			t.Fatalf("ParseLoginIdentifier(%q) error = %v, valid %v", tc.raw, err, tc.valid)
		}
		if tc.valid && (got.Email != tc.email || got.Phone != tc.phone) {
			t.Fatalf("ParseLoginIdentifier(%q) = %+v", tc.raw, got)
		}
	}
}
