package wallet

import (
	"errors"
	"fmt"
	"time"

	"github.com/victorhslito/junglegaming/internal/domain/money"
)

var (
	ErrInvalidWallet       = errors.New("invalid wallet data")
	ErrInvalidAmount       = errors.New("invalid wallet amount")
	ErrInvalidTimestamp    = errors.New("invalid wallet timestamp")
	ErrInsufficientBalance = errors.New("insufficient balance for debit operation")
)

type Wallet struct {
	id        string
	playerId  string
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

func New(id, playerId string, initialBalance money.Money, now time.Time) (Wallet, error) {
	if id == "" || playerId == "" {
		return Wallet{}, fmt.Errorf("%w: missing id or player id", ErrInvalidWallet)
	}
	if !initialBalance.IsValid() {
		return Wallet{}, fmt.Errorf("%w: initial balance is not initialized", ErrInvalidWallet)
	}
	if initialBalance.IsNegative() {
		return Wallet{}, fmt.Errorf("%w: initial balance cannot be negative", ErrInvalidAmount)
	}
	if now.IsZero() {
		return Wallet{}, fmt.Errorf("%w: now must be set", ErrInvalidTimestamp)
	}

	return Wallet{
		id:        id,
		playerId:  playerId,
		balance:   initialBalance,
		version:   1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func Rehydrate(id, playerId string, balance money.Money, version int64, createdAt, updatedAt time.Time) (Wallet, error) {
	if id == "" || playerId == "" {
		return Wallet{}, fmt.Errorf("%w: missing id or player id", ErrInvalidWallet)
	}
	if !balance.IsValid() {
		return Wallet{}, fmt.Errorf("%w: balance is not initialized", ErrInvalidWallet)
	}
	if balance.IsNegative() {
		return Wallet{}, fmt.Errorf("%w: persisted balance is negative", ErrInvalidWallet)
	}
	if version < 1 {
		return Wallet{}, fmt.Errorf("%w: version must be >= 1", ErrInvalidWallet)
	}
	if createdAt.IsZero() || updatedAt.IsZero() {
		return Wallet{}, fmt.Errorf("%w: createdAt and updatedAt must be set", ErrInvalidTimestamp)
	}

	return Wallet{
		id:        id,
		playerId:  playerId,
		balance:   balance,
		version:   version,
		createdAt: createdAt,
		updatedAt: updatedAt,
	}, nil
}

func (w Wallet) ID() string           { return w.id }
func (w Wallet) PlayerID() string     { return w.playerId }
func (w Wallet) Balance() money.Money { return w.balance }
func (w Wallet) Version() int64       { return w.version }
func (w Wallet) CreatedAt() time.Time { return w.createdAt }
func (w Wallet) UpdatedAt() time.Time { return w.updatedAt }

func (w *Wallet) Credit(amount money.Money, now time.Time) error {
	if !amount.IsValid() {
		return fmt.Errorf("%w: credit amount is not initialized", ErrInvalidAmount)
	}
	if !amount.IsPositive() {
		return fmt.Errorf("%w: credit amount must be strictly positive", ErrInvalidAmount)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: now must be set", ErrInvalidTimestamp)
	}

	newBalance, err := w.balance.Add(amount)
	if err != nil {
		return err
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = now
	return nil
}

func (w *Wallet) Debit(amount money.Money, now time.Time) error {
	if !amount.IsValid() {
		return fmt.Errorf("%w: debit amount is not initialized", ErrInvalidAmount)
	}
	if !amount.IsPositive() {
		return fmt.Errorf("%w: debit amount must be strictly positive", ErrInvalidAmount)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: now must be set", ErrInvalidTimestamp)
	}

	newBalance, err := w.balance.Subtract(amount)
	if err != nil {
		return err
	}
	if newBalance.IsNegative() {
		return fmt.Errorf("%w: current balance is insufficient", ErrInsufficientBalance)
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = now
	return nil
}
