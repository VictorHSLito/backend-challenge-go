package observability

import (
	"go.uber.org/fx"
)

// Module provides the logger and the metrics registry.
var Module = fx.Module("observability",
	fx.Provide(NewLogger),
)
