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
	InsertLedgerEntryTx(ctx context.Context, tx pgx.Tx, e ledger.Entry) error
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
