package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/victorhslito/junglegaming/internal/config"
)

func NewPool(lc fx.Lifecycle, cfg config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pc.MaxConns = cfg.DBMaxConns
	pc.ConnConfig.ConnectTimeout = 5 * time.Second

	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return waitFor(ctx, func(ctx context.Context) error { return pool.Ping(ctx) }, func(err error) {
				log.WarnContext(ctx, "postgres not ready, retrying", slog.Any("error", err))
			})
		},
		OnStop: func(context.Context) error {
			pool.Close()
			log.Info("postgres pool closed")
			return nil
		},
	})
	return pool, nil
}

func waitFor(ctx context.Context, check func(context.Context) error, onRetry func(error)) error {
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := check(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		onRetry(err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("dependency not ready: %w", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// PostgresHealth reports database readiness.
type PostgresHealth struct {
	pool *pgxpool.Pool
}

func NewPostgresHealth(pool *pgxpool.Pool) *PostgresHealth { return &PostgresHealth{pool: pool} }

func (h *PostgresHealth) Name() string { return "postgres" }

func (h *PostgresHealth) Check(ctx context.Context) error {
	var one int
	return h.pool.QueryRow(ctx, "SELECT 1").Scan(&one)
}
