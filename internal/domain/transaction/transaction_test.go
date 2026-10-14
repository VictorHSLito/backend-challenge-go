package transaction

import (
	"errors"
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

type externalArgs struct {
	id, providerID, externalTxID, idempotencyKey, payloadHash string
	walletID, playerID, roundID, gameID                       string
	kind                                                      wager.Kind
	amount                                                    money.Money
	referenceExtTxID                                          string
	now                                                       time.Time
}

func (a externalArgs) call() (WagerTransaction, error) {
	return NewExternal(a.id, a.providerID, a.externalTxID, a.idempotencyKey, a.payloadHash,
		a.walletID, a.playerID, a.roundID, a.gameID, a.kind, a.amount, a.referenceExtTxID, a.now)
}

func validExternal(t *testing.T) externalArgs {
	return externalArgs{
		id:             "tx-1",
		providerID:     "prov-a",
		externalTxID:   "ext-1",
		idempotencyKey: "prov-a:ext-1",
		payloadHash:    "hash-1",
		walletID:       "w-1",
		playerID:       "p-1",
		roundID:        "r-1",
		gameID:         "g-1",
		kind:           wager.KindBet,
		amount:         mustParse(t, "10.00", "BRL"),
		now:            fixedNow(),
	}
}

func mustExternalBet(t *testing.T) *WagerTransaction {
	t.Helper()
	tx, err := validExternal(t).call()
	if err != nil {
		t.Fatalf("validExternal.call(): %v", err)
	}
	return &tx
}

type rehydrateArgs struct {
	id, providerID, externalTxID, idempotencyKey, payloadHash string
	walletID, playerID, roundID, gameID                       string
	origin                                                    Origin
	kind                                                      wager.Kind
	status                                                    wager.Status
	amount                                                    money.Money
	referenceExtTxID, referenceTxID, failureCode              string
	observedBalance                                           money.Money
	observedWalletVersion                                     int64
	attempts                                                  int32
	nextAttemptAt                                             time.Time
	createdAt, updatedAt                                      time.Time
}

func (a rehydrateArgs) call() (WagerTransaction, error) {
	return Rehydrate(a.id, a.origin, a.providerID, a.externalTxID, a.idempotencyKey, a.payloadHash,
		a.walletID, a.playerID, a.roundID, a.gameID, a.kind, a.status, a.amount,
		a.referenceExtTxID, a.referenceTxID, a.failureCode, a.observedBalance, a.observedWalletVersion,
		a.attempts, a.nextAttemptAt, a.createdAt, a.updatedAt)
}

func validPendingRehydrate(t *testing.T) rehydrateArgs {
	now := fixedNow()
	return rehydrateArgs{
		id: "tx-1", providerID: "prov-a", externalTxID: "ext-1",
		idempotencyKey: "prov-a:ext-1", payloadHash: "hash-1",
		walletID: "w-1", playerID: "p-1", roundID: "r-1", gameID: "g-1",
		origin: OriginExternal, kind: wager.KindBet, status: wager.StatusPending,
		amount:    mustParse(t, "10.00", "BRL"),
		createdAt: now, updatedAt: now,
	}
}

func TestNewExternalHappyPathBet(t *testing.T) {
	tx, err := validExternal(t).call()
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	if tx.Status() != wager.StatusPending {
		t.Errorf("status = %q; want PENDING", tx.Status())
	}
	if tx.Origin() != OriginExternal {
		t.Errorf("origin = %q; want EXTERNAL", tx.Origin())
	}
	if tx.Kind() != wager.KindBet {
		t.Errorf("kind = %q; want BET", tx.Kind())
	}
	if tx.ID() != "tx-1" || tx.ProviderID() != "prov-a" || tx.ExternalTransactionID() != "ext-1" {
		t.Errorf("identity mismatch")
	}
	if tx.ReferenceExternalTransactionID() != "" || tx.ReferenceTransactionID() != "" {
		t.Errorf("expected empty references")
	}
	if tx.Attempts() != 0 {
		t.Errorf("attempts = %d; want 0", tx.Attempts())
	}
	if !tx.CreatedAt().Equal(tx.UpdatedAt()) {
		t.Errorf("createdAt and updatedAt should match at construction")
	}
}

func TestNewExternalHappyPathReversals(t *testing.T) {
	for _, k := range []wager.Kind{wager.KindRefund, wager.KindRollback} {
		a := validExternal(t)
		a.kind = k
		a.referenceExtTxID = "ref-ext-1"
		tx, err := a.call()
		if err != nil {
			t.Fatalf("NewExternal(%s): %v", k, err)
		}
		if tx.ReferenceExternalTransactionID() != "ref-ext-1" {
			t.Errorf("%s: referenceExtTxID = %q; want ref-ext-1", k, tx.ReferenceExternalTransactionID())
		}
	}
}

func TestNewExternalHappyPathLoss(t *testing.T) {
	a := validExternal(t)
	a.kind = wager.KindLoss
	a.amount = mustParse(t, "0.00", "BRL")
	tx, err := a.call()
	if err != nil {
		t.Fatalf("NewExternal(LOSS): %v", err)
	}
	if !tx.Money().IsZero() {
		t.Errorf("LOSS amount should be zero")
	}
}

func TestNewExternalRejects(t *testing.T) {
	zero := mustParse(t, "0.00", "BRL")
	usd := mustParse(t, "10.00", "USD")

	cases := []struct {
		name    string
		mod     func(a *externalArgs)
		wantErr error
	}{
		{"empty id", func(a *externalArgs) { a.id = "" }, ErrInvalidTransaction},
		{"empty providerID", func(a *externalArgs) { a.providerID = "" }, ErrInvalidTransaction},
		{"empty externalTxID", func(a *externalArgs) { a.externalTxID = "" }, ErrInvalidTransaction},
		{"empty idempotencyKey", func(a *externalArgs) { a.idempotencyKey = "" }, ErrInvalidTransaction},
		{"empty payloadHash", func(a *externalArgs) { a.payloadHash = "" }, ErrInvalidTransaction},
		{"empty walletID", func(a *externalArgs) { a.walletID = "" }, ErrInvalidTransaction},
		{"empty playerID", func(a *externalArgs) { a.playerID = "" }, ErrInvalidTransaction},
		{"empty roundID", func(a *externalArgs) { a.roundID = "" }, ErrInvalidTransaction},
		{"empty gameID", func(a *externalArgs) { a.gameID = "" }, ErrInvalidTransaction},
		{"zero now", func(a *externalArgs) { a.now = time.Time{} }, ErrInvalidTransaction},
		{"opening kind", func(a *externalArgs) { a.kind = wager.KindOpening }, wager.ErrInvalidKindRule},
		{"bet with zero amount", func(a *externalArgs) { a.amount = zero }, wager.ErrInvalidKindRule},
		{"win with zero amount", func(a *externalArgs) { a.kind = wager.KindWin; a.amount = zero }, wager.ErrInvalidKindRule},
		{"refund missing reference", func(a *externalArgs) { a.kind = wager.KindRefund }, wager.ErrInvalidKindRule},
		{"rollback missing reference", func(a *externalArgs) { a.kind = wager.KindRollback }, wager.ErrInvalidKindRule},
		{"loss with positive amount", func(a *externalArgs) { a.kind = wager.KindLoss }, wager.ErrInvalidKindRule},
		{"loss with usd amount", func(a *externalArgs) { a.kind = wager.KindLoss; a.amount = usd }, wager.ErrInvalidKindRule},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := validExternal(t)
			tc.mod(&a)
			_, err := a.call()
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestNewOpeningHappyPath(t *testing.T) {
	amt := mustParse(t, "1000.00", "BRL")
	now := fixedNow()
	tx, err := NewOpening("tx-open-1", "w-1", "p-1", amt, now)
	if err != nil {
		t.Fatalf("NewOpening: %v", err)
	}

	if tx.Origin() != OriginInternal {
		t.Errorf("origin = %q; want INTERNAL", tx.Origin())
	}
	if tx.Kind() != wager.KindOpening {
		t.Errorf("kind = %q; want OPENING", tx.Kind())
	}
	if tx.Status() != wager.StatusProcessed {
		t.Errorf("status = %q; want PROCESSED", tx.Status())
	}
	if tx.ProviderID() != OpeningProviderID {
		t.Errorf("providerID = %q; want %q", tx.ProviderID(), OpeningProviderID)
	}
	if tx.ExternalTransactionID() != "tx-open-1" {
		t.Errorf("externalTxID = %q; want tx-open-1", tx.ExternalTransactionID())
	}
	if tx.IdempotencyKey() != OpeningIdempotencyKey("w-1") {
		t.Errorf("idempotencyKey = %q; want %q", tx.IdempotencyKey(), OpeningIdempotencyKey("w-1"))
	}
	if tx.PayloadHash() != OpeningPayloadHash {
		t.Errorf("payloadHash = %q; want %q", tx.PayloadHash(), OpeningPayloadHash)
	}
	if tx.RoundID() != "" || tx.GameID() != "" {
		t.Errorf("roundID/gameID should be empty for opening")
	}
	if tx.FailureCode() != "" || tx.ReferenceExternalTransactionID() != "" || tx.ReferenceTransactionID() != "" {
		t.Errorf("opening should have no failureCode/reference")
	}
	if !tx.ObservedBalance().Equal(amt) {
		t.Errorf("observedBalance = %+v; want %+v", tx.ObservedBalance(), amt)
	}
	if tx.ObservedWalletVersion() != 1 {
		t.Errorf("observedWalletVersion = %d; want 1", tx.ObservedWalletVersion())
	}
	if tx.Attempts() != 0 {
		t.Errorf("attempts = %d; want 0", tx.Attempts())
	}
}

func TestNewOpeningRejects(t *testing.T) {
	validAmt := mustParse(t, "100.00", "BRL")
	zero := mustParse(t, "0.00", "BRL")
	neg := mustNew(t, -100, "BRL")
	var empty money.Money
	now := fixedNow()

	cases := []struct {
		name     string
		id, w, p string
		amt      money.Money
		now      time.Time
		wantErr  error
	}{
		{"empty id", "", "w", "p", validAmt, now, ErrInvalidTransaction},
		{"empty walletID", "id", "", "p", validAmt, now, ErrInvalidTransaction},
		{"empty playerID", "id", "w", "", validAmt, now, ErrInvalidTransaction},
		{"uninit amount", "id", "w", "p", empty, now, ErrInvalidAmount},
		{"zero amount", "id", "w", "p", zero, now, ErrInvalidAmount},
		{"negative amount", "id", "w", "p", neg, now, ErrInvalidAmount},
		{"zero now", "id", "w", "p", validAmt, time.Time{}, ErrInvalidTransaction},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewOpening(tc.id, tc.w, tc.p, tc.amt, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRehydrateHappyPathBetPending(t *testing.T) {
	tx, err := validPendingRehydrate(t).call()
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if tx.Status() != wager.StatusPending {
		t.Errorf("status = %q; want PENDING", tx.Status())
	}
}

func TestRehydrateHappyPathBetProcessed(t *testing.T) {
	a := validPendingRehydrate(t)
	a.status = wager.StatusProcessed
	a.observedBalance = mustParse(t, "90.00", "BRL")
	a.observedWalletVersion = 2
	tx, err := a.call()
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if tx.Status() != wager.StatusProcessed {
		t.Errorf("status = %q; want PROCESSED", tx.Status())
	}
	if tx.ObservedWalletVersion() != 2 {
		t.Errorf("observedWalletVersion = %d; want 2", tx.ObservedWalletVersion())
	}
}

func TestRehydrateHappyPathRefundProcessed(t *testing.T) {
	a := validPendingRehydrate(t)
	a.kind = wager.KindRefund
	a.referenceExtTxID = "ref-ext-1"
	a.status = wager.StatusProcessed
	a.observedBalance = mustParse(t, "110.00", "BRL")
	a.observedWalletVersion = 3
	tx, err := a.call()
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if tx.ReferenceExternalTransactionID() != "ref-ext-1" {
		t.Errorf("referenceExtTxID = %q; want ref-ext-1", tx.ReferenceExternalTransactionID())
	}
}

func TestRehydrateHappyPathLossProcessedNoObserved(t *testing.T) {
	a := validPendingRehydrate(t)
	a.kind = wager.KindLoss
	a.amount = mustParse(t, "0.00", "BRL")
	a.status = wager.StatusProcessed
	tx, err := a.call()
	if err != nil {
		t.Fatalf("Rehydrate LOSS PROCESSED: %v", err)
	}
	if tx.ObservedWalletVersion() != 0 {
		t.Errorf("observedWalletVersion = %d; want 0 for LOSS", tx.ObservedWalletVersion())
	}
}

func TestRehydrateHappyPathBetRejected(t *testing.T) {
	a := validPendingRehydrate(t)
	a.status = wager.StatusRejected
	a.failureCode = "INSUFFICIENT_FUNDS"
	tx, err := a.call()
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if tx.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Errorf("failureCode = %q; want INSUFFICIENT_FUNDS", tx.FailureCode())
	}
}

func TestRehydrateHappyPathOpeningProcessed(t *testing.T) {
	now := fixedNow()
	amt := mustParse(t, "100.00", "BRL")
	a := rehydrateArgs{
		id:                    "open-1",
		origin:                OriginInternal,
		providerID:            OpeningProviderID,
		externalTxID:          "open-1",
		idempotencyKey:        OpeningIdempotencyKey("w-1"),
		payloadHash:           OpeningPayloadHash,
		walletID:              "w-1",
		playerID:              "p-1",
		kind:                  wager.KindOpening,
		status:                wager.StatusProcessed,
		amount:                amt,
		observedBalance:       amt,
		observedWalletVersion: 1,
		createdAt:             now,
		updatedAt:             now,
	}
	tx, err := a.call()
	if err != nil {
		t.Fatalf("Rehydrate OPENING: %v", err)
	}
	if tx.Kind() != wager.KindOpening || tx.Origin() != OriginInternal {
		t.Errorf("origin/kind mismatch for opening rehydrate")
	}
}

func TestRehydrateRejects(t *testing.T) {
	zero := mustParse(t, "0.00", "BRL")
	var empty money.Money

	cases := []struct {
		name    string
		mod     func(a *rehydrateArgs)
		wantErr error
	}{
		{"empty id", func(a *rehydrateArgs) { a.id = "" }, ErrInvalidTransaction},
		{"empty providerID", func(a *rehydrateArgs) { a.providerID = "" }, ErrInvalidTransaction},
		{"empty externalTxID", func(a *rehydrateArgs) { a.externalTxID = "" }, ErrInvalidTransaction},
		{"empty idempotencyKey", func(a *rehydrateArgs) { a.idempotencyKey = "" }, ErrInvalidTransaction},
		{"empty walletID", func(a *rehydrateArgs) { a.walletID = "" }, ErrInvalidTransaction},
		{"empty playerID", func(a *rehydrateArgs) { a.playerID = "" }, ErrInvalidTransaction},
		{"negative attempts", func(a *rehydrateArgs) { a.attempts = -1 }, ErrInvalidTransaction},
		{"zero createdAt", func(a *rehydrateArgs) { a.createdAt = time.Time{} }, ErrInvalidTransaction},
		{"zero updatedAt", func(a *rehydrateArgs) { a.updatedAt = time.Time{} }, ErrInvalidTransaction},
		{"updatedAt before createdAt", func(a *rehydrateArgs) { a.updatedAt = a.createdAt.Add(-time.Minute) }, ErrInvalidTransaction},
		{"invalid origin", func(a *rehydrateArgs) { a.origin = Origin("WEIRD") }, ErrInvalidTransaction},
		{"invalid kind", func(a *rehydrateArgs) { a.kind = wager.Kind("WEIRD") }, wager.ErrUnknownKind},
		{"invalid status", func(a *rehydrateArgs) { a.status = wager.Status("WEIRD") }, wager.ErrUnknownStatus},
		{"opening with external origin", func(a *rehydrateArgs) { a.kind = wager.KindOpening }, ErrInvariant},
		{"bet with internal origin", func(a *rehydrateArgs) {
			a.origin = OriginInternal
			a.kind = wager.KindBet
		}, ErrInvariant},
		{"rejected without failureCode", func(a *rehydrateArgs) {
			a.status = wager.StatusRejected
		}, ErrInvalidFailureCode},
		{"failed without failureCode", func(a *rehydrateArgs) {
			a.status = wager.StatusFailed
		}, ErrInvalidFailureCode},
		{"processed money-moving without observed", func(a *rehydrateArgs) {
			a.status = wager.StatusProcessed
		}, ErrInvariant},
		{"processed money-moving with version=0", func(a *rehydrateArgs) {
			a.status = wager.StatusProcessed
			a.observedBalance = mustParse(t, "50.00", "BRL")
			a.observedWalletVersion = 0
		}, ErrInvariant},
		{"uninit amount for external", func(a *rehydrateArgs) { a.amount = empty }, wager.ErrInvalidKindRule},
		{"bet with zero amount", func(a *rehydrateArgs) { a.amount = zero }, wager.ErrInvalidKindRule},
		{"loss with positive amount", func(a *rehydrateArgs) {
			a.kind = wager.KindLoss
			a.amount = mustParse(t, "5.00", "BRL")
		}, wager.ErrInvalidKindRule},
		{"refund missing reference", func(a *rehydrateArgs) {
			a.kind = wager.KindRefund
			a.status = wager.StatusProcessed
			a.observedBalance = mustParse(t, "110.00", "BRL")
			a.observedWalletVersion = 2
		}, wager.ErrInvalidKindRule},
		{"bet with stray reference", func(a *rehydrateArgs) {
			a.referenceExtTxID = "stray-ref"
		}, ErrInvariant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := validPendingRehydrate(t)
			tc.mod(&a)
			_, err := a.call()
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestMarkProcessedHappyPath(t *testing.T) {
	tx := mustExternalBet(t)
	snap := mustParse(t, "90.00", "BRL")
	later := tx.CreatedAt().Add(time.Second)
	if err := tx.MarkProcessed(snap, 2, later); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if tx.Status() != wager.StatusProcessed {
		t.Errorf("status = %q; want PROCESSED", tx.Status())
	}
	if !tx.ObservedBalance().Equal(snap) {
		t.Errorf("observedBalance = %+v; want %+v", tx.ObservedBalance(), snap)
	}
	if tx.ObservedWalletVersion() != 2 {
		t.Errorf("observedWalletVersion = %d; want 2", tx.ObservedWalletVersion())
	}
	if !tx.UpdatedAt().Equal(later) {
		t.Errorf("updatedAt = %v; want %v", tx.UpdatedAt(), later)
	}
}

func TestMarkProcessedLossNoObservedRequired(t *testing.T) {
	a := validExternal(t)
	a.kind = wager.KindLoss
	a.amount = mustParse(t, "0.00", "BRL")
	tx, _ := a.call()
	later := tx.CreatedAt().Add(time.Second)

	var empty money.Money
	if err := tx.MarkProcessed(empty, 0, later); err != nil {
		t.Fatalf("MarkProcessed(LOSS): %v", err)
	}
	if tx.Status() != wager.StatusProcessed {
		t.Errorf("status = %q; want PROCESSED", tx.Status())
	}
}

func TestMarkProcessedRejections(t *testing.T) {
	validSnap := mustParse(t, "90.00", "BRL")
	var empty money.Money

	cases := []struct {
		name    string
		setup   func(t *testing.T) *WagerTransaction
		snap    money.Money
		version int64
		now     func(tx *WagerTransaction) time.Time
		wantErr error
	}{
		{
			name:  "zero now",
			setup: mustExternalBet, snap: validSnap, version: 2,
			now:     func(_ *WagerTransaction) time.Time { return time.Time{} },
			wantErr: ErrInvalidTransaction,
		},
		{
			name:  "now before createdAt",
			setup: mustExternalBet, snap: validSnap, version: 2,
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(-time.Second) },
			wantErr: ErrInvalidTransaction,
		},
		{
			name: "terminal receiver",
			setup: func(t *testing.T) *WagerTransaction {
				tx := mustExternalBet(t)
				if err := tx.MarkProcessed(validSnap, 2, tx.CreatedAt().Add(time.Second)); err != nil {
					t.Fatalf("priming: %v", err)
				}
				return tx
			},
			snap: validSnap, version: 2,
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Minute) },
			wantErr: wager.ErrInvalidTransition,
		},
		{
			name:  "money-moving without observed",
			setup: mustExternalBet, snap: empty, version: 0,
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Second) },
			wantErr: ErrInvariant,
		},
		{
			name:  "money-moving with version=0",
			setup: mustExternalBet, snap: validSnap, version: 0,
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Second) },
			wantErr: ErrInvariant,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := tc.setup(t)
			before := *tx
			err := tx.MarkProcessed(tc.snap, tc.version, tc.now(tx))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v; want %v", err, tc.wantErr)
			}
			if *tx != before {
				t.Errorf("state mutated on rejection")
			}
		})
	}
}

func TestMarkRejectedHappyPath(t *testing.T) {
	tx := mustExternalBet(t)
	later := tx.CreatedAt().Add(time.Second)
	if err := tx.MarkRejected("INSUFFICIENT_FUNDS", later); err != nil {
		t.Fatalf("MarkRejected: %v", err)
	}
	if tx.Status() != wager.StatusRejected {
		t.Errorf("status = %q; want REJECTED", tx.Status())
	}
	if tx.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Errorf("failureCode = %q; want INSUFFICIENT_FUNDS", tx.FailureCode())
	}
}

func TestMarkRejectedRejections(t *testing.T) {
	primed := func(t *testing.T) *WagerTransaction {
		tx := mustExternalBet(t)
		if err := tx.MarkRejected("FIRST", tx.CreatedAt().Add(time.Second)); err != nil {
			t.Fatalf("priming: %v", err)
		}
		return tx
	}

	cases := []struct {
		name    string
		setup   func(t *testing.T) *WagerTransaction
		code    string
		now     func(tx *WagerTransaction) time.Time
		wantErr error
	}{
		{
			name:  "zero now",
			setup: mustExternalBet, code: "CODE",
			now:     func(_ *WagerTransaction) time.Time { return time.Time{} },
			wantErr: ErrInvalidTransaction,
		},
		{
			name:  "terminal receiver",
			setup: primed, code: "CODE",
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Minute) },
			wantErr: wager.ErrInvalidTransition,
		},
		{
			name:  "terminal receiver with empty code",
			setup: primed, code: "",
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Minute) },
			wantErr: wager.ErrInvalidTransition,
		},
		{
			name:  "empty failure code on live txn",
			setup: mustExternalBet, code: "",
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Second) },
			wantErr: ErrInvalidFailureCode,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := tc.setup(t)
			before := *tx
			err := tx.MarkRejected(tc.code, tc.now(tx))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v; want %v", err, tc.wantErr)
			}
			if *tx != before {
				t.Errorf("state mutated on rejection")
			}
		})
	}
}

func TestMarkFailedHappyPath(t *testing.T) {
	tx := mustExternalBet(t)
	later := tx.CreatedAt().Add(time.Second)
	if err := tx.MarkFailed("INFRA_FAIL", later); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if tx.Status() != wager.StatusFailed {
		t.Errorf("status = %q; want FAILED", tx.Status())
	}
}

func TestMarkFailedEmptyCode(t *testing.T) {
	tx := mustExternalBet(t)
	before := *tx
	err := tx.MarkFailed("", tx.CreatedAt().Add(time.Second))
	if !errors.Is(err, ErrInvalidFailureCode) {
		t.Errorf("got %v; want ErrInvalidFailureCode", err)
	}
	if *tx != before {
		t.Errorf("state mutated on rejection")
	}
}

func TestMarkFailedTerminalBeforeEmptyCode(t *testing.T) {
	tx := mustExternalBet(t)
	if err := tx.MarkFailed("FIRST", tx.CreatedAt().Add(time.Second)); err != nil {
		t.Fatalf("priming: %v", err)
	}
	before := *tx
	err := tx.MarkFailed("", tx.CreatedAt().Add(time.Minute))
	if !errors.Is(err, wager.ErrInvalidTransition) {
		t.Errorf("got %v; want wager.ErrInvalidTransition", err)
	}
	if *tx != before {
		t.Errorf("state mutated on rejection")
	}
}

func TestMarkPendingReferenceHappyPath(t *testing.T) {
	tx := mustExternalBet(t)
	later := tx.CreatedAt().Add(time.Second)
	if err := tx.MarkPendingReference(later); err != nil {
		t.Fatalf("MarkPendingReference: %v", err)
	}
	if tx.Status() != wager.StatusPendingReference {
		t.Errorf("status = %q; want PENDING_REFERENCE", tx.Status())
	}
}

func TestMarkPendingReferenceSelfTransition(t *testing.T) {
	tx := mustExternalBet(t)
	if err := tx.MarkPendingReference(tx.CreatedAt().Add(time.Second)); err != nil {
		t.Fatalf("priming: %v", err)
	}
	before := *tx
	err := tx.MarkPendingReference(tx.CreatedAt().Add(time.Second * 2))
	if !errors.Is(err, wager.ErrInvalidTransition) {
		t.Errorf("got %v; want wager.ErrInvalidTransition", err)
	}
	if *tx != before {
		t.Errorf("state mutated on rejection")
	}
}

func TestBindReferenceHappyPath(t *testing.T) {
	tx := mustExternalBet(t)
	beforeStatus := tx.Status()
	later := tx.CreatedAt().Add(time.Second)
	if err := tx.BindReference("ref-tx-1", later); err != nil {
		t.Fatalf("BindReference: %v", err)
	}
	if tx.ReferenceTransactionID() != "ref-tx-1" {
		t.Errorf("referenceTransactionID = %q; want ref-tx-1", tx.ReferenceTransactionID())
	}
	if tx.Status() != beforeStatus {
		t.Errorf("status changed from %q to %q", beforeStatus, tx.Status())
	}
	if !tx.UpdatedAt().Equal(later) {
		t.Errorf("updatedAt = %v; want %v", tx.UpdatedAt(), later)
	}
}

func TestBindReferenceRejections(t *testing.T) {
	terminal := func(t *testing.T) *WagerTransaction {
		tx := mustExternalBet(t)
		snap := mustParse(t, "90.00", "BRL")
		if err := tx.MarkProcessed(snap, 2, tx.CreatedAt().Add(time.Second)); err != nil {
			t.Fatalf("priming: %v", err)
		}
		return tx
	}

	cases := []struct {
		name    string
		setup   func(t *testing.T) *WagerTransaction
		ref     string
		now     func(tx *WagerTransaction) time.Time
		wantErr error
	}{
		{
			name:  "empty reference",
			setup: mustExternalBet, ref: "",
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Second) },
			wantErr: ErrInvalidTransaction,
		},
		{
			name:  "terminal receiver",
			setup: terminal, ref: "ref-tx-1",
			now:     func(tx *WagerTransaction) time.Time { return tx.CreatedAt().Add(time.Minute) },
			wantErr: wager.ErrInvalidTransition,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := tc.setup(t)
			before := *tx
			err := tx.BindReference(tc.ref, tc.now(tx))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v; want %v", err, tc.wantErr)
			}
			if *tx != before {
				t.Errorf("state mutated on rejection")
			}
		})
	}
}

func TestIncrementAttemptsHappyPath(t *testing.T) {
	tx := mustExternalBet(t)
	later := tx.CreatedAt().Add(time.Second)
	if err := tx.IncrementAttempts(later); err != nil {
		t.Fatalf("IncrementAttempts: %v", err)
	}
	if tx.Attempts() != 1 {
		t.Errorf("attempts = %d; want 1", tx.Attempts())
	}
	if err := tx.IncrementAttempts(later.Add(time.Second)); err != nil {
		t.Fatalf("IncrementAttempts 2: %v", err)
	}
	if tx.Attempts() != 2 {
		t.Errorf("attempts = %d; want 2", tx.Attempts())
	}
}

func TestIncrementAttemptsTerminal(t *testing.T) {
	tx := mustExternalBet(t)
	snap := mustParse(t, "90.00", "BRL")
	if err := tx.MarkProcessed(snap, 2, tx.CreatedAt().Add(time.Second)); err != nil {
		t.Fatalf("priming: %v", err)
	}
	before := *tx
	err := tx.IncrementAttempts(tx.CreatedAt().Add(time.Minute))
	if !errors.Is(err, wager.ErrInvalidTransition) {
		t.Errorf("got %v; want wager.ErrInvalidTransition", err)
	}
	if *tx != before {
		t.Errorf("state mutated on rejection")
	}
}

func TestTerminalImmutability(t *testing.T) {
	build := func(t *testing.T) *WagerTransaction {
		tx := mustExternalBet(t)
		snap := mustParse(t, "90.00", "BRL")
		if err := tx.MarkProcessed(snap, 2, tx.CreatedAt().Add(time.Second)); err != nil {
			t.Fatalf("priming: %v", err)
		}
		return tx
	}

	t.Run("MarkProcessed", func(t *testing.T) {
		tx := build(t)
		before := *tx
		if err := tx.MarkProcessed(mustParse(t, "70.00", "BRL"), 3, tx.CreatedAt().Add(time.Minute)); !errors.Is(err, wager.ErrInvalidTransition) {
			t.Errorf("got %v; want wager.ErrInvalidTransition", err)
		}
		if *tx != before {
			t.Errorf("state mutated")
		}
	})
	t.Run("MarkRejected", func(t *testing.T) {
		tx := build(t)
		before := *tx
		if err := tx.MarkRejected("CODE", tx.CreatedAt().Add(time.Minute)); !errors.Is(err, wager.ErrInvalidTransition) {
			t.Errorf("got %v; want wager.ErrInvalidTransition", err)
		}
		if *tx != before {
			t.Errorf("state mutated")
		}
	})
	t.Run("MarkFailed", func(t *testing.T) {
		tx := build(t)
		before := *tx
		if err := tx.MarkFailed("CODE", tx.CreatedAt().Add(time.Minute)); !errors.Is(err, wager.ErrInvalidTransition) {
			t.Errorf("got %v; want wager.ErrInvalidTransition", err)
		}
		if *tx != before {
			t.Errorf("state mutated")
		}
	})
	t.Run("MarkPendingReference", func(t *testing.T) {
		tx := build(t)
		before := *tx
		if err := tx.MarkPendingReference(tx.CreatedAt().Add(time.Minute)); !errors.Is(err, wager.ErrInvalidTransition) {
			t.Errorf("got %v; want wager.ErrInvalidTransition", err)
		}
		if *tx != before {
			t.Errorf("state mutated")
		}
	})
	t.Run("BindReference", func(t *testing.T) {
		tx := build(t)
		before := *tx
		if err := tx.BindReference("ref", tx.CreatedAt().Add(time.Minute)); !errors.Is(err, wager.ErrInvalidTransition) {
			t.Errorf("got %v; want wager.ErrInvalidTransition", err)
		}
		if *tx != before {
			t.Errorf("state mutated")
		}
	})
	t.Run("IncrementAttempts", func(t *testing.T) {
		tx := build(t)
		before := *tx
		if err := tx.IncrementAttempts(tx.CreatedAt().Add(time.Minute)); !errors.Is(err, wager.ErrInvalidTransition) {
			t.Errorf("got %v; want wager.ErrInvalidTransition", err)
		}
		if *tx != before {
			t.Errorf("state mutated")
		}
	})
}
