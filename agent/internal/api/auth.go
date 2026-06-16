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
// When OIDC is enabled, the token is decoded and the roles claim is extracted.
// IMPORTANT: in production the signature MUST be verified against the OIDC provider's
// JWK endpoint. The current implementation decodes the payload only — suitable for
// single-cluster dev/staging with a trusted network. Add `coreos/go-oidc` for full
// cryptographic verification (see TODO below).
type authMiddleware struct {
	enabled    bool
	issuerURL  string
	clientID   string
	rolesClaim string
	log        *slog.Logger
}

// newAuthMiddleware constructs the auth middleware from config.
func newAuthMiddleware(_ context.Context, cfg config.APIConfig, log *slog.Logger) *authMiddleware {
	if !cfg.OIDCEnabled {
		log.Warn("api: OIDC auth DISABLED — all API requests are granted viewer access (dev mode)",
			"hint", "set API_OIDC_ENABLED=true and configure API_OIDC_ISSUER_URL in production")
		return &authMiddleware{enabled: false, log: log}
	}
	if cfg.OIDCIssuerURL == "" {
		log.Error("api: API_OIDC_ENABLED=true but API_OIDC_ISSUER_URL is empty — auth DISABLED (fail-open risk)")
	}
	return &authMiddleware{
		enabled:    cfg.OIDCEnabled,
		issuerURL:  cfg.OIDCIssuerURL,
		clientID:   cfg.OIDCClientID,
		rolesClaim: cfg.OIDCRolesClaimKey,
		log:        log,
	}
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

		role, err := a.extractRole(raw)
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

// extractRole decodes the JWT payload and extracts the roles claim.
//
// TODO: replace the base64 decode with a cryptographic signature check using
//       github.com/coreos/go-oidc/v3/oidc when the OIDC provider is configured.
//       The current implementation trusts the token payload without verifying the
//       signature — only safe on a private, trusted network. Wire:
//         provider, _ := oidc.NewProvider(ctx, issuerURL)
//         verifier := provider.Verifier(&oidc.Config{ClientID: clientID})
//         idToken, err := verifier.Verify(ctx, raw)
//         idToken.Claims(&claims)
func (a *authMiddleware) extractRole(raw string) (Role, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("not a JWT (expected 3 parts)")
	}
	// Pad the base64url-encoded payload.
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

	raw2, ok := claims[a.rolesClaim]
	if !ok {
		return RoleViewer, nil // absent claim → minimum role
	}
	switch v := raw2.(type) {
	case []any:
		roles := make([]string, 0, len(v))
		for _, r := range v {
			if s, ok := r.(string); ok {
				roles = append(roles, s)
			}
		}
		return highestRole(roles), nil
	case string:
		return highestRole([]string{v}), nil
	default:
		return RoleViewer, nil
	}
}

// callerRole returns the role attached to the request context (set by enforce()).
func callerRole(r *http.Request) Role {
	if v, ok := r.Context().Value(ctxRoleKey).(Role); ok {
		return v
	}
	return RoleViewer
}
