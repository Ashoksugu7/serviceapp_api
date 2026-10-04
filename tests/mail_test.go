package tests

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"serviceops360/api/internal/config"
	"serviceops360/api/internal/mail"
)

// fakeSMTP accepts one message without TLS and records what it received.
type fakeSMTP struct {
	listener net.Listener
	data     chan string
	starttls bool
}

func startFakeSMTP(t *testing.T, offerStartTLS bool) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeSMTP{listener: listener, data: make(chan string, 1), starttls: offerStartTLS}
	t.Cleanup(func() { _ = listener.Close() })
	go server.serve()
	return server
}

func (s *fakeSMTP) port() int { return s.listener.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTP) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 fake ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"):
			if s.starttls {
				write("250-fake")
				write("250 STARTTLS")
			} else {
				write("250 fake")
			}
		case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			write("250 OK")
		case command == "DATA":
			write("354 go ahead")
			var body bytes.Buffer
			for {
				part, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if part == ".\r\n" {
					break
				}
				body.WriteString(part)
			}
			s.data <- body.String()
			write("250 queued")
		case command == "QUIT":
			write("221 bye")
			return
		default:
			write("502 not implemented")
		}
	}
}

func TestSMTPSenderDeliversMultipartMessage(t *testing.T) {
	server := startFakeSMTP(t, false)
	sender, err := mail.New(mail.Config{Mode: "smtp", From: "ServiceOps360 <no-reply@example.test>", Host: "127.0.0.1", Port: server.port(), TLS: mail.TLSNone, Timeout: 5 * time.Second}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.Build("Ravi <ravi@example.test>", "Welcome to ServiceOps360", mail.Content{
		Heading:    "Welcome",
		Paragraphs: []string{"Your account <b>is</b> ready."},
		Details:    []mail.Detail{{Label: "Username", Value: "ravi@example.test"}},
		Action:     &mail.Action{Label: "Sign in", URL: "https://app.example.test/login"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	got := <-server.data
	for _, want := range []string{"From: \"ServiceOps360\" <no-reply@example.test>", "To: \"Ravi\" <ravi@example.test>", "Subject: Welcome to ServiceOps360", "multipart/alternative", "text/plain; charset=utf-8", "text/html; charset=utf-8", "Username: ravi@example.test", "&lt;b&gt;is&lt;/b&gt;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("message missing %q:\n%s", want, got)
		}
	}
}

func TestSMTPSenderRequiresStartTLSByDefault(t *testing.T) {
	server := startFakeSMTP(t, false)
	sender, err := mail.New(mail.Config{Mode: "smtp", From: "no-reply@example.test", Host: "127.0.0.1", Port: server.port(), TLS: mail.TLSStartTLS, Timeout: 5 * time.Second}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	message := mail.Message{To: "ravi@example.test", Subject: "Hello", Text: "Hello"}
	if err := sender.Send(context.Background(), message); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("error = %v, want STARTTLS refusal", err)
	}
}

func TestLogSenderHidesBodyAndRejectsHeaderInjection(t *testing.T) {
	var logs bytes.Buffer
	sender, err := mail.New(mail.Config{Mode: "log"}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), mail.Message{To: "ravi@example.test", Subject: "Temporary password", Text: "secret-temp-123"}); !errors.Is(err, mail.ErrNotSent) {
		t.Fatalf("log send error = %v, want ErrNotSent", err)
	}
	if strings.Contains(logs.String(), "secret-temp-123") || !strings.Contains(logs.String(), "ravi@example.test") {
		t.Fatalf("log output = %s", logs.String())
	}
	if err := sender.Send(context.Background(), mail.Message{To: "ravi@example.test", Subject: "Hi\r\nBcc: x@example.test", Text: "x"}); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestMailConfiguration(t *testing.T) {
	setValidEnvironment(t)
	for _, key := range []string{"MAIL_MODE", "MAIL_FROM", "SMTP_HOST", "SMTP_PORT", "SMTP_TLS", "SMTP_USERNAME", "SMTP_PASSWORD", "MAIL_TIMEOUT", "MAIL_LOG_BODY", "APP_BASE_URL"} {
		t.Setenv(key, "")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mail.Mode != "log" || cfg.Mail.Port != 587 || cfg.Mail.TLS != mail.TLSStartTLS || cfg.Mail.AppBaseURL != "http://localhost:3000" || cfg.Mail.LogBody {
		t.Fatalf("unexpected mail defaults: %+v", cfg.Mail)
	}
	t.Setenv("APP_BASE_URL", "http://200.141.10.110:3001/")
	if cfg, err := config.Load(); err != nil || cfg.Mail.AppBaseURL != "http://200.141.10.110:3001" {
		t.Fatalf("APP_BASE_URL = %q, %v", cfg.Mail.AppBaseURL, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"MAIL_MODE", "sendgrid"}, {"SMTP_PORT", "0"}, {"SMTP_TLS", "ssl"}, {"MAIL_TIMEOUT", "5m"},
		{"MAIL_LOG_BODY", "maybe"}, {"APP_BASE_URL", "ftp://example.test"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := config.Load(); err == nil {
				t.Fatalf("%s=%q accepted", tc.key, tc.value)
			}
		})
	}
	t.Setenv("MAIL_MODE", "smtp")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "SMTP_HOST") {
		t.Fatalf("smtp without host error = %v", err)
	}
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_TLS", "none")
	t.Setenv("SMTP_USERNAME", "user")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("credentials without TLS error = %v", err)
	}
}
