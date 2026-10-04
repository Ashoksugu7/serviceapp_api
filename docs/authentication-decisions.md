# Authentication and tenant enforcement

Tasks T05 and T06 implement the authentication and authorization baseline defined in [T01](t01-requirements.md). The application database has not been migrated.

## Authentication lifecycle

- Login normalizes email by trimming and lowercasing it. Unknown email, incorrect password, inactive user and suspended-company login all return the same authentication failure.
- Password hashes use Argon2id with 19 MiB memory, two iterations, parallelism one, a random 16-byte salt and a 32-byte result. New-password validation (T26) accepts 10–128 characters with at least one letter and one number, preserves spaces and Unicode, and rejects the packaged common-password set.
- Access tokens are HS256 JWTs with a 30-minute lifetime. Validation pins the algorithm and verifies issuer `serviceops360`, audience `serviceops360-api`, subject, expiry, issued time, token ID and at most 30 seconds of clock skew.
- `auth_sessions` stores the JWT token ID, user, creation time, expiry and optional revocation time. Raw bearer tokens are never stored.
- Every authenticated request loads the session and current user/company from PostgreSQL. Revoked or expired sessions, inactive users and stale role/company claims return 401. A currently suspended company returns `403 COMPANY_SUSPENDED`.
- Logout revokes the presented session and returns 204. Future password-change handling in T07 must revoke every session for that user in the same transaction as the hash update.

## Login rate limits

- Failed attempts use a shared 15-minute database window, with defaults of five attempts per normalized email and twenty per direct client IP.
- Stored keys are HMAC-SHA256 digests keyed by `JWT_SECRET`; raw emails and IP addresses are not persisted in the rate-limit table.
- The server reads the socket peer address and ignores forwarded-IP headers. Proxy trust can be added only with explicit trusted-proxy configuration.
- `AUTH_RATE_WINDOW`, `AUTH_EMAIL_FAILURE_LIMIT` and `AUTH_IP_FAILURE_LIMIT` tune the defaults. A blocked request returns 429 and `Retry-After`.

## Authorization boundary

- Authentication places a database-verified principal at the HTTP boundary. The shared company authorizer requires an allowed role and, for ADMIN/USER, an exact match between the route company and the authenticated company.
- SUPER_ADMIN can cross company boundaries only when the specific operation includes that role. Its identity has no company and it cannot author service transactions because transaction foreign keys require a company user.
- Later repositories must include the authorized company in every company-owned query and linked-record lookup. A body-supplied company or actor ID is never an authority source.
- API errors use the shared `{ "error": { "code", "message", "fields" } }` shape. Authentication distinguishes invalid credentials, missing/invalid sessions, suspension, forbidden operations and rate limiting.

## Bootstrap

The first SUPER_ADMIN is created with the local interactive `bootstrap-super-admin` command. It requires the initial and authentication migrations, refuses non-terminal input, never accepts a password argument, and uses a transaction advisory lock to prevent concurrent bootstrap accounts. It refuses to run after any SUPER_ADMIN exists.

## Verification

Unit and HTTP tests cover Argon2id encoding, password policy, normalized login, signed token validation, claim tampering, current-identity rechecks, revocation, suspension, tenant mismatch, role mismatch, rate limits, route methods and error responses.

The repository integration test passed against PostgreSQL with a disposable schema. The authentication migration passed constraint/down/reapply verification. All temporary changes were removed; the application database remains unchanged.

## Email or phone sign-in (T25, 4 October 2026)

- `POST /auth/login` takes `{ "identifier", "password" }`. An identifier containing `@` is an email; anything else is a phone number. `email` is still accepted from older clients.
- Users have an optional `phone`, stored as 6–20 digits (formatting such as spaces, `+`, `-` and parentheses is removed) and unique across all users through `users_phone_key` (migration `000004_user_phone`). Onboarding (`admin.phone`), user create and user PATCH accept it; empty or null clears it. A duplicate returns `409 CONFLICT` with `fields.phone`.
- Unknown accounts and wrong passwords return the same `Invalid email, mobile number or password`. Account rate limits use the normalized identifier, so `98765 43210` and `9876543210` share one limit.

## Temporary passwords (T28, 4 October 2026)

- Onboarding (first admin) and user creation no longer accept a password. The server generates a 12-character temporary password (letters and digits without look-alikes such as 0/O and 1/l), stores only its hash, sets `must_change_password` (migration `000005_must_change_password`) and emails the user their username, optional phone and temporary password after the account is saved ([email delivery](email-delivery.md)).
- Responses include `delivery: { email_sent, temporary_password? }`. The temporary password is returned only when the email was not delivered (log mode or SMTP failure) so the administrator can pass it on.
- `POST /companies/{companyId}/users/{userId}/reset-password` (SUPER_ADMIN, own-company ADMIN) issues a new temporary password, sets `must_change_password`, revokes all of the user's sessions and emails the password. User PATCH no longer accepts `password`.
- While `must_change_password` is true, every route except `GET /auth/me`, `POST /auth/logout` and `POST /auth/change-password` returns `403 PASSWORD_CHANGE_REQUIRED`.
- `POST /auth/change-password` (`current_password`, `new_password`) checks the current password, applies the T26 rule, rejects reusing the current password, clears the flag and revokes the user's other sessions.

## Forgot password (T29, 4 October 2026)

- `POST /auth/password-reset/request` (`identifier`: email or phone) always returns `202` with the same message, so accounts cannot be discovered. For an active user of an active company it stores a SHA-256 hash of a random 32-byte token (migration `000006_password_reset_tokens`), cancels the user's earlier open links and emails `APP_BASE_URL/reset-password?token=…` in the background. Requests count against the account and IP limits (`429` when exceeded).
- Links expire after 30 minutes and work once. The token is never logged (unless `MAIL_LOG_BODY=true` in development) or returned by the API.
- `POST /auth/password-reset/confirm` (`token`, `new_password`) applies the T26 rule, sets the password, clears `must_change_password`, consumes every open link for the user and revokes all sessions. Unknown, used and expired tokens return `400 RESET_TOKEN_INVALID`.

