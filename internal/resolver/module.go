package resolver

import (
	"context"
	"sync"

	"go.uber.org/fx"
)

var Module = fx.Module("resolver",
	fx.Provide(New),
	fx.Invoke(Register),
)

func Register(lc fx.Lifecycle, r *Resolver) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.Run(ctx)
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
