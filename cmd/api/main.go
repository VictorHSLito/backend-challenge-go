package main

import (
	"log/slog"

	"github.com/victorhslito/junglegaming/internal/config"
	"github.com/victorhslito/junglegaming/internal/httpapi"
	"github.com/victorhslito/junglegaming/internal/logger"
	"github.com/victorhslito/junglegaming/internal/postgres"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

func main() {
	fx.New(
		fx.Provide(
			config.LoadConfig,
			logger.NewLogger),
		fx.WithLogger(func(l *slog.Logger) fxevent.Logger {
			return &fxevent.SlogLogger{Logger: l}
		}),
		postgres.Module,
		httpapi.Module,
	).Run()
}
