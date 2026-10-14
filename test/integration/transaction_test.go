//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/service"
)

// openTestWallet is a small fixture: fresh wallet with 1000.00 BRL.
func openTestWallet(t *testing.T, svc services, ctx context.Context, amount string) (walletID, playerID string) {
	t.Helper()
	initial, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatalf("parse initial: %v", err)
	}
	playerID = uuid.NewString()
	w, err := svc.wallet.OpenWallet(ctx, playerID, initial)
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	return w.ID(), playerID
}

func betCmd(providerID, extTxID, walletID, playerID, amount string) service.ProcessTransactionCommand {
	m, _ := money.Parse(amount, "BRL")
	return service.ProcessTransactionCommand{
		ProviderID:            providerID,
		ExternalTransactionID: extTxID,
		PlayerID:              playerID,
		WalletID:              walletID,
		RoundID:               "round-1",
		GameID:                "fortune-chimp",
		Kind:                  wager.KindBet,
		Money:                 m,
	}
}

// §7 — a BET on a sufficient balance writes the trio (tx, wallet update,
// ledger) AND the two outbox events, all atomically.
func TestBet_HappyPath_WritesLedgerAndOutbox(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	walletID, playerID := openTestWallet(t, svc, ctx, "100.00")

	res, err := svc.tx.ProcessTransaction(ctx, "provider-a:tx-1", betCmd("provider-a", "tx-1", walletID, playerID, "25.00"))
	if err != nil {
		t.Fatalf("ProcessTransaction: %v", err)
	}
	if res.Status != wager.StatusProcessed {
		t.Fatalf("want PROCESSED, got %s", res.Status)
	}
	if got := res.Balance.Amount(); got != 7500 {
		t.Fatalf("want balance 7500 cents, got %d", got)
	}

	// 1 opening ledger (CREDIT) + 1 bet ledger (DEBIT) = 2 total.
	assertCount(t, ctx, "wallet_ledger_entries WHERE wallet_id=$1", 2, walletID)
	assertCount(t, ctx, "wallet_ledger_entries WHERE wallet_id=$1 AND direction='DEBIT'", 1, walletID)
	// 2 opening events + 2 bet events = 4 outbox rows total.
	assertCount(t, ctx, "outbox", 4)
}

// §7 — BET > balance is REJECTED with INSUFFICIENT_FUNDS, no ledger line,
// but the rejected tx and the rejection event are both persisted.
func TestBet_InsufficientFunds_RejectedWithFailureCode(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	walletID, playerID := openTestWallet(t, svc, ctx, "10.00")

	res, err := svc.tx.ProcessTransaction(ctx, "provider-a:tx-1", betCmd("provider-a", "tx-1", walletID, playerID, "25.00"))
	if err != nil {
		t.Fatalf("ProcessTransaction: %v", err)
	}
	if res.Status != wager.StatusRejected {
		t.Fatalf("want REJECTED, got %s", res.Status)
	}
	if res.FailureCode != service.FailureInsufficientFunds {
		t.Fatalf("want %s failure code, got %s", service.FailureInsufficientFunds, res.FailureCode)
	}
	assertCount(t, ctx, "wallet_ledger_entries WHERE wallet_id=$1 AND direction='DEBIT'", 0, walletID)
	assertCount(t, ctx, "outbox WHERE event_type='WagerTransactionRejected'", 1)
}

// §9 — same key + same payload → one row, replay returns the stored result.
func TestIdempotentReplay_SamePayload_SameResult(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	walletID, playerID := openTestWallet(t, svc, ctx, "100.00")

	cmd := betCmd("provider-a", "tx-1", walletID, playerID, "25.00")
	first, err := svc.tx.ProcessTransaction(ctx, "provider-a:tx-1", cmd)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.tx.ProcessTransaction(ctx, "provider-a:tx-1", cmd)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.TransactionID != first.TransactionID {
		t.Fatalf("replay returned different transaction id: first=%s second=%s", first.TransactionID, second.TransactionID)
	}
	if !second.IdempotentReplay {
		t.Fatalf("second call must set IdempotentReplay=true")
	}
	if !second.Balance.Equal(first.Balance) {
		t.Fatalf("replay balance %v != first %v", second.Balance, first.Balance)
	}
	// Still only one BET tx and one DEBIT entry.
	assertCount(t, ctx, "wager_transactions WHERE kind='BET'", 1)
	assertCount(t, ctx, "wallet_ledger_entries WHERE direction='DEBIT'", 1)
}

// §9 — same key + different payload → ErrIdempotencyConflict, no mutation.
func TestIdempotencyConflict_DifferentPayload_Rejected(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	walletID, playerID := openTestWallet(t, svc, ctx, "100.00")

	if _, err := svc.tx.ProcessTransaction(ctx, "provider-a:tx-1", betCmd("provider-a", "tx-1", walletID, playerID, "25.00")); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := svc.tx.ProcessTransaction(ctx, "provider-a:tx-1", betCmd("provider-a", "tx-1", walletID, playerID, "40.00"))
	if !errors.Is(err, service.ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict, got %v", err)
	}
	assertCount(t, ctx, "wager_transactions WHERE kind='BET'", 1)
	assertCount(t, ctx, "wallet_ledger_entries WHERE direction='DEBIT'", 1)
}
