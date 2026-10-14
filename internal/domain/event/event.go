// Package event defines the outbound integration events the service emits.
//
// Events are a *domain* concern: their shape is part of our public contract
// with downstream consumers, so they belong next to the other aggregates
// (money, wager, wallet, …) and must not depend on any infrastructure
// package. The outbox adapter marshals them to JSON and stores the bytes;
// the publisher reads those bytes and ships them to the broker. Neither
// side needs to interpret the payload.
package event

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
)

// Type is the stable string identifier a consumer uses to route an event.
type Type string

const (
	TypeWagerTransactionProcessed        Type = "WagerTransactionProcessed"
	TypeWagerTransactionRejected         Type = "WagerTransactionRejected"
	TypeWagerTransactionPendingReference Type = "WagerTransactionPendingReference"
	TypeWalletBalanceChanged             Type = "WalletBalanceChanged"
)

// Version is a monotonically increasing schema version per event Type.
// Bump when a field is added or removed in a breaking way; add a new
// Type when the shape changes radically.
const Version = 1

// Envelope is the on-the-wire shape. It is immutable once constructed —
// the fields are exported for JSON marshalling but we never mutate them
// after NewEnvelope returns.
type Envelope struct {
	EventID       string          `json:"eventId"`
	EventType     Type            `json:"eventType"`
	AggregateID   string          `json:"aggregateId"`
	CorrelationID string          `json:"correlationId"`
	CausationID   string          `json:"causationId,omitempty"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Version       int             `json:"version"`
	Data          json.RawMessage `json:"data"`
}

// NewEnvelope assembles the envelope around an already-marshalled payload.
// The caller usually uses one of the typed New…Event helpers below instead
// of calling this directly.
func NewEnvelope(eventType Type, aggregateID, correlationID, causationID string, now time.Time, data json.RawMessage) (Envelope, error) {
	if eventType == "" {
		return Envelope{}, fmt.Errorf("event: eventType is required")
	}
	if aggregateID == "" {
		return Envelope{}, fmt.Errorf("event: aggregateId is required")
	}
	if now.IsZero() {
		return Envelope{}, fmt.Errorf("event: occurredAt cannot be zero")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, fmt.Errorf("event: failed to generate id: %w", err)
	}
	if correlationID == "" {
		correlationID = id.String()
	}
	return Envelope{
		EventID:       id.String(),
		EventType:     eventType,
		AggregateID:   aggregateID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		OccurredAt:    now.UTC(),
		Version:       Version,
		Data:          data,
	}, nil
}

// --- Typed payloads ---

type WagerTransactionProcessed struct {
	TransactionID         string      `json:"transactionId"`
	ProviderID            string      `json:"providerId"`
	ExternalTransactionID string      `json:"externalTransactionId"`
	PlayerID              string      `json:"playerId"`
	WalletID              string      `json:"walletId"`
	RoundID               string      `json:"roundId,omitempty"`
	GameID                string      `json:"gameId,omitempty"`
	Kind                  wager.Kind  `json:"kind"`
	Money                 money.Money `json:"money"`
}

type WagerTransactionRejected struct {
	TransactionID         string      `json:"transactionId"`
	ProviderID            string      `json:"providerId"`
	ExternalTransactionID string      `json:"externalTransactionId"`
	PlayerID              string      `json:"playerId"`
	WalletID              string      `json:"walletId"`
	RoundID               string      `json:"roundId,omitempty"`
	GameID                string      `json:"gameId,omitempty"`
	Kind                  wager.Kind  `json:"kind"`
	Money                 money.Money `json:"money"`
	FailureCode           string      `json:"failureCode"`
}

type WagerTransactionPendingReference struct {
	TransactionID                  string      `json:"transactionId"`
	ProviderID                     string      `json:"providerId"`
	ExternalTransactionID          string      `json:"externalTransactionId"`
	PlayerID                       string      `json:"playerId"`
	WalletID                       string      `json:"walletId"`
	RoundID                        string      `json:"roundId,omitempty"`
	GameID                         string      `json:"gameId,omitempty"`
	Kind                           wager.Kind  `json:"kind"`
	Money                          money.Money `json:"money"`
	ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId"`
}

type WalletBalanceChanged struct {
	WalletID      string           `json:"walletId"`
	TransactionID string           `json:"transactionId"`
	Direction     wager.Direction  `json:"direction"`
	Money         money.Money      `json:"money"`
	BalanceBefore money.Money      `json:"balanceBefore"`
	BalanceAfter  money.Money      `json:"balanceAfter"`
	WalletVersion int64            `json:"walletVersion"`
}

// --- Typed constructors ---

// Each helper marshals a typed payload and wraps it in an Envelope so the
// service layer never touches json.RawMessage directly.

func NewProcessedEvent(correlationID, causationID string, now time.Time, p WagerTransactionProcessed) (Envelope, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return Envelope{}, err
	}
	return NewEnvelope(TypeWagerTransactionProcessed, p.TransactionID, correlationID, causationID, now, raw)
}

func NewRejectedEvent(correlationID, causationID string, now time.Time, p WagerTransactionRejected) (Envelope, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return Envelope{}, err
	}
	return NewEnvelope(TypeWagerTransactionRejected, p.TransactionID, correlationID, causationID, now, raw)
}

func NewPendingReferenceEvent(correlationID, causationID string, now time.Time, p WagerTransactionPendingReference) (Envelope, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return Envelope{}, err
	}
	return NewEnvelope(TypeWagerTransactionPendingReference, p.TransactionID, correlationID, causationID, now, raw)
}

func NewBalanceChangedEvent(correlationID, causationID string, now time.Time, p WalletBalanceChanged) (Envelope, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return Envelope{}, err
	}
	return NewEnvelope(TypeWalletBalanceChanged, p.WalletID, correlationID, causationID, now, raw)
}
