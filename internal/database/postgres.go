package database

import (
	"context"
	"errors"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"serviceops360/api/internal/config"
)

// Open verifies connectivity before returning the pool. Connection errors are
// deliberately redacted because driver errors can contain connection details.
func Open(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	poolConfig.MaxConns = cfg.DBMaxConns
	poolConfig.ConnConfig.ConnectTimeout = cfg.DBConnectTimeout
	connectCtx, cancel := context.WithTimeout(ctx, cfg.DBConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, errors.New("could not initialize PostgreSQL pool")
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, connectionError(err)
	}
	return pool, nil
}

func connectionError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000":
			return errors.New("PostgreSQL authentication failed; check database credentials and access rules")
		case "3D000":
			return errors.New("configured PostgreSQL database does not exist")
		case "53300":
			return errors.New("PostgreSQL connection limit reached")
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("PostgreSQL connection timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("PostgreSQL connection canceled")
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return errors.New("PostgreSQL connection refused; check host, port and server availability")
	}
	return errors.New("could not connect to PostgreSQL; check connection settings, TLS and server availability")
}
