package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"serviceops360/api/internal/mail"
	"serviceops360/api/internal/service"
)

type captureSender struct {
	message mail.Message
	err     error
}

func (c *captureSender) Send(_ context.Context, message mail.Message) error {
	c.message = message
	return c.err
}

func TestAccountMailDelivery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	phone := "9876543210"
	email := credentialEmail{name: "Ravi", email: "ravi@example.test", phone: &phone, company: "Acme Services", temporaryPassword: "Tmp7xYz2abcd"}

	sender := &captureSender{}
	delivered := AccountMail{Sender: sender, AppBaseURL: "http://200.141.10.110:3001"}.deliver(context.Background(), logger, email)
	if !delivered.EmailSent || delivered.TemporaryPassword != nil {
		t.Fatalf("delivered = %+v; the password must not be returned once emailed", delivered)
	}
	for _, want := range []string{"ravi@example.test", "9876543210", "Tmp7xYz2abcd", "Acme Services", "http://200.141.10.110:3001/login"} {
		if !strings.Contains(sender.message.Text, want) {
			t.Fatalf("welcome email missing %q:\n%s", want, sender.message.Text)
		}
	}

	for name, err := range map[string]error{"log mode": mail.ErrNotSent, "SMTP failure": errors.New("connection refused")} {
		result := AccountMail{Sender: &captureSender{err: err}}.deliver(context.Background(), logger, email)
		if result.EmailSent || result.TemporaryPassword == nil || *result.TemporaryPassword != "Tmp7xYz2abcd" {
			t.Fatalf("%s: delivery = %+v; the admin needs the password to pass on", name, result)
		}
	}

	email.reset = true
	if subjectFor(email) != "Your ServiceOps360 password was reset" || !strings.Contains(contentFor(email, "http://x").Paragraphs[1], "reset") {
		t.Fatal("reset email wording")
	}
}

func TestResetEmail(t *testing.T) {
	sender := &captureSender{}
	reset := service.PasswordResetEmail{User: service.PasswordResetUser{Name: "Ravi", Email: "ravi@example.test", Company: "Acme"}, Token: "abc_DEF-123"}
	AccountMail{Sender: sender, AppBaseURL: "http://200.141.10.110:3001"}.sendResetLink(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), reset)
	if sender.message.To != "ravi@example.test" || !strings.Contains(sender.message.Text, "http://200.141.10.110:3001/reset-password?token=abc_DEF-123") || !strings.Contains(sender.message.Text, "30 minutes") {
		t.Fatalf("reset email: %+v", sender.message)
	}
}
