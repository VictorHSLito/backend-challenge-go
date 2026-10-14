package service

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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/domain/event"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
)

type TransactionService struct {
	tx     TxManager
	wallet WalletRepository
	txRepo TransactionRepository
	outbox OutboxRepository
	inbox  InboxRepository
	logger *slog.Logger
}

func NewTransactionService(
	tx TxManager,
	wallet WalletRepository,
	txRepo TransactionRepository,
	outbox OutboxRepository,
	inbox InboxRepository,
	logger *slog.Logger,
) *TransactionService {
	return &TransactionService{tx: tx, wallet: wallet, txRepo: txRepo, outbox: outbox, inbox: inbox, logger: logger}
}

type ProcessTransactionCommand struct {
	ProviderID                     string      `json:"providerId"`
	ExternalTransactionID          string      `json:"externalTransactionId"`
	PlayerID                       string      `json:"playerId"`
	WalletID                       string      `json:"walletId"`
	RoundID                        string      `json:"roundId"`
	GameID                         string      `json:"gameId"`
	Kind                           wager.Kind  `json:"kind"`
	Money                          money.Money `json:"money"`
	ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId"`
}

type ProcessTransactionResult struct {
	TransactionID    string       `json:"transactionId"`
	Status           wager.Status `json:"status"`
	Balance          money.Money  `json:"balance"`
	FailureCode      string       `json:"failureCode"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

// PayloadHash computes a deterministic hash of the business fields of a
// transaction command, excluding transport metadata and the idempotency key.
func PayloadHash(cmd ProcessTransactionCommand) string {
	payload := struct {
		ProviderID                     string `json:"providerId"`
		ExternalTransactionID          string `json:"externalTransactionId"`
		PlayerID                       string `json:"playerId"`
		WalletID                       string `json:"walletId"`
		RoundID                        string `json:"roundId"`
		GameID                         string `json:"gameId"`
		Kind                           string `json:"kind"`
		Amount                         int64  `json:"amount"`
		Currency                       string `json:"currency"`
		ReferenceExternalTransactionID string `json:"referenceExternalTransactionId"`
	}{
		cmd.ProviderID, cmd.ExternalTransactionID, cmd.PlayerID, cmd.WalletID,
		cmd.RoundID, cmd.GameID, string(cmd.Kind),
		cmd.Money.Amount(), cmd.Money.Currency(),
		cmd.ReferenceExternalTransactionID,
	}
	bytes, _ := json.Marshal(payload)
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:])
}

func toReplayResult(wt transaction.WagerTransaction) ProcessTransactionResult {
	bal := wt.ObservedBalance()
	if !bal.IsValid() {
		bal, _ = money.Zero(wt.Money().Currency())
	}
	return ProcessTransactionResult{
		TransactionID:    wt.ID(),
		Status:           wt.Status(),
		Balance:          bal,
		FailureCode:      wt.FailureCode(),
		IdempotentReplay: true,
	}
}

func (s *TransactionService) GetTransaction(ctx context.Context, id string) (transaction.WagerTransaction, error) {
	if strings.TrimSpace(id) == "" {
		return transaction.WagerTransaction{}, fmt.Errorf("%w: transaction ID cannot be empty", transaction.ErrInvalidTransaction)
	}
	wt, found, err := s.txRepo.GetTransactionByID(ctx, id)
	if err != nil {
		return transaction.WagerTransaction{}, fmt.Errorf("failed to get transaction by id: %w", err)
	}
	if !found {
		return transaction.WagerTransaction{}, ErrTransactionNotFound
	}
	return wt, nil
}

func (s *TransactionService) GetTransactionByProvider(ctx context.Context, providerID, externalTxID string) (transaction.WagerTransaction, error) {
	if strings.TrimSpace(providerID) == "" || strings.TrimSpace(externalTxID) == "" {
		return transaction.WagerTransaction{}, fmt.Errorf("%w: providerID and externalTxID cannot be empty", transaction.ErrInvalidTransaction)
	}
	wt, found, err := s.txRepo.GetTransactionByProviderExternalID(ctx, providerID, externalTxID)
	if err != nil {
		return transaction.WagerTransaction{}, fmt.Errorf("failed to get transaction by provider external ID: %w", err)
	}
	if !found {
		return transaction.WagerTransaction{}, ErrTransactionNotFound
	}
	return wt, nil
}

// ProcessTransaction is the HTTP-facing entrypoint. It opens its own
// transaction and performs the full business workflow inside it.
func (s *TransactionService) ProcessTransaction(ctx context.Context, idempotencyKey string, cmd ProcessTransactionCommand) (ProcessTransactionResult, error) {
	if !isSupportedExternalKind(cmd.Kind) {
		return ProcessTransactionResult{}, ErrUnsupportedKind
	}
	var result ProcessTransactionResult
	err := s.tx.RunInTx(ctx, func(tx pgx.Tx) error {
		r, err := s.processInTx(ctx, tx, idempotencyKey, cmd, time.Now())
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	return result, err
}

// ProcessFromMessage is the SQS-facing entrypoint. It additionally takes a
// (consumerName, messageID, messageHash) triple for inbox dedup. The whole
// thing — inbox write, business work, outbox events — commits atomically.
func (s *TransactionService) ProcessFromMessage(
	ctx context.Context,
	consumerName, messageID, messageHash, idempotencyKey string,
	cmd ProcessTransactionCommand,
) (ProcessTransactionResult, error) {
	if !isSupportedExternalKind(cmd.Kind) {
		return ProcessTransactionResult{}, ErrUnsupportedKind
	}
	var result ProcessTransactionResult
	err := s.tx.RunInTx(ctx, func(tx pgx.Tx) error {
		now := time.Now()
		inserted, prior, err := s.inbox.MarkReceivedTx(ctx, tx, consumerName, messageID, messageHash, now)
		if err != nil {
			return err
		}
		if !inserted && prior.MessageHash != messageHash {
			// Same messageId, different content → poison. The consumer
			// should delete the message so it doesn't loop in the queue.
			return ErrIdempotencyConflict
		}

		// Whether the inbox row was fresh or already seen, processInTx's own
		// idempotency check against (providerId, idempotencyKey) decides what
		// happens: fresh work on first sight, replay result otherwise.
		r, err := s.processInTx(ctx, tx, idempotencyKey, cmd, now)
		if err != nil {
			return err
		}
		result = r

		if inserted {
			if err := s.inbox.MarkCompletedTx(ctx, tx, consumerName, messageID, r.TransactionID, now); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func isSupportedExternalKind(k wager.Kind) bool {
	switch k {
	case wager.KindBet, wager.KindWin, wager.KindLoss, wager.KindRefund, wager.KindRollback:
		return true
	}
	return false
}

// processInTx holds the complete business workflow and must only be called
// from inside a RunInTx callback. Both ProcessTransaction and
// ProcessFromMessage delegate here so there's exactly one source of truth
// for state transitions, event emission and persistence order.
func (s *TransactionService) processInTx(
	ctx context.Context, tx pgx.Tx,
	idempotencyKey string, cmd ProcessTransactionCommand, now time.Time,
) (ProcessTransactionResult, error) {
	hash := PayloadHash(cmd)

	existing, found, err := s.txRepo.GetTransactionByProviderIdempotencyKeyTx(ctx, tx, cmd.ProviderID, idempotencyKey)
	if err != nil {
		return ProcessTransactionResult{}, err
	}
	if found {
		if existing.PayloadHash() != hash {
			return ProcessTransactionResult{}, ErrIdempotencyConflict
		}
		return toReplayResult(existing), nil
	}

	w, found, err := s.wallet.GetWalletForUpdateTx(ctx, tx, cmd.WalletID)
	if err != nil {
		return ProcessTransactionResult{}, err
	}
	if !found {
		return ProcessTransactionResult{}, ErrWalletNotFound
	}

	balanceBefore := w.Balance()

	txID, err := uuid.NewV7()
	if err != nil {
		return ProcessTransactionResult{}, err
	}
	wt, err := transaction.NewExternal(
		txID.String(), cmd.ProviderID, cmd.ExternalTransactionID,
		idempotencyKey, hash, cmd.WalletID, cmd.PlayerID, cmd.RoundID, cmd.GameID,
		cmd.Kind, cmd.Money, cmd.ReferenceExternalTransactionID, now,
	)
	if err != nil {
		return ProcessTransactionResult{}, err
	}

	// --- helpers for the three terminal shapes ---

	persistRejected := func(code, refInternalID string) (ProcessTransactionResult, error) {
		if refInternalID != "" {
			if err := wt.BindReference(refInternalID, now); err != nil {
				return ProcessTransactionResult{}, err
			}
		}
		if err := wt.SetObservedBalance(balanceBefore); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := wt.MarkRejected(code, now); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.emitRejectedEvent(ctx, tx, wt, now); err != nil {
			return ProcessTransactionResult{}, err
		}
		return ProcessTransactionResult{
			TransactionID: wt.ID(), Status: wt.Status(), Balance: balanceBefore, FailureCode: wt.FailureCode(),
		}, nil
	}

	persistPendingReference := func(refInternalID string) (ProcessTransactionResult, error) {
		if refInternalID != "" {
			if err := wt.BindReference(refInternalID, now); err != nil {
				return ProcessTransactionResult{}, err
			}
		}
		if err := wt.SetObservedBalance(balanceBefore); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := wt.MarkPendingReference(now); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.emitPendingReferenceEvent(ctx, tx, wt, now); err != nil {
			return ProcessTransactionResult{}, err
		}
		return ProcessTransactionResult{
			TransactionID: wt.ID(), Status: wt.Status(), Balance: balanceBefore,
		}, nil
	}

	// --- kind-specific logic ---

	switch cmd.Kind {
	case wager.KindBet:
		if err := w.Debit(cmd.Money, now); err != nil {
			if errors.Is(err, wallet.ErrInsufficientBalance) {
				return persistRejected(FailureInsufficientFunds, "")
			}
			return ProcessTransactionResult{}, err
		}
		if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.applyMovingTx(ctx, tx, wt, w, cmd.Money, balanceBefore, wager.DirectionDebit, now); err != nil {
			return ProcessTransactionResult{}, err
		}

	case wager.KindWin:
		if err := w.Credit(cmd.Money, now); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.applyMovingTx(ctx, tx, wt, w, cmd.Money, balanceBefore, wager.DirectionCredit, now); err != nil {
			return ProcessTransactionResult{}, err
		}

	case wager.KindLoss:
		if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
			return ProcessTransactionResult{}, err
		}
		// LOSS emits only the processed event — no balance change (README §7).
		if err := s.emitProcessedEvent(ctx, tx, wt, now); err != nil {
			return ProcessTransactionResult{}, err
		}

	case wager.KindRefund, wager.KindRollback:
		ref, found, err := s.txRepo.GetTransactionByProviderExternalIDTx(ctx, tx, cmd.ProviderID, cmd.ReferenceExternalTransactionID)
		if err != nil {
			return ProcessTransactionResult{}, err
		}
		if !found {
			return persistPendingReference("")
		}
		switch ref.Status() {
		case wager.StatusPending, wager.StatusPendingReference:
			return persistPendingReference(ref.ID())
		case wager.StatusRejected, wager.StatusFailed:
			return persistRejected(FailureReferenceNotProcessed, ref.ID())
		case wager.StatusProcessed:
		default:
			return ProcessTransactionResult{}, fmt.Errorf("unexpected reference status %q", ref.Status())
		}
		if ref.ProviderID() != cmd.ProviderID ||
			ref.PlayerID() != cmd.PlayerID ||
			ref.WalletID() != cmd.WalletID ||
			ref.Money().Currency() != cmd.Money.Currency() ||
			ref.RoundID() != cmd.RoundID {
			return persistRejected(FailureReferenceMismatch, ref.ID())
		}
		cmp, err := ref.Money().Compare(cmd.Money)
		if err != nil {
			return ProcessTransactionResult{}, err
		}
		if cmp != 0 {
			return persistRejected(FailureReferenceAmountMismatch, ref.ID())
		}
		if !wager.IsReversible(cmd.Kind, ref.Kind()) {
			return persistRejected(FailureReferenceUnreversable, ref.ID())
		}
		_, foundRev, err := s.txRepo.GetProcessedReversalTx(ctx, tx, cmd.ProviderID, ref.ID())
		if err != nil {
			return ProcessTransactionResult{}, err
		}
		if foundRev {
			return persistRejected(FailureReferenceAlreadyReversed, ref.ID())
		}
		if err := wt.BindReference(ref.ID(), now); err != nil {
			return ProcessTransactionResult{}, err
		}
		direction, err := wager.ReversalDirection(cmd.Kind, ref.Kind())
		if err != nil {
			return ProcessTransactionResult{}, err
		}
		if direction == wager.DirectionCredit {
			if err := w.Credit(cmd.Money, now); err != nil {
				return ProcessTransactionResult{}, err
			}
		} else {
			if err := w.Debit(cmd.Money, now); err != nil {
				if errors.Is(err, wallet.ErrInsufficientBalance) {
					return persistRejected(FailureRollbackInsufficientFunds, ref.ID())
				}
				return ProcessTransactionResult{}, err
			}
		}
		if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
			return ProcessTransactionResult{}, err
		}
		if err := s.applyMovingTx(ctx, tx, wt, w, cmd.Money, balanceBefore, direction, now); err != nil {
			return ProcessTransactionResult{}, err
		}
	}

	return ProcessTransactionResult{
		TransactionID: wt.ID(), Status: wt.Status(), Balance: w.Balance(),
	}, nil
}

// applyMovingTx persists the trio of writes (transaction, wallet, ledger)
// that every money-moving operation needs AND emits the two events
// (processed + balance changed) inside the same tx.
func (s *TransactionService) applyMovingTx(
	ctx context.Context, tx pgx.Tx,
	wt transaction.WagerTransaction, w wallet.Wallet,
	amount, balanceBefore money.Money, direction wager.Direction, now time.Time,
) error {
	leID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var entry ledger.Entry
	if direction == wager.DirectionCredit {
		entry, err = ledger.NewCredit(leID.String(), w.ID(), wt.ID(), amount, balanceBefore, now)
	} else {
		entry, err = ledger.NewDebit(leID.String(), w.ID(), wt.ID(), amount, balanceBefore, now)
	}
	if err != nil {
		return err
	}

	if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
		return err
	}
	if err := s.wallet.UpdateWalletTx(ctx, tx, w); err != nil {
		return err
	}
	if err := s.wallet.InsertLedgerEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	if err := s.emitProcessedEvent(ctx, tx, wt, now); err != nil {
		return err
	}
	return s.emitBalanceChangedEvent(ctx, tx, wt, w, amount, balanceBefore, direction, now)
}

// --- outbox event helpers ---

func (s *TransactionService) emitProcessedEvent(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction, now time.Time) error {
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
	return s.outbox.InsertEventTx(ctx, tx, env)
}

func (s *TransactionService) emitRejectedEvent(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction, now time.Time) error {
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
	return s.outbox.InsertEventTx(ctx, tx, env)
}

func (s *TransactionService) emitPendingReferenceEvent(ctx context.Context, tx pgx.Tx, wt transaction.WagerTransaction, now time.Time) error {
	env, err := event.NewPendingReferenceEvent(wt.ID(), "", now, event.WagerTransactionPendingReference{
		TransactionID:                  wt.ID(),
		ProviderID:                     wt.ProviderID(),
		ExternalTransactionID:          wt.ExternalTransactionID(),
		PlayerID:                       wt.PlayerID(),
		WalletID:                       wt.WalletID(),
		RoundID:                        wt.RoundID(),
		GameID:                         wt.GameID(),
		Kind:                           wt.Kind(),
		Money:                          wt.Money(),
		ReferenceExternalTransactionID: wt.ReferenceExternalTransactionID(),
	})
	if err != nil {
		return err
	}
	return s.outbox.InsertEventTx(ctx, tx, env)
}

func (s *TransactionService) emitBalanceChangedEvent(
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
	return s.outbox.InsertEventTx(ctx, tx, env)
}
