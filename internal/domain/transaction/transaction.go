package transaction

import (
	"errors"
	"fmt"
	"time"

	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
)

var (
	ErrInvalidTransaction = errors.New("invalid transaction data or missing required fields")
	ErrInvalidAmount      = errors.New("invalid transaction amount or currency mismatch")
	ErrInvariant          = errors.New("transaction invariant violated")
	ErrInvalidFailureCode = errors.New("failure code is required for rejected or failed transactions")
)

const (
	OpeningProviderID  = "SYSTEM"
	OpeningPayloadHash = "internal-opening"
)

func OpeningIdempotencyKey(walletID string) string {
	return "opening:" + walletID
}

type Origin string

const (
	OriginInternal Origin = "INTERNAL"
	OriginExternal Origin = "EXTERNAL"
)

func ParseOrigin(val string) (Origin, error) {
	o := Origin(val)
	switch o {
	case OriginInternal, OriginExternal:
		return o, nil
	default:
		return "", fmt.Errorf("%w: unknown origin %q", ErrInvalidTransaction, val)
	}
}

type WagerTransaction struct {
	id                             string
	origin                         Origin
	providerID                     string
	externalTransactionID          string
	idempotencyKey                 string
	payloadHash                    string
	walletID                       string
	playerID                       string
	roundID                        string
	gameID                         string
	kind                           wager.Kind
	status                         wager.Status
	money                          money.Money
	referenceExternalTransactionID string
	referenceTransactionID         string
	failureCode                    string
	observedBalance                money.Money
	observedWalletVersion          int64
	attempts                       int32
	createdAt                      time.Time
	updatedAt                      time.Time
}

func NewExternal(
	id, providerID, externalTxID, idempotencyKey, payloadHash, walletID, playerID, roundID, gameID string,
	kind wager.Kind, amount money.Money, referenceExtTxID string, now time.Time,
) (WagerTransaction, error) {
	if id == "" || providerID == "" || externalTxID == "" || idempotencyKey == "" || payloadHash == "" || walletID == "" || playerID == "" {
		return WagerTransaction{}, fmt.Errorf("%w: missing mandatory fields in external transaction", ErrInvalidTransaction)
	}
	if roundID == "" || gameID == "" {
		return WagerTransaction{}, fmt.Errorf("%w: roundID and gameID are mandatory for external transactions", ErrInvalidTransaction)
	}
	if now.IsZero() {
		return WagerTransaction{}, fmt.Errorf("%w: timestamp 'now' cannot be zero", ErrInvalidTransaction)
	}
	if err := kind.ValidateExternal(amount, referenceExtTxID); err != nil {
		return WagerTransaction{}, err
	}

	return WagerTransaction{
		id:                             id,
		origin:                         OriginExternal,
		providerID:                     providerID,
		externalTransactionID:          externalTxID,
		idempotencyKey:                 idempotencyKey,
		payloadHash:                    payloadHash,
		walletID:                       walletID,
		playerID:                       playerID,
		roundID:                        roundID,
		gameID:                         gameID,
		kind:                           kind,
		status:                         wager.StatusPending,
		money:                          amount,
		referenceExternalTransactionID: referenceExtTxID,
		attempts:                       0,
		createdAt:                      now,
		updatedAt:                      now,
	}, nil
}

func NewOpening(id, walletID, playerID string, amount money.Money, now time.Time) (WagerTransaction, error) {
	if id == "" || walletID == "" || playerID == "" {
		return WagerTransaction{}, fmt.Errorf("%w: missing mandatory fields in opening transaction", ErrInvalidTransaction)
	}
	if now.IsZero() {
		return WagerTransaction{}, fmt.Errorf("%w: timestamp 'now' cannot be zero", ErrInvalidTransaction)
	}
	if !amount.IsValid() || !amount.IsPositive() {
		return WagerTransaction{}, fmt.Errorf("%w: opening requires a valid positive amount", ErrInvalidAmount)
	}

	return WagerTransaction{
		id:                    id,
		origin:                OriginInternal,
		providerID:            OpeningProviderID,
		externalTransactionID: id,
		idempotencyKey:        OpeningIdempotencyKey(walletID),
		payloadHash:           OpeningPayloadHash,
		walletID:              walletID,
		playerID:              playerID,
		kind:                  wager.KindOpening,
		status:                wager.StatusProcessed,
		money:                 amount,
		observedBalance:       amount,
		observedWalletVersion: 1,
		attempts:              0,
		createdAt:             now,
		updatedAt:             now,
	}, nil
}

func Rehydrate(
	id string, origin Origin, providerID, externalTxID, idempotencyKey, payloadHash, walletID, playerID, roundID, gameID string,
	kind wager.Kind, status wager.Status, amount money.Money, referenceExtTxID, referenceTxID, failureCode string,
	observedBalance money.Money, observedWalletVersion int64, attempts int32, createdAt, updatedAt time.Time,
) (WagerTransaction, error) {
	if id == "" || walletID == "" || playerID == "" || providerID == "" || externalTxID == "" || idempotencyKey == "" {
		return WagerTransaction{}, fmt.Errorf("%w: missing mandatory fields during rehydration", ErrInvalidTransaction)
	}
	if attempts < 0 || createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return WagerTransaction{}, fmt.Errorf("%w: invalid timestamps or attempts count", ErrInvalidTransaction)
	}
	if _, err := ParseOrigin(string(origin)); err != nil {
		return WagerTransaction{}, err
	}
	if _, err := wager.ParseKind(string(kind)); err != nil {
		return WagerTransaction{}, err
	}
	if _, err := wager.ParseStatus(string(status)); err != nil {
		return WagerTransaction{}, err
	}

	if (kind == wager.KindOpening) != (origin == OriginInternal) {
		return WagerTransaction{}, fmt.Errorf("%w: origin and kind must agree (OPENING ⇔ INTERNAL)", ErrInvariant)
	}

	if origin == OriginExternal {
		if err := kind.ValidateExternal(amount, referenceExtTxID); err != nil {
			return WagerTransaction{}, err
		}
	} else {
		if !amount.IsValid() || !amount.IsPositive() {
			return WagerTransaction{}, fmt.Errorf("%w: internal (OPENING) requires a valid positive amount", ErrInvalidAmount)
		}
	}

	if !kind.RequiresReference() && referenceExtTxID != "" {
		return WagerTransaction{}, fmt.Errorf("%w: %s must not carry a referenceExternalTransactionId", ErrInvariant, kind)
	}

	if status == wager.StatusRejected || status == wager.StatusFailed {
		if failureCode == "" {
			return WagerTransaction{}, fmt.Errorf("%w: failureCode is required for terminal error status", ErrInvalidFailureCode)
		}
	}

	if status == wager.StatusProcessed && kind.MovesMoney() {
		if !observedBalance.IsValid() || observedWalletVersion < 1 {
			return WagerTransaction{}, fmt.Errorf("%w: processed money-moving transaction lacks a valid observed snapshot", ErrInvariant)
		}
	}

	return WagerTransaction{
		id:                             id,
		origin:                         origin,
		providerID:                     providerID,
		externalTransactionID:          externalTxID,
		idempotencyKey:                 idempotencyKey,
		payloadHash:                    payloadHash,
		walletID:                       walletID,
		playerID:                       playerID,
		roundID:                        roundID,
		gameID:                         gameID,
		kind:                           kind,
		status:                         status,
		money:                          amount,
		referenceExternalTransactionID: referenceExtTxID,
		referenceTransactionID:         referenceTxID,
		failureCode:                    failureCode,
		observedBalance:                observedBalance,
		observedWalletVersion:          observedWalletVersion,
		attempts:                       attempts,
		createdAt:                      createdAt,
		updatedAt:                      updatedAt,
	}, nil
}

func (wt *WagerTransaction) checkMutable(now time.Time) error {
	if wt.status.IsTerminal() {
		return fmt.Errorf("%w: cannot mutate terminal transaction %s", wager.ErrInvalidTransition, wt.id)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: timestamp 'now' cannot be zero", ErrInvalidTransaction)
	}
	if now.Before(wt.createdAt) {
		return fmt.Errorf("%w: 'now' cannot be before createdAt", ErrInvalidTransaction)
	}
	return nil
}

func (wt *WagerTransaction) MarkProcessed(observedBalance money.Money, observedWalletVersion int64, now time.Time) error {
	if err := wt.checkMutable(now); err != nil {
		return err
	}
	if err := wager.ValidateTransition(wt.status, wager.StatusProcessed); err != nil {
		return err
	}
	if wt.kind.MovesMoney() {
		if !observedBalance.IsValid() || observedWalletVersion < 1 {
			return fmt.Errorf("%w: observed balance and version are required for money-moving operations", ErrInvariant)
		}
	}

	wt.status = wager.StatusProcessed
	wt.observedBalance = observedBalance
	wt.observedWalletVersion = observedWalletVersion
	wt.updatedAt = now
	return nil
}

func (wt *WagerTransaction) MarkRejected(failureCode string, now time.Time) error {
	if err := wt.checkMutable(now); err != nil {
		return err
	}
	if err := wager.ValidateTransition(wt.status, wager.StatusRejected); err != nil {
		return err
	}
	if failureCode == "" {
		return ErrInvalidFailureCode
	}

	wt.status = wager.StatusRejected
	wt.failureCode = failureCode
	wt.updatedAt = now
	return nil
}

func (wt *WagerTransaction) MarkFailed(failureCode string, now time.Time) error {
	if err := wt.checkMutable(now); err != nil {
		return err
	}
	if err := wager.ValidateTransition(wt.status, wager.StatusFailed); err != nil {
		return err
	}
	if failureCode == "" {
		return ErrInvalidFailureCode
	}

	wt.status = wager.StatusFailed
	wt.failureCode = failureCode
	wt.updatedAt = now
	return nil
}

func (wt *WagerTransaction) MarkPendingReference(now time.Time) error {
	if err := wt.checkMutable(now); err != nil {
		return err
	}
	if err := wager.ValidateTransition(wt.status, wager.StatusPendingReference); err != nil {
		return err
	}

	wt.status = wager.StatusPendingReference
	wt.updatedAt = now
	return nil
}

func (wt *WagerTransaction) BindReference(referenceTransactionID string, now time.Time) error {
	if err := wt.checkMutable(now); err != nil {
		return err
	}
	if referenceTransactionID == "" {
		return fmt.Errorf("%w: reference transaction ID cannot be empty", ErrInvalidTransaction)
	}

	wt.referenceTransactionID = referenceTransactionID
	wt.updatedAt = now
	return nil
}

func (wt *WagerTransaction) IncrementAttempts(now time.Time) error {
	if err := wt.checkMutable(now); err != nil {
		return err
	}
	wt.attempts++
	wt.updatedAt = now
	return nil
}

func (wt WagerTransaction) ID() string                             { return wt.id }
func (wt WagerTransaction) Origin() Origin                         { return wt.origin }
func (wt WagerTransaction) ProviderID() string                     { return wt.providerID }
func (wt WagerTransaction) ExternalTransactionID() string          { return wt.externalTransactionID }
func (wt WagerTransaction) IdempotencyKey() string                 { return wt.idempotencyKey }
func (wt WagerTransaction) PayloadHash() string                    { return wt.payloadHash }
func (wt WagerTransaction) WalletID() string                       { return wt.walletID }
func (wt WagerTransaction) PlayerID() string                       { return wt.playerID }
func (wt WagerTransaction) RoundID() string                        { return wt.roundID }
func (wt WagerTransaction) GameID() string                         { return wt.gameID }
func (wt WagerTransaction) Kind() wager.Kind                       { return wt.kind }
func (wt WagerTransaction) Status() wager.Status                   { return wt.status }
func (wt WagerTransaction) Money() money.Money                     { return wt.money }
func (wt WagerTransaction) ReferenceExternalTransactionID() string { return wt.referenceExternalTransactionID }
func (wt WagerTransaction) ReferenceTransactionID() string         { return wt.referenceTransactionID }
func (wt WagerTransaction) FailureCode() string                    { return wt.failureCode }
func (wt WagerTransaction) ObservedBalance() money.Money           { return wt.observedBalance }
func (wt WagerTransaction) ObservedWalletVersion() int64           { return wt.observedWalletVersion }
func (wt WagerTransaction) Attempts() int32                        { return wt.attempts }
func (wt WagerTransaction) CreatedAt() time.Time                   { return wt.createdAt }
func (wt WagerTransaction) UpdatedAt() time.Time                   { return wt.updatedAt }
