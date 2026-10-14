package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/domain/event"
	"github.com/victorhslito/junglegaming/internal/service"
)

type OutboxRepo struct{ pool *pgxpool.Pool }

func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo { return &OutboxRepo{pool: pool} }

var _ service.OutboxRepository = (*OutboxRepo)(nil)

// InsertEventTx stores the envelope. `payload` is the whole envelope JSON —
// we do NOT split "data" from the envelope here because consumers want to
// receive the exact bytes the service produced; splitting would mean
// reassembling at publish time and leaving room for inconsistencies.
func (r *OutboxRepo) InsertEventTx(ctx context.Context, tx pgx.Tx, env event.Envelope) error {
	payload, err := marshalEnvelope(env)
	if err != nil {
		return err
	}
	const query = `
		INSERT INTO outbox (event_id, aggregate_id, event_type, payload, occurred_at, attempts, next_delivery_at)
		VALUES ($1, $2, $3, $4, $5, 0, $5)
	`
	if _, err := tx.Exec(ctx, query, env.EventID, env.AggregateID, string(env.EventType), payload, env.OccurredAt); err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}
	return nil
}

// FetchDuePendingTx: FOR UPDATE SKIP LOCKED is what makes multiple publisher
// instances safe. Each worker opens its own tx and gets a disjoint slice;
// if a worker crashes between SELECT and COMMIT, the row-level locks are
// released and the next worker picks up the row on its next tick.
func (r *OutboxRepo) FetchDuePendingTx(ctx context.Context, tx pgx.Tx, now time.Time, limit int) ([]service.PendingOutboxEvent, error) {
	const query = `
		SELECT event_id, aggregate_id, event_type, payload, occurred_at, attempts, next_delivery_at
		FROM outbox
		WHERE published_at IS NULL
		  AND next_delivery_at <= $1
		ORDER BY next_delivery_at, event_id
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`
	rows, err := tx.Query(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending outbox events: %w", err)
	}
	defer rows.Close()

	out := make([]service.PendingOutboxEvent, 0, limit)
	for rows.Next() {
		var pe service.PendingOutboxEvent
		var typeStr string
		if err := rows.Scan(&pe.EventID, &pe.AggregateID, &typeStr, &pe.Payload, &pe.OccurredAt, &pe.Attempts, &pe.NextDeliveryAt); err != nil {
			return nil, fmt.Errorf("failed to scan outbox row: %w", err)
		}
		pe.EventType = event.Type(typeStr)
		out = append(out, pe)
	}
	return out, rows.Err()
}

func (r *OutboxRepo) MarkPublishedTx(ctx context.Context, tx pgx.Tx, eventID string, now time.Time) error {
	const query = `UPDATE outbox SET published_at = $1 WHERE event_id = $2`
	ct, err := tx.Exec(ctx, query, now, eventID)
	if err != nil {
		return fmt.Errorf("failed to mark outbox published: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("outbox event not found: %s", eventID)
	}
	return nil
}

func (r *OutboxRepo) MarkRetryTx(ctx context.Context, tx pgx.Tx, eventID string, nextDeliveryAt time.Time) error {
	const query = `
		UPDATE outbox
		SET attempts = attempts + 1,
		    next_delivery_at = $2
		WHERE event_id = $1
	`
	ct, err := tx.Exec(ctx, query, eventID, nextDeliveryAt)
	if err != nil {
		return fmt.Errorf("failed to mark outbox retry: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("outbox event not found: %s", eventID)
	}
	return nil
}

// marshalEnvelope serializes the whole envelope — not just the "data" field.
// Keeping it in one place lets the publisher just do `sqs.Publish(payload)`
// without re-marshalling.
func marshalEnvelope(env event.Envelope) ([]byte, error) {
	return json.Marshal(env)
}
