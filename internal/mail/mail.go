// Package mail sends transactional email (T27): welcome messages with
// temporary passwords and password-reset links. Callers send only after their
// database transaction commits, and treat a failed send as reportable, not fatal.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"
)

// Message is one email. Text is required; HTML is optional.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, message Message) error
}

type TLSMode string

const (
	TLSStartTLS TLSMode = "starttls" // port 587: plain connection upgraded with STARTTLS (required)
	TLSImplicit TLSMode = "tls"      // port 465: TLS from the first byte
	TLSNone     TLSMode = "none"     // local relays only; credentials are refused without TLS
)

// Config selects and configures the sender.
type Config struct {
	Mode       string // "log" (default) or "smtp"
	From       string // e.g. "ServiceOps360 <no-reply@example.com>"
	Host       string
	Port       int
	Username   string
	Password   string
	TLS        TLSMode
	Timeout    time.Duration
	LogBody    bool   // log mode only: include the message body (development)
	AppBaseURL string // public UI address used in links, without a trailing slash
}

// New returns the sender for cfg.
func New(cfg Config, logger *slog.Logger) (Sender, error) {
	switch cfg.Mode {
	case "", "log":
		return &LogSender{logger: logger, logBody: cfg.LogBody}, nil
	case "smtp":
		from, err := mail.ParseAddress(cfg.From)
		if err != nil {
			return nil, fmt.Errorf("MAIL_FROM must be an email address")
		}
		if cfg.Host == "" || cfg.Port < 1 || cfg.Port > 65535 {
			return nil, fmt.Errorf("SMTP_HOST and SMTP_PORT are required for MAIL_MODE=smtp")
		}
		if cfg.TLS == TLSNone && cfg.Username != "" {
			return nil, fmt.Errorf("SMTP_TLS=none cannot be used with SMTP_USERNAME; credentials need TLS")
		}
		return &SMTPSender{config: cfg, from: from}, nil
	default:
		return nil, fmt.Errorf("MAIL_MODE must be log or smtp")
	}
}

// LogSender writes messages to the application log instead of sending them.
// Bodies can contain temporary passwords and reset links, so they are logged
// only when explicitly enabled for development.
type LogSender struct {
	logger  *slog.Logger
	logBody bool
}

func (s *LogSender) Send(_ context.Context, message Message) error {
	if err := message.validate(); err != nil {
		return err
	}
	attrs := []any{"to", message.To, "subject", message.Subject}
	if s.logBody {
		attrs = append(attrs, "body", message.Text)
	}
	s.logger.Info("email not sent (MAIL_MODE=log)", attrs...)
	return ErrNotSent
}

var errHeaderInjection = errors.New("mail: header values must not contain line breaks")

// ErrNotSent reports that a valid message was only logged (MAIL_MODE=log), so
// callers can tell users the email was not delivered.
var ErrNotSent = errors.New("mail: not sent (MAIL_MODE=log)")

func (m Message) validate() error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return errHeaderInjection
	}
	if _, err := mail.ParseAddress(m.To); err != nil {
		return fmt.Errorf("mail: invalid recipient: %w", err)
	}
	if strings.TrimSpace(m.Subject) == "" || strings.TrimSpace(m.Text) == "" {
		return errors.New("mail: subject and text are required")
	}
	return nil
}
