// Package outboxpublisher reads unpublished events from the outbox table
// and ships them to SQS. Multiple instances may run in parallel; they
// coordinate via `FOR UPDATE SKIP LOCKED` on the Postgres side.
package outboxpublisher

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/config"
	"github.com/victorhslito/junglegaming/internal/domain/event"
	"github.com/victorhslito/junglegaming/internal/integration/sqs"
	"github.com/victorhslito/junglegaming/internal/service"
)

type Config struct {
	PollInterval time.Duration
	BatchSize    int
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
}

func DefaultConfig() Config {
	return Config{
		PollInterval: 2 * time.Second,
		BatchSize:    10,
		BaseBackoff:  5 * time.Second,
		MaxBackoff:   5 * time.Minute,
	}
}

type Publisher struct {
	tx        service.TxManager
	outbox    service.OutboxRepository
	sqs       *sqs.Client
	queueURL  string
	cfg       Config
	logger    *slog.Logger
}

func New(
	tx service.TxManager,
	outbox service.OutboxRepository,
	client *sqs.Client,
	appCfg config.Config,
	logger *slog.Logger,
) (*Publisher, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url, err := client.ResolveQueueURL(ctx, appCfg.SQSEventsQueue)
	if err != nil {
		return nil, err
	}
	return &Publisher{
		tx:       tx,
		outbox:   outbox,
		sqs:      client,
		queueURL: url,
		cfg:      DefaultConfig(),
		logger:   logger,
	}, nil
}

func (p *Publisher) Run(ctx context.Context) {
	p.logger.Info("outbox publisher starting", "queue", p.queueURL, "poll_interval", p.cfg.PollInterval, "batch", p.cfg.BatchSize)
	for {
		processed, err := p.runOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			p.logger.Error("outbox publisher iteration failed", "error", err)
		}
		wait := p.cfg.PollInterval
		if processed > 0 {
			wait = 0 // drain fast when there's work
		}
		select {
		case <-ctx.Done():
			p.logger.Info("outbox publisher stopping")
			return
		case <-time.After(wait):
		}
	}
}

// runOnce opens a single tx and publishes up to BatchSize events. Returns
// the number of events it attempted (not necessarily succeeded).
func (p *Publisher) runOnce(ctx context.Context) (int, error) {
	var processed int
	err := p.tx.RunInTx(ctx, func(tx pgx.Tx) error {
		now := time.Now()
		rows, err := p.outbox.FetchDuePendingTx(ctx, tx, now, p.cfg.BatchSize)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := p.publishOne(ctx, tx, row, now); err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	return processed, err
}

func (p *Publisher) publishOne(ctx context.Context, tx pgx.Tx, row service.PendingOutboxEvent, now time.Time) error {
	groupID := groupIDFor(row)
	err := p.sqs.Publish(ctx, sqs.PublishInput{
		QueueURL:               p.queueURL,
		Body:                   string(row.Payload),
		MessageGroupID:         groupID,
		MessageDeduplicationID: row.EventID, // stable across republishes
	})
	if err != nil {
		// Transient-looking failure — reschedule with exponential backoff.
		// We don't distinguish "retryable" vs "permanent" here because
		// SQS errors tend to all be worth retrying; a truly poison event
		// will keep failing until the max backoff and sit there for a human.
		delay := backoff(p.cfg.BaseBackoff, p.cfg.MaxBackoff, row.Attempts)
		p.logger.Warn("outbox publish failed, rescheduling",
			"event_id", row.EventID, "event_type", row.EventType, "attempts", row.Attempts+1, "next_delivery_at", now.Add(delay), "error", err)
		return p.outbox.MarkRetryTx(ctx, tx, row.EventID, now.Add(delay))
	}
	return p.outbox.MarkPublishedTx(ctx, tx, row.EventID, now)
}

// groupIDFor picks the FIFO MessageGroupId:
//   - WalletBalanceChanged → walletId (same wallet always ordered)
//   - everything else      → aggregateId of the event (transactionId)
// Different groups are published independently, so wallets never block
// each other.
func groupIDFor(row service.PendingOutboxEvent) string {
	if row.EventType == event.TypeWalletBalanceChanged {
		var payload struct {
			Data struct {
				WalletID string `json:"walletId"`
			} `json:"data"`
		}
		if err := json.Unmarshal(row.Payload, &payload); err == nil && payload.Data.WalletID != "" {
			return payload.Data.WalletID
		}
	}
	return row.AggregateID
}

func backoff(base, max time.Duration, attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	shift := attempts
	if shift > 20 {
		shift = 20
	}
	d := base << shift
	if d <= 0 || d > max {
		return max
	}
	return d
}
