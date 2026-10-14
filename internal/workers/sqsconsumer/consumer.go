// Package sqsconsumer owns the long-running goroutine that reads
// `wager-transactions.fifo` and feeds each message to the
// TransactionService. It is intentionally the only place that knows
// about the inbox-table dedup + SQS visibility interactions.
package sqsconsumer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/victorhslito/junglegaming/internal/config"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/integration/sqs"
	"github.com/victorhslito/junglegaming/internal/service"
)

// ConsumerName is the stable identifier stored in `inbox.consumer_name`.
// Keep it stable: changing it means every past message counts as unseen.
const ConsumerName = "wager-transactions-consumer"

type Config struct {
	MaxMessages       int32
	WaitSeconds       int32
	VisibilityTimeout int32
	IdleBackoff       time.Duration
}

func DefaultConfig() Config {
	return Config{
		MaxMessages:       10,
		WaitSeconds:       20, // SQS long poll cap
		VisibilityTimeout: 30,
		IdleBackoff:       time.Second,
	}
}

type Consumer struct {
	sqs      *sqs.Client
	svc      *service.TransactionService
	queueURL string
	cfg      Config
	logger   *slog.Logger
}

func New(
	client *sqs.Client,
	svc *service.TransactionService,
	appCfg config.Config,
	logger *slog.Logger,
) (*Consumer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url, err := client.ResolveQueueURL(ctx, appCfg.SQSInputQueue)
	if err != nil {
		return nil, err
	}
	return &Consumer{sqs: client, svc: svc, queueURL: url, cfg: DefaultConfig(), logger: logger}, nil
}

func (c *Consumer) Run(ctx context.Context) {
	c.logger.Info("sqs consumer starting", "queue", c.queueURL, "max_messages", c.cfg.MaxMessages, "wait_seconds", c.cfg.WaitSeconds)
	for {
		if ctx.Err() != nil {
			c.logger.Info("sqs consumer stopping")
			return
		}
		msgs, err := c.sqs.Receive(ctx, c.queueURL, c.cfg.MaxMessages, c.cfg.WaitSeconds, c.cfg.VisibilityTimeout)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.logger.Info("sqs consumer stopping")
				return
			}
			c.logger.Error("sqs receive failed", "error", err)
			sleep(ctx, c.cfg.IdleBackoff)
			continue
		}
		for _, msg := range msgs {
			c.handleOne(ctx, msg)
		}
	}
}

// handleOne processes exactly one message, keeping error handling isolated
// so a poison message can't take down the loop.
func (c *Consumer) handleOne(ctx context.Context, msg sqs.ReceivedMessage) {
	envelope, cmd, idemKey, parseErr := parseMessage(msg.Body)
	if parseErr != nil {
		// Malformed messages are terminal — SQS redelivery can't fix them.
		// Delete so they land in no-man's land; a human will find them in
		// the DLQ once the queue's maxReceiveCount gives up (but we don't
		// want to re-receive the same bad bytes 10 times to get there).
		c.logger.Error("rejecting unparseable SQS message", "message_id", msg.MessageID, "error", parseErr)
		c.delete(ctx, msg)
		return
	}

	msgHash := payloadHash(msg.Body)
	_, err := c.svc.ProcessFromMessage(ctx, ConsumerName, envelope.MessageID, msgHash, idemKey, cmd)
	switch {
	case err == nil:
		c.delete(ctx, msg)
	case errors.Is(err, service.ErrUnsupportedKind),
		errors.Is(err, service.ErrIdempotencyConflict),
		errors.Is(err, money.ErrInvalidMoney),
		errors.Is(err, money.ErrCurrencyMismatch):
		// Business-terminal: the broker retrying won't change the outcome.
		c.logger.Warn("message rejected (terminal), deleting", "message_id", envelope.MessageID, "error", err)
		c.delete(ctx, msg)
	case errors.Is(err, service.ErrWalletNotFound):
		// Could be transient (wallet about to be created) or definitive.
		// Letting SQS redeliver after visibility timeout is the simplest
		// choice; the maxReceiveCount redrive policy will DLQ it eventually.
		c.logger.Warn("wallet not found, allowing redelivery", "message_id", envelope.MessageID)
	default:
		c.logger.Error("transient processing failure, allowing redelivery", "message_id", envelope.MessageID, "error", err)
	}
}

func (c *Consumer) delete(ctx context.Context, msg sqs.ReceivedMessage) {
	if err := c.sqs.Delete(ctx, c.queueURL, msg.ReceiptHandle); err != nil {
		c.logger.Error("failed to delete SQS message after commit", "message_id", msg.MessageID, "error", err)
	}
}

// --- parsing ---

type messageEnvelope struct {
	MessageID  string          `json:"messageId"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type messageData struct {
	ProviderID                     string      `json:"providerId"`
	ExternalTransactionID          string      `json:"externalTransactionId"`
	IdempotencyKey                 string      `json:"idempotencyKey"`
	PlayerID                       string      `json:"playerId"`
	WalletID                       string      `json:"walletId"`
	RoundID                        string      `json:"roundId"`
	GameID                         string      `json:"gameId"`
	Kind                           string      `json:"kind"`
	Money                          money.Money `json:"money"`
	ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId"`
}

func parseMessage(body string) (messageEnvelope, service.ProcessTransactionCommand, string, error) {
	var env messageEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return messageEnvelope{}, service.ProcessTransactionCommand{}, "", fmt.Errorf("decode envelope: %w", err)
	}
	if strings.TrimSpace(env.MessageID) == "" {
		return messageEnvelope{}, service.ProcessTransactionCommand{}, "", fmt.Errorf("missing messageId")
	}
	if env.Type != "" && env.Type != "WagerTransactionRequested" {
		return messageEnvelope{}, service.ProcessTransactionCommand{}, "", fmt.Errorf("unexpected type %q", env.Type)
	}
	var d messageData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return messageEnvelope{}, service.ProcessTransactionCommand{}, "", fmt.Errorf("decode data: %w", err)
	}
	if strings.TrimSpace(d.IdempotencyKey) == "" {
		return messageEnvelope{}, service.ProcessTransactionCommand{}, "", fmt.Errorf("missing idempotencyKey")
	}
	kind, err := wager.ParseKind(d.Kind)
	if err != nil {
		return messageEnvelope{}, service.ProcessTransactionCommand{}, "", fmt.Errorf("invalid kind: %w", err)
	}
	cmd := service.ProcessTransactionCommand{
		ProviderID:                     d.ProviderID,
		ExternalTransactionID:          d.ExternalTransactionID,
		PlayerID:                       d.PlayerID,
		WalletID:                       d.WalletID,
		RoundID:                        d.RoundID,
		GameID:                         d.GameID,
		Kind:                           kind,
		Money:                          d.Money,
		ReferenceExternalTransactionID: d.ReferenceExternalTransactionID,
	}
	return env, cmd, d.IdempotencyKey, nil
}

// payloadHash hashes the raw bytes of the SQS body for inbox hash storage.
// It matches "same bytes → same hash", so a replay of the exact SQS message
// is detected as a no-op; a different body under the same messageId trips
// ErrIdempotencyConflict.
func payloadHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
