package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"
)

// SMTPSender delivers mail through an SMTP relay.
type SMTPSender struct {
	config Config
	from   *mail.Address
}

func (s *SMTPSender) Send(ctx context.Context, message Message) error {
	if err := message.validate(); err != nil {
		return err
	}
	to, _ := mail.ParseAddress(message.To)
	body, err := s.compose(message, to)
	if err != nil {
		return err
	}
	timeout := s.config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	dialer := &net.Dialer{}
	var conn net.Conn
	if s.config.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: s.config.Host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("mail: connect to SMTP server: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, s.config.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: SMTP greeting: %w", err)
	}
	defer client.Close()

	if s.config.TLS == TLSStartTLS || s.config.TLS == "" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("mail: SMTP server does not offer STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.config.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: STARTTLS: %w", err)
		}
	}
	if s.config.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)); err != nil {
			return fmt.Errorf("mail: SMTP authentication failed: %w", err)
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return fmt.Errorf("mail: sender rejected: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mail: recipient rejected: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		_ = writer.Close()
		return fmt.Errorf("mail: write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("mail: message rejected: %w", err)
	}
	return client.Quit()
}

// compose builds a MIME message: text only, or multipart/alternative with HTML.
func (s *SMTPSender) compose(message Message, to *mail.Address) ([]byte, error) {
	var buf bytes.Buffer
	header := func(key, value string) { fmt.Fprintf(&buf, "%s: %s\r\n", key, value) }
	header("From", s.from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", message.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+randomID()+"@"+domainOf(s.from.Address)+">")
	header("MIME-Version", "1.0")

	if message.HTML == "" {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		return buf.Bytes(), writeQuoted(&buf, message.Text)
	}
	parts := multipart.NewWriter(&buf)
	header("Content-Type", "multipart/alternative; boundary="+parts.Boundary())
	buf.WriteString("\r\n")
	for _, part := range []struct{ kind, body string }{{"text/plain", message.Text}, {"text/html", message.HTML}} {
		w, err := parts.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.kind + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQuoted(w, part.body); err != nil {
			return nil, err
		}
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeQuoted(w interface{ Write([]byte) (int, error) }, text string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(text)); err != nil {
		return err
	}
	return qp.Close()
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func domainOf(address string) string {
	for i := len(address) - 1; i >= 0; i-- {
		if address[i] == '@' {
			return address[i+1:]
		}
	}
	return "localhost"
}
