package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorhslito/junglegaming/internal/domain/ledger"
	"github.com/victorhslito/junglegaming/internal/domain/money"
	"github.com/victorhslito/junglegaming/internal/domain/wallet"
	"github.com/victorhslito/junglegaming/internal/service"
)

type openWalletRequest struct {
	PlayerID       string      `json:"playerId"`
	InitialBalance money.Money `json:"initialBalance"`
}

type walletResponse struct {
	ID       string      `json:"id"`
	PlayerID string      `json:"playerId"`
	Balance  money.Money `json:"balance"`
	Version  int64       `json:"version"`
}

func toWalletResponse(w wallet.Wallet) walletResponse {
	return walletResponse{
		ID:       w.ID(),
		PlayerID: w.PlayerID(),
		Balance:  w.Balance(),
		Version:  w.Version(),
	}
}

func CreateWalletHandler(svc *service.WalletService, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req openWalletRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()

		if err := decoder.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "corrupt or invalid json body")
			return
		}

		if strings.TrimSpace(req.PlayerID) == "" {
			writeError(w, http.StatusBadRequest, "MISSING_FIELD", "field 'playerId' is required")
			return
		}

		createdWallet, err := svc.OpenWallet(r.Context(), req.PlayerID, req.InitialBalance)
		if err != nil {
			switch {
			case errors.Is(err, money.ErrInvalidMoney),
				errors.Is(err, wallet.ErrInvalidWallet),
				errors.Is(err, wallet.ErrInvalidAmount):
				writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
				return
			case errors.Is(err, service.ErrWalletAlreadyExists):
				writeError(w, http.StatusConflict, "WALLET_ALREADY_EXISTS", "wallet already exists for this player and currency")
				return
			default:
				log.Error("failed to open wallet", "error", err)
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toWalletResponse(createdWallet))
	}
}

func GetWalletHandler(svc *service.WalletService, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		walletID := r.PathValue("walletId")

		if _, err := uuid.Parse(walletID); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_WALLET_ID", "provided walletId is not a valid UUID")
			return
		}

		foundWallet, err := svc.GetWallet(r.Context(), walletID)
		if err != nil {
			if errors.Is(err, service.ErrWalletNotFound) {
				writeError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet not found")
				return
			}

			log.Error("failed to fetch wallet", "wallet_id", walletID, "error", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(toWalletResponse(foundWallet))
	}
}

type ledgerEntryResponse struct {
	ID             string      `json:"id"`
	WalletID       string      `json:"walletId"`
	TransactionID  string      `json:"transactionId"`
	Direction      string      `json:"direction"`
	Amount         money.Money `json:"amount"`
	BalanceBefore  money.Money `json:"balanceBefore"`
	BalanceAfter   money.Money `json:"balanceAfter"`
	CreatedAt      time.Time   `json:"createdAt"`
}

type ledgerPageResponse struct {
	Entries    []ledgerEntryResponse `json:"entries"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

func toLedgerEntryResponse(e ledger.Entry) ledgerEntryResponse {
	return ledgerEntryResponse{
		ID:            e.ID(),
		WalletID:      e.WalletID(),
		TransactionID: e.TransactionID(),
		Direction:     string(e.Direction()),
		Amount:        e.Amount(),
		BalanceBefore: e.BalanceBefore(),
		BalanceAfter:  e.BalanceAfter(),
		CreatedAt:     e.CreatedAt(),
	}
}

func GetLedgerHandler(svc *service.WalletService, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		walletID := r.PathValue("walletId")
		if _, err := uuid.Parse(walletID); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_WALLET_ID", "provided walletId is not a valid UUID")
			return
		}

		cursor := r.URL.Query().Get("cursor")

		limit := 50
		if lim := r.URL.Query().Get("limit"); lim != "" {
			parsed, err := strconv.Atoi(lim)
			if err != nil || parsed <= 0 {
				writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be a positive integer")
				return
			}
			if parsed > 500 {
				parsed = 500
			}
			limit = parsed
		}

		page, err := svc.GetLedger(r.Context(), walletID, cursor, limit)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrWalletNotFound):
				writeError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet not found")
				return
			case errors.Is(err, service.ErrInvalidCursor):
				writeError(w, http.StatusBadRequest, "INVALID_CURSOR", "provided cursor is invalid")
				return
			default:
				log.Error("failed to list ledger", "wallet_id", walletID, "error", err)
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
				return
			}
		}

		resp := ledgerPageResponse{
			Entries:    make([]ledgerEntryResponse, 0, len(page.Entries)),
			NextCursor: page.NextCursor,
		}
		for _, e := range page.Entries {
			resp.Entries = append(resp.Entries, toLedgerEntryResponse(e))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type reconciliationResponse struct {
	WalletID          string      `json:"walletId"`
	StoredBalance     money.Money `json:"storedBalance"`
	CalculatedBalance money.Money `json:"calculatedBalance"`
	Difference        money.Money `json:"difference"`
	Consistent        bool        `json:"consistent"`
	CheckedEntries    int         `json:"checkedEntries"`
}

func ReconciliationHandler(svc *service.WalletService, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		walletID := r.PathValue("walletId")
		if _, err := uuid.Parse(walletID); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_WALLET_ID", "provided walletId is not a valid UUID")
			return
		}

		result, err := svc.Reconcile(r.Context(), walletID)
		if err != nil {
			if errors.Is(err, service.ErrWalletNotFound) {
				writeError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet not found")
				return
			}
			log.Error("failed to reconcile wallet", "wallet_id", walletID, "error", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(reconciliationResponse{
			WalletID:          result.WalletID,
			StoredBalance:     result.StoredBalance,
			CalculatedBalance: result.CalculatedBalance,
			Difference:        result.Difference,
			Consistent:        result.Consistent,
			CheckedEntries:    result.CheckedEntries,
		})
	}
}
