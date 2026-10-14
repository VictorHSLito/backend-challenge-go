package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/service"
)

type InboxRepo struct{ pool *pgxpool.Pool }

func NewInboxRepo(pool *pgxpool.Pool) *InboxRepo { return &InboxRepo{pool: pool} }

var _ service.InboxRepository = (*InboxRepo)(nil)

// MarkReceivedTx uses INSERT … ON CONFLICT DO NOTHING to atomically claim
// the (consumer_name, message_id) slot. We detect "already seen" by trying
// a SELECT afterwards and returning the stored hash so the caller can
// compare it with the hash of the message it just received.
//
// NOTE: `transaction_id` is populated by MarkCompletedTx *after* the actual
// transaction row has been inserted, so it stays NULL between received
// and completed. This is intentional — the FK is just a convenience join
// for debugging; dedup correctness is entirely on the primary key.
func (r *InboxRepo) MarkReceivedTx(ctx context.Context, tx pgx.Tx, consumerName, messageID, messageHash string, now time.Time) (bool, service.InboxMessage, error) {
	const insert = `
		INSERT INTO inbox (consumer_name, message_id, message_hash, received_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (consumer_name, message_id) DO NOTHING
	`
	ct, err := tx.Exec(ctx, insert, consumerName, messageID, messageHash, now)
	if err != nil {
		return false, service.InboxMessage{}, fmt.Errorf("failed to insert inbox row: %w", err)
	}
	if ct.RowsAffected() == 1 {
		return true, service.InboxMessage{}, nil
	}
	// Already present → load the prior row for the caller's hash check.
	prior, err := r.loadInboxRowTx(ctx, tx, consumerName, messageID)
	if err != nil {
		return false, service.InboxMessage{}, err
	}
	return false, prior, nil
}

func (r *InboxRepo) loadInboxRowTx(ctx context.Context, tx pgx.Tx, consumerName, messageID string) (service.InboxMessage, error) {
	const query = `
		SELECT consumer_name, message_id, message_hash, transaction_id, received_at, completed_at
		FROM inbox
		WHERE consumer_name = $1 AND message_id = $2
	`
	var (
		msg      service.InboxMessage
		txID     sql.NullString
		complAt  sql.NullTime
	)
	err := tx.QueryRow(ctx, query, consumerName, messageID).
		Scan(&msg.ConsumerName, &msg.MessageID, &msg.MessageHash, &txID, &msg.ReceivedAt, &complAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.InboxMessage{}, fmt.Errorf("inbox row vanished between insert conflict and load: %s/%s", consumerName, messageID)
		}
		return service.InboxMessage{}, fmt.Errorf("failed to load inbox row: %w", err)
	}
	if txID.Valid {
		msg.TransactionID = txID.String
	}
	if complAt.Valid {
		t := complAt.Time
		msg.CompletedAt = &t
	}
	return msg, nil
}

func (r *InboxRepo) MarkCompletedTx(ctx context.Context, tx pgx.Tx, consumerName, messageID, transactionID string, now time.Time) error {
	const query = `
		UPDATE inbox
		SET transaction_id = $3, completed_at = $4
		WHERE consumer_name = $1 AND message_id = $2
	`
	ct, err := tx.Exec(ctx, query, consumerName, messageID, nullableString(transactionID), now)
	if err != nil {
		return fmt.Errorf("failed to mark inbox completed: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("inbox row not found for completion: %s/%s", consumerName, messageID)
	}
	return nil
}
