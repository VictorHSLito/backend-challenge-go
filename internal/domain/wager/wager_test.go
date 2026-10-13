package wager

import (
	"errors"
	"testing"

	"github.com/victorhslito/junglegaming/internal/domain/money"
)

func TestParseKindValid(t *testing.T) {
	for _, k := range []Kind{KindOpening, KindBet, KindWin, KindLoss, KindRefund, KindRollback} {
		got, err := ParseKind(string(k))
		if err != nil || got != k {
			t.Errorf("ParseKind(%q): got (%q, %v); want (%q, nil)", k, got, err, k)
		}
	}
}

func TestParseKindInvalid(t *testing.T) {
	for _, in := range []string{"", " ", "bet", "BET ", " BET", "bEt", "UNKNOWN"} {
		if _, err := ParseKind(in); !errors.Is(err, ErrUnknownKind) {
			t.Errorf("ParseKind(%q): got %v; want ErrUnknownKind", in, err)
		}
	}
}

func TestParseStatusValid(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed} {
		got, err := ParseStatus(string(s))
		if err != nil || got != s {
			t.Errorf("ParseStatus(%q): got (%q, %v); want (%q, nil)", s, got, err, s)
		}
	}
}

func TestParseStatusInvalid(t *testing.T) {
	for _, in := range []string{"", "processed", "PROCESSED ", "pending_reference", "UNKNOWN"} {
		if _, err := ParseStatus(in); !errors.Is(err, ErrUnknownStatus) {
			t.Errorf("ParseStatus(%q): got %v; want ErrUnknownStatus", in, err)
		}
	}
}

func TestParseDirectionValid(t *testing.T) {
	for _, d := range []Direction{DirectionDebit, DirectionCredit} {
		got, err := ParseDirection(string(d))
		if err != nil || got != d {
			t.Errorf("ParseDirection(%q): got (%q, %v); want (%q, nil)", d, got, err, d)
		}
	}
}

func TestParseDirectionInvalid(t *testing.T) {
	for _, in := range []string{"", "debit", "credit", "CREDIT ", "UNKNOWN"} {
		if _, err := ParseDirection(in); !errors.Is(err, ErrUnknownDirection) {
			t.Errorf("ParseDirection(%q): got %v; want ErrUnknownDirection", in, err)
		}
	}
}

func TestKindPredicateTable(t *testing.T) {
	cases := []struct {
		kind              Kind
		isExternal        bool
		requiresReference bool
		movesMoney        bool
	}{
		{KindOpening, false, false, true},
		{KindBet, true, false, true},
		{KindWin, true, false, true},
		{KindLoss, true, false, false},
		{KindRefund, true, true, true},
		{KindRollback, true, true, true},
	}
	for _, tc := range cases {
		if got := tc.kind.IsExternal(); got != tc.isExternal {
			t.Errorf("%s.IsExternal() = %v; want %v", tc.kind, got, tc.isExternal)
		}
		if got := tc.kind.RequiresReference(); got != tc.requiresReference {
			t.Errorf("%s.RequiresReference() = %v; want %v", tc.kind, got, tc.requiresReference)
		}
		if got := tc.kind.MovesMoney(); got != tc.movesMoney {
			t.Errorf("%s.MovesMoney() = %v; want %v", tc.kind, got, tc.movesMoney)
		}
	}
}

func TestStatusTerminalTable(t *testing.T) {
	cases := []struct {
		status   Status
		terminal bool
	}{
		{StatusPending, false},
		{StatusPendingReference, false},
		{StatusProcessed, true},
		{StatusRejected, true},
		{StatusFailed, true},
	}
	for _, tc := range cases {
		if got := tc.status.IsTerminal(); got != tc.terminal {
			t.Errorf("%s.IsTerminal() = %v; want %v", tc.status, got, tc.terminal)
		}
	}
}

func TestValidateTransitionMatrix(t *testing.T) {
	all := []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed}
	allowed := map[Status]map[Status]bool{
		StatusPending: {
			StatusProcessed:        true,
			StatusRejected:         true,
			StatusFailed:           true,
			StatusPendingReference: true,
		},
		StatusPendingReference: {
			StatusProcessed: true,
			StatusRejected:  true,
			StatusFailed:    true,
		},
	}
	for _, from := range all {
		for _, to := range all {
			err := ValidateTransition(from, to)
			want := allowed[from][to]
			if want {
				if err != nil {
					t.Errorf("ValidateTransition(%s -> %s): got %v; want nil", from, to, err)
				}
			} else {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Errorf("ValidateTransition(%s -> %s): got %v; want ErrInvalidTransition", from, to, err)
				}
			}
		}
	}
}

func TestValidateExternalHappyPaths(t *testing.T) {
	pos, _ := money.Parse("10.00", "BRL")
	zero, _ := money.Zero("BRL")

	cases := []struct {
		kind   Kind
		amount money.Money
		refExt string
	}{
		{KindBet, pos, ""},
		{KindWin, pos, ""},
		{KindRefund, pos, "ext-123"},
		{KindRollback, pos, "ext-456"},
		{KindLoss, zero, ""},
	}
	for _, tc := range cases {
		if err := tc.kind.ValidateExternal(tc.amount, tc.refExt); err != nil {
			t.Errorf("ValidateExternal(%s, amount=%d, ref=%q): unexpected %v",
				tc.kind, tc.amount.Amount(), tc.refExt, err)
		}
	}
}

func TestValidateExternalRejectsOpening(t *testing.T) {
	pos, _ := money.Parse("10.00", "BRL")
	if err := KindOpening.ValidateExternal(pos, "ext-123"); !errors.Is(err, ErrInvalidKindRule) {
		t.Errorf("OPENING external: got %v; want ErrInvalidKindRule", err)
	}
}

func TestValidateExternalAmountRules(t *testing.T) {
	zero, _ := money.Zero("BRL")
	pos, _ := money.Parse("10.00", "BRL")
	neg, _ := money.New(-100, "BRL")

	for _, k := range []Kind{KindBet, KindWin} {
		if err := k.ValidateExternal(zero, ""); !errors.Is(err, ErrInvalidKindRule) {
			t.Errorf("%s with zero amount: got %v; want ErrInvalidKindRule", k, err)
		}
		if err := k.ValidateExternal(neg, ""); !errors.Is(err, ErrInvalidKindRule) {
			t.Errorf("%s with negative amount: got %v; want ErrInvalidKindRule", k, err)
		}
	}

	for _, k := range []Kind{KindRefund, KindRollback} {
		if err := k.ValidateExternal(zero, "ext-1"); !errors.Is(err, ErrInvalidKindRule) {
			t.Errorf("%s with zero amount: got %v; want ErrInvalidKindRule", k, err)
		}
		if err := k.ValidateExternal(neg, "ext-1"); !errors.Is(err, ErrInvalidKindRule) {
			t.Errorf("%s with negative amount: got %v; want ErrInvalidKindRule", k, err)
		}
	}

	if err := KindLoss.ValidateExternal(pos, ""); !errors.Is(err, ErrInvalidKindRule) {
		t.Errorf("LOSS with positive amount: got %v; want ErrInvalidKindRule", err)
	}
	if err := KindLoss.ValidateExternal(neg, ""); !errors.Is(err, ErrInvalidKindRule) {
		t.Errorf("LOSS with negative amount: got %v; want ErrInvalidKindRule", err)
	}
}

func TestValidateExternalReferenceRequired(t *testing.T) {
	pos, _ := money.Parse("10.00", "BRL")
	for _, k := range []Kind{KindRefund, KindRollback} {
		if err := k.ValidateExternal(pos, ""); !errors.Is(err, ErrInvalidKindRule) {
			t.Errorf("%s empty reference: got %v; want ErrInvalidKindRule", k, err)
		}
		if err := k.ValidateExternal(pos, "   "); !errors.Is(err, ErrInvalidKindRule) {
			t.Errorf("%s whitespace reference: got %v; want ErrInvalidKindRule", k, err)
		}
	}
}

func TestValidateExternalRejectsUninitializedMoney(t *testing.T) {
	var empty money.Money
	for _, k := range []Kind{KindBet, KindWin, KindLoss, KindRefund, KindRollback} {
		if err := k.ValidateExternal(empty, "ext-1"); !errors.Is(err, ErrInvalidKindRule) {
			t.Errorf("%s with uninitialized money: got %v; want ErrInvalidKindRule", k, err)
		}
	}
}

func TestValidateExternalUnknownKind(t *testing.T) {
	pos, _ := money.Parse("10.00", "BRL")
	weird := Kind("WEIRD")
	if err := weird.ValidateExternal(pos, "ext-1"); !errors.Is(err, ErrInvalidKindRule) {
		t.Errorf("unknown kind: got %v; want ErrInvalidKindRule", err)
	}
}
