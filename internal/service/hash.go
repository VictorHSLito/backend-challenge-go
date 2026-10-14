package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
)

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
		ProviderID:                     cmd.ProviderID,
		ExternalTransactionID:          cmd.ExternalTransactionID,
		PlayerID:                       cmd.PlayerID,
		WalletID:                       cmd.WalletID,
		RoundID:                        cmd.RoundID,
		GameID:                         cmd.GameID,
		Kind:                           string(cmd.Kind),
		Amount:                         cmd.Money.Amount(),
		Currency:                       cmd.Money.Currency(),
		ReferenceExternalTransactionID: cmd.ReferenceExternalTransactionID,
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

func isReversible(reversal, original wager.Kind) bool {
	if reversal == wager.KindRefund {
		return original == wager.KindBet
	}
	if reversal == wager.KindRollback {
		switch original {
		case wager.KindBet, wager.KindWin, wager.KindRefund:
			return true
		}
	}
	return false
}

func reversalDirection(reversal, original wager.Kind) wager.Direction {
	if reversal == wager.KindRefund {
		return wager.DirectionCredit
	}
	switch original {
	case wager.KindBet:
		return wager.DirectionCredit
	case wager.KindWin:
		return wager.DirectionDebit
	case wager.KindRefund:
		return wager.DirectionDebit
	}
	panic("unreachable")
}

func (s *Service) ProcessTransaction(ctx context.Context, idempotencyKey string, cmd ProcessTransactionCommand) (ProcessTransactionResult, error) {
	switch cmd.Kind {
	case wager.KindBet, wager.KindWin, wager.KindLoss, wager.KindRefund, wager.KindRollback:
	default:
		return ProcessTransactionResult{}, ErrUnsupportedKind
	}

	hash := PayloadHash(cmd)
	now := time.Now()
	var result ProcessTransactionResult

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		existing, found, err := s.repo.GetTransactionByProviderIdempotencyKeyTx(ctx, tx, cmd.ProviderID, idempotencyKey)
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

		w, found, err := s.repo.GetWalletForUpdateTx(ctx, tx, cmd.WalletID)
		if err != nil {
			return err
		}
		if !found {
			return ErrWalletNotFound
		}

		balanceBefore := w.Balance()

		txID, _ := uuid.NewV7()
		wt, err := transaction.NewExternal(
			txID.String(), cmd.ProviderID, cmd.ExternalTransactionID,
			idempotencyKey, hash, cmd.WalletID, cmd.PlayerID, cmd.RoundID, cmd.GameID,
			cmd.Kind, cmd.Money, cmd.ReferenceExternalTransactionID, now,
		)
		if err != nil {
			return err
		}

		persistRejected := func(code string, refInternalID string) error {
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
			if err := s.repo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}
			result = ProcessTransactionResult{
				TransactionID:    wt.ID(),
				Status:           wt.Status(),
				Balance:          balanceBefore,
				FailureCode:      wt.FailureCode(),
				IdempotentReplay: false,
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
			leID, _ := uuid.NewV7()
			entry, err := ledger.NewDebit(leID.String(), w.ID(), wt.ID(), cmd.Money, balanceBefore, now)
			if err != nil {
				return err
			}

			if err := s.repo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}
			if err := s.repo.UpdateWalletTx(ctx, tx, w); err != nil {
				return err
			}
			if err := s.repo.InsertLedgerEntryTx(ctx, tx, entry); err != nil {
				return err
			}

		case wager.KindWin:
			if err := w.Credit(cmd.Money, now); err != nil {
				return err
			}
			if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
				return err
			}
			leID, _ := uuid.NewV7()
			entry, err := ledger.NewCredit(leID.String(), w.ID(), wt.ID(), cmd.Money, balanceBefore, now)
			if err != nil {
				return err
			}

			if err := s.repo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}
			if err := s.repo.UpdateWalletTx(ctx, tx, w); err != nil {
				return err
			}
			if err := s.repo.InsertLedgerEntryTx(ctx, tx, entry); err != nil {
				return err
			}

		case wager.KindLoss:
			if err := wt.MarkProcessed(w.Balance(), w.Version(), now); err != nil {
				return err
			}
			if err := s.repo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}

		case wager.KindRefund, wager.KindRollback:
			ref, found, err := s.repo.GetTransactionByProviderExternalIDTx(ctx, tx, cmd.ProviderID, cmd.ReferenceExternalTransactionID)
			if err != nil {
				return err
			}
			if !found {
				return persistRejected(FailureReferenceNotFound, "")
			}

			if ref.Status() != wager.StatusProcessed {
				return persistRejected(FailureReferenceNotProcessed, ref.ID())
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

			if !isReversible(cmd.Kind, ref.Kind()) {
				return persistRejected(FailureReferenceUnreversable, ref.ID())
			}

			_, foundRev, err := s.repo.GetProcessedReversalTx(ctx, tx, cmd.ProviderID, ref.ID())
			if err != nil {
				return err
			}
			if foundRev {
				return persistRejected(FailureReferenceAlreadyReversed, ref.ID())
			}

			if err := wt.BindReference(ref.ID(), now); err != nil {
				return err
			}

			direction := reversalDirection(cmd.Kind, ref.Kind())
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
			leID, _ := uuid.NewV7()
			var entry ledger.Entry
			if direction == wager.DirectionCredit {
				entry, err = ledger.NewCredit(leID.String(), w.ID(), wt.ID(), cmd.Money, balanceBefore, now)
			} else {
				entry, err = ledger.NewDebit(leID.String(), w.ID(), wt.ID(), cmd.Money, balanceBefore, now)
			}
			if err != nil {
				return err
			}

			if err := s.repo.InsertWagerTransactionTx(ctx, tx, wt); err != nil {
				return err
			}
			if err := s.repo.UpdateWalletTx(ctx, tx, w); err != nil {
				return err
			}
			if err := s.repo.InsertLedgerEntryTx(ctx, tx, entry); err != nil {
				return err
			}
		}

		result = ProcessTransactionResult{
			TransactionID:    wt.ID(),
			Status:           wt.Status(),
			Balance:          w.Balance(),
			IdempotentReplay: false,
		}
		return nil
	})

	return result, err
}
