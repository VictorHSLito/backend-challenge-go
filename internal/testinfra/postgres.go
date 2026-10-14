// Package testinfra holds helpers shared by integration tests. Keep this
// package infra-only: no business imports, no reliance on the service
// layer. Tests can then import it from anywhere without creating cycles.
package testinfra

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer owns the testcontainer plus a ready-to-use connection
// pool. Call Close() when the TestMain exits.
type PostgresContainer struct {
	container *postgres.PostgresContainer
	DSN       string
	Pool      *pgxpool.Pool
}

// StartPostgres boots a Postgres 16 container, applies every migration in
// migrations/ (sorted by filename), and returns a connected pool. It is
// safe to call once per package from TestMain.
func StartPostgres(ctx context.Context) (*PostgresContainer, error) {
	container, err := postgres.Run(ctx,
		"postgres:16.13",
		postgres.WithDatabase("wager_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("resolve dsn: %w", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 20

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := applyMigrations(ctx, pool); err != nil {
		pool.Close()
		_ = container.Terminate(ctx)
		return nil, err
	}
	return &PostgresContainer{container: container, DSN: dsn, Pool: pool}, nil
}

// Close releases the pool and terminates the container. Idempotent-ish —
// safe to call once at the end of TestMain.
func (p *PostgresContainer) Close(ctx context.Context) {
	if p.Pool != nil {
		p.Pool.Close()
	}
	if p.container != nil {
		_ = p.container.Terminate(ctx)
	}
}

// Truncate wipes every table so each test starts from a clean slate. The
// append-only ledger trigger only guards UPDATE and DELETE, so TRUNCATE
// still works — that's deliberate (the schema is for the running app,
// not for test isolation).
func (p *PostgresContainer) Truncate(ctx context.Context, t testing.TB) {
	t.Helper()
	_, err := p.Pool.Exec(ctx, `
		TRUNCATE wallet_ledger_entries, wager_transactions, wallets, inbox, outbox RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("truncate failed: %v", err)
	}
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("cannot resolve migrations directory")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	var upFiles []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".up.sql") {
			upFiles = append(upFiles, name)
		}
	}
	sort.Strings(upFiles)

	for _, name := range upFiles {
		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}
