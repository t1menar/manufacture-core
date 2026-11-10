// Package db owns the PostgreSQL pool, the embedded migrations and the transaction helper.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open connects a pool and verifies the connection.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "manufacture"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies all embedded migrations under a PostgreSQL session advisory lock,
// so several instances starting at once never run them twice. It returns the
// versions that were applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	return migrateSQL(ctx, sqlDB)
}

func migrateSQL(ctx context.Context, sqlDB *sql.DB) ([]int64, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("migration provider: %w", err)
	}
	res, err := p.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate up: %w", err)
	}
	applied := make([]int64, 0, len(res))
	for _, r := range res {
		applied = append(applied, r.Source.Version)
	}
	return applied, nil
}

// WithTx runs fn in one transaction; it commits only if fn returns nil.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	err = fn(tx)
	if err != nil {
		return err
	}
	err = tx.Commit(ctx)
	return err
}

// IsUniqueViolation reports whether err is a unique constraint violation (SQLSTATE 23505),
// optionally on a named constraint or index.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// MigrateDown rolls back every migration (tests check that each Down works).
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub)
	if err != nil {
		return err
	}
	_, err = p.DownTo(ctx, 0)
	return err
}
