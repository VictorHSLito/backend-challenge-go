package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
	"github.com/victorhslito/junglegaming/internal/service"
)

type WalletRepo struct{ pool *pgxpool.Pool }

func NewWalletRepo(pool *pgxpool.Pool) *WalletRepo {
	return &WalletRepo{pool: pool}
}

// Assert that WalletRepo implements WalletRepository
var _ service.WalletRepository = (*WalletRepo)(nil)

type walletRow struct {
	ID        string
	PlayerID  string
	Amount    int64
	Currency  string
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (r *WalletRepo) InsertWallet(ctx context.Context, w wallet.Wallet) error {
	query := `
		INSERT INTO wallets (id, player_id, balance_amount, currency, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.pool.Exec(
		ctx,
		query,
		w.ID(),
		w.PlayerID(),
		w.Balance().Amount(),
		w.Balance().Currency(),
		w.Version(),
		w.CreatedAt(),
		w.UpdatedAt(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return service.ErrWalletAlreadyExists
		}
		return fmt.Errorf("failed to insert wallet: %w", err)
	}
	return nil
}

func (r *WalletRepo) GetWalletByID(ctx context.Context, id string) (wallet.Wallet, bool, error) {
	query := `
		SELECT id, player_id, balance_amount, currency, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`
	var row walletRow
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&row.ID,
		&row.PlayerID,
		&row.Amount,
		&row.Currency,
		&row.Version,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return wallet.Wallet{}, false, nil
		}
		return wallet.Wallet{}, false, fmt.Errorf("failed to get wallet by id: %w", err)
	}

	m, err := money.New(row.Amount, row.Currency)
	if err != nil {
		return wallet.Wallet{}, false, fmt.Errorf("failed to instantiate money from row: %w", err)
	}

	w, err := wallet.Rehydrate(row.ID, row.PlayerID, m, row.Version, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return wallet.Wallet{}, false, fmt.Errorf("failed to rehydrate wallet: %w", err)
	}

	return w, true, nil
}

func (r *WalletRepo) GetWalletForUpdate(ctx context.Context, tx pgx.Tx, id string) (wallet.Wallet, bool, error) {
	query := `
		SELECT id, player_id, balance_amount, currency, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
		FOR UPDATE
	`
	var row walletRow
	err := tx.QueryRow(ctx, query, id).Scan(
		&row.ID,
		&row.PlayerID,
		&row.Amount,
		&row.Currency,
		&row.Version,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return wallet.Wallet{}, false, nil
		}
		return wallet.Wallet{}, false, fmt.Errorf("failed to get wallet for update: %w", err)
	}

	m, err := money.New(row.Amount, row.Currency)
	if err != nil {
		return wallet.Wallet{}, false, fmt.Errorf("failed to instantiate money from row: %w", err)
	}

	w, err := wallet.Rehydrate(row.ID, row.PlayerID, m, row.Version, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return wallet.Wallet{}, false, fmt.Errorf("failed to rehydrate wallet for update: %w", err)
	}

	return w, true, nil
}
