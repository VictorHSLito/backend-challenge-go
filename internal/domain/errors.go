package domain

import (
	"errors"
	"fmt"
)

var (
	// ErrValidation marks input that can never be accepted as sent (correctable by the caller).
	ErrValidation = errors.New("validation failed")
	// ErrNotFound marks a missing aggregate.
	ErrNotFound = errors.New("not found")
	// ErrConflict marks a request that contradicts persisted state (idempotency or uniqueness).
	ErrConflict = errors.New("conflict")
	// ErrInvariant marks an attempt to break an aggregate invariant; it indicates a bug or corrupted state.
	ErrInvariant = errors.New("invariant violated")
)

var (
	ErrInvalidAmount    = fmt.Errorf("%w: invalid amount", ErrValidation)
	ErrInvalidCurrency  = fmt.Errorf("%w: invalid currency", ErrValidation)
	ErrInvalidMoney     = fmt.Errorf("%w: money is not initialized", ErrInvariant)
	ErrCurrencyMismatch = errors.New("currency mismatch")
	ErrMoneyOverflow    = errors.New("money overflow")
)

var (
	ErrInsufficientFunds    = errors.New("insufficient funds")
	ErrInvalidTransition    = fmt.Errorf("%w: invalid state transition", ErrInvariant)
	ErrWalletNotFound       = fmt.Errorf("%w: wallet", ErrNotFound)
	ErrTxNotFound           = fmt.Errorf("%w: wager transaction", ErrNotFound)
	ErrWalletExists         = fmt.Errorf("%w: wallet already exists for player and currency", ErrConflict)
	ErrIdempotencyKeyReused = fmt.Errorf("%w: idempotency key reused with a different payload", ErrConflict)
	ErrExternalIDReused     = fmt.Errorf("%w: external transaction already registered with another idempotency key", ErrConflict)
	ErrMessageHashMismatch  = fmt.Errorf("%w: message id redelivered with a different payload", ErrConflict)
)

type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

func (e *FieldError) Unwrap() error { return ErrValidation }

func InvalidField(field, reason string) error {
	return &FieldError{Field: field, Reason: reason}
}
