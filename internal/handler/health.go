package handler

import (
	"net/http"

	"github.com/victorhslito/junglegaming/internal/service"
)

type HealthHandler struct {
	svc *service.HealthService
}

func NewHealthHandler(svc *service.HealthService) *HealthHandler {
	return &HealthHandler{svc: svc}
}

func (h *HealthHandler) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": string(service.HealthUp)})
}

func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	report := h.svc.Ready(r.Context())
	status := http.StatusOK
	if report.Status != service.HealthUp {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, report)
}
