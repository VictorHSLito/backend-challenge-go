package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
)

// TxManager owns the transaction boundary. The service calls RunInTx and
// hands the pgx.Tx to repository Tx methods inside the callback.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

// WalletRepository groups persistence for the wallet aggregate: the wallet
// row itself and its immutable ledger entries.
type WalletRepository interface {
	InsertWalletTx(ctx context.Context, tx pgx.Tx, w wallet.Wallet) error
	UpdateWalletTx(ctx context.Context, tx pgx.Tx, w wallet.Wallet) error
	GetWalletByID(ctx context.Context, id string) (wallet.Wallet, bool, error)
	GetWalletForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (wallet.Wallet, bool, error)

	InsertLedgerEntryTx(ctx context.Context, tx pgx.Tx, e ledger.Entry) error
	ListLedgerEntries(ctx context.Context, walletID string, cursorCreatedAt time.Time, cursorID string, limit int) ([]ledger.Entry, bool, time.Time, string, error)
	SumLedgerForWallet(ctx context.Context, walletID string) (creditSum, debitSum int64, currency string, checkedEntries int, err error)
}

// TransactionRepository groups persistence for the wager_transaction aggregate.
type TransactionRepository interface {
	InsertWagerTransactionTx(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction) error
	UpdateWagerTransactionTx(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction) error

	GetTransactionByID(ctx context.Context, id string) (transaction.WagerTransaction, bool, error)
	GetTransactionByIDTx(ctx context.Context, tx pgx.Tx, id string) (transaction.WagerTransaction, bool, error)
	GetTransactionByProviderExternalID(ctx context.Context, providerID, externalTxID string) (transaction.WagerTransaction, bool, error)
	GetTransactionByProviderExternalIDTx(ctx context.Context, tx pgx.Tx, providerID, externalTxID string) (transaction.WagerTransaction, bool, error)
	GetTransactionByProviderIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, providerID, idempotencyKey string) (transaction.WagerTransaction, bool, error)
	GetProcessedReversalTx(ctx context.Context, tx pgx.Tx, providerID, originalTransactionID string) (transaction.WagerTransaction, bool, error)
	FetchOneDuePendingReferenceTx(ctx context.Context, tx pgx.Tx, now time.Time) (transaction.WagerTransaction, bool, error)
}
