// Package store keeps the core state in PostgreSQL: resources and their revisions,
// settings and the audit log. Runs live in the engine tables (added with the engine).
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/t1menar/manufacture-core/internal/spec"
)

// Store is safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps a pool whose database is migrated.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the pool to packages that own other tables (engine).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks the database connection.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Source says where a change came from; it is stored with every revision and audit entry.
type Source string

// Sources.
const (
	SourceCLI       Source = "cli"
	SourceUI        Source = "ui"
	SourceBootstrap Source = "bootstrap"
	SourceAPI       Source = "api"
)

// Valid reports whether s is a known source.
func (s Source) Valid() bool {
	switch s {
	case SourceCLI, SourceUI, SourceBootstrap, SourceAPI:
		return true
	default:
		return false
	}
}

// Errors returned by the store. Callers map them to API status codes.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// ConflictError explains why an operation conflicts with the current state.
type ConflictError struct{ Reason string }

func (e *ConflictError) Error() string { return e.Reason }

// Is makes errors.Is(err, ErrConflict) true.
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// ValidationError carries the diagnostics of a rejected apply.
type ValidationError struct{ Diagnostics spec.Diagnostics }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %d error(s)", e.Diagnostics.Errors())
}

// resourceLock serializes changes of resources, so reference checks
// (a pipeline needs its modules) cannot race with deletes.
const resourceLock = 0x6d665f7265730001 // "mf_res" + 1

func lockResources(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(resourceLock))
	return err
}
