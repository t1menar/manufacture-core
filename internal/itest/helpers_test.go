// Package itest holds integration tests that need PostgreSQL (MF_TEST_DATABASE_URL)
// and, later, RabbitMQ (MF_TEST_AMQP_URL). They are skipped when the variables are unset.
package itest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/t1menar/manufacture-core/internal/db"
	"github.com/t1menar/manufacture-core/internal/store"
)

const envDatabaseURL = "MF_TEST_DATABASE_URL"

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// newDB creates an empty, migrated database for one test and drops it afterwards.
func newDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv(envDatabaseURL)
	if base == "" {
		t.Skipf("%s is not set", envDatabaseURL)
	}
	ctx := ctxT(t)
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "mf_it_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		c := context.Background()
		_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(c)
	})
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func newStore(t *testing.T) *store.Store { return store.New(newDB(t)) }

func file(name, content string) store.File { return store.File{Name: name, Content: []byte(content)} }

const helloModules = `
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-init}
spec: {image: localhost:5001/mf-hello-init:0.1.0, replicas: 1}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-hello}
spec: {image: localhost:5001/mf-hello-hello:0.1.0, replicas: 2}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-world}
spec: {image: localhost:5001/mf-hello-world:0.1.0}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-exclaim}
spec: {image: localhost:5001/mf-hello-exclaim:0.1.0}
`

const helloPipeline = `
apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: hello-world}
spec:
  trigger: {source: hello-init}
  stages:
    - name: hello
      step: {module: hello-hello, timeout: 30s}
    - name: world
      step: {module: hello-world, timeout: 30s}
    - name: exclaim
      step: {module: hello-exclaim, timeout: 30s}
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func quietLog() *slog.Logger {
	if os.Getenv("MF_TEST_VERBOSE") != "" {
		return slog.Default()
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
