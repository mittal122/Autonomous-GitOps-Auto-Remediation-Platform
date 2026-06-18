package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/uid"
)

// NotifierReloader is the subset of *notifier.CompositeNotifier the API needs
// to apply Slack/PagerDuty credentials live, without a process restart.
type NotifierReloader interface {
	Reload(cfg config.NotifierConfig)
}

// ---------------------------------------------------------------------------
// GET /api/v1/integrations/notifications
// ---------------------------------------------------------------------------

type notificationsIntegrationResponse struct {
	Configured             bool   `json:"configured"`
	SlackChannelID         string `json:"slack_channel_id,omitempty"`
	HasSlackBotToken       bool   `json:"has_slack_bot_token"`
	HasSlackSigningSecret  bool   `json:"has_slack_signing_secret"`
	HasPagerDutyRoutingKey bool   `json:"has_pagerduty_routing_key"`
}

func (s *Server) handleGetNotificationsIntegration(w http.ResponseWriter, r *http.Request) error {
	var resp notificationsIntegrationResponse
	if s.settings != nil {
		if saved, ok, err := s.settings.LoadNotifierSettings(r.Context()); err == nil && ok {
			resp.Configured = saved.SlackBotToken != "" || saved.PagerDutyRoutingKey != ""
			resp.SlackChannelID = saved.SlackChannelID
			resp.HasSlackBotToken = saved.SlackBotToken != ""
			resp.HasSlackSigningSecret = saved.SlackSigningSecret != ""
			resp.HasPagerDutyRoutingKey = saved.PagerDutyRoutingKey != ""
		}
	}
	return jsonOK(w, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/notifications
// ---------------------------------------------------------------------------

// notificationsRequest mirrors NotifierSettings but with pointer fields for the
// secrets, so omitted JSON keys mean "keep the existing saved value" rather than
// "clear it".
type notificationsRequest struct {
	SlackBotToken       *string `json:"slack_bot_token"`
	SlackSigningSecret  *string `json:"slack_signing_secret"`
	SlackChannelID      string  `json:"slack_channel_id"`
	PagerDutyRoutingKey *string `json:"pagerduty_routing_key"`
}

func (s *Server) handleSaveNotificationsIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req notificationsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}

	if s.settings == nil {
		return &apiError{"persistence unavailable; cannot save settings", http.StatusServiceUnavailable}
	}

	existing, _, _ := s.settings.LoadNotifierSettings(r.Context())

	resolve := func(req *string, existing string) string {
		if req != nil {
			return *req
		}
		return existing
	}

	saved := settings.NotifierSettings{
		SlackBotToken:       resolve(req.SlackBotToken, existing.SlackBotToken),
		SlackSigningSecret:  resolve(req.SlackSigningSecret, existing.SlackSigningSecret),
		SlackChannelID:      req.SlackChannelID,
		PagerDutyRoutingKey: resolve(req.PagerDutyRoutingKey, existing.PagerDutyRoutingKey),
	}
	if err := s.settings.SaveNotifierSettings(r.Context(), saved); err != nil {
		s.log.Warn("api: save notifier settings failed", "error", err)
		return &apiError{"failed to save settings", http.StatusInternalServerError}
	}

	if s.notifReload != nil {
		s.notifReload.Reload(config.NotifierConfig{
			SlackBotToken:       saved.SlackBotToken,
			SlackSigningSecret:  saved.SlackSigningSecret,
			SlackChannelID:      saved.SlackChannelID,
			PagerDutyRoutingKey: saved.PagerDutyRoutingKey,
		})
	}

	s.record(r.Context(), uid.New(), "integrations", audit.Stage("IntegrationConfigured"), "notifications-saved", map[string]string{
		"slack_channel_id": saved.SlackChannelID,
		"source":           "web-api",
	})

	return jsonOK(w, map[string]any{"saved": true})
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/notifications/test
// ---------------------------------------------------------------------------

type notificationsTestRequest struct {
	Channel             string `json:"channel"` // "slack" | "pagerduty"
	SlackBotToken       string `json:"slack_bot_token"`
	PagerDutyRoutingKey string `json:"pagerduty_routing_key"`
}

type notificationsTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Server) handleTestNotificationsIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req notificationsTestRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}

	switch req.Channel {
	case "slack":
		return jsonOK(w, testSlackAuth(r.Context(), req.SlackBotToken))
	case "pagerduty":
		return jsonOK(w, validatePagerDutyKeyFormat(req.PagerDutyRoutingKey))
	default:
		return &apiError{"channel must be 'slack' or 'pagerduty'", http.StatusBadRequest}
	}
}

// testSlackAuth calls Slack's official auth.test endpoint — read-only, no side
// effects, the documented way to verify a bot token actually works.
func testSlackAuth(ctx context.Context, botToken string) notificationsTestResult {
	if botToken == "" {
		return notificationsTestResult{OK: false, Message: "slack_bot_token is required"}
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/auth.test", nil)
	if err != nil {
		return notificationsTestResult{OK: false, Message: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Authorization", "Bearer "+botToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return notificationsTestResult{OK: false, Message: fmt.Sprintf("cannot reach Slack: %v", err)}
	}
	defer resp.Body.Close()

	var result struct {
		OK    bool   `json:"ok"`
		Team  string `json:"team"`
		User  string `json:"user"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return notificationsTestResult{OK: false, Message: "could not parse Slack response"}
	}
	if !result.OK {
		return notificationsTestResult{OK: false, Message: "Slack rejected the token: " + result.Error}
	}
	return notificationsTestResult{OK: true, Message: fmt.Sprintf("Connected as %s in workspace %s", result.User, result.Team)}
}

// validatePagerDutyKeyFormat checks the routing key shape only. PagerDuty's API
// has no harmless connectivity ping — a real test would trigger a visible
// incident in the user's account, so this stays format-only.
func validatePagerDutyKeyFormat(key string) notificationsTestResult {
	key = strings.TrimSpace(key)
	if key == "" {
		return notificationsTestResult{OK: false, Message: "pagerduty_routing_key is required"}
	}
	if len(key) != 32 {
		return notificationsTestResult{OK: false, Message: fmt.Sprintf("expected a 32-character routing key, got %d characters", len(key))}
	}
	return notificationsTestResult{OK: true, Message: "Format looks valid (PagerDuty has no harmless connectivity test — this will be verified the first time it's used to escalate)"}
}
