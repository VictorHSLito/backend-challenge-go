package postgres

import (
	"github.com/victorhslito/junglegaming/internal/service"
	"go.uber.org/fx"
)

var Module = fx.Module("walletrepo",
	fx.Provide(NewWalletRepo),
	fx.Provide(func(r *WalletRepo) service.Repository {
		return r
	}),
)
