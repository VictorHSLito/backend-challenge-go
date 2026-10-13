package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/service"
)

func NewMux(pool *pgxpool.Pool, svc *service.Service, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", HealthLiveHandler)
	mux.HandleFunc("GET /health/ready", HealthReadyHandler(pool))

	mux.HandleFunc("POST /wallets", CreateWalletHandler(svc, logger))
	mux.HandleFunc("GET /wallets/{walletId}", GetWalletHandler(svc, logger))

	mux.HandleFunc("GET /wagering/transactions/{transactionId}", GetTransactionHandler(svc, logger))
	mux.HandleFunc("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}", GetTransactionByProviderHandler(svc, logger))

	mux.HandleFunc("POST /wagering/transactions", CreateTransactionHandler(svc, logger))
	return mux
}
