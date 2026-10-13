package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/transaction"
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

func GetTransactionHandler(svc *service.Service, log *slog.Logger) http.HandlerFunc {
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

func GetTransactionByProviderHandler(svc *service.Service, log *slog.Logger) http.HandlerFunc {
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
