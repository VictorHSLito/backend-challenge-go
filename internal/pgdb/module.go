package pgdb

import "go.uber.org/fx"

var Module = fx.Module("postgres",
	fx.Provide(NewPool),
)
