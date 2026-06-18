package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/uid"
)

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/reveal — admin-only, fully audited.
//
// Every other endpoint in this file redacts secrets in its GET response
// (booleans like has_api_key, never the value itself). This is the ONLY path
// that ever returns a saved secret's plaintext value, satisfying the
// Settings page's "Show/Hide" requirement without weakening the default
// response shape everywhere else.
// ---------------------------------------------------------------------------

type revealRequest struct {
	Category string `json:"category"` // "llm" | "notifications" | "gitops" | "loki"
	Field    string `json:"field"`
}

type revealResponse struct {
	Value string `json:"value"`
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
	var req revealRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}

	if s.settings == nil {
		return &apiError{"persistence unavailable", http.StatusServiceUnavailable}
	}

	value, found, err := s.revealValue(r.Context(), req.Category, req.Field)
	if err != nil {
		s.log.Warn("api: reveal lookup failed", "error", err, "category", req.Category, "field", req.Field)
		return &apiError{"failed to load settings", http.StatusInternalServerError}
	}
	if !found {
		return &apiError{"no value saved for this category/field", http.StatusNotFound}
	}

	approver := callerRole(r)
	s.record(r.Context(), uid.New(), "integrations", audit.Stage("SecretRevealed"), "ok", map[string]string{
		"category": req.Category,
		"field":    req.Field,
		"operator": string(approver),
		"source":   "web-api",
	})

	return jsonOK(w, revealResponse{Value: value})
}

// revealValue looks up one specific secret field. Never logs the value itself.
func (s *Server) revealValue(ctx context.Context, category, field string) (string, bool, error) {
	switch category {
	case "llm":
		saved, ok, err := s.settings.LoadLLMSettings(ctx)
		if err != nil || !ok {
			return "", false, err
		}
		if field == "api_key" {
			return saved.APIKey, saved.APIKey != "", nil
		}
	case "notifications":
		saved, ok, err := s.settings.LoadNotifierSettings(ctx)
		if err != nil || !ok {
			return "", false, err
		}
		switch field {
		case "slack_bot_token":
			return saved.SlackBotToken, saved.SlackBotToken != "", nil
		case "slack_signing_secret":
			return saved.SlackSigningSecret, saved.SlackSigningSecret != "", nil
		case "pagerduty_routing_key":
			return saved.PagerDutyRoutingKey, saved.PagerDutyRoutingKey != "", nil
		}
	case "gitops":
		saved, ok, err := s.settings.LoadGitOpsSettings(ctx)
		if err != nil || !ok {
			return "", false, err
		}
		if field == "auth_token" {
			return saved.AuthToken, saved.AuthToken != "", nil
		}
	case "loki":
		saved, ok, err := s.settings.LoadLokiSettings(ctx)
		if err != nil || !ok {
			return "", false, err
		}
		if field == "auth_header" {
			return saved.AuthHeader, saved.AuthHeader != "", nil
		}
	}
	return "", false, nil
}
