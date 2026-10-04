// Command send-test-email sends one message with the API's email settings so
// SMTP can be checked before account emails depend on it:
//
//	MAIL_MODE=smtp SMTP_HOST=… go run ./cmd/send-test-email you@example.com
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"serviceops360/api/internal/config"
	"serviceops360/api/internal/mail"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: send-test-email recipient@example.com")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "send-test-email:", err)
		os.Exit(1)
	}
}

func run(recipient string) error {
	cfg, err := config.LoadMail()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	sender, err := mail.New(cfg, logger)
	if err != nil {
		return err
	}
	message, err := mail.Build(recipient, "ServiceOps360 test email", mail.Content{
		Heading:    "Email is working",
		Paragraphs: []string{"This test message was sent with the ServiceOps360 API email settings."},
		Details:    []mail.Detail{{Label: "Mode", Value: cfg.Mode}, {Label: "Sent", Value: time.Now().Format(time.RFC1123)}},
		Action:     &mail.Action{Label: "Open ServiceOps360", URL: cfg.AppBaseURL},
	})
	if err != nil {
		return err
	}
	if err := sender.Send(context.Background(), message); errors.Is(err, mail.ErrNotSent) {
		fmt.Println("MAIL_MODE=log: the message was written to the log above, not delivered.")
		return nil
	} else if err != nil {
		return err
	}
	fmt.Printf("Test email handed to %s sender for %s.\n", cfg.Mode, recipient)
	return nil
}
