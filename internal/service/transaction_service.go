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
	logger *slog.Logger
}

func NewTransactionService(tx TxManager, wallet WalletRepository, txRepo TransactionRepository, logger *slog.Logger) *TransactionService {
	return &TransactionService{tx: tx, wallet: wallet, txRepo: txRepo, logger: logger}
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
// Field order follows the struct declaration (encoding/json emits in order).
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

func (s *TransactionService) ProcessTransaction(ctx context.Context, idempotencyKey string, cmd ProcessTransactionCommand) (ProcessTransactionResult, error) {
	switch cmd.Kind {
	case wager.KindBet, wager.KindWin, wager.KindLoss, wager.KindRefund, wager.KindRollback:
	default:
		return ProcessTransactionResult{}, ErrUnsupportedKind
	}

	hash := PayloadHash(cmd)
	now := time.Now()
	var result ProcessTransactionResult

	err := s.tx.RunInTx(ctx, func(tx pgx.Tx) error {
		existing, found, err := s.txRepo.GetTransactionByProviderIdempotencyKeyTx(ctx, tx, cmd.ProviderID, idempotencyKey)
		if err != nil {
			return err
		}
		if found {
			if existing.PayloadHash() != hash {
				return ErrIdempotencyConflict
			}
			result = toReplayResult(existing)
			return nil
		}

		w, found, err := s.wallet.GetWalletForUpdateTx(ctx, tx, cmd.WalletID)
		if err != nil {
			return err
		}
		if !found {
			return ErrWalletNotFound
		}

		balanceBefore := w.Balance()

		txID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		wt, err := transaction.NewExternal(
			txID.String(), cmd.ProviderID, cmd.ExternalTransactionID,
			idempotencyKey, hash, cmd.WalletID, cmd.PlayerID, cmd.RoundID, cmd.GameID,
			cmd.Kind, cmd.Money, cmd.ReferenceExternalTransactionID, now,
		)
		if err != nil {
			return err
		}

		persistRejected := func(code, refInternalID string) error {
			if refInternalID != "" {
				if err := wt.BindReference(refInternalID, now); err != nil {
					return err
				}
			}
			if err := wt.SetObservedBalance(balanceBefore); err != nil {
				return err
			}
			if err := wt.MarkRejected(code, now); err != nil {
				return err
			}
			if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}
			result = ProcessTransactionResult{
				TransactionID: wt.ID(), Status: wt.Status(), Balance: balanceBefore,
				FailureCode: wt.FailureCode(),
			}
			return nil
		}

		persistPendingReference := func(refInternalID string) error {
			if refInternalID != "" {
				if err := wt.BindReference(refInternalID, now); err != nil {
					return err
				}
			}
			if err := wt.SetObservedBalance(balanceBefore); err != nil {
				return err
			}
			if err := wt.MarkPendingReference(now); err != nil {
				return err
			}
			if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}
			result = ProcessTransactionResult{
				TransactionID: wt.ID(), Status: wt.Status(), Balance: balanceBefore,
			}
			return nil
		}

		switch cmd.Kind {
		case wager.KindBet:
			if err := w.Debit(cmd.Money, now); err != nil {
				if errors.Is(err, wallet.ErrInsufficientBalance) {
					return persistRejected(FailureInsufficientFunds, "")
				}
				return err
			}
			if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
				return err
			}
			if err := s.applyMovingTx(ctx, tx, wt, w, cmd.Money, balanceBefore, wager.DirectionDebit, now); err != nil {
				return err
			}

		case wager.KindWin:
			if err := w.Credit(cmd.Money, now); err != nil {
				return err
			}
			if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
				return err
			}
			if err := s.applyMovingTx(ctx, tx, wt, w, cmd.Money, balanceBefore, wager.DirectionCredit, now); err != nil {
				return err
			}

		case wager.KindLoss:
			if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
				return err
			}
			if err := s.txRepo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}

		case wager.KindRefund, wager.KindRollback:
			ref, found, err := s.txRepo.GetTransactionByProviderExternalIDTx(ctx, tx, cmd.ProviderID, cmd.ReferenceExternalTransactionID)
			if err != nil {
				return err
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
				return fmt.Errorf("unexpected reference status %q", ref.Status())
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
				return err
			}
			if cmp != 0 {
				return persistRejected(FailureReferenceAmountMismatch, ref.ID())
			}
			if !wager.IsReversible(cmd.Kind, ref.Kind()) {
				return persistRejected(FailureReferenceUnreversable, ref.ID())
			}

			_, foundRev, err := s.txRepo.GetProcessedReversalTx(ctx, tx, cmd.ProviderID, ref.ID())
			if err != nil {
				return err
			}
			if foundRev {
				return persistRejected(FailureReferenceAlreadyReversed, ref.ID())
			}

			if err := wt.BindReference(ref.ID(), now); err != nil {
				return err
			}

			direction, err := wager.ReversalDirection(cmd.Kind, ref.Kind())
			if err != nil {
				return err
			}
			if direction == wager.DirectionCredit {
				if err := w.Credit(cmd.Money, now); err != nil {
					return err
				}
			} else {
				if err := w.Debit(cmd.Money, now); err != nil {
					if errors.Is(err, wallet.ErrInsufficientBalance) {
						return persistRejected(FailureRollbackInsufficientFunds, ref.ID())
					}
					return err
				}
			}
			if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
				return err
			}
			if err := s.applyMovingTx(ctx, tx, wt, w, cmd.Money, balanceBefore, direction, now); err != nil {
				return err
			}
		}

		result = ProcessTransactionResult{
			TransactionID: wt.ID(), Status: wt.Status(), Balance: w.Balance(),
		}
		return nil
	})

	return result, err
}

// applyMovingTx persists the trio of writes (transaction, wallet, ledger) that every
// successful money-moving operation needs.
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
	return s.wallet.InsertLedgerEntryTx(ctx, tx, entry)
}
