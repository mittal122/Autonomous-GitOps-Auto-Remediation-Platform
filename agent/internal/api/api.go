// Package api exposes a read-first REST API for the AutoSRE Web UI.
//
// Safety contract:
//   - All GET endpoints are read-only; they never call remediator, gitwriter, or k8s APIs.
//   - The only write endpoints are:
//       POST /api/v1/approvals/{id}/approve  — routes through the existing fail-closed registry
//       POST /api/v1/approvals/{id}/reject   — same path
//       POST /api/v1/control/kill-switch     — toggles orchestrator.SetKillSwitch(); admin only; audited
//   - There is intentionally NO endpoint to change remediation logic, policy rules,
//     git config, or any Kubernetes resource.
//   - Every approval and control-plane action is recorded in the audit log.
//   - Auth fails closed: missing/invalid token → 401; insufficient role → 403.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/policy"
	"github.com/autosre/agent/internal/uid"
)

// IncidentLister provides a snapshot of all known incidents.
type IncidentLister interface {
	ListIncidents() []contracts.Incident
}

// ControlPlane gives the API read/write access to the orchestrator's live toggles.
// Writing requires admin role and is audited.
type ControlPlane interface {
	KillSwitchEngaged() bool
	SetKillSwitch(engaged bool)
	ApplyEnabled() bool
	InFlightCount() int
}

// Server is the API server. Wire it into the main HTTP mux via Handler().
type Server struct {
	cor         IncidentLister
	orch        ControlPlane
	sink        audit.AuditSink
	notif       *notifier.CompositeNotifier
	pol         *policy.Engine
	learner     *http.Client
	learnerAddr string
	auth        *authMiddleware
	log         *slog.Logger
}

// NewServer creates a Server.
func NewServer(
	ctx context.Context,
	cfg config.APIConfig,
	cor IncidentLister,
	orch ControlPlane,
	sink audit.AuditSink,
	notif *notifier.CompositeNotifier,
	pol *policy.Engine,
	learnerAddr string,
	log *slog.Logger,
) *Server {
	return &Server{
		cor:         cor,
		orch:        orch,
		sink:        sink,
		notif:       notif,
		pol:         pol,
		learner:     &http.Client{Timeout: 5 * time.Second},
		learnerAddr: learnerAddr,
		auth:        newAuthMiddleware(ctx, cfg, log),
		log:         log,
	}
}

// Handler returns an http.Handler that mounts all API routes under /api/v1/.
// It also registers a static file handler for the Web UI at / when webUIDir is set.
func (s *Server) Handler(webUIDir string) http.Handler {
	mux := http.NewServeMux()

	// Read endpoints — viewer or higher.
	mux.Handle("GET /api/v1/incidents", s.auth.enforce(s.handle(s.handleListIncidents), RoleViewer))
	mux.Handle("GET /api/v1/incidents/{id}", s.auth.enforce(s.handle(s.handleGetIncident), RoleViewer))
	mux.Handle("GET /api/v1/incidents/{id}/trace", s.auth.enforce(s.handle(s.handleGetTrace), RoleViewer))
	mux.Handle("GET /api/v1/approvals/pending", s.auth.enforce(s.handle(s.handleListApprovals), RoleViewer))
	mux.Handle("GET /api/v1/stats", s.auth.enforce(s.handle(s.handleGetStats), RoleViewer))
	mux.Handle("GET /api/v1/status", s.auth.enforce(s.handle(s.handleGetStatus), RoleViewer))

	// Approval writes — operator or higher.
	mux.Handle("POST /api/v1/approvals/{id}/approve", s.auth.enforce(s.handle(s.handleApprove), RoleOperator))
	mux.Handle("POST /api/v1/approvals/{id}/reject", s.auth.enforce(s.handle(s.handleReject), RoleOperator))

	// Control toggles — admin only.
	mux.Handle("POST /api/v1/control/kill-switch", s.auth.enforce(s.handle(s.handleKillSwitch), RoleAdmin))

	// Health (no auth).
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, map[string]string{"status": "ok"}) //nolint:errcheck
	})

	// CORS pre-flight for cross-origin calls from the Vite dev server.
	mux.HandleFunc("/api/", corsHeaders)

	// Web UI static files or placeholder.
	if webUIDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(webUIDir)))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, uiPlaceholder)
		})
	}

	return mux
}

// ---------------------------------------------------------------------------
// GET handlers (read-only)
// ---------------------------------------------------------------------------

func (s *Server) handleListIncidents(w http.ResponseWriter, _ *http.Request) error {
	return jsonOK(w, s.cor.ListIncidents())
}

func (s *Server) handleGetIncident(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	for _, inc := range s.cor.ListIncidents() {
		if inc.ID == id {
			return jsonOK(w, inc)
		}
	}
	return &apiError{"incident not found", http.StatusNotFound}
}

func (s *Server) handleGetTrace(w http.ResponseWriter, r *http.Request) error {
	incidentID := r.PathValue("id")
	q := r.URL.Query()

	filter := audit.QueryFilter{IncidentID: incidentID, Limit: 500}
	if v := q.Get("trace_id"); v != "" {
		filter.TraceID = v
		filter.IncidentID = "" // trace_id supersedes incident_id
	}
	if v := q.Get("stage"); v != "" {
		filter.Stage = audit.Stage(v)
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}

	events, err := s.sink.Query(r.Context(), filter)
	if err != nil {
		s.log.Warn("api: trace query failed", "error", err, "incident_id", incidentID)
		return &apiError{"trace query failed", http.StatusInternalServerError}
	}

	return jsonOK(w, map[string]any{
		"incident_id": incidentID,
		"events":      events,
		"count":       len(events),
	})
}

func (s *Server) handleListApprovals(w http.ResponseWriter, _ *http.Request) error {
	return jsonOK(w, s.notif.ListPendingApprovals())
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) error {
	if s.learnerAddr == "" {
		return jsonOK(w, map[string]any{
			"total_outcomes":         0,
			"by_failure_mode_action": map[string]any{},
			"note":                   "learner not configured (LEARNER_ADDR is empty)",
		})
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.learnerAddr+"/stats", nil)
	if err != nil {
		return &apiError{"cannot build learner request", http.StatusInternalServerError}
	}
	resp, err := s.learner.Do(req)
	if err != nil {
		s.log.Warn("api: learner unreachable", "error", err)
		return &apiError{"learner service unreachable", http.StatusBadGateway}
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return nil
}

func (s *Server) handleGetStatus(w http.ResponseWriter, _ *http.Request) error {
	return jsonOK(w, map[string]any{
		"apply_enabled":              s.orch.ApplyEnabled(),
		"kill_switch_engaged":        s.orch.KillSwitchEngaged(),
		"in_flight_pipelines":        s.orch.InFlightCount(),
		"circuit_breaker_tripped":    s.pol.CircuitBreakerTripped(),
		"circuit_breaker_count":      s.pol.CircuitBreakerCount(),
		"circuit_breaker_max":        s.pol.CircuitBreakerMax(),
		"circuit_breaker_window_sec": s.pol.CircuitBreakerWindowSeconds(),
	})
}

// ---------------------------------------------------------------------------
// POST handlers (writes)
// ---------------------------------------------------------------------------

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) error {
	return s.resolveApproval(w, r, contracts.ApprovalApproved)
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) error {
	return s.resolveApproval(w, r, contracts.ApprovalDenied)
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, decision contracts.ApprovalDecision) error {
	requestID := r.PathValue("id")
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))

	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &req)

	approver := fmt.Sprintf("%s-user", callerRole(r))
	result := contracts.ApprovalResult{
		RequestID: requestID,
		Decision:  decision,
		Approver:  approver,
		DecidedAt: time.Now(),
		Reason:    req.Reason,
	}

	if !s.notif.ResolveApproval(requestID, result) {
		return &apiError{"approval request not found or already resolved", http.StatusNotFound}
	}

	outcome := "approved"
	if decision == contracts.ApprovalDenied {
		outcome = "rejected"
	}
	s.record(r.Context(), uid.New(), requestID, audit.StageApprovalResolved, outcome, map[string]string{
		"approver": approver,
		"reason":   req.Reason,
		"source":   "web-api",
	})

	return jsonOK(w, map[string]string{
		"request_id": requestID,
		"decision":   string(decision),
		"approver":   approver,
	})
}

func (s *Server) handleKillSwitch(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
	var req struct {
		Engaged bool   `json:"engaged"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{`invalid JSON: expected {"engaged": bool, "reason": "..."}`, http.StatusBadRequest}
	}

	prev := s.orch.KillSwitchEngaged()
	s.orch.SetKillSwitch(req.Engaged)

	approver := fmt.Sprintf("%s-user", callerRole(r))
	s.log.Warn("api: kill switch toggled",
		"previous", prev, "new", req.Engaged, "operator", approver, "reason", req.Reason)

	outcome := "engaged"
	if !req.Engaged {
		outcome = "disengaged"
	}
	s.record(r.Context(), uid.New(), "control-plane", audit.Stage("KillSwitchToggled"), outcome, map[string]string{
		"operator":    approver,
		"reason":      req.Reason,
		"previous":    strconv.FormatBool(prev),
		"new_setting": strconv.FormatBool(req.Engaged),
		"source":      "web-api",
	})

	return jsonOK(w, map[string]any{
		"kill_switch_engaged": req.Engaged,
		"previous":            prev,
		"operator":            approver,
	})
}

// ---------------------------------------------------------------------------
// Handler infrastructure
// ---------------------------------------------------------------------------

type apiError struct {
	msg  string
	code int
}

func (e *apiError) Error() string { return fmt.Sprintf("%d: %s", e.code, e.msg) }

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) handle(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := h(w, r); err != nil {
			if ae, ok := err.(*apiError); ok {
				jsonError(w, ae.msg, ae.code)
			} else {
				s.log.Warn("api: handler error", "error", err, "path", r.URL.Path)
				jsonError(w, "internal server error", http.StatusInternalServerError)
			}
		}
	}
}

func jsonOK(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func corsHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) record(ctx context.Context, traceID, incidentID string, stage audit.Stage, outcome string, details map[string]string) {
	if s.sink == nil {
		return
	}
	ev := audit.AuditEvent{
		Timestamp:  time.Now(),
		TraceID:    traceID,
		IncidentID: incidentID,
		Stage:      stage,
		Outcome:    outcome,
		Details:    details,
	}
	if err := s.sink.Record(ctx, ev); err != nil {
		s.log.Warn("api: audit record failed (non-fatal)", "error", err, "stage", stage)
	}
}

const uiPlaceholder = `<!DOCTYPE html>
<html><head><title>AutoSRE</title><style>body{font-family:sans-serif;padding:2rem}</style></head>
<body>
<h2>AutoSRE Agent</h2>
<p>Web UI not built yet. Run:</p>
<pre>cd web-ui && npm install && npm run build</pre>
<p>API endpoints:</p>
<ul>
  <li><a href="/api/v1/status">/api/v1/status</a></li>
  <li><a href="/api/v1/incidents">/api/v1/incidents</a></li>
  <li><a href="/api/v1/stats">/api/v1/stats</a></li>
  <li><a href="/api/v1/approvals/pending">/api/v1/approvals/pending</a></li>
  <li><a href="/api/v1/health">/api/v1/health</a></li>
</ul>
</body></html>`
