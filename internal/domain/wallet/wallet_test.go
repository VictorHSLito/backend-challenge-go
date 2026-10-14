package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/victorhslito/junglegaming/internal/domain/money"
)

func mustParse(t *testing.T, amount, currency string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, currency)
	if err != nil {
		t.Fatalf("money.Parse(%q, %q): %v", amount, currency, err)
	}
	return m
}

func fixedNow() time.Time {
	return time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
}

func TestNewHappyPath(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	now := fixedNow()
	w, err := New("wallet-1", "player-1", initial, now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.ID() != "wallet-1" || w.PlayerID() != "player-1" {
		t.Errorf("identity: id=%q playerID=%q", w.ID(), w.PlayerID())
	}
	if !w.Balance().Equal(initial) {
		t.Errorf("balance = %+v; want %+v", w.Balance(), initial)
	}
	if w.Version() != 1 {
		t.Errorf("version = %d; want 1", w.Version())
	}
	if !w.CreatedAt().Equal(now) || !w.UpdatedAt().Equal(now) {
		t.Errorf("timestamps: createdAt=%v updatedAt=%v want %v", w.CreatedAt(), w.UpdatedAt(), now)
	}
}

func TestNewZeroInitialBalanceAllowed(t *testing.T) {
	zero := mustParse(t, "0.00", "BRL")
	w, err := New("wallet-1", "player-1", zero, fixedNow())
	if err != nil {
		t.Fatalf("New with zero initial balance: %v", err)
	}
	if !w.Balance().IsZero() || w.Version() != 1 {
		t.Errorf("got balance=%+v version=%d; want zero/version=1", w.Balance(), w.Version())
	}
}

func TestNewRejects(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	now := fixedNow()
	var empty money.Money
	neg, _ := money.New(-100, "BRL")

	cases := []struct {
		name    string
		id, pid string
		bal     money.Money
		now     time.Time
		wantErr error
	}{
		{"empty id", "", "p1", initial, now, ErrInvalidWallet},
		{"empty playerID", "w1", "", initial, now, ErrInvalidWallet},
		{"uninit balance", "w1", "p1", empty, now, ErrInvalidWallet},
		{"negative balance", "w1", "p1", neg, now, ErrInvalidAmount},
		{"zero now", "w1", "p1", initial, time.Time{}, ErrInvalidTimestamp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.id, tc.pid, tc.bal, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRehydrateHappyPath(t *testing.T) {
	bal := mustParse(t, "500.00", "BRL")
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	w, err := Rehydrate("wallet-1", "player-1", bal, 7, createdAt, updatedAt)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if w.Version() != 7 {
		t.Errorf("version = %d; want 7", w.Version())
	}
	if !w.CreatedAt().Equal(createdAt) || !w.UpdatedAt().Equal(updatedAt) {
		t.Errorf("timestamps mutated: createdAt=%v updatedAt=%v", w.CreatedAt(), w.UpdatedAt())
	}
	if !w.Balance().Equal(bal) {
		t.Errorf("balance = %+v; want %+v", w.Balance(), bal)
	}
}

func TestRehydrateRejects(t *testing.T) {
	bal := mustParse(t, "500.00", "BRL")
	now := fixedNow()
	var empty money.Money
	neg, _ := money.New(-100, "BRL")

	cases := []struct {
		name    string
		id, pid string
		bal     money.Money
		version int64
		ca, ua  time.Time
		wantErr error
	}{
		{"empty id", "", "p1", bal, 3, now, now, ErrInvalidWallet},
		{"empty playerID", "w1", "", bal, 3, now, now, ErrInvalidWallet},
		{"uninit balance", "w1", "p1", empty, 3, now, now, ErrInvalidWallet},
		{"negative balance", "w1", "p1", neg, 3, now, now, ErrInvalidWallet},
		{"zero version", "w1", "p1", bal, 0, now, now, ErrInvalidWallet},
		{"negative version", "w1", "p1", bal, -1, now, now, ErrInvalidWallet},
		{"zero createdAt", "w1", "p1", bal, 3, time.Time{}, now, ErrInvalidTimestamp},
		{"zero updatedAt", "w1", "p1", bal, 3, now, time.Time{}, ErrInvalidTimestamp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Rehydrate(tc.id, tc.pid, tc.bal, tc.version, tc.ca, tc.ua)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreditHappyPath(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	t0 := fixedNow()
	t1 := t0.Add(time.Minute)
	w, _ := New("w1", "p1", initial, t0)

	amount := mustParse(t, "50.00", "BRL")
	if err := w.Credit(amount, t1); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	want := mustParse(t, "150.00", "BRL")
	if !w.Balance().Equal(want) {
		t.Errorf("balance = %+v; want %+v", w.Balance(), want)
	}
	if w.Version() != 2 {
		t.Errorf("version = %d; want 2", w.Version())
	}
	if !w.UpdatedAt().Equal(t1) {
		t.Errorf("updatedAt = %v; want %v", w.UpdatedAt(), t1)
	}
	if !w.CreatedAt().Equal(t0) {
		t.Errorf("createdAt moved: got %v; want %v", w.CreatedAt(), t0)
	}
}

func TestCreditRejectsAndPreservesState(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	t0 := fixedNow()
	usdAmt := mustParse(t, "10.00", "USD")
	zeroAmt := mustParse(t, "0.00", "BRL")
	brl10 := mustParse(t, "10.00", "BRL")
	negAmt, _ := money.New(-100, "BRL")
	var empty money.Money

	cases := []struct {
		name    string
		amount  money.Money
		now     time.Time
		wantErr error
	}{
		{"currency mismatch", usdAmt, t0, money.ErrCurrencyMismatch},
		{"zero amount", zeroAmt, t0, ErrInvalidAmount},
		{"negative amount", negAmt, t0, ErrInvalidAmount},
		{"uninit amount", empty, t0, ErrInvalidAmount},
		{"zero now", brl10, time.Time{}, ErrInvalidTimestamp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := New("w1", "p1", initial, t0)
			err := w.Credit(tc.amount, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v; want %v", err, tc.wantErr)
			}
			if !w.Balance().Equal(initial) || w.Version() != 1 || !w.UpdatedAt().Equal(t0) {
				t.Errorf("state mutated on rejection: balance=%+v version=%d updatedAt=%v",
					w.Balance(), w.Version(), w.UpdatedAt())
			}
		})
	}
}

func TestDebitHappyPath(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	t0 := fixedNow()
	t1 := t0.Add(time.Minute)
	w, _ := New("w1", "p1", initial, t0)

	amount := mustParse(t, "80.00", "BRL")
	if err := w.Debit(amount, t1); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	want := mustParse(t, "20.00", "BRL")
	if !w.Balance().Equal(want) {
		t.Errorf("balance = %+v; want %+v", w.Balance(), want)
	}
	if w.Version() != 2 {
		t.Errorf("version = %d; want 2", w.Version())
	}
	if !w.UpdatedAt().Equal(t1) {
		t.Errorf("updatedAt = %v; want %v", w.UpdatedAt(), t1)
	}
}

func TestDebitExactBalance(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	w, _ := New("w1", "p1", initial, fixedNow())
	if err := w.Debit(initial, fixedNow().Add(time.Minute)); err != nil {
		t.Fatalf("Debit exact balance: %v", err)
	}
	if !w.Balance().IsZero() {
		t.Errorf("balance = %+v; want zero", w.Balance())
	}
	if w.Version() != 2 {
		t.Errorf("version = %d; want 2", w.Version())
	}
}

func TestDebitInsufficientFunds(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	t0 := fixedNow()
	w, _ := New("w1", "p1", initial, t0)

	excessive := mustParse(t, "200.00", "BRL")
	err := w.Debit(excessive, t0.Add(time.Minute))
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("got %v; want ErrInsufficientBalance", err)
	}
	if !w.Balance().Equal(initial) || w.Version() != 1 || !w.UpdatedAt().Equal(t0) {
		t.Errorf("state mutated on insufficient-funds rejection: balance=%+v version=%d updatedAt=%v",
			w.Balance(), w.Version(), w.UpdatedAt())
	}
}

func TestDebitRejectsAndPreservesState(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	t0 := fixedNow()
	usdAmt := mustParse(t, "10.00", "USD")
	zeroAmt := mustParse(t, "0.00", "BRL")
	brl10 := mustParse(t, "10.00", "BRL")
	negAmt, _ := money.New(-100, "BRL")
	var empty money.Money

	cases := []struct {
		name    string
		amount  money.Money
		now     time.Time
		wantErr error
	}{
		{"currency mismatch", usdAmt, t0, money.ErrCurrencyMismatch},
		{"zero amount", zeroAmt, t0, ErrInvalidAmount},
		{"negative amount", negAmt, t0, ErrInvalidAmount},
		{"uninit amount", empty, t0, ErrInvalidAmount},
		{"zero now", brl10, time.Time{}, ErrInvalidTimestamp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := New("w1", "p1", initial, t0)
			err := w.Debit(tc.amount, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v; want %v", err, tc.wantErr)
			}
			if !w.Balance().Equal(initial) || w.Version() != 1 || !w.UpdatedAt().Equal(t0) {
				t.Errorf("state mutated on rejection: balance=%+v version=%d updatedAt=%v",
					w.Balance(), w.Version(), w.UpdatedAt())
			}
		})
	}
}

func TestVersionMonotonicityAcrossOperations(t *testing.T) {
	initial := mustParse(t, "100.00", "BRL")
	t0 := fixedNow()
	w, _ := New("w1", "p1", initial, t0)

	ten := mustParse(t, "10.00", "BRL")
	if err := w.Credit(ten, t0.Add(1*time.Minute)); err != nil {
		t.Fatalf("Credit 1: %v", err)
	}
	if err := w.Credit(ten, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("Credit 2: %v", err)
	}
	if w.Version() != 3 {
		t.Errorf("version after 2 credits = %d; want 3", w.Version())
	}

	five := mustParse(t, "5.00", "BRL")
	if err := w.Debit(five, t0.Add(3*time.Minute)); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if w.Version() != 4 {
		t.Errorf("version after 1 debit = %d; want 4", w.Version())
	}
	if !w.UpdatedAt().Equal(t0.Add(3 * time.Minute)) {
		t.Errorf("updatedAt = %v; want %v", w.UpdatedAt(), t0.Add(3*time.Minute))
	}
}
