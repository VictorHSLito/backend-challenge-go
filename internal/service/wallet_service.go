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

type WalletService struct {
	tx     TxManager
	wallet WalletRepository
	txRepo TransactionRepository
	logger *slog.Logger
}

func NewWalletService(tx TxManager, wallet WalletRepository, txRepo TransactionRepository, logger *slog.Logger) *WalletService {
	return &WalletService{tx: tx, wallet: wallet, txRepo: txRepo, logger: logger}
}

func (s *WalletService) OpenWallet(ctx context.Context, playerID string, initialMoney money.Money) (wallet.Wallet, error) {
	if strings.TrimSpace(playerID) == "" {
		return wallet.Wallet{}, fmt.Errorf("%w: playerID cannot be empty", wallet.ErrInvalidWallet)
	}
	if !initialMoney.IsValid() {
		return wallet.Wallet{}, money.ErrInvalidMoney
	}

	walletUUID, err := uuid.NewV7()
	if err != nil {
		return wallet.Wallet{}, fmt.Errorf("failed to generate wallet id: %w", err)
	}
	now := time.Now()

	w, err := wallet.New(walletUUID.String(), playerID, initialMoney, now)
	if err != nil {
		return wallet.Wallet{}, err
	}

	if initialMoney.IsZero() {
		if err := s.tx.RunInTx(ctx, func(tx pgx.Tx) error {
			return s.wallet.InsertWalletTx(ctx, tx, w)
		}); err != nil {
			return wallet.Wallet{}, err
		}
		s.logger.Info("wallet opened successfully", "wallet_id", w.ID(), "player_id", playerID)
		return w, nil
	}

	txID, err := uuid.NewV7()
	if err != nil {
		return wallet.Wallet{}, fmt.Errorf("failed to generate opening tx id: %w", err)
	}
	leID, err := uuid.NewV7()
	if err != nil {
		return wallet.Wallet{}, fmt.Errorf("failed to generate opening ledger id: %w", err)
	}
	opening, err := transaction.NewOpening(txID.String(), w.ID(), w.PlayerID(), initialMoney, now)
	if err != nil {
		return wallet.Wallet{}, err
	}
	zero, err := money.Zero(initialMoney.Currency())
	if err != nil {
		return wallet.Wallet{}, err
	}
	entry, err := ledger.NewCredit(leID.String(), w.ID(), txID.String(), initialMoney, zero, now)
	if err != nil {
		return wallet.Wallet{}, err
	}

	if err := s.tx.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := s.wallet.InsertWalletTx(ctx, tx, w); err != nil {
			return err
		}
		if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, opening); err != nil {
			return err
		}
		return s.wallet.InsertLedgerEntryTx(ctx, tx, entry)
	}); err != nil {
		return wallet.Wallet{}, err
	}

	s.logger.Info("wallet opened successfully", "wallet_id", w.ID(), "player_id", playerID)
	return w, nil
}

func (s *WalletService) GetWallet(ctx context.Context, walletID string) (wallet.Wallet, error) {
	if strings.TrimSpace(walletID) == "" {
		return wallet.Wallet{}, fmt.Errorf("%w: walletID cannot be empty", wallet.ErrInvalidWallet)
	}
	w, found, err := s.wallet.GetWalletByID(ctx, walletID)
	if err != nil {
		return wallet.Wallet{}, fmt.Errorf("failed to get wallet by id: %w", err)
	}
	if !found {
		return wallet.Wallet{}, ErrWalletNotFound
	}
	return w, nil
}

type LedgerPage struct {
	Entries    []ledger.Entry
	NextCursor string
}

func (s *WalletService) GetLedger(ctx context.Context, walletID, cursor string, limit int) (LedgerPage, error) {
	if strings.TrimSpace(walletID) == "" {
		return LedgerPage{}, fmt.Errorf("%w: walletID cannot be empty", wallet.ErrInvalidWallet)
	}
	if _, found, err := s.wallet.GetWalletByID(ctx, walletID); err != nil {
		return LedgerPage{}, fmt.Errorf("failed to resolve wallet for ledger: %w", err)
	} else if !found {
		return LedgerPage{}, ErrWalletNotFound
	}

	cursorCreatedAt, cursorID, err := DecodeLedgerCursor(cursor)
	if err != nil {
		return LedgerPage{}, err
	}

	entries, hasMore, nextTime, nextID, err := s.wallet.ListLedgerEntries(ctx, walletID, cursorCreatedAt, cursorID, limit)
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
	WalletID          string
	StoredBalance     money.Money
	CalculatedBalance money.Money
	Difference        money.Money
	Consistent        bool
	CheckedEntries    int
}

func (s *WalletService) Reconcile(ctx context.Context, walletID string) (ReconciliationResult, error) {
	if strings.TrimSpace(walletID) == "" {
		return ReconciliationResult{}, fmt.Errorf("%w: walletID cannot be empty", wallet.ErrInvalidWallet)
	}

	var result ReconciliationResult
	err := s.tx.RunInTx(ctx, func(tx pgx.Tx) error {
		w, found, err := s.wallet.GetWalletForUpdateTx(ctx, tx, walletID)
		if err != nil {
			return err
		}
		if !found {
			return ErrWalletNotFound
		}

		creditSum, debitSum, currency, checked, err := s.wallet.SumLedgerForWallet(ctx, walletID)
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
