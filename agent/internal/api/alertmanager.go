package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/uid"
)

// ---------------------------------------------------------------------------
// GET /api/v1/integrations/alertmanager
// ---------------------------------------------------------------------------

type alertmanagerIntegrationResponse struct {
	WebhookURL       string `json:"webhook_url"`
	YAMLSnippet      string `json:"yaml_snippet"`
	OperatorDetected bool   `json:"operator_detected"`
}

func (s *Server) handleGetAlertmanagerIntegration(w http.ResponseWriter, r *http.Request) error {
	webhookURL := alertmanagerWebhookURL(r)

	operatorDetected := false
	if s.k8s != nil {
		operatorDetected = s.k8s.OperatorDetected(r.Context())
	}

	return jsonOK(w, alertmanagerIntegrationResponse{
		WebhookURL:       webhookURL,
		YAMLSnippet:      alertmanagerYAMLSnippet(webhookURL),
		OperatorDetected: operatorDetected,
	})
}

// alertmanagerWebhookURL derives the externally reachable webhook URL. Set
// PUBLIC_BASE_URL when the agent is behind a proxy/load balancer that changes
// the host the agent itself sees.
func alertmanagerWebhookURL(r *http.Request) string {
	if base := os.Getenv("PUBLIC_BASE_URL"); base != "" {
		return strings.TrimRight(base, "/") + "/webhook/alertmanager"
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xfp := r.Header.Get("X-Forwarded-Proto"); xfp != "" {
		scheme = xfp
	}
	return fmt.Sprintf("%s://%s/webhook/alertmanager", scheme, r.Host)
}

func alertmanagerYAMLSnippet(webhookURL string) string {
	return fmt.Sprintf(`receivers:
- name: autosre
  webhook_configs:
  - url: %s
    send_resolved: true

route:
  receiver: autosre
`, webhookURL)
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/alertmanager/apply
// ---------------------------------------------------------------------------

func (s *Server) handleApplyAlertmanagerIntegration(w http.ResponseWriter, r *http.Request) error {
	if s.k8s == nil {
		return jsonOK(w, map[string]any{"applied": false, "reason": "Kubernetes API access unavailable"})
	}

	webhookURL := alertmanagerWebhookURL(r)
	applied, reason := s.k8s.ApplyAlertmanagerWebhook(r.Context(), webhookURL)

	s.record(r.Context(), uid.New(), "integrations", audit.Stage("IntegrationConfigured"), "alertmanager-apply", map[string]string{
		"applied": fmt.Sprintf("%v", applied),
		"reason":  reason,
		"source":  "web-api",
	})

	return jsonOK(w, map[string]any{"applied": applied, "reason": reason, "webhook_url": webhookURL})
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/alertmanager/test
// ---------------------------------------------------------------------------

// syntheticAlertPayload is a minimal, valid Alertmanager webhook body used to
// exercise the real ingestor webhook handler end-to-end without a network round trip.
const syntheticAlertPayload = `{"alerts":[{"status":"firing","labels":{"alertname":"AutoSREIntegrationTest","severity":"info"},"annotations":{"summary":"Synthetic test alert sent from the Integrations page"}}]}`

func (s *Server) handleTestAlertmanagerIntegration(w http.ResponseWriter, r *http.Request) error {
	if s.ing == nil {
		return &apiError{"ingestor unavailable; cannot test the webhook handler", http.StatusServiceUnavailable}
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", bytes.NewReader([]byte(syntheticAlertPayload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ing.WebhookHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		return &apiError{fmt.Sprintf("webhook handler returned HTTP %d: %s", rec.Code, rec.Body.String()), http.StatusInternalServerError}
	}

	return jsonOK(w, map[string]any{"ok": true, "message": "Synthetic alert accepted — check the Dashboard for a new test incident"})
}
