//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/service"
)

// §9 — Opening with a positive balance must create the wallet, an OPENING
// wager_transaction in PROCESSED, one credit ledger entry, and both outbox
// events — all in the SAME commit.
func TestOpenWallet_HappyPath_EmitsEvents(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)

	initial, err := money.Parse("1000.00", "BRL")
	if err != nil {
		t.Fatalf("parse initial: %v", err)
	}
	playerID := uuid.NewString()

	w, err := svc.wallet.OpenWallet(ctx, playerID, initial)
	if err != nil {
		t.Fatalf("OpenWallet failed: %v", err)
	}
	if got := w.Balance().Amount(); got != 100000 {
		t.Fatalf("want balance 100000 cents, got %d", got)
	}
	if got := w.Version(); got != 1 {
		t.Fatalf("want version 1, got %d", got)
	}

	// Exactly one OPENING tx, one ledger credit, two outbox events.
	assertCount(t, ctx, "wager_transactions WHERE kind='OPENING' AND status='PROCESSED'", 1)
	assertCount(t, ctx, "wallet_ledger_entries WHERE wallet_id=$1 AND direction='CREDIT'", 1, w.ID())
	assertCount(t, ctx, "outbox WHERE event_type='WagerTransactionProcessed'", 1)
	assertCount(t, ctx, "outbox WHERE event_type='WalletBalanceChanged'", 1)
}

// §9 — Zero balance opening must NOT create OPENING tx, ledger, or events.
func TestOpenWallet_Zero_NoOpening(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)

	zero, _ := money.Zero("BRL")
	playerID := uuid.NewString()

	w, err := svc.wallet.OpenWallet(ctx, playerID, zero)
	if err != nil {
		t.Fatalf("OpenWallet failed: %v", err)
	}
	if !w.Balance().IsZero() {
		t.Fatalf("want zero balance, got %d", w.Balance().Amount())
	}
	assertCount(t, ctx, "wager_transactions", 0)
	assertCount(t, ctx, "wallet_ledger_entries", 0)
	assertCount(t, ctx, "outbox", 0)
}

// §6.2 — (playerId, currency) must be unique.
func TestOpenWallet_DuplicatePlayerCurrency_Conflict(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)

	initial, _ := money.Parse("50.00", "BRL")
	playerID := uuid.NewString()

	if _, err := svc.wallet.OpenWallet(ctx, playerID, initial); err != nil {
		t.Fatalf("first OpenWallet failed: %v", err)
	}
	_, err := svc.wallet.OpenWallet(ctx, playerID, initial)
	if !errors.Is(err, service.ErrWalletAlreadyExists) {
		t.Fatalf("expected ErrWalletAlreadyExists, got %v", err)
	}
}

// --- helpers ---

func assertCount(t *testing.T, ctx context.Context, where string, want int, args ...any) {
	t.Helper()
	var n int
	query := "SELECT COUNT(*) FROM " + where
	if err := pg.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q failed: %v", query, err)
	}
	if n != want {
		t.Fatalf("count(%s) = %d, want %d", where, n, want)
	}
}
