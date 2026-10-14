package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
	"github.com/victorhslito/junglegaming/internal/service"
)

type WalletRepo struct{ pool *pgxpool.Pool }

func NewWalletRepo(pool *pgxpool.Pool) *WalletRepo { return &WalletRepo{pool: pool} }

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

func (r *WalletRepo) InsertWalletTx(ctx context.Context, tx pgx.Tx, w wallet.Wallet) error {
	query := `
		INSERT INTO wallets (id, player_id, balance_amount, currency, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := tx.Exec(
		ctx, query,
		w.ID(), w.PlayerID(), w.Balance().Amount(), w.Balance().Currency(),
		w.Version(), w.CreatedAt(), w.UpdatedAt(),
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

func (r *WalletRepo) UpdateWalletTx(ctx context.Context, tx pgx.Tx, w wallet.Wallet) error {
	query := `
		UPDATE wallets
		SET balance_amount = $1, version = $2, updated_at = $3
		WHERE id = $4
	`
	cmdTag, err := tx.Exec(ctx, query, w.Balance().Amount(), w.Version(), w.UpdatedAt(), w.ID())
	if err != nil {
		return fmt.Errorf("failed to update wallet: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found for update: %s", w.ID())
	}
	return nil
}

func (r *WalletRepo) GetWalletByID(ctx context.Context, id string) (wallet.Wallet, bool, error) {
	query := `
		SELECT id, player_id, balance_amount, currency, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`
	return scanWallet(r.pool.QueryRow(ctx, query, id))
}

func (r *WalletRepo) GetWalletForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (wallet.Wallet, bool, error) {
	query := `
		SELECT id, player_id, balance_amount, currency, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
		FOR UPDATE
	`
	return scanWallet(tx.QueryRow(ctx, query, id))
}

func scanWallet(row pgx.Row) (wallet.Wallet, bool, error) {
	var r walletRow
	err := row.Scan(&r.ID, &r.PlayerID, &r.Amount, &r.Currency, &r.Version, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return wallet.Wallet{}, false, nil
		}
		return wallet.Wallet{}, false, fmt.Errorf("failed to get wallet: %w", err)
	}
	m, err := money.New(r.Amount, r.Currency)
	if err != nil {
		return wallet.Wallet{}, false, fmt.Errorf("failed to instantiate money from row: %w", err)
	}
	w, err := wallet.Rehydrate(r.ID, r.PlayerID, m, r.Version, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return wallet.Wallet{}, false, fmt.Errorf("failed to rehydrate wallet: %w", err)
	}
	return w, true, nil
}

func (r *WalletRepo) InsertLedgerEntryTx(ctx context.Context, tx pgx.Tx, e ledger.Entry) error {
	query := `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount, currency,
			balance_before, balance_after, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := tx.Exec(
		ctx, query,
		e.ID(), e.WalletID(), e.TransactionID(), string(e.Direction()),
		e.Amount().Amount(), e.Amount().Currency(),
		e.BalanceBefore().Amount(), e.BalanceAfter().Amount(), e.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert ledger entry: %w", err)
	}
	return nil
}

func (r *WalletRepo) ListLedgerEntries(ctx context.Context, walletID string, cursorCreatedAt time.Time, cursorID string, limit int) ([]ledger.Entry, bool, time.Time, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var (
		rows pgx.Rows
		err  error
	)
	if cursorID == "" {
		rows, err = r.pool.Query(ctx, `
			SELECT id, wallet_id, transaction_id, direction, amount, currency, balance_before, balance_after, created_at
			FROM wallet_ledger_entries
			WHERE wallet_id = $1
			ORDER BY created_at, id
			LIMIT $2
		`, walletID, limit+1)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT id, wallet_id, transaction_id, direction, amount, currency, balance_before, balance_after, created_at
			FROM wallet_ledger_entries
			WHERE wallet_id = $1
			  AND (created_at, id) > ($2, $3)
			ORDER BY created_at, id
			LIMIT $4
		`, walletID, cursorCreatedAt, cursorID, limit+1)
	}
	if err != nil {
		return nil, false, time.Time{}, "", fmt.Errorf("failed to list ledger entries: %w", err)
	}
	defer rows.Close()

	entries := make([]ledger.Entry, 0, limit)
	for rows.Next() {
		var (
			id, walletIDOut, transactionID, dirStr, currency string
			amount, balanceBefore, balanceAfter              int64
			createdAt                                        time.Time
		)
		if err := rows.Scan(&id, &walletIDOut, &transactionID, &dirStr, &amount, &currency, &balanceBefore, &balanceAfter, &createdAt); err != nil {
			return nil, false, time.Time{}, "", fmt.Errorf("failed to scan ledger row: %w", err)
		}
		dir, err := wager.ParseDirection(dirStr)
		if err != nil {
			return nil, false, time.Time{}, "", err
		}
		amtM, err := money.New(amount, currency)
		if err != nil {
			return nil, false, time.Time{}, "", err
		}
		bbM, err := money.New(balanceBefore, currency)
		if err != nil {
			return nil, false, time.Time{}, "", err
		}
		baM, err := money.New(balanceAfter, currency)
		if err != nil {
			return nil, false, time.Time{}, "", err
		}
		e, err := ledger.Rehydrate(id, walletIDOut, transactionID, dir, amtM, bbM, baM, createdAt)
		if err != nil {
			return nil, false, time.Time{}, "", err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, time.Time{}, "", err
	}

	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	var nextTime time.Time
	var nextID string
	if hasMore {
		last := entries[len(entries)-1]
		nextTime = last.CreatedAt()
		nextID = last.ID()
	}
	return entries, hasMore, nextTime, nextID, nil
}

func (r *WalletRepo) SumLedgerForWallet(ctx context.Context, walletID string) (int64, int64, string, int, error) {
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'DEBIT'  THEN amount ELSE 0 END), 0),
			COALESCE(MAX(currency), ''),
			COUNT(*)
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
	`
	var (
		creditSum, debitSum int64
		currency            string
		checked             int
	)
	if err := r.pool.QueryRow(ctx, query, walletID).Scan(&creditSum, &debitSum, &currency, &checked); err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to sum ledger: %w", err)
	}
	return creditSum, debitSum, currency, checked, nil
}
