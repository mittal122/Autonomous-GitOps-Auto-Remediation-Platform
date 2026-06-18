package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/uid"
)

// ---------------------------------------------------------------------------
// GET /api/v1/integrations/safety
// ---------------------------------------------------------------------------

type safetyResponse struct {
	ApplyEnabled      bool `json:"apply_enabled"`
	KillSwitchEngaged bool `json:"kill_switch_engaged"`
}

func (s *Server) handleGetSafety(w http.ResponseWriter, _ *http.Request) error {
	return jsonOK(w, safetyResponse{
		ApplyEnabled:      s.orch.ApplyEnabled(),
		KillSwitchEngaged: s.orch.KillSwitchEngaged(),
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/safety
// ---------------------------------------------------------------------------

// Toggling the kill switch is intentionally NOT exposed here — it already has
// its own dedicated, audited endpoint at POST /api/v1/control/kill-switch.
// This endpoint only controls Apply Enabled (the dry-run-vs-real-commits gate).
type safetyRequest struct {
	ApplyEnabled bool   `json:"apply_enabled"`
	Reason       string `json:"reason"`
}

func (s *Server) handleSetSafety(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req safetyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}

	prev := s.orch.ApplyEnabled()
	s.orch.SetApplyEnabled(req.ApplyEnabled)

	approver := fmt.Sprintf("%s-user", callerRole(r))
	s.log.Warn("api: apply-enabled toggled",
		"previous", prev, "new", req.ApplyEnabled, "operator", approver, "reason", req.Reason)

	outcome := "enabled"
	if !req.ApplyEnabled {
		outcome = "disabled"
	}
	s.record(r.Context(), uid.New(), "control-plane", audit.Stage("SafetyControlToggled"), outcome, map[string]string{
		"setting":     "apply_enabled",
		"previous":    boolStr(prev),
		"new_setting": boolStr(req.ApplyEnabled),
		"reason":      req.Reason,
		"operator":    approver,
		"source":      "web-api",
	})

	return jsonOK(w, safetyResponse{
		ApplyEnabled:      req.ApplyEnabled,
		KillSwitchEngaged: s.orch.KillSwitchEngaged(),
	})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
