package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/service"
)

func NewMux(pool *pgxpool.Pool, walletSvc *service.WalletService, txSvc *service.TransactionService, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", HealthLiveHandler)
	mux.HandleFunc("GET /health/ready", HealthReadyHandler(pool))

	mux.HandleFunc("POST /wallets", CreateWalletHandler(walletSvc, logger))
	mux.HandleFunc("GET /wallets/{walletId}", GetWalletHandler(walletSvc, logger))
	mux.HandleFunc("GET /wallets/{walletId}/ledger", GetLedgerHandler(walletSvc, logger))
	mux.HandleFunc("POST /wallets/{walletId}/reconciliation", ReconciliationHandler(walletSvc, logger))

	mux.HandleFunc("GET /wagering/transactions/{transactionId}", GetTransactionHandler(txSvc, logger))
	mux.HandleFunc("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}", GetTransactionByProviderHandler(txSvc, logger))

	mux.HandleFunc("POST /wagering/transactions", CreateTransactionHandler(txSvc, logger))
	return mux
}
