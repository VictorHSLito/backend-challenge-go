package auth

import "context"

// RoleInternal is the default realm role name used to flag the trusted
// internal service account. Overridable via OIDC_INTERNAL_ROLE.
const RoleInternal = "internal"

// Principal is the authenticated caller for a single HTTP request.
// It is built by the validator from the JWT and carried through the
// request context — handlers (and, if ever needed, services) read it
// with FromContext.
type Principal struct {
	// Subject is the JWT `sub` claim — for client_credentials tokens
	// it is the id of the service-account user behind the client.
	Subject string

	// ClientID is the Keycloak client that was granted the token
	// (claim `azp`, fallback `client_id`). For providers this is also
	// their ProviderID.
	ClientID string

	// Roles are the realm-level roles granted to this principal
	// (JWT `realm_access.roles`).
	Roles []string

	// IsInternal is true when Roles contains the configured internal role.
	// Precomputed so handlers do a single boolean check.
	IsInternal bool

	// ProviderID equals ClientID for provider principals, empty for internal
	// principals. This is the single value handlers compare against the
	// providerId they find in request bodies, path parameters or DB rows.
	ProviderID string
}

type ctxKey struct{}

// WithPrincipal returns a new context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the Principal previously stored by WithPrincipal.
// The boolean is false when the context has no principal (e.g. a public
// endpoint, or a bug where auth middleware was forgotten).
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
