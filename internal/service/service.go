package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
)

type WalletRepository interface {
	InsertWallet(ctx context.Context, w wallet.Wallet) error
	GetWalletByID(ctx context.Context, id string) (wallet.Wallet, bool, error)
	GetWalletForUpdate(ctx context.Context, tx pgx.Tx, id string) (wallet.Wallet, bool, error)
}

type Service struct {
	repo   WalletRepository
	logger *slog.Logger
}

func NewService(repo WalletRepository, logger *slog.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

func (s *Service) OpenWallet(ctx context.Context, playerID string, initialMoney money.Money) (wallet.Wallet, error) {
	if strings.TrimSpace(playerID) == "" {
		return wallet.Wallet{}, fmt.Errorf("%w: playerID cannot be empty", wallet.ErrInvalidWallet)
	}

	if !initialMoney.IsValid() {
		return wallet.Wallet{}, money.ErrInvalidMoney
	}

	walletUUID, err := uuid.NewV7()
	if err != nil {
		return wallet.Wallet{}, fmt.Errorf("failed to generate uuidv7: %w", err)
	}

	w, err := wallet.New(walletUUID.String(), playerID, initialMoney, time.Now())
	if err != nil {
		return wallet.Wallet{}, err
	}

	err = s.repo.InsertWallet(ctx, w)
	if err != nil {
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
