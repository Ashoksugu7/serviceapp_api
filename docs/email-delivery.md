# Email delivery (T27)

The API sends transactional email for account onboarding (T28) and password reset (T29). Delivery is configured with environment variables and checked at startup; a wrong setting stops the API with a clear error.

| Variable | Default | Meaning |
| --- | --- | --- |
| `MAIL_MODE` | `log` | `log` writes each email to the API log instead of sending it; `smtp` sends through `SMTP_HOST`. |
| `MAIL_LOG_BODY` | `false` | Log mode only: include the body. Bodies contain temporary passwords and reset links — enable only on a development machine. |
| `MAIL_FROM` | `ServiceOps360 <no-reply@localhost>` | Sender shown to recipients; must be an address the SMTP provider allows. |
| `APP_BASE_URL` | `http://localhost:3000` | Public UI address used in email links, e.g. `http://200.141.10.110:3001`. |
| `SMTP_HOST`, `SMTP_PORT` | —, `587` | SMTP relay. Required for `smtp`. |
| `SMTP_TLS` | `starttls` | `starttls` (port 587, STARTTLS required), `tls` (port 465) or `none` (local relay only; refused with credentials). |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | empty | Relay credentials (PLAIN authentication over TLS). |
| `MAIL_TIMEOUT` | `10s` | Per-message limit, 1s–1m. |

## Behaviour

- Code sends only after its database transaction commits. A failed send is logged and reported to the caller (for example `email_sent: false`); it never undoes the saved change.
- Messages have plain-text and HTML parts (`internal/mail`). Header values containing line breaks are rejected.
- Log mode records recipient and subject; never enable `MAIL_LOG_BODY` in production.

## Check the settings

With the same environment as the API (no database needed):

```sh
go run ./cmd/send-test-email you@example.com
```

In log mode the message appears in the output; with `MAIL_MODE=smtp` it is delivered or the command prints the SMTP error.
