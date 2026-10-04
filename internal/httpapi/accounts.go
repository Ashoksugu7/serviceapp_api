package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"serviceops360/api/internal/mail"
	"serviceops360/api/internal/service"
)

// AccountMail sends the emails that carry temporary passwords (T28).
type AccountMail struct {
	Sender     mail.Sender
	AppBaseURL string
}

// credentialDelivery tells the administrator whether the new user was emailed.
// The temporary password is returned only when the email was not delivered,
// so the administrator can pass it on; it is never stored in plain text.
type credentialDelivery struct {
	EmailSent         bool    `json:"email_sent"`
	TemporaryPassword *string `json:"temporary_password,omitempty"`
}

type credentialEmail struct {
	reset             bool // false: welcome email for a new account
	name, email       string
	phone             *string
	company           string
	temporaryPassword string
}

// deliver sends the email after the account change has been committed. It
// stops early enough to answer within the request deadline; a slow or failed
// send is reported, never treated as a failed request.
func (a AccountMail) deliver(ctx context.Context, logger *slog.Logger, e credentialEmail) credentialDelivery {
	notSent := credentialDelivery{TemporaryPassword: &e.temporaryPassword}
	if a.Sender == nil {
		return notSent
	}
	message, err := mail.Build(e.email, subjectFor(e), contentFor(e, a.AppBaseURL))
	if err != nil {
		logger.Error("account email could not be built", "error", err)
		return notSent
	}
	sendCtx, cancel := context.WithoutCancel(ctx), func() {}
	if deadline, ok := ctx.Deadline(); ok {
		sendCtx, cancel = context.WithDeadline(sendCtx, deadline.Add(-time.Second))
	}
	defer cancel()
	switch err := a.Sender.Send(sendCtx, message); {
	case err == nil:
		return credentialDelivery{EmailSent: true}
	case errors.Is(err, mail.ErrNotSent):
		return notSent
	default:
		logger.Warn("account email not delivered", "error", err)
		return notSent
	}
}

func subjectFor(e credentialEmail) string {
	if e.reset {
		return "Your ServiceOps360 password was reset"
	}
	return "Your ServiceOps360 account"
}

func contentFor(e credentialEmail, appBaseURL string) mail.Content {
	details := []mail.Detail{{Label: "Username", Value: e.email}}
	if e.phone != nil && *e.phone != "" {
		details = append(details, mail.Detail{Label: "Or mobile number", Value: *e.phone})
	}
	details = append(details, mail.Detail{Label: "Temporary password", Value: e.temporaryPassword})
	content := mail.Content{
		Heading: "Welcome to ServiceOps360",
		Paragraphs: []string{
			"Hello " + e.name + ",",
			"An account has been created for you at " + e.company + ". Sign in with the details below.",
		},
		Details: details,
		Action:  &mail.Action{Label: "Sign in", URL: appBaseURL + "/login"},
		Footer:  "You will be asked to choose your own password when you first sign in. If you did not expect this email, contact your administrator.",
	}
	if e.reset {
		content.Heading = "Your password was reset"
		content.Paragraphs = []string{
			"Hello " + e.name + ",",
			"An administrator at " + e.company + " reset your ServiceOps360 password. You have been signed out everywhere; sign in with the temporary password below.",
		}
	}
	return content
}

// sendResetLink emails a password-reset link (T29). Handlers call it in the
// background so the response does not reveal whether the account exists; the
// link is never logged or returned.
func (a AccountMail) sendResetLink(ctx context.Context, logger *slog.Logger, reset service.PasswordResetEmail) {
	if a.Sender == nil {
		return
	}
	message, err := mail.Build(reset.User.Email, "Reset your ServiceOps360 password", resetContent(reset, a.AppBaseURL))
	if err != nil {
		logger.Error("reset email could not be built", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := a.Sender.Send(ctx, message); err != nil && !errors.Is(err, mail.ErrNotSent) {
		logger.Warn("password reset email not delivered", "error", err)
	}
}

func resetContent(reset service.PasswordResetEmail, appBaseURL string) mail.Content {
	return mail.Content{
		Heading: "Reset your password",
		Paragraphs: []string{
			"Hello " + reset.User.Name + ",",
			"We received a request to reset the password for your " + reset.User.Company + " account on ServiceOps360.",
		},
		Action: &mail.Action{Label: "Choose a new password", URL: appBaseURL + "/reset-password?token=" + reset.Token},
		Footer: "The link works once and expires in 30 minutes. If you did not ask for this, ignore this email; your password stays the same.",
	}
}
