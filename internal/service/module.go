package service

import "go.uber.org/fx"

var Module = fx.Module(
	"service",
	fx.Provide(NewWalletService),
	fx.Provide(NewTransactionService),
)
