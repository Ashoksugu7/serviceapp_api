package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"serviceops360/api/internal/config"
	"serviceops360/api/internal/database"
	"serviceops360/api/internal/httpapi"
	"serviceops360/api/internal/mail"
	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	authService, err := service.NewAuthService(repository.NewAuthStore(pool), service.AuthConfig{
		Secret: []byte(cfg.JWTSecret), Issuer: "serviceops360", Audience: "serviceops360-api",
		TokenTTL: cfg.AuthTokenTTL, ClockSkew: cfg.AuthClockSkew, RateWindow: cfg.AuthRateWindow,
		EmailLimit: cfg.AuthEmailLimit, IPLimit: cfg.AuthIPLimit,
	})
	if err != nil {
		return err
	}
	// Built at startup so mail settings are validated before serving.
	mailer, err := mail.New(cfg.Mail, logger)
	if err != nil {
		return err
	}
	logger.Info("email delivery", "mode", cfg.Mail.Mode, "app_base_url", cfg.Mail.AppBaseURL)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewApplicationHandler(pool, logger, cfg.RequestTimeout, authService, repository.NewCatalogStore(pool), httpapi.AccountMail{Sender: mailer, AppBaseURL: cfg.Mail.AppBaseURL}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.RequestTimeout,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	return serve(ctx, server, listener, cfg.ShutdownTimeout, logger)
}

// serve accepts an already-bound listener, allowing lifecycle checks on an
// ephemeral port without touching the developer's running API.
func serve(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("API started", "address", listener.Addr().String())
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("API shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
