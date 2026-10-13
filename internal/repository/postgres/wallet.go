package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
	"github.com/victorhslito/junglegaming/internal/service"
)

type WalletRepo struct{ pool *pgxpool.Pool }

func NewWalletRepo(pool *pgxpool.Pool) *WalletRepo {
	return &WalletRepo{pool: pool}
}

// Assert that WalletRepo implements WalletRepository
var _ service.Repository = (*WalletRepo)(nil)

type walletRow struct {
	ID        string
	PlayerID  string
	Amount    int64
	Currency  string
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (r *WalletRepo) RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *WalletRepo) InsertWalletTx(ctx context.Context, tx pgx.Tx, w wallet.Wallet) error {
	query := `
		INSERT INTO wallets (id, player_id, balance_amount, currency, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := tx.Exec(
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

func (r *WalletRepo) InsertWagerTransactionTx(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction) error {
	query := `
        INSERT INTO wager_transactions (
            id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
            wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
            reference_external_transaction_id, reference_transaction_id, failure_code,
            observed_balance_amount, observed_wallet_version, attempts, created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
        )
    `

	roundID := sql.NullString{String: wt.RoundID(), Valid: wt.RoundID() != ""}
	gameID := sql.NullString{String: wt.GameID(), Valid: wt.GameID() != ""}
	refExtTxID := sql.NullString{String: wt.ReferenceExternalTransactionID(), Valid: wt.ReferenceExternalTransactionID() != ""}
	refTxID := sql.NullString{String: wt.ReferenceTransactionID(), Valid: wt.ReferenceTransactionID() != ""}
	failureCode := sql.NullString{String: wt.FailureCode(), Valid: wt.FailureCode() != ""}

	var obsBalance sql.NullInt64
	if wt.ObservedBalance().IsValid() {
		obsBalance = sql.NullInt64{Int64: wt.ObservedBalance().Amount(), Valid: true}
	}

	var obsVersion sql.NullInt64
	if wt.ObservedWalletVersion() > 0 {
		obsVersion = sql.NullInt64{Int64: wt.ObservedWalletVersion(), Valid: true}
	}

	_, err := tx.Exec(
		ctx,
		query,
		wt.ID(),
		string(wt.Origin()),
		wt.ProviderID(),
		wt.ExternalTransactionID(),
		wt.IdempotencyKey(),
		wt.PayloadHash(),
		wt.WalletID(),
		wt.PlayerID(),
		roundID,
		gameID,
		string(wt.Kind()),
		string(wt.Status()),
		wt.Money().Amount(),
		wt.Money().Currency(),
		refExtTxID,
		refTxID,
		failureCode,
		obsBalance,
		obsVersion,
		wt.Attempts(),
		wt.CreatedAt(),
		wt.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert wager transaction: %w", err)
	}

	return nil
}

func (r *WalletRepo) InsertLedgerEntryTx(ctx context.Context, tx pgx.Tx, e ledger.Entry) error {
	query := `
        INSERT INTO wallet_ledger_entries (
            id, wallet_id, transaction_id, direction, amount, currency,
            balance_before, balance_after, created_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9
        )
    `

	_, err := tx.Exec(
		ctx,
		query,
		e.ID(),
		e.WalletID(),
		e.TransactionID(),
		string(e.Direction()),
		e.Amount().Amount(),
		e.Amount().Currency(),
		e.BalanceBefore().Amount(),
		e.BalanceAfter().Amount(),
		e.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert ledger entry: %w", err)
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

func (r *WalletRepo) GetWalletForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (wallet.Wallet, bool, error) {
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

func scanWagerTransaction(row pgx.Row) (transaction.WagerTransaction, error) {
	var (
		id, originStr, providerID, externalTxID, idempotencyKey, payloadHash string
		walletID, playerID                                                   string
		roundID, gameID                                                      sql.NullString
		kindStr, statusStr                                                   string
		amount                                                               int64
		currency                                                             string
		refExtTxID, refTxID, failureCode                                     sql.NullString
		obsBalanceAmount, obsWalletVersion                                   sql.NullInt64
		attempts                                                             int32
		createdAt, updatedAt                                                 time.Time
	)

	err := row.Scan(
		&id, &originStr, &providerID, &externalTxID, &idempotencyKey, &payloadHash,
		&walletID, &playerID, &roundID, &gameID, &kindStr, &statusStr, &amount, &currency,
		&refExtTxID, &refTxID, &failureCode, &obsBalanceAmount, &obsWalletVersion,
		&attempts, &createdAt, &updatedAt,
	)
	if err != nil {
		return transaction.WagerTransaction{}, err
	}

	origin, err := transaction.ParseOrigin(originStr)
	if err != nil {
		return transaction.WagerTransaction{}, err
	}
	kind, err := wager.ParseKind(kindStr)
	if err != nil {
		return transaction.WagerTransaction{}, err
	}
	status, err := wager.ParseStatus(statusStr)
	if err != nil {
		return transaction.WagerTransaction{}, err
	}
	m, err := money.New(amount, currency)
	if err != nil {
		return transaction.WagerTransaction{}, err
	}

	var obsBalance money.Money
	if obsBalanceAmount.Valid {
		obsBalance, _ = money.New(obsBalanceAmount.Int64, currency)
	}

	var version int64
	if obsWalletVersion.Valid {
		version = obsWalletVersion.Int64
	}

	return transaction.Rehydrate(
		id, origin, providerID, externalTxID, idempotencyKey, payloadHash,
		walletID, playerID, roundID.String, gameID.String, kind, status, m,
		refExtTxID.String, refTxID.String, failureCode.String,
		obsBalance, version, attempts, createdAt, updatedAt,
	)
}

func (r *WalletRepo) GetTransactionByID(ctx context.Context, id string) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
		       reference_external_transaction_id, reference_transaction_id, failure_code,
		       observed_balance_amount, observed_wallet_version, attempts, created_at, updated_at
		FROM wager_transactions
		WHERE id = $1
	`
	wt, err := scanWagerTransaction(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transaction.WagerTransaction{}, false, nil
		}
		return transaction.WagerTransaction{}, false, fmt.Errorf("failed to get transaction by id: %w", err)
	}
	return wt, true, nil
}

func (r *WalletRepo) GetTransactionByProviderExternalID(ctx context.Context, providerID, externalTxID string) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
		       reference_external_transaction_id, reference_transaction_id, failure_code,
		       observed_balance_amount, observed_wallet_version, attempts, created_at, updated_at
		FROM wager_transactions
		WHERE provider_id = $1 AND external_transaction_id = $2
	`
	wt, err := scanWagerTransaction(r.pool.QueryRow(ctx, query, providerID, externalTxID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transaction.WagerTransaction{}, false, nil
		}
		return transaction.WagerTransaction{}, false, fmt.Errorf("failed to get transaction by provider external id: %w", err)
	}
	return wt, true, nil
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

func (r *WalletRepo) GetTransactionByProviderIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, providerID, idempotencyKey string) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
		       reference_external_transaction_id, reference_transaction_id, failure_code,
		       observed_balance_amount, observed_wallet_version, attempts, created_at, updated_at
		FROM wager_transactions
		WHERE provider_id = $1 AND idempotency_key = $2
	`
	wt, err := scanWagerTransaction(tx.QueryRow(ctx, query, providerID, idempotencyKey))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transaction.WagerTransaction{}, false, nil
		}
		return transaction.WagerTransaction{}, false, fmt.Errorf("failed to get transaction by provider idempotency key: %w", err)
	}
	return wt, true, nil
}

func (r *WalletRepo) GetTransactionByIDTx(ctx context.Context, tx pgx.Tx, id string) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
		       reference_external_transaction_id, reference_transaction_id, failure_code,
		       observed_balance_amount, observed_wallet_version, attempts, created_at, updated_at
		FROM wager_transactions
		WHERE id = $1
	`
	wt, err := scanWagerTransaction(tx.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transaction.WagerTransaction{}, false, nil
		}
		return transaction.WagerTransaction{}, false, fmt.Errorf("failed to get transaction by id tx: %w", err)
	}
	return wt, true, nil
}

func (r *WalletRepo) GetTransactionByProviderExternalIDTx(ctx context.Context, tx pgx.Tx, providerID, externalTxID string) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
		       reference_external_transaction_id, reference_transaction_id, failure_code,
		       observed_balance_amount, observed_wallet_version, attempts, created_at, updated_at
		FROM wager_transactions
		WHERE provider_id = $1 AND external_transaction_id = $2
	`
	wt, err := scanWagerTransaction(tx.QueryRow(ctx, query, providerID, externalTxID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transaction.WagerTransaction{}, false, nil
		}
		return transaction.WagerTransaction{}, false, fmt.Errorf("failed to get transaction by provider external id tx: %w", err)
	}
	return wt, true, nil
}

func (r *WalletRepo) GetProcessedReversalTx(ctx context.Context, tx pgx.Tx, providerID, originalTransactionID string) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
		       reference_external_transaction_id, reference_transaction_id, failure_code,
		       observed_balance_amount, observed_wallet_version, attempts, created_at, updated_at
		FROM wager_transactions
		WHERE reference_transaction_id = $1 AND status = 'PROCESSED'
		LIMIT 1
	`
	wt, err := scanWagerTransaction(tx.QueryRow(ctx, query, originalTransactionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transaction.WagerTransaction{}, false, nil
		}
		return transaction.WagerTransaction{}, false, fmt.Errorf("failed to get processed reversal tx: %w", err)
	}
	return wt, true, nil
}
