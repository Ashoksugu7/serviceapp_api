package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"serviceops360/api/internal/mail"
)

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	DBMaxConns       int32
	DBConnectTimeout time.Duration
	RequestTimeout   time.Duration
	ShutdownTimeout  time.Duration
	JWTSecret        string
	AuthTokenTTL     time.Duration
	AuthClockSkew    time.Duration
	AuthRateWindow   time.Duration
	AuthEmailLimit   int
	AuthIPLimit      int
	Mail             mail.Config
}

func Load() (Config, error) { return load(os.Getenv) }

// LoadMail reads only the email settings, for tools that need no database.
func LoadMail() (mail.Config, error) { return loadMail(os.Getenv) }

func load(getenv func(string) string) (Config, error) {
	c := Config{
		HTTPAddr: "127.0.0.1:8080", DBMaxConns: 10,
		AuthTokenTTL: 30 * time.Minute, AuthClockSkew: 30 * time.Second,
		AuthRateWindow: 15 * time.Minute, AuthEmailLimit: 5, AuthIPLimit: 20,
	}
	if addr := getenv("HTTP_ADDR"); addr != "" {
		c.HTTPAddr = addr
	}
	_, port, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("HTTP_ADDR port must be between 1 and 65535")
	}
	c.DatabaseURL = strings.TrimSpace(getenv("DATABASE_URL"))
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	// DATABASE_URL is a URI, not a Python driver URL or libpq keyword string.
	// Do not include its value in errors: it can contain credentials.
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return Config{}, fmt.Errorf("DATABASE_URL must use postgres:// or postgresql://")
	}
	c.JWTSecret = getenv("JWT_SECRET")
	if len([]byte(c.JWTSecret)) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	if value := getenv("DB_MAX_CONNS"); value != "" {
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("DB_MAX_CONNS must be a positive 32-bit integer")
		}
		c.DBMaxConns = int32(n)
	}
	for _, setting := range []struct {
		name     string
		fallback time.Duration
		target   *time.Duration
	}{
		{"DB_CONNECT_TIMEOUT", 5 * time.Second, &c.DBConnectTimeout},
		{"HTTP_REQUEST_TIMEOUT", 10 * time.Second, &c.RequestTimeout},
		{"HTTP_SHUTDOWN_TIMEOUT", 15 * time.Second, &c.ShutdownTimeout},
	} {
		*setting.target = setting.fallback
		if value := getenv(setting.name); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 || d > time.Hour {
				return Config{}, fmt.Errorf("%s must be a positive duration no greater than 1h", setting.name)
			}
			*setting.target = d
		}
	}
	if value := getenv("AUTH_RATE_WINDOW"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d < time.Minute || d > time.Hour {
			return Config{}, fmt.Errorf("AUTH_RATE_WINDOW must be between 1m and 1h")
		}
		c.AuthRateWindow = d
	}
	for _, setting := range []struct {
		name   string
		target *int
	}{
		{"AUTH_EMAIL_FAILURE_LIMIT", &c.AuthEmailLimit},
		{"AUTH_IP_FAILURE_LIMIT", &c.AuthIPLimit},
	} {
		if value := getenv(setting.name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 10000 {
				return Config{}, fmt.Errorf("%s must be between 1 and 10000", setting.name)
			}
			*setting.target = n
		}
	}
	mailConfig, err := loadMail(getenv)
	if err != nil {
		return Config{}, err
	}
	c.Mail = mailConfig
	return c, nil
}

// loadMail reads T27 email settings. MAIL_MODE=log (default) writes emails to
// the log; MAIL_MODE=smtp sends them through SMTP_HOST.
func loadMail(getenv func(string) string) (mail.Config, error) {
	m := mail.Config{
		Mode: strings.ToLower(strings.TrimSpace(getenv("MAIL_MODE"))),
		From: strings.TrimSpace(getenv("MAIL_FROM")), Host: strings.TrimSpace(getenv("SMTP_HOST")),
		Port: 587, Username: getenv("SMTP_USERNAME"), Password: getenv("SMTP_PASSWORD"),
		TLS: mail.TLSStartTLS, Timeout: 10 * time.Second, AppBaseURL: "http://localhost:3000",
	}
	if m.Mode == "" {
		m.Mode = "log"
	}
	if m.Mode != "log" && m.Mode != "smtp" {
		return mail.Config{}, fmt.Errorf("MAIL_MODE must be log or smtp")
	}
	if m.From == "" {
		m.From = "ServiceOps360 <no-reply@localhost>"
	}
	if value := getenv("SMTP_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return mail.Config{}, fmt.Errorf("SMTP_PORT must be between 1 and 65535")
		}
		m.Port = port
	}
	if value := strings.ToLower(strings.TrimSpace(getenv("SMTP_TLS"))); value != "" {
		switch mail.TLSMode(value) {
		case mail.TLSStartTLS, mail.TLSImplicit, mail.TLSNone:
			m.TLS = mail.TLSMode(value)
		default:
			return mail.Config{}, fmt.Errorf("SMTP_TLS must be starttls, tls or none")
		}
	}
	if value := getenv("MAIL_TIMEOUT"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d < time.Second || d > time.Minute {
			return mail.Config{}, fmt.Errorf("MAIL_TIMEOUT must be between 1s and 1m")
		}
		m.Timeout = d
	}
	if value := getenv("MAIL_LOG_BODY"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return mail.Config{}, fmt.Errorf("MAIL_LOG_BODY must be true or false")
		}
		m.LogBody = enabled
	}
	if value := strings.TrimSpace(getenv("APP_BASE_URL")); value != "" {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return mail.Config{}, fmt.Errorf("APP_BASE_URL must be an http(s) address such as https://app.example.com")
		}
		m.AppBaseURL = strings.TrimRight(value, "/")
	}
	if m.Mode == "smtp" && m.Host == "" {
		return mail.Config{}, fmt.Errorf("SMTP_HOST is required when MAIL_MODE=smtp")
	}
	if m.Mode == "smtp" && m.TLS == mail.TLSNone && m.Username != "" {
		return mail.Config{}, fmt.Errorf("SMTP_TLS=none cannot be used with SMTP_USERNAME; credentials need TLS")
	}
	return m, nil
}
