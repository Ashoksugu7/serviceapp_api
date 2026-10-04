package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"serviceops360/api/internal/service"
)

type AuthStore struct {
	pool *pgxpool.Pool
}

func NewAuthStore(pool *pgxpool.Pool) *AuthStore { return &AuthStore{pool: pool} }

func (s *AuthStore) BootstrapSuperAdmin(ctx context.Context, name, email, passwordHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A transaction-scoped advisory lock serializes bootstrap attempts even
	// before a SUPER_ADMIN row exists to lock.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('serviceops360-super-admin-bootstrap'))`); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE role='SUPER_ADMIN')`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errors.New("a SUPER_ADMIN already exists")
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO users(name,email,password_hash,role,status)
        VALUES ($1,$2,$3,'SUPER_ADMIN','ACTIVE')`, name, email, passwordHash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *AuthStore) AuthenticateAndCreateSession(
	ctx context.Context,
	login service.LoginIdentifier,
	password string,
	verify service.PasswordVerifier,
	sessionID string,
	createdAt time.Time,
	expiresAt time.Time,
) (service.Identity, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return service.Identity{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	where, value := `lower(u.email)=$1`, login.Email
	if login.Phone != "" {
		where, value = `u.phone=$1`, login.Phone
	}
	row := tx.QueryRow(ctx, identitySelect+`
        WHERE `+where+`
        FOR UPDATE OF u`, value)
	identity, passwordHash, err := scanIdentity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		verify("", password)
		return service.Identity{}, service.ErrInvalidCredentials
	}
	if err != nil {
		return service.Identity{}, err
	}
	validPassword := verify(passwordHash, password)
	if !validPassword || identity.User.Status != "ACTIVE" ||
		(identity.Company != nil && identity.Company.Status != "ACTIVE") {
		return service.Identity{}, service.ErrInvalidCredentials
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO auth_sessions(id,user_id,created_at,expires_at)
        VALUES ($1,$2,$3,$4)`, sessionID, identity.User.ID, createdAt, expiresAt); err != nil {
		return service.Identity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return service.Identity{}, err
	}
	return identity, nil
}

func (s *AuthStore) LoadSessionIdentity(ctx context.Context, sessionID, userID string, now time.Time) (service.Identity, error) {
	row := s.pool.QueryRow(ctx, identitySelect+`
        JOIN auth_sessions a ON a.user_id=u.id
        WHERE a.id=$1 AND u.id=$2 AND a.revoked_at IS NULL AND a.expires_at>$3`, sessionID, userID, now)
	identity, _, err := scanIdentity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Identity{}, service.ErrUnauthenticated
	}
	if err != nil {
		return service.Identity{}, err
	}
	if identity.User.Status != "ACTIVE" {
		return service.Identity{}, service.ErrUnauthenticated
	}
	if identity.Company != nil && identity.Company.Status != "ACTIVE" {
		return service.Identity{}, service.ErrCompanySuspended
	}
	return identity, nil
}

func (s *AuthStore) RevokeSession(ctx context.Context, sessionID, userID string, now time.Time) error {
	command, err := s.pool.Exec(ctx, `
        UPDATE auth_sessions SET revoked_at=$3
        WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, sessionID, userID, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return service.ErrUnauthenticated
	}
	return nil
}

func (s *AuthStore) RateLimitCounts(ctx context.Context, emailKey, ipKey []byte, windowStart time.Time) (int, int, error) {
	var emailCount, ipCount int
	err := s.pool.QueryRow(ctx, `
        SELECT
            COALESCE(max(attempts) FILTER (WHERE scope='EMAIL' AND key_hash=$1),0),
            COALESCE(max(attempts) FILTER (WHERE scope='IP' AND key_hash=$2),0)
        FROM auth_rate_limits
        WHERE window_start=$3
          AND ((scope='EMAIL' AND key_hash=$1) OR (scope='IP' AND key_hash=$2))`,
		emailKey, ipKey, windowStart).Scan(&emailCount, &ipCount)
	return emailCount, ipCount, err
}

func (s *AuthStore) RecordLoginFailure(ctx context.Context, emailKey, ipKey []byte, windowStart, windowEnd time.Time) (int, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM auth_rate_limits WHERE expires_at<=$1`, windowStart); err != nil {
		return 0, 0, err
	}
	upsert := `
        INSERT INTO auth_rate_limits(scope,key_hash,window_start,attempts,expires_at)
        VALUES ($1,$2,$3,1,$4)
        ON CONFLICT (scope,key_hash,window_start)
        DO UPDATE SET attempts=auth_rate_limits.attempts+1, expires_at=EXCLUDED.expires_at
        RETURNING attempts`
	var emailCount, ipCount int
	if err := tx.QueryRow(ctx, upsert, "EMAIL", emailKey, windowStart, windowEnd).Scan(&emailCount); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, upsert, "IP", ipKey, windowStart, windowEnd).Scan(&ipCount); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return emailCount, ipCount, nil
}

const identitySelect = `
    SELECT u.id::text,u.company_id::text,u.name,u.email,u.phone,u.password_hash,u.role,u.status,u.must_change_password,
           c.id::text,c.name,c.email,c.contact,c.address,c.status,c.created_at,c.updated_at
    FROM users u
    LEFT JOIN companies c ON c.id=u.company_id`

type rowScanner interface {
	Scan(...any) error
}

func scanIdentity(row rowScanner) (service.Identity, string, error) {
	var identity service.Identity
	var userCompanyID pgtype.Text
	var companyID, companyName, companyEmail, companyContact, companyAddress, companyStatus pgtype.Text
	var companyCreated, companyUpdated pgtype.Timestamptz
	var passwordHash string
	var phone pgtype.Text
	err := row.Scan(
		&identity.User.ID, &userCompanyID, &identity.User.Name, &identity.User.Email, &phone, &passwordHash,
		&identity.User.Role, &identity.User.Status, &identity.User.MustChangePassword,
		&companyID, &companyName, &companyEmail, &companyContact, &companyAddress,
		&companyStatus, &companyCreated, &companyUpdated,
	)
	if err != nil {
		return service.Identity{}, "", err
	}
	if userCompanyID.Valid {
		identity.User.CompanyID = &userCompanyID.String
	}
	identity.User.Phone = optionalText(phone)
	if companyID.Valid {
		identity.Company = &service.Company{
			ID: companyID.String, Name: companyName.String,
			Email: optionalText(companyEmail), Contact: optionalText(companyContact), Address: optionalText(companyAddress),
			Status: companyStatus.String, CreatedAt: companyCreated.Time, UpdatedAt: companyUpdated.Time,
		}
	}
	return identity, passwordHash, nil
}

func optionalText(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

// ChangePassword verifies the current password, stores the new hash, clears
// must_change_password and revokes the user's other sessions in one transaction.
func (s *AuthStore) ChangePassword(ctx context.Context, userID, sessionID, currentPassword string, verify service.PasswordVerifier, newHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentHash string
	if err := tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 AND status='ACTIVE' FOR UPDATE`, userID).Scan(&currentHash); errors.Is(err, pgx.ErrNoRows) {
		return service.ErrUnauthenticated
	} else if err != nil {
		return err
	}
	if !verify(currentHash, currentPassword) {
		return service.ErrInvalidCredentials
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,must_change_password=false WHERE id=$1`, userID, newHash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`, userID, sessionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreatePasswordReset stores a reset token for an active account (and active
// company) and cancels the user's earlier open links. found is false when no
// such account exists.
func (s *AuthStore) CreatePasswordReset(ctx context.Context, login service.LoginIdentifier, tokenHash []byte, expiresAt time.Time) (service.PasswordResetUser, bool, error) {
	where, value := `lower(u.email)=$1`, login.Email
	if login.Phone != "" {
		where, value = `u.phone=$1`, login.Phone
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return service.PasswordResetUser{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	var user service.PasswordResetUser
	var company pgtype.Text
	err = tx.QueryRow(ctx, `
        SELECT u.id::text,u.name,u.email,c.name FROM users u
        LEFT JOIN companies c ON c.id=u.company_id
        WHERE `+where+` AND u.status='ACTIVE' AND (c.id IS NULL OR c.status='ACTIVE')
        FOR UPDATE OF u`, value).Scan(&userID, &user.Name, &user.Email, &company)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.PasswordResetUser{}, false, nil
	}
	if err != nil {
		return service.PasswordResetUser{}, false, err
	}
	user.Company = company.String
	if user.Company == "" {
		user.Company = "ServiceOps360"
	}
	if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at=now() WHERE user_id=$1 AND used_at IS NULL`, userID); err != nil {
		return service.PasswordResetUser{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO password_reset_tokens(user_id,token_hash,expires_at) VALUES ($1,$2,$3)`, userID, tokenHash, expiresAt); err != nil {
		return service.PasswordResetUser{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return service.PasswordResetUser{}, false, err
	}
	return user, true, nil
}

// ConfirmPasswordReset consumes an open, unexpired token: it sets the password,
// clears must_change_password, closes every open link for the user and revokes
// all sessions in one transaction.
func (s *AuthStore) ConfirmPasswordReset(ctx context.Context, tokenHash []byte, newHash string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `
        SELECT t.user_id::text FROM password_reset_tokens t
        JOIN users u ON u.id=t.user_id
        LEFT JOIN companies c ON c.id=u.company_id
        WHERE t.token_hash=$1 AND t.used_at IS NULL AND t.expires_at>$2
          AND u.status='ACTIVE' AND (c.id IS NULL OR c.status='ACTIVE')
        FOR UPDATE OF t,u`, tokenHash, now).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrResetTokenInvalid
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,must_change_password=false WHERE id=$1`, userID, newHash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at=$2 WHERE user_id=$1 AND used_at IS NULL`, userID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
