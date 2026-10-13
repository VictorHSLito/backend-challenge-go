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

func (s *Service) ProcessTransaction(ctx context.Context, idempotencyKey string, cmd ProcessTransactionCommand) (ProcessTransactionResult, error) {
	if cmd.Kind != wager.KindBet {
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

		txID, _ := uuid.NewV7()
		wt, err := transaction.NewExternal(
			txID.String(), cmd.ProviderID, cmd.ExternalTransactionID,
			idempotencyKey, hash, cmd.WalletID, cmd.PlayerID, cmd.RoundID, cmd.GameID,
			cmd.Kind, cmd.Money, cmd.ReferenceExternalTransactionID, now,
		)
		if err != nil {
			return err
		}

		balanceBefore := w.Balance()
		if err := w.Debit(cmd.Money, now); err != nil {
			if errors.Is(err, wallet.ErrInsufficientBalance) {
				if err := wt.MarkRejected("INSUFFICIENT_FUNDS", now); err != nil {
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
