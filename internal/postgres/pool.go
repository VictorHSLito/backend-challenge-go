package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/config"
	"go.uber.org/fx"
)

func NewPool(lc fx.Lifecycle, cfg config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database, verify database url: %w", err)
	}

	poolCfg.MaxConns = 10
	poolCfg.ConnConfig.ConnectTimeout = 5 * time.Second

	logger.Info("connecting to PostgreSQL...", "addr", poolCfg.ConnConfig.Host)

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping on postgres (fail fast): %w", err)
	}

	logger.Info("connected successfully on PostgreSQL")

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			logger.Info("ending pool connections...")
			pool.Close()
			return nil
		},
	})

	return pool, nil
}
