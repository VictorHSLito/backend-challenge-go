package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/service"
)

type TransactionRepo struct{ pool *pgxpool.Pool }

func NewTransactionRepo(pool *pgxpool.Pool) *TransactionRepo { return &TransactionRepo{pool: pool} }

var _ service.TransactionRepository = (*TransactionRepo)(nil)

const wagerTransactionSelectCols = `
	id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
	wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
	reference_external_transaction_id, reference_transaction_id, failure_code,
	observed_balance_amount, observed_wallet_version, attempts, next_attempt_at,
	created_at, updated_at
`

func (r *TransactionRepo) InsertWagerTransactionTx(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction) error {
	query := `
		INSERT INTO wager_transactions (
			id, origin, provider_id, external_transaction_id, idempotency_key, payload_hash,
			wallet_id, player_id, round_id, game_id, kind, status, amount, currency,
			reference_external_transaction_id, reference_transaction_id, failure_code,
			observed_balance_amount, observed_wallet_version, attempts, next_attempt_at,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			$15, $16, $17, $18, $19, $20, $21, $22, $23
		)
	`

	roundID := nullableString(wt.RoundID())
	gameID := nullableString(wt.GameID())
	refExtTxID := nullableString(wt.ReferenceExternalTransactionID())
	refTxID := nullableString(wt.ReferenceTransactionID())
	failureCode := nullableString(wt.FailureCode())
	obsBalance := nullableInt64(wt.ObservedBalance().Amount(), wt.ObservedBalance().IsValid())
	obsVersion := nullableInt64(wt.ObservedWalletVersion(), wt.ObservedWalletVersion() > 0)
	nextAttemptAt := nullableTime(wt.NextAttemptAt())

	_, err := tx.Exec(
		ctx, query,
		wt.ID(), string(wt.Origin()), wt.ProviderID(), wt.ExternalTransactionID(),
		wt.IdempotencyKey(), wt.PayloadHash(), wt.WalletID(), wt.PlayerID(),
		roundID, gameID, string(wt.Kind()), string(wt.Status()),
		wt.Money().Amount(), wt.Money().Currency(),
		refExtTxID, refTxID, failureCode, obsBalance, obsVersion,
		wt.Attempts(), nextAttemptAt, wt.CreatedAt(), wt.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert wager transaction: %w", err)
	}
	return nil
}

func (r *TransactionRepo) UpdateWagerTransactionTx(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction) error {
	query := `
		UPDATE wager_transactions
		SET status = $1,
		    reference_transaction_id = $2,
		    failure_code = $3,
		    observed_balance_amount = $4,
		    observed_wallet_version = $5,
		    attempts = $6,
		    next_attempt_at = $7,
		    updated_at = $8
		WHERE id = $9
	`
	refTxID := nullableString(wt.ReferenceTransactionID())
	failureCode := nullableString(wt.FailureCode())
	obsBalance := nullableInt64(wt.ObservedBalance().Amount(), wt.ObservedBalance().IsValid())
	obsVersion := nullableInt64(wt.ObservedWalletVersion(), wt.ObservedWalletVersion() > 0)
	nextAttemptAt := nullableTime(wt.NextAttemptAt())

	cmdTag, err := tx.Exec(ctx, query,
		string(wt.Status()), refTxID, failureCode, obsBalance, obsVersion,
		wt.Attempts(), nextAttemptAt, wt.UpdatedAt(), wt.ID(),
	)
	if err != nil {
		return fmt.Errorf("failed to update wager transaction: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("wager transaction not found for update: %s", wt.ID())
	}
	return nil
}

func (r *TransactionRepo) GetTransactionByID(ctx context.Context, id string) (transaction.WagerTransaction, bool, error) {
	return r.selectOne(ctx, r.pool.QueryRow(ctx, `SELECT `+wagerTransactionSelectCols+` FROM wager_transactions WHERE id = $1`, id))
}

func (r *TransactionRepo) GetTransactionByIDTx(ctx context.Context, tx pgx.Tx, id string) (transaction.WagerTransaction, bool, error) {
	return r.selectOne(ctx, tx.QueryRow(ctx, `SELECT `+wagerTransactionSelectCols+` FROM wager_transactions WHERE id = $1`, id))
}

func (r *TransactionRepo) GetTransactionByProviderExternalID(ctx context.Context, providerID, externalTxID string) (transaction.WagerTransaction, bool, error) {
	return r.selectOne(ctx, r.pool.QueryRow(ctx,
		`SELECT `+wagerTransactionSelectCols+` FROM wager_transactions WHERE provider_id = $1 AND external_transaction_id = $2`,
		providerID, externalTxID))
}

func (r *TransactionRepo) GetTransactionByProviderExternalIDTx(ctx context.Context, tx pgx.Tx, providerID, externalTxID string) (transaction.WagerTransaction, bool, error) {
	return r.selectOne(ctx, tx.QueryRow(ctx,
		`SELECT `+wagerTransactionSelectCols+` FROM wager_transactions WHERE provider_id = $1 AND external_transaction_id = $2`,
		providerID, externalTxID))
}

func (r *TransactionRepo) GetTransactionByProviderIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, providerID, idempotencyKey string) (transaction.WagerTransaction, bool, error) {
	return r.selectOne(ctx, tx.QueryRow(ctx,
		`SELECT `+wagerTransactionSelectCols+` FROM wager_transactions WHERE provider_id = $1 AND idempotency_key = $2`,
		providerID, idempotencyKey))
}

func (r *TransactionRepo) GetProcessedReversalTx(ctx context.Context, tx pgx.Tx, providerID, originalTransactionID string) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT ` + wagerTransactionSelectCols + `
		FROM wager_transactions
		WHERE reference_transaction_id = $1
		  AND provider_id = $2
		  AND status = 'PROCESSED'
		  AND kind IN ('REFUND', 'ROLLBACK')
		LIMIT 1
	`
	return r.selectOne(ctx, tx.QueryRow(ctx, query, originalTransactionID, providerID))
}

func (r *TransactionRepo) FetchOneDuePendingReferenceTx(ctx context.Context, tx pgx.Tx, now time.Time) (transaction.WagerTransaction, bool, error) {
	query := `
		SELECT ` + wagerTransactionSelectCols + `
		FROM wager_transactions
		WHERE status = 'PENDING_REFERENCE'
		  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		ORDER BY next_attempt_at NULLS FIRST, created_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`
	return r.selectOne(ctx, tx.QueryRow(ctx, query, now))
}

func (r *TransactionRepo) selectOne(_ context.Context, row pgx.Row) (transaction.WagerTransaction, bool, error) {
	wt, err := scanWagerTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transaction.WagerTransaction{}, false, nil
		}
		return transaction.WagerTransaction{}, false, fmt.Errorf("failed to fetch wager transaction: %w", err)
	}
	return wt, true, nil
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
		nextAttemptAt                                                        sql.NullTime
		createdAt, updatedAt                                                 time.Time
	)

	err := row.Scan(
		&id, &originStr, &providerID, &externalTxID, &idempotencyKey, &payloadHash,
		&walletID, &playerID, &roundID, &gameID, &kindStr, &statusStr, &amount, &currency,
		&refExtTxID, &refTxID, &failureCode, &obsBalanceAmount, &obsWalletVersion,
		&attempts, &nextAttemptAt, &createdAt, &updatedAt,
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
	var nextAttempt time.Time
	if nextAttemptAt.Valid {
		nextAttempt = nextAttemptAt.Time
	}

	return transaction.Rehydrate(
		id, origin, providerID, externalTxID, idempotencyKey, payloadHash,
		walletID, playerID, roundID.String, gameID.String, kind, status, m,
		refExtTxID.String, refTxID.String, failureCode.String,
		obsBalance, version, attempts, nextAttempt, createdAt, updatedAt,
	)
}

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
func nullableInt64(v int64, valid bool) sql.NullInt64 {
	if !valid {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}
func nullableTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
