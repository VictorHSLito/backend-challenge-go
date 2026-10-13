package httpapi

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewMux(pool *pgxpool.Pool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", HealthLiveHandler)
	mux.HandleFunc("GET /health/ready", HealthReadyHandler(pool))
	return mux
}
