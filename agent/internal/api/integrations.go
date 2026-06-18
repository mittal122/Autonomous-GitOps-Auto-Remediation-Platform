// Integration endpoints let the web UI configure Loki and Alertmanager without
// editing .env files or restarting the agent. Settings are persisted (encrypted)
// via internal/settings and applied live through IntegrationsControl.
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/ingestor"
	"github.com/autosre/agent/internal/k8sdetect"
	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/uid"
)

// IntegrationsControl is the subset of *ingestor.Ingestor the API needs to manage
// the Loki integration live (without a process restart) and to exercise the
// Alertmanager webhook handler for the "test connection" action.
type IntegrationsControl interface {
	ReloadLoki(cfg ingestor.LokiConfig) error
	LokiStatus() ingestor.LokiStatus
	WebhookHandler() http.Handler
}

// KubernetesControl is the subset of *k8sdetect.Detector the API needs for the
// Kubernetes status card and the Alertmanager Operator-CRD auto-apply action.
type KubernetesControl interface {
	Status(ctx context.Context) k8sdetect.Status
	OperatorDetected(ctx context.Context) bool
	ApplyAlertmanagerWebhook(ctx context.Context, webhookURL string) (applied bool, reason string)
}

// ---------------------------------------------------------------------------
// GET /api/v1/integrations — aggregate summary for the dashboard and the setup
// wizard's first-run detection.
// ---------------------------------------------------------------------------

type integrationsSummaryResponse struct {
	Loki          lokiSummary         `json:"loki"`
	Alertmanager  alertmanagerSummary `json:"alertmanager"`
	Kubernetes    k8sdetect.Status    `json:"kubernetes"`
	AnyConfigured bool                `json:"any_configured"`
}

type lokiSummary struct {
	Configured bool                `json:"configured"`
	Status     ingestor.LokiStatus `json:"status"`
}

type alertmanagerSummary struct {
	WebhookURL       string `json:"webhook_url"`
	OperatorDetected bool   `json:"operator_detected"`
}

func (s *Server) handleGetIntegrationsSummary(w http.ResponseWriter, r *http.Request) error {
	var loki lokiSummary
	if s.settings != nil {
		if _, ok, err := s.settings.LoadLokiSettings(r.Context()); err == nil && ok {
			loki.Configured = true
		}
	}
	if s.ing != nil {
		loki.Status = s.ing.LokiStatus()
		if !loki.Configured && loki.Status.Enabled {
			loki.Configured = true
		}
	}

	am := alertmanagerSummary{WebhookURL: alertmanagerWebhookURL(r)}
	k8sStatus := k8sdetect.Status{Connected: false, Error: "Kubernetes API access not configured"}
	if s.k8s != nil {
		am.OperatorDetected = s.k8s.OperatorDetected(r.Context())
		k8sStatus = s.k8s.Status(r.Context())
	}

	return jsonOK(w, integrationsSummaryResponse{
		Loki:          loki,
		Alertmanager:  am,
		Kubernetes:    k8sStatus,
		AnyConfigured: loki.Configured,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/integrations/kubernetes
// ---------------------------------------------------------------------------

func (s *Server) handleGetKubernetesIntegration(w http.ResponseWriter, r *http.Request) error {
	if s.k8s == nil {
		return jsonOK(w, k8sdetect.Status{Connected: false, Error: "Kubernetes API access not configured"})
	}
	return jsonOK(w, s.k8s.Status(r.Context()))
}

// ---------------------------------------------------------------------------
// GET /api/v1/integrations/loki
// ---------------------------------------------------------------------------

type lokiIntegrationResponse struct {
	Configured    bool                `json:"configured"`
	Addr          string              `json:"addr,omitempty"`
	Query         string              `json:"query,omitempty"`
	PollInterval  string              `json:"poll_interval,omitempty"`
	Timeout       string              `json:"timeout,omitempty"`
	HasAuthHeader bool                `json:"has_auth_header"`
	Status        ingestor.LokiStatus `json:"status"`
}

func (s *Server) handleGetLokiIntegration(w http.ResponseWriter, r *http.Request) error {
	var resp lokiIntegrationResponse

	if s.settings != nil {
		if saved, ok, err := s.settings.LoadLokiSettings(r.Context()); err == nil && ok {
			resp.Configured = true
			resp.Addr = saved.Addr
			resp.Query = saved.Query
			resp.PollInterval = saved.PollInterval
			resp.Timeout = saved.Timeout
			resp.HasAuthHeader = saved.AuthHeader != ""
		}
	}
	if s.ing != nil {
		resp.Status = s.ing.LokiStatus()
		if !resp.Configured && resp.Status.Enabled {
			resp.Configured = true
			resp.Addr = resp.Status.Addr
		}
	}
	return jsonOK(w, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/loki
// ---------------------------------------------------------------------------

// lokiRequest is shared by the save and test-connection endpoints.
// AuthHeader is a pointer so the save endpoint can distinguish "field omitted —
// keep the existing saved value" from "field present and empty — clear it".
type lokiRequest struct {
	Addr         string  `json:"addr"`
	Query        string  `json:"query"`
	PollInterval string  `json:"poll_interval"`
	Timeout      string  `json:"timeout"`
	AuthHeader   *string `json:"auth_header"`
}

func (s *Server) handleSaveLokiIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req lokiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}
	if req.Addr == "" {
		return &apiError{"addr is required", http.StatusBadRequest}
	}
	if _, err := url.ParseRequestURI(req.Addr); err != nil {
		return &apiError{"addr must be a valid URL", http.StatusBadRequest}
	}

	pollInterval := req.PollInterval
	if pollInterval == "" {
		pollInterval = "30s"
	}
	parsedPoll, err := time.ParseDuration(pollInterval)
	if err != nil {
		return &apiError{"poll_interval must be a valid duration (e.g. 30s)", http.StatusBadRequest}
	}
	timeout := req.Timeout
	if timeout == "" {
		timeout = "10s"
	}
	parsedTimeout, err := time.ParseDuration(timeout)
	if err != nil {
		return &apiError{"timeout must be a valid duration (e.g. 10s)", http.StatusBadRequest}
	}
	query := req.Query
	if query == "" {
		query = `{namespace=~".+"}`
	}

	if s.settings == nil {
		return &apiError{"persistence unavailable; cannot save settings", http.StatusServiceUnavailable}
	}

	authHeader := ""
	if req.AuthHeader != nil {
		authHeader = *req.AuthHeader
	} else if existing, ok, _ := s.settings.LoadLokiSettings(r.Context()); ok {
		authHeader = existing.AuthHeader
	}

	saved := settings.LokiSettings{
		Addr:         req.Addr,
		Query:        query,
		PollInterval: pollInterval,
		Timeout:      timeout,
		AuthHeader:   authHeader,
	}
	if err := s.settings.SaveLokiSettings(r.Context(), saved); err != nil {
		s.log.Warn("api: save loki settings failed", "error", err)
		return &apiError{"failed to save settings", http.StatusInternalServerError}
	}

	if s.ing != nil {
		if err := s.ing.ReloadLoki(ingestor.LokiConfig{
			Addr:         req.Addr,
			Query:        query,
			PollInterval: parsedPoll,
			Timeout:      parsedTimeout,
		}); err != nil {
			s.log.Warn("api: reload loki failed", "error", err)
			return &apiError{"settings saved, but failed to apply live: " + err.Error(), http.StatusInternalServerError}
		}
	}

	s.record(r.Context(), uid.New(), "integrations", audit.Stage("IntegrationConfigured"), "loki-saved", map[string]string{
		"addr":   req.Addr,
		"source": "web-api",
	})

	return jsonOK(w, map[string]any{"saved": true, "addr": req.Addr})
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/loki/test
// ---------------------------------------------------------------------------

func (s *Server) handleTestLokiIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req lokiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}
	if req.Addr == "" {
		return &apiError{"addr is required", http.StatusBadRequest}
	}

	timeout := 10 * time.Second
	if req.Timeout != "" {
		if d, err := time.ParseDuration(req.Timeout); err == nil {
			timeout = d
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout+2*time.Second)
	defer cancel()

	result := ingestor.TestLokiConnection(ctx, ingestor.LokiConfig{
		Addr:    req.Addr,
		Query:   req.Query,
		Timeout: timeout,
	})
	return jsonOK(w, result)
}
