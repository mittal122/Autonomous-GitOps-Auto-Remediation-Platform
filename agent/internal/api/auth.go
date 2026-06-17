package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/autosre/agent/internal/config"
	"github.com/coreos/go-oidc/v3/oidc"
)

// Role is the user's authorisation level.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

type contextKey int

const ctxRoleKey contextKey = iota

// roleLevel returns a numeric rank; higher = more privileged.
func roleLevel(r Role) int {
	switch r {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// highestRole returns the most privileged role in the list.
func highestRole(roles []string) Role {
	best := RoleViewer
	for _, r := range roles {
		candidate := Role(r)
		if roleLevel(candidate) > roleLevel(best) {
			best = candidate
		}
	}
	return best
}

// authMiddleware validates Bearer tokens and attaches the caller's role to the context.
//
// When OIDC is disabled (dev mode) every request is treated as viewer — the API is
// accessible without credentials. This is logged loudly so no one runs it in production
// without noticing.
//
// When OIDC is enabled with a valid issuerURL, tokens are cryptographically verified
// against the OIDC provider's JWK endpoint using github.com/coreos/go-oidc/v3.
//
// When OIDC is enabled but issuerURL is empty (unsafe fallback for staging/tests),
// only the base64-decoded payload is read — the signature is NOT checked.
type authMiddleware struct {
	enabled    bool
	issuerURL  string
	clientID   string
	rolesClaim string
	verifier   *oidc.IDTokenVerifier // non-nil when OIDC provider is reachable
	log        *slog.Logger
}

// newAuthMiddleware constructs the auth middleware from config.
// When OIDCEnabled=true and OIDCIssuerURL is non-empty, it attempts to connect to
// the OIDC provider to fetch the JWK set. A connection failure is logged but does
// not prevent startup — the middleware falls back to the unsafe base64 decode path
// and logs a prominent warning on every request.
func newAuthMiddleware(ctx context.Context, cfg config.APIConfig, log *slog.Logger) *authMiddleware {
	if !cfg.OIDCEnabled {
		log.Warn("api: OIDC auth DISABLED — all API requests are granted viewer access (dev mode)",
			"hint", "set API_OIDC_ENABLED=true and configure API_OIDC_ISSUER_URL in production")
		return &authMiddleware{enabled: false, log: log}
	}

	m := &authMiddleware{
		enabled:    true,
		issuerURL:  cfg.OIDCIssuerURL,
		clientID:   cfg.OIDCClientID,
		rolesClaim: cfg.OIDCRolesClaimKey,
		log:        log,
	}

	if cfg.OIDCIssuerURL == "" {
		log.Error("api: API_OIDC_ENABLED=true but API_OIDC_ISSUER_URL is empty — " +
			"JWT signatures NOT verified (unsafe base64 decode mode)")
		return m
	}

	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		log.Error("api: OIDC provider unreachable — JWT signatures NOT verified (unsafe base64 fallback)",
			"issuer", cfg.OIDCIssuerURL, "error", err)
		return m
	}

	m.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})
	log.Info("api: OIDC signature verification enabled", "issuer", cfg.OIDCIssuerURL)
	return m
}

// enforce wraps next, requiring at least minRole from the caller.
// Unauthenticated → 401. Insufficient role → 403.
func (a *authMiddleware) enforce(next http.Handler, minRole Role) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			// Dev mode: grant viewer to everyone, but still enforce the endpoint's minRole.
			// Kill switch and other admin endpoints must remain protected even without OIDC.
			ctx := context.WithValue(r.Context(), ctxRoleKey, RoleViewer)
			if roleLevel(RoleViewer) < roleLevel(minRole) {
				jsonError(w,
					fmt.Sprintf("insufficient permissions: %s required, got viewer (dev mode — enable OIDC to authenticate as %s)", minRole, minRole),
					http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		raw := extractBearer(r)
		if raw == "" {
			jsonError(w, "authentication required", http.StatusUnauthorized)
			return
		}

		role, err := a.extractRole(r.Context(), raw)
		if err != nil {
			a.log.Warn("api: token extraction failed", "error", err, "remote", r.RemoteAddr)
			jsonError(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		if roleLevel(role) < roleLevel(minRole) {
			jsonError(w, fmt.Sprintf("insufficient permissions: %s required, got %s", minRole, role),
				http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), ctxRoleKey, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractBearer extracts the token from "Authorization: Bearer <token>".
func extractBearer(r *http.Request) string {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(hdr, "Bearer ")
}

// extractRole validates the raw JWT and extracts the roles claim.
//
// When a.verifier is set (OIDC provider reachable at startup), the token signature
// is cryptographically verified using the provider's JWK set.
//
// When a.verifier is nil (no issuerURL or provider unreachable), the payload is
// base64-decoded without signature verification — safe only on a private, trusted
// network (dev/staging). A warning is logged on every call in this mode.
func (a *authMiddleware) extractRole(ctx context.Context, raw string) (Role, error) {
	if a.verifier != nil {
		return a.extractRoleOIDC(ctx, raw)
	}
	if a.issuerURL != "" {
		a.log.Warn("api: JWT signature NOT verified — OIDC provider was unavailable at startup")
	}
	return a.extractRoleUnsafe(raw)
}

// extractRoleOIDC verifies the JWT signature via the OIDC provider and extracts roles.
func (a *authMiddleware) extractRoleOIDC(ctx context.Context, raw string) (Role, error) {
	idToken, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("OIDC token verification: %w", err)
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return "", fmt.Errorf("extract claims: %w", err)
	}
	return rolesFromClaims(claims, a.rolesClaim), nil
}

// extractRoleUnsafe decodes the JWT payload without verifying the signature.
// Only safe on a private, trusted network.
func (a *authMiddleware) extractRoleUnsafe(raw string) (Role, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("not a JWT (expected 3 parts)")
	}
	payload := parts[1]
	if r := len(payload) % 4; r != 0 {
		payload += strings.Repeat("=", 4-r)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("JWT payload decode: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return "", fmt.Errorf("JWT claims unmarshal: %w", err)
	}
	return rolesFromClaims(claims, a.rolesClaim), nil
}

// rolesFromClaims extracts the highest role from the given claims map.
func rolesFromClaims(claims map[string]any, claimKey string) Role {
	raw, ok := claims[claimKey]
	if !ok {
		return RoleViewer
	}
	switch v := raw.(type) {
	case []any:
		roles := make([]string, 0, len(v))
		for _, r := range v {
			if s, ok := r.(string); ok {
				roles = append(roles, s)
			}
		}
		return highestRole(roles)
	case string:
		return highestRole([]string{v})
	default:
		return RoleViewer
	}
}

// callerRole returns the role attached to the request context (set by enforce()).
func callerRole(r *http.Request) Role {
	if v, ok := r.Context().Value(ctxRoleKey).(Role); ok {
		return v
	}
	return RoleViewer
}
