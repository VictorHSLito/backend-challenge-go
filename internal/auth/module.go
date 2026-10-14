package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/victorhslito/junglegaming/internal/config"
	"go.uber.org/fx"
)

var Module = fx.Module("auth",
	fx.Provide(newValidator),
)

// newValidator is called once at startup by Fx. Discovery happens here, so
// if Keycloak is unreachable the entire process fails to start — which is
// the expected behaviour for a service that treats auth as mandatory.
func newValidator(cfg config.Config, logger *slog.Logger) (*Validator, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("initializing OIDC validator",
		"issuer", cfg.OIDCIssuerURL,
		"discovery_url", cfg.OIDCDiscoveryURL,
		"audience", cfg.OIDCAudience)
	v, err := NewValidator(ctx, Config{
		IssuerURL:    cfg.OIDCIssuerURL,
		DiscoveryURL: cfg.OIDCDiscoveryURL,
		Audience:     cfg.OIDCAudience,
		InternalRole: cfg.OIDCInternalRole,
	})
	if err != nil {
		return nil, err
	}
	logger.Info("OIDC validator ready")
	return v, nil
}
