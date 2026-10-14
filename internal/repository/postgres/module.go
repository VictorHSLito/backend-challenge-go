package postgres

import (
	"github.com/victorhslito/junglegaming/internal/service"
	"go.uber.org/fx"
)

var Module = fx.Module("repository",
	fx.Provide(NewTxRunner),
	fx.Provide(NewWalletRepo),
	fx.Provide(NewTransactionRepo),
	fx.Provide(NewOutboxRepo),
	fx.Provide(NewInboxRepo),
	fx.Provide(func(r *TxRunner) service.TxManager { return r }),
	fx.Provide(func(r *WalletRepo) service.WalletRepository { return r }),
	fx.Provide(func(r *TransactionRepo) service.TransactionRepository { return r }),
	fx.Provide(func(r *OutboxRepo) service.OutboxRepository { return r }),
	fx.Provide(func(r *InboxRepo) service.InboxRepository { return r }),
)
