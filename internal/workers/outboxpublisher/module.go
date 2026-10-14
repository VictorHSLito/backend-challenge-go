package outboxpublisher

import (
	"context"
	"sync"

	"go.uber.org/fx"
)

var Module = fx.Module("outboxpublisher",
	fx.Provide(New),
	fx.Invoke(Register),
)

// Register starts the publisher goroutine with Fx-managed lifecycle. On
// shutdown the context cancels, Run returns, and WaitGroup.Wait blocks
// until the current loop iteration finishes — so we never kill a tx mid-flight.
func Register(lc fx.Lifecycle, p *Publisher) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.Run(ctx)
			}()
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			wg.Wait()
			return nil
		},
	})
}
