package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/domain/event"
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

// PendingOutboxEvent is what the publisher worker reads from the DB. It is
// the stored envelope plus bookkeeping fields (attempts, next_delivery_at).
type PendingOutboxEvent struct {
	EventID        string
	AggregateID    string
	EventType      event.Type
	Payload        []byte
	OccurredAt     time.Time
	Attempts       int
	NextDeliveryAt time.Time
}

// OutboxRepository: write side used by services; read side used by the
// publisher worker. Living on one interface keeps the Fx graph small.
type OutboxRepository interface {
	// InsertEventTx stores the envelope atomically with the surrounding
	// business change. Called by services/resolver inside their RunInTx.
	InsertEventTx(ctx context.Context, tx pgx.Tx, env event.Envelope) error

	// FetchDuePendingTx locks (SKIP LOCKED) up to `limit` rows that are
	// unpublished and whose next_delivery_at <= now. The caller publishes
	// each, then marks it published or scheduled-for-retry — all inside
	// the same tx so a crash releases the locks for another publisher.
	FetchDuePendingTx(ctx context.Context, tx pgx.Tx, now time.Time, limit int) ([]PendingOutboxEvent, error)
	MarkPublishedTx(ctx context.Context, tx pgx.Tx, eventID string, now time.Time) error
	MarkRetryTx(ctx context.Context, tx pgx.Tx, eventID string, nextDeliveryAt time.Time) error
}

// InboxMessage is what MarkReceivedTx returns on a conflict so the caller
// can tell "already seen with same hash" (safe to drop) from "same id,
// different hash" (poison / divergence).
type InboxMessage struct {
	ConsumerName  string
	MessageID     string
	MessageHash   string
	TransactionID string
	ReceivedAt    time.Time
	CompletedAt   *time.Time
}

// InboxRepository is the dedup table used by SQS-style consumers.
type InboxRepository interface {
	// MarkReceivedTx inserts a (consumer, messageId) row with ON CONFLICT
	// DO NOTHING. Returns `inserted=true` on first sight; false otherwise,
	// in which case `prior` holds the existing row so the caller can
	// compare hashes.
	MarkReceivedTx(ctx context.Context, tx pgx.Tx, consumerName, messageID, messageHash string, now time.Time) (inserted bool, prior InboxMessage, err error)
	MarkCompletedTx(ctx context.Context, tx pgx.Tx, consumerName, messageID, transactionID string, now time.Time) error
}
