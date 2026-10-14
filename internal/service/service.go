package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
)

type Repository interface {
	RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error
	InsertWalletTx(ctx context.Context, tx pgx.Tx, w wallet.Wallet) error
	GetWalletByID(ctx context.Context, id string) (wallet.Wallet, bool, error)
	GetWalletForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (wallet.Wallet, bool, error)
	InsertWagerTransactionTx(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction) error
	UpdateWagerTransactionTx(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction) error
	InsertLedgerEntryTx(ctx context.Context, tx pgx.Tx, e ledger.Entry) error
	GetTransactionByID(ctx context.Context, id string) (transaction.WagerTransaction, bool, error)
	GetTransactionByIDTx(ctx context.Context, tx pgx.Tx, id string) (transaction.WagerTransaction, bool, error)
	GetTransactionByProviderExternalID(ctx context.Context, providerID, externalTxID string) (transaction.WagerTransaction, bool, error)
	GetTransactionByProviderExternalIDTx(ctx context.Context, tx pgx.Tx, providerID, externalTxID string) (transaction.WagerTransaction, bool, error)
	UpdateWalletTx(ctx context.Context, tx pgx.Tx, w wallet.Wallet) error
	GetTransactionByProviderIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, providerID, idempotencyKey string) (transaction.WagerTransaction, bool, error)
	GetProcessedReversalTx(ctx context.Context, tx pgx.Tx, providerID, externalTxID string) (transaction.WagerTransaction, bool, error)
	FetchOneDuePendingReferenceTx(ctx context.Context, tx pgx.Tx, now time.Time) (transaction.WagerTransaction, bool, error)
	ListLedgerEntries(ctx context.Context, walletID string, cursorCreatedAt time.Time, cursorID string, limit int) ([]ledger.Entry, bool, time.Time, string, error)
	SumLedgerForWallet(ctx context.Context, walletID string) (creditSum, debitSum int64, currency string, checkedEntries int, err error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func NewService(repo Repository, logger *slog.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

func (s *Service) OpenWallet(ctx context.Context, playerID string, initialMoney money.Money) (wallet.Wallet, error) {
	if strings.TrimSpace(playerID) == "" {
		return wallet.Wallet{}, fmt.Errorf("%w: playerID cannot be empty", wallet.ErrInvalidWallet)
	}

	if !initialMoney.IsValid() {
		return wallet.Wallet{}, money.ErrInvalidMoney
	}

	walletUUID, _ := uuid.NewV7()

	w, err := wallet.New(walletUUID.String(), playerID, initialMoney, time.Now())
	if err != nil {
		return wallet.Wallet{}, err
	}

	if initialMoney.IsZero() {
		return w, s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
			return s.repo.InsertWalletTx(ctx, tx, w)
		})
	}

	txID, _ := uuid.NewV7()
	leID, _ := uuid.NewV7()
	opening, _ := transaction.NewOpening(txID.String(), w.ID(), w.PlayerID(), initialMoney, time.Now())
	zero, _ := money.Zero(initialMoney.Currency())
	entry, _ := ledger.NewCredit(leID.String(), w.ID(), txID.String(), initialMoney, zero, time.Now())

	if err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := s.repo.InsertWalletTx(ctx, tx, w); err != nil {
			return err
		}
		if err := s.repo.InsertWagerTransactionTx(ctx, tx, opening); err != nil {
			return err
		}
		return s.repo.InsertLedgerEntryTx(ctx, tx, entry)
	}); err != nil {
		return wallet.Wallet{}, err
	}

	s.logger.Info("wallet opened successfully", "wallet_id", w.ID(), "player_id", playerID)
	return w, nil
}

func (s *Service) GetWallet(ctx context.Context, walletID string) (wallet.Wallet, error) {
	if strings.TrimSpace(walletID) == "" {
		return wallet.Wallet{}, fmt.Errorf("%w: walletID cannot be empty", wallet.ErrInvalidWallet)
	}

	w, found, err := s.repo.GetWalletByID(ctx, walletID)
	if err != nil {
		return wallet.Wallet{}, fmt.Errorf("failed to get wallet by id: %w", err)
	}
	if !found {
		return wallet.Wallet{}, ErrWalletNotFound
	}

	return w, nil
}

func (s *Service) GetTransaction(ctx context.Context, id string) (transaction.WagerTransaction, error) {
	if strings.TrimSpace(id) == "" {
		return transaction.WagerTransaction{}, fmt.Errorf("%w: transaction ID cannot be empty", transaction.ErrInvalidTransaction)
	}

	wt, found, err := s.repo.GetTransactionByID(ctx, id)
	if err != nil {
		return transaction.WagerTransaction{}, fmt.Errorf("failed to get transaction by id: %w", err)
	}
	if !found {
		return transaction.WagerTransaction{}, ErrTransactionNotFound
	}

	return wt, nil
}
func (s *Service) GetTransactionByProvider(ctx context.Context, providerID, externalTxID string) (transaction.WagerTransaction, error) {
	if strings.TrimSpace(providerID) == "" || strings.TrimSpace(externalTxID) == "" {
		return transaction.WagerTransaction{}, fmt.Errorf("%w: providerID and externalTxID cannot be empty", transaction.ErrInvalidTransaction)
	}

	wt, found, err := s.repo.GetTransactionByProviderExternalID(ctx, providerID, externalTxID)
	if err != nil {
		return transaction.WagerTransaction{}, fmt.Errorf("failed to get transaction by provider external ID: %w", err)
	}
	if !found {
		return transaction.WagerTransaction{}, ErrTransactionNotFound
	}
	return wt, nil
}

type LedgerPage struct {
	Entries    []ledger.Entry
	NextCursor string
}

func (s *Service) GetLedger(ctx context.Context, walletID, cursor string, limit int) (LedgerPage, error) {
	if strings.TrimSpace(walletID) == "" {
		return LedgerPage{}, fmt.Errorf("%w: walletID cannot be empty", wallet.ErrInvalidWallet)
	}
	if _, found, err := s.repo.GetWalletByID(ctx, walletID); err != nil {
		return LedgerPage{}, fmt.Errorf("failed to resolve wallet for ledger: %w", err)
	} else if !found {
		return LedgerPage{}, ErrWalletNotFound
	}

	cursorCreatedAt, cursorID, err := DecodeLedgerCursor(cursor)
	if err != nil {
		return LedgerPage{}, err
	}

	entries, hasMore, nextTime, nextID, err := s.repo.ListLedgerEntries(ctx, walletID, cursorCreatedAt, cursorID, limit)
	if err != nil {
		return LedgerPage{}, err
	}

	var nextCursor string
	if hasMore {
		nextCursor = EncodeLedgerCursor(nextTime, nextID)
	}
	return LedgerPage{Entries: entries, NextCursor: nextCursor}, nil
}

type ReconciliationResult struct {
	WalletID           string
	StoredBalance      money.Money
	CalculatedBalance  money.Money
	Difference         money.Money
	Consistent         bool
	CheckedEntries     int
}

func (s *Service) Reconcile(ctx context.Context, walletID string) (ReconciliationResult, error) {
	if strings.TrimSpace(walletID) == "" {
		return ReconciliationResult{}, fmt.Errorf("%w: walletID cannot be empty", wallet.ErrInvalidWallet)
	}

	var result ReconciliationResult
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		w, found, err := s.repo.GetWalletForUpdateTx(ctx, tx, walletID)
		if err != nil {
			return err
		}
		if !found {
			return ErrWalletNotFound
		}

		creditSum, debitSum, currency, checked, err := s.repo.SumLedgerForWallet(ctx, walletID)
		if err != nil {
			return err
		}
		if currency == "" {
			currency = w.Balance().Currency()
		}

		calculated, err := money.New(creditSum-debitSum, currency)
		if err != nil {
			return err
		}
		stored := w.Balance()
		diff, err := stored.Subtract(calculated)
		if err != nil {
			return err
		}

		result = ReconciliationResult{
			WalletID:          w.ID(),
			StoredBalance:     stored,
			CalculatedBalance: calculated,
			Difference:        diff,
			Consistent:        diff.IsZero(),
			CheckedEntries:    checked,
		}
		if !result.Consistent {
			s.logger.Warn("reconciliation divergence",
				"wallet_id", walletID,
				"stored", stored.Amount(),
				"calculated", calculated.Amount(),
				"difference", diff.Amount())
		}
		return nil
	})
	if err != nil {
		return ReconciliationResult{}, err
	}
	return result, nil
}
