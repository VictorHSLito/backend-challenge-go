package ledger

import (
	"errors"
	"fmt"
	"time"

	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
)

var (
	ErrInvalidEntry    = errors.New("invalid ledger entry data")
	ErrInvalidAmount   = errors.New("invalid ledger amount or currency mismatch")
	ErrLedgerInvariant = errors.New("ledger financial invariant violated")
)

type Entry struct {
	id            string
	walletID      string
	transactionID string
	direction     wager.Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

func validateCommon(id, walletID, transactionID string, amount, balanceBefore money.Money, createdAt time.Time) error {
	if id == "" || walletID == "" || transactionID == "" {
		return fmt.Errorf("%w: missing required IDs", ErrInvalidEntry)
	}
	if createdAt.IsZero() {
		return fmt.Errorf("%w: createdAt must be set", ErrInvalidEntry)
	}
	if !amount.IsValid() || !amount.IsPositive() {
		return fmt.Errorf("%w: amount must be valid and strictly positive", ErrInvalidAmount)
	}
	if !balanceBefore.IsValid() || balanceBefore.IsNegative() {
		return fmt.Errorf("%w: balanceBefore must be valid and non-negative", ErrInvalidAmount)
	}
	if amount.Currency() != balanceBefore.Currency() {
		return fmt.Errorf("%w: currency mismatch between amount and balanceBefore", ErrInvalidAmount)
	}
	return nil
}

func NewDebit(id, walletID, transactionID string, amount, balanceBefore money.Money, now time.Time) (Entry, error) {
	if err := validateCommon(id, walletID, transactionID, amount, balanceBefore, now); err != nil {
		return Entry{}, err
	}

	balanceAfter, err := balanceBefore.Subtract(amount)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: overflow during debit subtraction", ErrLedgerInvariant)
	}
	if balanceAfter.IsNegative() {
		return Entry{}, fmt.Errorf("%w: debit cannot result in negative balanceAfter", ErrLedgerInvariant)
	}

	return Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     wager.DirectionDebit,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     now,
	}, nil
}

func NewCredit(id, walletID, transactionID string, amount, balanceBefore money.Money, now time.Time) (Entry, error) {
	if err := validateCommon(id, walletID, transactionID, amount, balanceBefore, now); err != nil {
		return Entry{}, err
	}

	balanceAfter, err := balanceBefore.Add(amount)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: overflow during credit addition", ErrLedgerInvariant)
	}

	return Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     wager.DirectionCredit,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     now,
	}, nil
}

func Rehydrate(id, walletID, transactionID string, direction wager.Direction, amount, balanceBefore, balanceAfter money.Money, createdAt time.Time) (Entry, error) {
	if err := validateCommon(id, walletID, transactionID, amount, balanceBefore, createdAt); err != nil {
		return Entry{}, err
	}

	switch direction {
	case wager.DirectionDebit, wager.DirectionCredit:
	default:
		return Entry{}, fmt.Errorf("%w: invalid direction %q", ErrInvalidEntry, direction)
	}

	if !balanceAfter.IsValid() || balanceAfter.IsNegative() {
		return Entry{}, fmt.Errorf("%w: balanceAfter must be valid and non-negative", ErrInvalidAmount)
	}
	if amount.Currency() != balanceAfter.Currency() {
		return Entry{}, fmt.Errorf("%w: currency mismatch between amount and balanceAfter", ErrInvalidAmount)
	}

	var expected money.Money
	var err error
	if direction == wager.DirectionDebit {
		expected, err = balanceBefore.Subtract(amount)
	} else {
		expected, err = balanceBefore.Add(amount)
	}
	if err != nil || !expected.Equal(balanceAfter) {
		return Entry{}, fmt.Errorf("%w: persisted balanceAfter does not match the arithmetic identity", ErrLedgerInvariant)
	}

	return Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     createdAt,
	}, nil
}

func (e Entry) ID() string                 { return e.id }
func (e Entry) WalletID() string           { return e.walletID }
func (e Entry) TransactionID() string      { return e.transactionID }
func (e Entry) Direction() wager.Direction { return e.direction }
func (e Entry) Amount() money.Money        { return e.amount }
func (e Entry) BalanceBefore() money.Money { return e.balanceBefore }
func (e Entry) BalanceAfter() money.Money  { return e.balanceAfter }
func (e Entry) CreatedAt() time.Time       { return e.createdAt }
