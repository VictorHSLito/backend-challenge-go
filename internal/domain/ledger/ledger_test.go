package ledger

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
)

func mustParse(t *testing.T, amount, currency string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, currency)
	if err != nil {
		t.Fatalf("money.Parse(%q, %q): %v", amount, currency, err)
	}
	return m
}

func mustNew(t *testing.T, units int64, currency string) money.Money {
	t.Helper()
	m, err := money.New(units, currency)
	if err != nil {
		t.Fatalf("money.New(%d, %q): %v", units, currency, err)
	}
	return m
}

func fixedNow() time.Time {
	return time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
}

func TestNewHappyPaths(t *testing.T) {
	amt := mustParse(t, "25.00", "BRL")
	before := mustParse(t, "100.00", "BRL")
	now := fixedNow()

	debit, err := NewDebit("e-1", "w-1", "tx-1", amt, before, now)
	if err != nil {
		t.Fatalf("NewDebit: %v", err)
	}
	wantDebitAfter := mustParse(t, "75.00", "BRL")
	if !debit.BalanceAfter().Equal(wantDebitAfter) {
		t.Errorf("debit BalanceAfter = %+v; want %+v", debit.BalanceAfter(), wantDebitAfter)
	}
	if debit.Direction() != wager.DirectionDebit {
		t.Errorf("debit Direction = %q; want DEBIT", debit.Direction())
	}
	if debit.ID() != "e-1" || debit.WalletID() != "w-1" || debit.TransactionID() != "tx-1" {
		t.Errorf("IDs: %q %q %q", debit.ID(), debit.WalletID(), debit.TransactionID())
	}
	if !debit.CreatedAt().Equal(now) {
		t.Errorf("createdAt = %v; want %v", debit.CreatedAt(), now)
	}

	credit, err := NewCredit("e-2", "w-1", "tx-2", amt, before, now)
	if err != nil {
		t.Fatalf("NewCredit: %v", err)
	}
	wantCreditAfter := mustParse(t, "125.00", "BRL")
	if !credit.BalanceAfter().Equal(wantCreditAfter) {
		t.Errorf("credit BalanceAfter = %+v; want %+v", credit.BalanceAfter(), wantCreditAfter)
	}
	if credit.Direction() != wager.DirectionCredit {
		t.Errorf("credit Direction = %q; want CREDIT", credit.Direction())
	}
}

func TestNewDebitExactBalanceProducesZero(t *testing.T) {
	amt := mustParse(t, "100.00", "BRL")
	before := mustParse(t, "100.00", "BRL")
	e, err := NewDebit("e-1", "w-1", "tx-1", amt, before, fixedNow())
	if err != nil {
		t.Fatalf("NewDebit at exact balance: %v", err)
	}
	if !e.BalanceAfter().IsZero() {
		t.Errorf("balanceAfter = %+v; want zero", e.BalanceAfter())
	}
}

type newCase struct {
	name     string
	id       string
	walletID string
	txID     string
	amount   money.Money
	before   money.Money
	now      time.Time
	wantErr  error
}

func newRejectCases(t *testing.T) []newCase {
	t.Helper()
	validAmt := mustParse(t, "10.00", "BRL")
	validBefore := mustParse(t, "100.00", "BRL")
	zero := mustParse(t, "0.00", "BRL")
	usd := mustParse(t, "10.00", "USD")
	negAmt := mustNew(t, -100, "BRL")
	negBefore := mustNew(t, -100, "BRL")
	var empty money.Money
	now := fixedNow()

	return []newCase{
		{"empty id", "", "w-1", "tx-1", validAmt, validBefore, now, ErrInvalidEntry},
		{"empty walletID", "e-1", "", "tx-1", validAmt, validBefore, now, ErrInvalidEntry},
		{"empty transactionID", "e-1", "w-1", "", validAmt, validBefore, now, ErrInvalidEntry},
		{"zero timestamp", "e-1", "w-1", "tx-1", validAmt, validBefore, time.Time{}, ErrInvalidEntry},
		{"uninit amount", "e-1", "w-1", "tx-1", empty, validBefore, now, ErrInvalidAmount},
		{"zero amount", "e-1", "w-1", "tx-1", zero, validBefore, now, ErrInvalidAmount},
		{"negative amount", "e-1", "w-1", "tx-1", negAmt, validBefore, now, ErrInvalidAmount},
		{"uninit balanceBefore", "e-1", "w-1", "tx-1", validAmt, empty, now, ErrInvalidAmount},
		{"negative balanceBefore", "e-1", "w-1", "tx-1", validAmt, negBefore, now, ErrInvalidAmount},
		{"currency mismatch amount/balanceBefore", "e-1", "w-1", "tx-1", usd, validBefore, now, ErrInvalidAmount},
	}
}

func TestNewDebitRejects(t *testing.T) {
	for _, tc := range newRejectCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDebit(tc.id, tc.walletID, tc.txID, tc.amount, tc.before, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("NewDebit: got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestNewCreditRejects(t *testing.T) {
	for _, tc := range newRejectCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCredit(tc.id, tc.walletID, tc.txID, tc.amount, tc.before, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("NewCredit: got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestNewDebitNegativeResultInvariant(t *testing.T) {
	amt := mustParse(t, "150.00", "BRL")
	before := mustParse(t, "100.00", "BRL")
	_, err := NewDebit("e-1", "w-1", "tx-1", amt, before, fixedNow())
	if !errors.Is(err, ErrLedgerInvariant) {
		t.Errorf("got %v; want ErrLedgerInvariant", err)
	}
}

func TestNewCreditOverflowInvariant(t *testing.T) {
	nearMax := mustNew(t, math.MaxInt64-50, "BRL")
	amt := mustNew(t, 100, "BRL")
	_, err := NewCredit("e-1", "w-1", "tx-1", amt, nearMax, fixedNow())
	if !errors.Is(err, ErrLedgerInvariant) {
		t.Errorf("got %v; want ErrLedgerInvariant", err)
	}
}

func TestRehydrateHappyPaths(t *testing.T) {
	amt := mustParse(t, "25.00", "BRL")
	before := mustParse(t, "100.00", "BRL")
	debitAfter := mustParse(t, "75.00", "BRL")
	creditAfter := mustParse(t, "125.00", "BRL")
	now := fixedNow()

	deb, err := Rehydrate("e-1", "w-1", "tx-1", wager.DirectionDebit, amt, before, debitAfter, now)
	if err != nil {
		t.Fatalf("Rehydrate debit: %v", err)
	}
	if !deb.BalanceAfter().Equal(debitAfter) {
		t.Errorf("debit BalanceAfter = %+v; want %+v", deb.BalanceAfter(), debitAfter)
	}
	if !deb.CreatedAt().Equal(now) {
		t.Errorf("createdAt = %v; want %v", deb.CreatedAt(), now)
	}

	cred, err := Rehydrate("e-2", "w-1", "tx-2", wager.DirectionCredit, amt, before, creditAfter, now)
	if err != nil {
		t.Fatalf("Rehydrate credit: %v", err)
	}
	if !cred.BalanceAfter().Equal(creditAfter) {
		t.Errorf("credit BalanceAfter = %+v; want %+v", cred.BalanceAfter(), creditAfter)
	}
}

func TestRehydrateTamperedArithmetic(t *testing.T) {
	amt := mustParse(t, "25.00", "BRL")
	before := mustParse(t, "100.00", "BRL")
	wrong := mustParse(t, "80.00", "BRL")
	now := fixedNow()

	if _, err := Rehydrate("e-1", "w-1", "tx-1", wager.DirectionDebit, amt, before, wrong, now); !errors.Is(err, ErrLedgerInvariant) {
		t.Errorf("debit tampered: got %v; want ErrLedgerInvariant", err)
	}
	if _, err := Rehydrate("e-2", "w-1", "tx-2", wager.DirectionCredit, amt, before, wrong, now); !errors.Is(err, ErrLedgerInvariant) {
		t.Errorf("credit tampered: got %v; want ErrLedgerInvariant", err)
	}
}

func TestRehydrateCommonRejects(t *testing.T) {
	after := mustParse(t, "75.00", "BRL")
	for _, tc := range newRejectCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Rehydrate(tc.id, tc.walletID, tc.txID, wager.DirectionDebit, tc.amount, tc.before, after, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Rehydrate: got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRehydrateSpecificRejects(t *testing.T) {
	amt := mustParse(t, "25.00", "BRL")
	before := mustParse(t, "100.00", "BRL")
	after := mustParse(t, "75.00", "BRL")
	now := fixedNow()
	var empty money.Money
	negAfter := mustNew(t, -100, "BRL")
	usdAfter := mustParse(t, "75.00", "USD")

	cases := []struct {
		name    string
		dir     wager.Direction
		after   money.Money
		wantErr error
	}{
		{"unknown direction", wager.Direction("WEIRD"), after, ErrInvalidEntry},
		{"empty direction", wager.Direction(""), after, ErrInvalidEntry},
		{"uninit balanceAfter", wager.DirectionDebit, empty, ErrInvalidAmount},
		{"negative balanceAfter", wager.DirectionDebit, negAfter, ErrInvalidAmount},
		{"currency mismatch amount/balanceAfter", wager.DirectionDebit, usdAfter, ErrInvalidAmount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Rehydrate("e-1", "w-1", "tx-1", tc.dir, amt, before, tc.after, now)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Rehydrate: got %v; want %v", err, tc.wantErr)
			}
		})
	}
}
