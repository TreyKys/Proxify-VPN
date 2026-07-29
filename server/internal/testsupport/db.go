// Package testsupport wires a real Postgres up for tests.
//
// The provisioning logic lives in SQL as much as in Go — partial unique
// indexes, FOR UPDATE ordering, SKIP LOCKED claims. Faking the database would
// test a different program than the one we ship, so these tests need a real
// server. Set TEST_DATABASE_URL to run them; without it they skip.
package testsupport

import (
	"context"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/treykys/proxify-vpn/server/internal/store"
	"github.com/treykys/proxify-vpn/server/migrations"
)

// NewStore returns a store backed by a migrated, empty database. Tables are
// truncated (not dropped) between tests, so migrations run once per process.
func NewStore(t *testing.T) *store.Store {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := truncate(ctx, pool); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	return store.NewWithPool(pool)
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	const createTable = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now())`
	if _, err := pool.Exec(ctx, createTable); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var applied bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).
			Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			return err
		}
	}
	return nil
}

// truncate clears user data but leaves `plans` alone — plans are seed data that
// the migration inserts, and tests read them as fixtures.
func truncate(ctx context.Context, pool *pgxpool.Pool) error {
	const q = `
		TRUNCATE peer_assignments, devices, servers, subscriptions, payments,
		         webhook_events, refresh_tokens, users RESTART IDENTITY CASCADE`
	_, err := pool.Exec(ctx, q)
	return err
}
