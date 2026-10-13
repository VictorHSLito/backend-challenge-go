package wager

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorhslito/junglegaming/internal/domain/money"
)

var (
	ErrUnknownKind       = errors.New("unknown wager transaction kind")
	ErrUnknownStatus     = errors.New("unknown wager transaction status")
	ErrUnknownDirection  = errors.New("unknown ledger entry direction")
	ErrInvalidTransition = errors.New("invalid state transition for wager transaction")
	ErrInvalidKindRule   = errors.New("wager transaction violates kind-specific business rule")
)

type Kind string

const (
	KindOpening  Kind = "OPENING"
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
)

func ParseKind(val string) (Kind, error) {
	k := Kind(val)
	switch k {
	case KindOpening, KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return k, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownKind, val)
	}
}

func (k Kind) IsExternal() bool        { return k != KindOpening }
func (k Kind) RequiresReference() bool { return k == KindRefund || k == KindRollback }
func (k Kind) MovesMoney() bool        { return k != KindLoss }

type Status string

const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

func ParseStatus(val string) (Status, error) {
	s := Status(val)
	switch s {
	case StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed:
		return s, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownStatus, val)
	}
}

func (s Status) IsTerminal() bool {
	return s == StatusProcessed || s == StatusRejected || s == StatusFailed
}

type Direction string

const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

func ParseDirection(val string) (Direction, error) {
	d := Direction(val)
	switch d {
	case DirectionDebit, DirectionCredit:
		return d, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownDirection, val)
	}
}

func ValidateTransition(from, to Status) error {
	if from == to {
		return fmt.Errorf("%w: self-transition from %s to %s is illegal", ErrInvalidTransition, from, to)
	}
	if from.IsTerminal() {
		return fmt.Errorf("%w: cannot transition out of terminal state %s", ErrInvalidTransition, from)
	}

	switch from {
	case StatusPending:
		switch to {
		case StatusProcessed, StatusRejected, StatusFailed, StatusPendingReference:
			return nil
		}
	case StatusPendingReference:
		switch to {
		case StatusProcessed, StatusRejected, StatusFailed:
			return nil
		}
	}

	return fmt.Errorf("%w: invalid transition from %s to %s", ErrInvalidTransition, from, to)
}

func (k Kind) ValidateExternal(amount money.Money, referenceExtTxID string) error {
	if k == KindOpening {
		return fmt.Errorf("%w: OPENING kind is forbidden at external boundaries", ErrInvalidKindRule)
	}
	if !amount.IsValid() {
		return fmt.Errorf("%w: kind %s requires an initialized amount", ErrInvalidKindRule, k)
	}

	switch k {
	case KindBet, KindWin:
		if !amount.IsPositive() {
			return fmt.Errorf("%w: kind %s requires a positive amount", ErrInvalidKindRule, k)
		}
	case KindRefund, KindRollback:
		if !amount.IsPositive() {
			return fmt.Errorf("%w: kind %s requires a positive amount", ErrInvalidKindRule, k)
		}
		if strings.TrimSpace(referenceExtTxID) == "" {
			return fmt.Errorf("%w: kind %s requires a reference external transaction ID", ErrInvalidKindRule, k)
		}
	case KindLoss:
		if !amount.IsZero() {
			return fmt.Errorf("%w: kind LOSS requires an amount equal to zero", ErrInvalidKindRule)
		}
	default:
		return fmt.Errorf("%w: unknown or unhandled kind %q", ErrInvalidKindRule, string(k))
	}

	return nil
}
