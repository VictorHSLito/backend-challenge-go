package sqsconsumer

import (
	"context"
	"sync"

	"go.uber.org/fx"
)

var Module = fx.Module("sqsconsumer",
	fx.Provide(New),
	fx.Invoke(Register),
)

// Register wires the consumer into Fx's lifecycle. SIGTERM → cancel → the
// current Receive returns `context.Canceled` → the loop exits → wg.Wait
// unblocks → shutdown proceeds. In-flight messages that already started
// processing finish their DB tx (because that uses the main ctx inherited
// before cancel).
func Register(lc fx.Lifecycle, c *Consumer) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.Run(ctx)
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
