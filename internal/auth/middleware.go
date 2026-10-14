package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// writeAuthError emits the same shape as httpapi.writeError so API consumers
// see one consistent error contract. Duplicated here to avoid importing the
// httpapi package (which would create a cycle: httpapi → auth → httpapi).
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="wager-api"`)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

// extractBearer parses `Authorization: Bearer <token>`. We purposefully do
// NOT accept the token anywhere else (query string, cookie, body) — only
// the header. That removes an entire class of CSRF-style attacks.
func extractBearer(h http.Header) (string, bool) {
	v := h.Get("Authorization")
	if v == "" {
		return "", false
	}
	const prefix = "Bearer "
	if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(v[len(prefix):]), true
}

// RequireAuthenticated validates the Bearer token and injects the Principal
// into the request context. Role-based decisions are left to the composed
// middlewares (RequireInternal / RequireProvider) or to the handler itself.
func RequireAuthenticated(v *Validator, log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := extractBearer(r.Header)
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing or malformed bearer token")
			return
		}
		p, err := v.ValidateBearer(r.Context(), raw)
		if err != nil {
			// Deliberately log at Debug: an unauthenticated caller is noise
			// in production, not an error worth paging on.
			log.Debug("rejected token", "error", err, "path", r.URL.Path)
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired token")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// RequireInternal narrows RequireAuthenticated to callers whose JWT carries
// the configured internal realm role. Use on wallet-level endpoints that
// providers must never reach (POST /wallets, GET /wallets/*, reconciliation,
// ledger).
func RequireInternal(v *Validator, log *slog.Logger, next http.Handler) http.Handler {
	return RequireAuthenticated(v, log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := FromContext(r.Context())
		if !p.IsInternal {
			writeAuthError(w, http.StatusForbidden, "FORBIDDEN", "internal access required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RequireProvider requires a non-empty ProviderID on the Principal. An
// internal token has no ProviderID and is therefore refused here — internal
// callers submit wallet operations, not external wagering transactions.
func RequireProvider(v *Validator, log *slog.Logger, next http.Handler) http.Handler {
	return RequireAuthenticated(v, log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := FromContext(r.Context())
		if p.ProviderID == "" {
			writeAuthError(w, http.StatusForbidden, "FORBIDDEN", "provider identity required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}
