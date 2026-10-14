package resolver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/domain/event"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
	"github.com/victorhslito/junglegaming/internal/service"
)

type Config struct {
	PollInterval time.Duration
	BaseBackoff  time.Duration
	MaxAttempts  int32
	MaxBackoff   time.Duration
}

func DefaultConfig() Config {
	return Config{
		PollInterval: 5 * time.Second,
		BaseBackoff:  10 * time.Second,
		MaxAttempts:  10,
		MaxBackoff:   1 * time.Hour,
	}
}

type Resolver struct {
	tx     service.TxManager
	wallet service.WalletRepository
	txRepo service.TransactionRepository
	outbox service.OutboxRepository
	logger *slog.Logger
	cfg    Config
}

func New(
	tx service.TxManager,
	wallet service.WalletRepository,
	txRepo service.TransactionRepository,
	outbox service.OutboxRepository,
	logger *slog.Logger,
) *Resolver {
	return &Resolver{tx: tx, wallet: wallet, txRepo: txRepo, outbox: outbox, logger: logger, cfg: DefaultConfig()}
}

func (r *Resolver) Run(ctx context.Context) {
	r.logger.Info("resolver worker starting", "poll_interval", r.cfg.PollInterval, "max_attempts", r.cfg.MaxAttempts)
	for {
		processed, err := r.processOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Error("resolver iteration failed", "error", err)
		}
		wait := time.Duration(0)
		if !processed {
			wait = r.cfg.PollInterval
		}
		select {
		case <-ctx.Done():
			r.logger.Info("resolver worker stopping")
			return
		case <-time.After(wait):
		}
	}
}

func (r *Resolver) processOne(ctx context.Context) (bool, error) {
	var processed bool
	err := r.tx.RunInTx(ctx, func(tx pgx.Tx) error {
		pending, found, err := r.txRepo.FetchOneDuePendingReferenceTx(ctx, tx, time.Now())
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		processed = true
		return r.resolve(ctx, tx, pending)
	})
	return processed, err
}

func (r *Resolver) resolve(ctx context.Context, tx pgx.Tx, pending transaction.WagerTransaction) error {
	now := time.Now()

	ref, found, err := r.txRepo.GetTransactionByProviderExternalIDTx(ctx, tx, pending.ProviderID(), pending.ReferenceExternalTransactionID())
	if err != nil {
		return err
	}

	if !found || ref.Status() == wager.StatusPending || ref.Status() == wager.StatusPendingReference {
		return r.rescheduleOrGiveUp(ctx, tx, &pending, now)
	}

	if ref.Status() == wager.StatusRejected || ref.Status() == wager.StatusFailed {
		return r.finalizeRejected(ctx, tx, &pending, service.FailureReferenceNotProcessed, ref.ID(), now)
	}

	if ref.ProviderID() != pending.ProviderID() ||
		ref.PlayerID() != pending.PlayerID() ||
		ref.WalletID() != pending.WalletID() ||
		ref.Money().Currency() != pending.Money().Currency() ||
		ref.RoundID() != pending.RoundID() {
		return r.finalizeRejected(ctx, tx, &pending, service.FailureReferenceMismatch, ref.ID(), now)
	}

	cmp, err := ref.Money().Compare(pending.Money())
	if err != nil {
		return err
	}
	if cmp != 0 {
		return r.finalizeRejected(ctx, tx, &pending, service.FailureReferenceAmountMismatch, ref.ID(), now)
	}

	if !wager.IsReversible(pending.Kind(), ref.Kind()) {
		return r.finalizeRejected(ctx, tx, &pending, service.FailureReferenceUnreversable, ref.ID(), now)
	}

	_, foundRev, err := r.txRepo.GetProcessedReversalTx(ctx, tx, pending.ProviderID(), ref.ID())
	if err != nil {
		return err
	}
	if foundRev {
		return r.finalizeRejected(ctx, tx, &pending, service.FailureReferenceAlreadyReversed, ref.ID(), now)
	}

	w, wFound, err := r.wallet.GetWalletForUpdateTx(ctx, tx, pending.WalletID())
	if err != nil {
		return err
	}
	if !wFound {
		return r.finalizeRejected(ctx, tx, &pending, service.FailureReferenceMismatch, ref.ID(), now)
	}

	balanceBefore := w.Balance()
	direction, err := wager.ReversalDirection(pending.Kind(), ref.Kind())
	if err != nil {
		return err
	}

	if direction == wager.DirectionCredit {
		if err := w.Credit(pending.Money(), now); err != nil {
			return err
		}
	} else {
		if err := w.Debit(pending.Money(), now); err != nil {
			if errors.Is(err, wallet.ErrInsufficientBalance) {
				return r.finalizeRejected(ctx, tx, &pending, service.FailureRollbackInsufficientFunds, ref.ID(), now)
			}
			return err
		}
	}

	if err := pending.BindReference(ref.ID(), now); err != nil {
		return err
	}
	if err := pending.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
		return err
	}

	leID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var entry ledger.Entry
	if direction == wager.DirectionCredit {
		entry, err = ledger.NewCredit(leID.String(), w.ID(), pending.ID(), pending.Money(), balanceBefore, now)
	} else {
		entry, err = ledger.NewDebit(leID.String(), w.ID(), pending.ID(), pending.Money(), balanceBefore, now)
	}
	if err != nil {
		return err
	}

	if err := r.txRepo.UpdateWagerTransactionTx(ctx, tx, pending); err != nil {
		return err
	}
	if err := r.wallet.UpdateWalletTx(ctx, tx, w); err != nil {
		return err
	}
	if err := r.wallet.InsertLedgerEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	if err := r.emitProcessedEvent(ctx, tx, pending, now); err != nil {
		return err
	}
	if err := r.emitBalanceChangedEvent(ctx, tx, pending, w, pending.Money(), balanceBefore, direction, now); err != nil {
		return err
	}

	r.logger.Info("resolved pending reference",
		"transaction_id", pending.ID(),
		"reference_id", ref.ID(),
		"status", "PROCESSED")
	return nil
}

func (r *Resolver) rescheduleOrGiveUp(ctx context.Context, tx pgx.Tx, pending *transaction.WagerTransaction, now time.Time) error {
	if pending.Attempts()+1 >= r.cfg.MaxAttempts {
		return r.finalizeRejected(ctx, tx, pending, service.FailureReferenceNotFound, "", now)
	}

	shift := uint(pending.Attempts())
	if shift > 20 {
		shift = 20
	}
	delay := r.cfg.BaseBackoff << shift
	if delay > r.cfg.MaxBackoff {
		delay = r.cfg.MaxBackoff
	}
	nextAttempt := now.Add(delay)

	if err := pending.ScheduleRetry(nextAttempt, now); err != nil {
		return err
	}
	if err := r.txRepo.UpdateWagerTransactionTx(ctx, tx, *pending); err != nil {
		return err
	}
	r.logger.Info("rescheduled pending reference",
		"transaction_id", pending.ID(),
		"attempts", pending.Attempts(),
		"next_attempt_at", nextAttempt)
	return nil
}

func (r *Resolver) finalizeRejected(ctx context.Context, tx pgx.Tx, pending *transaction.WagerTransaction, failureCode, refInternalID string, now time.Time) error {
	if refInternalID != "" && pending.ReferenceTransactionID() == "" {
		if err := pending.BindReference(refInternalID, now); err != nil {
			return err
		}
	}
	if err := pending.MarkRejected(failureCode, now); err != nil {
		return err
	}
	if err := r.txRepo.UpdateWagerTransactionTx(ctx, tx, *pending); err != nil {
		return err
	}
	if err := r.emitRejectedEvent(ctx, tx, *pending, now); err != nil {
		return err
	}
	r.logger.Info("rejected pending reference",
		"transaction_id", pending.ID(),
		"failure_code", failureCode)
	return nil
}

// --- outbox helpers (same shape as TransactionService's) ---

func (r *Resolver) emitProcessedEvent(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction, now time.Time) error {
	env, err := event.NewProcessedEvent(wt.ID(), "", now, event.WagerTransactionProcessed{
		TransactionID:         wt.ID(),
		ProviderID:            wt.ProviderID(),
		ExternalTransactionID: wt.ExternalTransactionID(),
		PlayerID:              wt.PlayerID(),
		WalletID:              wt.WalletID(),
		RoundID:               wt.RoundID(),
		GameID:                wt.GameID(),
		Kind:                  wt.Kind(),
		Money:                 wt.Money(),
	})
	if err != nil {
		return err
	}
	return r.outbox.InsertEventTx(ctx, tx, env)
}

func (r *Resolver) emitRejectedEvent(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction, now time.Time) error {
	env, err := event.NewRejectedEvent(wt.ID(), "", now, event.WagerTransactionRejected{
		TransactionID:         wt.ID(),
		ProviderID:            wt.ProviderID(),
		ExternalTransactionID: wt.ExternalTransactionID(),
		PlayerID:              wt.PlayerID(),
		WalletID:              wt.WalletID(),
		RoundID:               wt.RoundID(),
		GameID:                wt.GameID(),
		Kind:                  wt.Kind(),
		Money:                 wt.Money(),
		FailureCode:           wt.FailureCode(),
	})
	if err != nil {
		return err
	}
	return r.outbox.InsertEventTx(ctx, tx, env)
}

func (r *Resolver) emitBalanceChangedEvent(
	ctx context.Context, tx pgx.Tx,
	wt transaction.WagerTransaction, w wallet.Wallet,
	amount, balanceBefore money.Money, direction wager.Direction, now time.Time,
) error {
	env, err := event.NewBalanceChangedEvent(wt.ID(), "", now, event.WalletBalanceChanged{
		WalletID:      w.ID(),
		TransactionID: wt.ID(),
		Direction:     direction,
		Money:         amount,
		BalanceBefore: balanceBefore,
		BalanceAfter:  w.Balance(),
		WalletVersion: w.Version(),
	})
	if err != nil {
		return err
	}
	return r.outbox.InsertEventTx(ctx, tx, env)
}
