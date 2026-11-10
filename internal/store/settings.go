package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const settingAdminTokenHash = "admin_token_sha256" //nolint:gosec // a settings key, not a credential.

// EnsureAdminToken returns the SHA-256 of the admin token stored in the database.
// On the first call against an empty database it generates a token, stores its
// hash and returns the token itself (created is true); it is never readable again.
func (s *Store) EnsureAdminToken(ctx context.Context) (hash [32]byte, token string, created bool, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return hash, "", false, err
	}
	candidate := "mf_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(candidate))
	tag, err := s.pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`,
		settingAdminTokenHash, hex.EncodeToString(sum[:]))
	if err != nil {
		return hash, "", false, err
	}
	if tag.RowsAffected() == 1 {
		return sum, candidate, true, nil
	}
	var stored string
	if err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingAdminTokenHash).Scan(&stored); err != nil {
		return hash, "", false, err
	}
	raw, err := hex.DecodeString(stored)
	if err != nil || len(raw) != len(hash) {
		return hash, "", false, fmt.Errorf("setting %s is corrupt", settingAdminTokenHash)
	}
	copy(hash[:], raw)
	return hash, "", false, nil
}

// AuditEntry is one operator action.
type AuditEntry struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Actor   string          `json:"actor"`
	Action  string          `json:"action"`
	Target  string          `json:"target"`
	Details json.RawMessage `json:"details"`
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Audit records an operator action outside of a resource change (run start, session).
func (s *Store) Audit(ctx context.Context, actor, action, target string, details map[string]any) error {
	return audit(ctx, s.pool, actor, action, target, details)
}

func audit(ctx context.Context, q execer, actor, action, target string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	d, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO audit (actor, action, target, details) VALUES ($1, $2, $3, $4)`, actor, action, target, d)
	return err
}

// ListAudit returns entries newest first; before is an exclusive id cursor (0 for the start).
func (s *Store) ListAudit(ctx context.Context, before int64, limit int) ([]AuditEntry, error) {
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := s.pool.Query(ctx, `SELECT id, at, actor, action, target, details FROM audit
		WHERE id < $1 ORDER BY id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AuditEntry, error) {
		var e AuditEntry
		var d []byte
		err := row.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &d)
		e.Details = d
		return e, err
	})
}
