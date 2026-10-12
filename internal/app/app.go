package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/victorhslito/junglegaming/internal/config"
	"github.com/victorhslito/junglegaming/internal/handler"
	"github.com/victorhslito/junglegaming/internal/observability"
	"github.com/victorhslito/junglegaming/internal/service"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

const StopTimeout = 30 * time.Second

func Options() fx.Option {
	return fx.Options(
		config.Module,
		observability.Module,

		fx.Provide(
			service.NewHealthService,
			handler.NewHealthHandler,
		),

		fx.Supply([]service.HealthChecker{}),

		fx.StartTimeout(60*time.Second),
		fx.StopTimeout(StopTimeout),
		fx.WithLogger(func(log *slog.Logger) fxevent.Logger {
			l := &fxevent.SlogLogger{Logger: log}
			l.UseLogLevel(slog.LevelDebug)
			return l
		}),
		fx.Invoke(startHTTPServer),
	)
}

func startHTTPServer(
	lc fx.Lifecycle,
	cfg config.Config,
	log *slog.Logger,
	health *handler.HealthHandler,
) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", health.Live)
	mux.HandleFunc("GET /health/ready", health.Ready)

	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: mux,
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			listener, err := net.Listen("tcp", server.Addr)
			if err != nil {
				return err
			}

			log.Info("HTTP server started", "addr", server.Addr)

			go func() {
				if err := server.Serve(listener); err != nil &&
					!errors.Is(err, http.ErrServerClosed) {
					log.Error("HTTP server stopped", "error", err)
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			return server.Shutdown(ctx)
		},
	})
}
