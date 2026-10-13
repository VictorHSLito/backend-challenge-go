package main

import (
	"log/slog"

	"github.com/victorhslito/junglegaming/internal/config"
	"github.com/victorhslito/junglegaming/internal/httpapi"
	"github.com/victorhslito/junglegaming/internal/logger"
	"github.com/victorhslito/junglegaming/internal/pgdb"
	"github.com/victorhslito/junglegaming/internal/repository/postgres"
	"github.com/victorhslito/junglegaming/internal/service"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

func main() {
	fx.New(
		fx.Provide(
			config.LoadConfig,
			logger.NewLogger,
			postgres.NewWalletRepo,
			func(r *postgres.WalletRepo) service.WalletRepository {
				return r
			},
			service.NewService,
		),
		fx.WithLogger(func(l *slog.Logger) fxevent.Logger {
			return &fxevent.SlogLogger{Logger: l}
		}),
		pgdb.Module,
		httpapi.Module,
	).Run()
}
