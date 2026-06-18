package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/policy"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type fakeIncidentLister struct {
	incidents []contracts.Incident
}

func (f *fakeIncidentLister) ListIncidents() []contracts.Incident { return f.incidents }

type fakeControlPlane struct {
	killSwitch   bool
	applyEnabled bool
	inflight     int
}

func (f *fakeControlPlane) KillSwitchEngaged() bool { return f.killSwitch }
func (f *fakeControlPlane) SetKillSwitch(e bool)    { f.killSwitch = e }
func (f *fakeControlPlane) ApplyEnabled() bool      { return f.applyEnabled }
func (f *fakeControlPlane) SetApplyEnabled(e bool)  { f.applyEnabled = e }
func (f *fakeControlPlane) InFlightCount() int      { return f.inflight }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestServer wires a Server for unit tests.
// When oidcEnabled=false (dev mode), all requests get viewer access.
func newTestServer(t *testing.T, cor IncidentLister, orch ControlPlane, sink audit.AuditSink, oidcEnabled bool) *Server {
	t.Helper()
	log := slog.Default()
	pol := policy.New(policy.PolicyConfig{}, log)
	notif := notifier.New(config.NotifierConfig{ApprovalTimeout: time.Minute, SendTimeout: time.Second}, log)
	return NewServer(context.Background(), config.APIConfig{
		OIDCEnabled:       oidcEnabled,
		OIDCRolesClaimKey: "roles",
	}, cor, orch, sink, notif, pol, "", nil, nil, nil, nil, nil, log)
}

// makeBearer returns a synthetic Bearer token with the given roles in the payload.
// Signature is not verified (auth middleware only decodes payload in dev mode).
func makeBearer(roles []string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	p, _ := json.Marshal(map[string]any{"roles": roles, "sub": "test-user"})
	payload := base64.RawURLEncoding.EncodeToString(p)
	return "Bearer " + header + "." + payload + ".testsig"
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body []byte, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// GET /api/v1/health — no auth required
// ---------------------------------------------------------------------------

func TestHealth_NoAuth(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/health", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
}

// ---------------------------------------------------------------------------
// GET read endpoints (dev mode — all viewer)
// ---------------------------------------------------------------------------

func TestListIncidents_DevMode(t *testing.T) {
	cor := &fakeIncidentLister{incidents: []contracts.Incident{{ID: "inc-1", Severity: "high"}}}
	srv := newTestServer(t, cor, &fakeControlPlane{}, &audit.MemorySink{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/incidents", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var got []contracts.Incident
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "inc-1" {
		t.Fatalf("unexpected incidents: %+v", got)
	}
}

func TestGetIncident_NotFound(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/incidents/notexist", nil, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestGetStatus_DevMode(t *testing.T) {
	orch := &fakeControlPlane{applyEnabled: false, killSwitch: false, inflight: 2}
	srv := newTestServer(t, &fakeIncidentLister{}, orch, &audit.MemorySink{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/status", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["apply_enabled"] != false {
		t.Errorf("expected apply_enabled=false, got %v", got["apply_enabled"])
	}
	if got["in_flight_pipelines"] != float64(2) {
		t.Errorf("expected in_flight_pipelines=2, got %v", got["in_flight_pipelines"])
	}
}

func TestGetStats_NoLearner(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/stats", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, hasNote := got["note"]; !hasNote {
		t.Errorf("expected 'note' field in no-learner response, got %+v", got)
	}
}

func TestListApprovals_Empty(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/approvals/pending", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST approval endpoints — unknown ID returns 404
// ---------------------------------------------------------------------------

func TestApprove_UnknownID(t *testing.T) {
	// Use OIDC-enabled server + operator token so we reach the 404 path (not 403).
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	body, _ := json.Marshal(map[string]string{"reason": "test"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/approvals/unknown/approve", body, makeBearer([]string{"operator"}))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body)
	}
}

func TestReject_UnknownID(t *testing.T) {
	// Use OIDC-enabled server + operator token so we reach the 404 path (not 403).
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	body, _ := json.Marshal(map[string]string{"reason": "test"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/approvals/unknown/reject", body, makeBearer([]string{"operator"}))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body)
	}
}

// ---------------------------------------------------------------------------
// POST /api/v1/control/kill-switch — admin only; audited
// ---------------------------------------------------------------------------

func TestKillSwitch_Toggle_DevMode(t *testing.T) {
	orch := &fakeControlPlane{killSwitch: false}
	sink := &audit.MemorySink{}
	srv := newTestServer(t, &fakeIncidentLister{}, orch, sink, false)

	// In dev mode, all requests are granted viewer (not admin). But the kill-switch
	// endpoint requires admin. Dev mode grants viewer, so this should return 403.
	body, _ := json.Marshal(map[string]any{"engaged": true, "reason": "test"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/control/kill-switch", body, "")
	// Dev mode = viewer only → 403 (insufficient role)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body)
	}
}

func TestKillSwitch_Toggle_AsAdmin(t *testing.T) {
	orch := &fakeControlPlane{killSwitch: false}
	sink := &audit.MemorySink{}
	// Use OIDC-enabled server so we can pass an admin token.
	srv := newTestServer(t, &fakeIncidentLister{}, orch, sink, true)

	body, _ := json.Marshal(map[string]any{"engaged": true, "reason": "incident drill"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/control/kill-switch", body, makeBearer([]string{"admin"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	if !orch.killSwitch {
		t.Error("expected kill switch to be engaged after toggle")
	}

	// Audit event must have been recorded.
	events := sink.All()
	if len(events) == 0 {
		t.Fatal("expected audit event for kill-switch toggle, got none")
	}
	found := false
	for _, ev := range events {
		if ev.Stage == "KillSwitchToggled" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("KillSwitchToggled audit event not found; events: %+v", events)
	}
}

func TestKillSwitch_BadJSON(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/control/kill-switch", []byte("{bad json"), makeBearer([]string{"admin"}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Safety controls — GET/POST /api/v1/integrations/safety
// ---------------------------------------------------------------------------

func TestGetSafety_DevMode(t *testing.T) {
	orch := &fakeControlPlane{applyEnabled: false, killSwitch: true}
	srv := newTestServer(t, &fakeIncidentLister{}, orch, &audit.MemorySink{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/safety", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got safetyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ApplyEnabled || !got.KillSwitchEngaged {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestSetSafety_AsAdmin(t *testing.T) {
	orch := &fakeControlPlane{applyEnabled: false}
	sink := &audit.MemorySink{}
	srv := newTestServer(t, &fakeIncidentLister{}, orch, sink, true)

	body, _ := json.Marshal(map[string]any{"apply_enabled": true, "reason": "ready for production"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/safety", body, makeBearer([]string{"admin"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	if !orch.applyEnabled {
		t.Error("expected apply_enabled to be true after toggle")
	}

	found := false
	for _, ev := range sink.All() {
		if ev.Stage == "SafetyControlToggled" {
			found = true
		}
	}
	if !found {
		t.Error("expected SafetyControlToggled audit event")
	}
}

func TestSetSafety_OperatorForbidden(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	body, _ := json.Marshal(map[string]any{"apply_enabled": true})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/safety", body, makeBearer([]string{"operator"}))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 (apply_enabled is admin-only, like the kill switch), got %d: %s", rr.Code, rr.Body)
	}
}

// ---------------------------------------------------------------------------
// RBAC enforcement (OIDC-enabled server)
// ---------------------------------------------------------------------------

func TestRBAC_NoToken_Returns401(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	// No Authorization header — viewer endpoint should 401.
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/incidents", nil, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestRBAC_ViewerTokenCanReadIncidents(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/incidents", nil, makeBearer([]string{"viewer"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

func TestRBAC_ViewerCannotApprove(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	body, _ := json.Marshal(map[string]string{"reason": "test"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/approvals/x/approve", body, makeBearer([]string{"viewer"}))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

func TestRBAC_OperatorCanApproveRoute(t *testing.T) {
	// Operator passes RBAC; unknown ID → 404 (not 403). Proves route is reached.
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	body, _ := json.Marshal(map[string]string{"reason": "test"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/approvals/unknown/approve", body, makeBearer([]string{"operator"}))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 (passed RBAC, not found), got %d: %s", rr.Code, rr.Body)
	}
}

func TestRBAC_OperatorCannotToggleKillSwitch(t *testing.T) {
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, &audit.MemorySink{}, true)
	body, _ := json.Marshal(map[string]any{"engaged": true, "reason": "test"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/control/kill-switch", body, makeBearer([]string{"operator"}))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Audit — nil sink must not panic
// ---------------------------------------------------------------------------

func TestAudit_NilSink_NoPanic(t *testing.T) {
	// Server with nil sink — no panics expected.
	srv := newTestServer(t, &fakeIncidentLister{}, &fakeControlPlane{}, nil, true)
	// Drive a kill-switch toggle as admin to trigger record().
	body, _ := json.Marshal(map[string]any{"engaged": false, "reason": "nil sink test"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/control/kill-switch", body, makeBearer([]string{"admin"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
}
