package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
	"github.com/victorhslito/junglegaming/internal/domain/wager"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
	"github.com/victorhslito/junglegaming/internal/service"
)

type transactionResponse struct {
	ID                             string       `json:"id"`
	Origin                         string       `json:"origin"`
	ProviderID                     string       `json:"providerId"`
	ExternalTransactionID          string       `json:"externalTransactionId"`
	WalletID                       string       `json:"walletId"`
	PlayerID                       string       `json:"playerId"`
	RoundID                        string       `json:"roundId,omitempty"`
	GameID                         string       `json:"gameId,omitempty"`
	Kind                           string       `json:"kind"`
	Status                         string       `json:"status"`
	Money                          money.Money  `json:"money"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId,omitempty"`
	ReferenceTransactionID         string       `json:"referenceTransactionId,omitempty"`
	FailureCode                    string       `json:"failureCode,omitempty"`
	Balance                        *money.Money `json:"balance,omitempty"`
	WalletVersion                  int64        `json:"walletVersion,omitempty"`
	CreatedAt                      time.Time    `json:"createdAt"`
	UpdatedAt                      time.Time    `json:"updatedAt"`
}

type processTransactionRequest struct {
	ProviderID                     string      `json:"providerId"`
	ExternalTransactionID          string      `json:"externalTransactionId"`
	PlayerID                       string      `json:"playerId"`
	WalletID                       string      `json:"walletId"`
	RoundID                        string      `json:"roundId"`
	GameID                         string      `json:"gameId"`
	Kind                           string      `json:"kind"`
	Money                          money.Money `json:"money"`
	ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId"`
}

type processTransactionResponse struct {
	TransactionID    string      `json:"transactionId"`
	Status           string      `json:"status"`
	Balance          money.Money `json:"balance"`
	FailureCode      string      `json:"failureCode,omitempty"`
	IdempotentReplay bool        `json:"idempotentReplay"`
}

func toTransactionResponse(wt transaction.WagerTransaction) transactionResponse {
	var balancePtr *money.Money
	obsBal := wt.ObservedBalance()
	if obsBal.IsValid() {
		b := obsBal
		balancePtr = &b
	}

	var version int64
	if wt.ObservedWalletVersion() >= 1 {
		version = wt.ObservedWalletVersion()
	} else {
		balancePtr = nil
	}

	return transactionResponse{
		ID:                             wt.ID(),
		Origin:                         string(wt.Origin()),
		ProviderID:                     wt.ProviderID(),
		ExternalTransactionID:          wt.ExternalTransactionID(),
		WalletID:                       wt.WalletID(),
		PlayerID:                       wt.PlayerID(),
		RoundID:                        wt.RoundID(),
		GameID:                         wt.GameID(),
		Kind:                           string(wt.Kind()),
		Status:                         string(wt.Status()),
		Money:                          wt.Money(),
		ReferenceExternalTransactionID: wt.ReferenceExternalTransactionID(),
		ReferenceTransactionID:         wt.ReferenceTransactionID(),
		FailureCode:                    wt.FailureCode(),
		Balance:                        balancePtr,
		WalletVersion:                  version,
		CreatedAt:                      wt.CreatedAt(),
		UpdatedAt:                      wt.UpdatedAt(),
	}
}

func GetTransactionHandler(svc *service.TransactionService, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txID := r.PathValue("transactionId")
		if _, err := uuid.Parse(txID); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_TRANSACTION_ID", "provided transactionId is not a valid UUID")
			return
		}

		wt, err := svc.GetTransaction(r.Context(), txID)
		if err != nil {
			if errors.Is(err, service.ErrTransactionNotFound) {
				writeError(w, http.StatusNotFound, "TRANSACTION_NOT_FOUND", "transaction not found")
				return
			}
			log.Error("failed to fetch transaction", "transaction_id", txID, "error", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(toTransactionResponse(wt))
	}
}

func GetTransactionByProviderHandler(svc *service.TransactionService, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providerID := r.PathValue("providerId")
		externalTxID := r.PathValue("externalTransactionId")

		if providerID == "" || externalTxID == "" {
			writeError(w, http.StatusBadRequest, "MISSING_FIELD", "providerId and externalTxId are required")
			return
		}

		wt, err := svc.GetTransactionByProvider(r.Context(), providerID, externalTxID)
		if err != nil {
			if errors.Is(err, service.ErrTransactionNotFound) {
				writeError(w, http.StatusNotFound, "TRANSACTION_NOT_FOUND", "transaction not found")
				return
			}
			log.Error("failed to fetch transaction by provider", "provider_id", providerID, "external_tx_id", externalTxID, "error", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(toTransactionResponse(wt))
	}
}

func CreateTransactionHandler(svc *service.TransactionService, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idemKey := r.Header.Get("Idempotency-Key")
		if strings.TrimSpace(idemKey) == "" {
			writeError(w, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", "header 'Idempotency-Key' is required")
			return
		}

		var req processTransactionRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "corrupt or invalid json body")
			return
		}

		if strings.TrimSpace(req.ProviderID) == "" || strings.TrimSpace(req.ExternalTransactionID) == "" {
			writeError(w, http.StatusBadRequest, "MISSING_FIELD", "providerId and externalTransactionId are required")
			return
		}

		if _, err := uuid.Parse(req.WalletID); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_WALLET_ID", "walletId must be a valid UUID")
			return
		}
		if _, err := uuid.Parse(req.PlayerID); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_PLAYER_ID", "playerId must be a valid UUID")
			return
		}

		kind, err := wager.ParseKind(req.Kind)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_KIND", "unknown transaction kind")
			return
		}

		cmd := service.ProcessTransactionCommand{
			ProviderID:                     req.ProviderID,
			ExternalTransactionID:          req.ExternalTransactionID,
			PlayerID:                       req.PlayerID,
			WalletID:                       req.WalletID,
			RoundID:                        req.RoundID,
			GameID:                         req.GameID,
			Kind:                           kind,
			Money:                          req.Money,
			ReferenceExternalTransactionID: req.ReferenceExternalTransactionID,
		}

		res, err := svc.ProcessTransaction(r.Context(), idemKey, cmd)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrWalletNotFound):
				writeError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet not found")
				return
			case errors.Is(err, service.ErrIdempotencyConflict):
				writeError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "payload mismatch for idempotency key")
				return
			case errors.Is(err, service.ErrUnsupportedKind):
				writeError(w, http.StatusBadRequest, "UNSUPPORTED_KIND", "only BET is supported")
				return
			case errors.Is(err, money.ErrCurrencyMismatch):
				writeError(w, http.StatusBadRequest, "CURRENCY_MISMATCH", "transaction currency does not match wallet currency")
				return
			case errors.Is(err, money.ErrInvalidMoney),
				errors.Is(err, wallet.ErrInvalidWallet),
				errors.Is(err, wallet.ErrInvalidAmount),
				errors.Is(err, wager.ErrInvalidKindRule):
				writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
				return
			default:
				logger.Error("failed to process transaction", "error", err)
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
				return
			}
		}

		statusCode := http.StatusCreated
		switch {
		case res.IdempotentReplay:
			statusCode = http.StatusOK
		case res.Status == wager.StatusPendingReference:
			statusCode = http.StatusAccepted
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(processTransactionResponse{
			TransactionID:    res.TransactionID,
			Status:           string(res.Status),
			Balance:          res.Balance,
			FailureCode:      res.FailureCode,
			IdempotentReplay: res.IdempotentReplay,
		})
	}
}
