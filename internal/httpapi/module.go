package httpapi

import "go.uber.org/fx"

var Module = fx.Module("http",
	fx.Provide(NewMux, NewServer),
	fx.Invoke(Register),
)
