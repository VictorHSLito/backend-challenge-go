package service

import (
	"errors"
)

var (
	ErrWalletNotFound      = errors.New("wallet not found")
	ErrWalletAlreadyExists = errors.New("wallet already exists for this player and currency")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrIdempotencyConflict = errors.New("idempotency key conflict: payload mismatch")
	ErrUnsupportedKind     = errors.New("unsupported transaction kind")
)
