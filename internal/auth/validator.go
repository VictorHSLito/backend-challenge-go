package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Config is the slice of settings the validator needs. The httpapi/auth
// package consumes it; config.Config maps env vars onto it inside module.go.
type Config struct {
	IssuerURL    string
	Audience     string
	InternalRole string
}

func (c Config) withDefaults() Config {
	if c.InternalRole == "" {
		c.InternalRole = RoleInternal
	}
	return c
}

// Validator verifies bearer JWTs against the configured OIDC issuer and
// turns them into Principals. It is safe for concurrent use after
// construction (go-oidc's Verifier is thread-safe and caches JWKS).
type Validator struct {
	verifier *oidc.IDTokenVerifier
	cfg      Config
}

// NewValidator contacts the IdP's discovery document and prepares a
// token verifier. It must only be called once, at process start — if
// the IdP is unreachable we fail-fast and the Fx lifecycle aborts
// start-up, which is what the challenge spec expects.
func NewValidator(ctx context.Context, cfg Config) (*Validator, error) {
	cfg = cfg.withDefaults()
	if cfg.IssuerURL == "" {
		return nil, fmt.Errorf("auth: OIDC issuer URL is required")
	}

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc discovery failed for issuer %q: %w", cfg.IssuerURL, err)
	}

	oidcCfg := &oidc.Config{
		ClientID:          cfg.Audience,
		SkipClientIDCheck: cfg.Audience == "",
	}
	return &Validator{verifier: provider.Verifier(oidcCfg), cfg: cfg}, nil
}

// keycloakClaims holds the subset of claims we care about. Keycloak emits
// many more; we deliberately decode only what authorization needs.
type keycloakClaims struct {
	Subject     string `json:"sub"`
	AZP         string `json:"azp"`
	ClientID    string `json:"client_id"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// ValidateBearer checks signature, issuer, audience, exp/nbf and returns
// the Principal. Any validation failure is returned as an error so the
// middleware can map it to a 401.
func (v *Validator) ValidateBearer(ctx context.Context, raw string) (Principal, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, fmt.Errorf("token rejected: %w", err)
	}

	var c keycloakClaims
	if err := token.Claims(&c); err != nil {
		return Principal{}, fmt.Errorf("token claims unreadable: %w", err)
	}

	clientID := c.AZP
	if clientID == "" {
		clientID = c.ClientID
	}

	isInternal := containsRole(c.RealmAccess.Roles, v.cfg.InternalRole)

	p := Principal{
		Subject:    c.Subject,
		ClientID:   clientID,
		Roles:      c.RealmAccess.Roles,
		IsInternal: isInternal,
	}
	// Internal callers deliberately carry no ProviderID — handlers use that
	// to tell "wallet-op" paths apart from "provider submits a transaction".
	if !isInternal {
		p.ProviderID = clientID
	}
	return p, nil
}

func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}
