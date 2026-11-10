package itest

import (
	"testing"

	"github.com/t1menar/manufacture-core/internal/db"
)

// TestMigrationsDownAndUp checks that every migration can be rolled back and re-applied.
func TestMigrationsDownAndUp(t *testing.T) {
	pool := newDB(t)
	ctx := ctxT(t)
	if err := db.MigrateDown(ctx, pool); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tables left after down: %d %v", n, err)
	}
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
