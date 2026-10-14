package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorhslito/junglegaming/internal/auth"
	"github.com/victorhslito/junglegaming/internal/service"
)

func NewMux(
	pool *pgxpool.Pool,
	walletSvc *service.WalletService,
	txSvc *service.TransactionService,
	validator *auth.Validator,
	logger *slog.Logger,
) *http.ServeMux {
	mux := http.NewServeMux()

	// Public health probes — Kubernetes/LB style, no auth.
	mux.HandleFunc("GET /health/live", HealthLiveHandler)
	mux.HandleFunc("GET /health/ready", HealthReadyHandler(pool))

	// Wallet operations are internal-only (README §2):
	// "operações de carteira são restritas ao serviço interno".
	internal := func(h http.HandlerFunc) http.Handler {
		return auth.RequireInternal(validator, logger, h)
	}
	mux.Handle("POST /wallets", internal(CreateWalletHandler(walletSvc, logger)))
	mux.Handle("GET /wallets/{walletId}", internal(GetWalletHandler(walletSvc, logger)))
	mux.Handle("GET /wallets/{walletId}/ledger", internal(GetLedgerHandler(walletSvc, logger)))
	mux.Handle("POST /wallets/{walletId}/reconciliation", internal(ReconciliationHandler(walletSvc, logger)))

	// Transaction submission: only a provider token — never internal — may
	// submit an external wagering operation. The handler then enforces that
	// the body's providerId matches the token's provider.
	mux.Handle("POST /wagering/transactions",
		auth.RequireProvider(validator, logger, CreateTransactionHandler(txSvc, logger)))

	// Transaction reads: any authenticated caller may hit the endpoint,
	// but the handler itself blocks a provider from seeing another
	// provider's data (and lets internal see everything).
	authenticated := func(h http.HandlerFunc) http.Handler {
		return auth.RequireAuthenticated(validator, logger, h)
	}
	mux.Handle("GET /wagering/transactions/{transactionId}",
		authenticated(GetTransactionHandler(txSvc, logger)))
	mux.Handle("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}",
		authenticated(GetTransactionByProviderHandler(txSvc, logger)))

	return mux
}
