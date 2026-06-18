package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/ingestor"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/policy"
	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/store"
)

// fakeIntegrationsControl is a test double for IntegrationsControl.
type fakeIntegrationsControl struct {
	status      ingestor.LokiStatus
	reloadCfg   ingestor.LokiConfig
	reloadCalls int
	reloadErr   error
}

func (f *fakeIntegrationsControl) ReloadLoki(cfg ingestor.LokiConfig) error {
	f.reloadCalls++
	f.reloadCfg = cfg
	return f.reloadErr
}

func (f *fakeIntegrationsControl) LokiStatus() ingestor.LokiStatus { return f.status }

func (f *fakeIntegrationsControl) WebhookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// newTestServerWithIntegrations is like newTestServer but also wires Loki integration
// control + a real (temp-dir-backed) encrypted settings store.
func newTestServerWithIntegrations(t *testing.T, ing IntegrationsControl, oidcEnabled bool) *Server {
	t.Helper()
	log := slog.Default()
	pol := policy.New(policy.PolicyConfig{}, log)
	notif := notifier.New(config.NotifierConfig{}, log)

	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") + "?_journal_mode=WAL"
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	key, err := settings.EnsureMasterKey(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("EnsureMasterKey failed: %v", err)
	}
	settingsStore, err := settings.New(db, key)
	if err != nil {
		t.Fatalf("settings.New failed: %v", err)
	}

	return NewServer(context.Background(), config.APIConfig{
		OIDCEnabled:       oidcEnabled,
		OIDCRolesClaimKey: "roles",
	}, &fakeIncidentLister{}, &fakeControlPlane{},
		&audit.MemorySink{}, notif, pol, "", ing, nil, settingsStore, log)
}

func TestGetIntegrationsSummary_NoneConfigured(t *testing.T) {
	srv := newTestServerWithIntegrations(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got integrationsSummaryResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AnyConfigured {
		t.Errorf("expected AnyConfigured=false, got %+v", got)
	}
	if !strings.HasSuffix(got.Alertmanager.WebhookURL, "/webhook/alertmanager") {
		t.Errorf("unexpected webhook URL: %q", got.Alertmanager.WebhookURL)
	}
}

func TestGetIntegrationsSummary_ReflectsSavedLoki(t *testing.T) {
	srv := newTestServerWithIntegrations(t, &fakeIntegrationsControl{}, true)
	operator := makeBearer([]string{"operator"})

	saveBody := []byte(`{"addr":"http://loki:3100"}`)
	if rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/loki", saveBody, operator); rr.Code != http.StatusOK {
		t.Fatalf("save failed: %d: %s", rr.Code, rr.Body)
	}

	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations", nil, operator)
	var got integrationsSummaryResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.AnyConfigured || !got.Loki.Configured {
		t.Errorf("expected Loki configured after save, got %+v", got)
	}
}

func TestGetKubernetesIntegration_NoControl(t *testing.T) {
	srv := newTestServerWithIntegrations(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/kubernetes", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got struct {
		Connected bool   `json:"connected"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Connected || got.Error == "" {
		t.Errorf("expected disconnected with a reason, got %+v", got)
	}
}

func TestGetLokiIntegration_NotConfigured(t *testing.T) {
	srv := newTestServerWithIntegrations(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/loki", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got lokiIntegrationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Configured {
		t.Errorf("expected Configured=false, got %+v", got)
	}
}

func TestSaveLokiIntegration_PersistsAndReloads(t *testing.T) {
	fake := &fakeIntegrationsControl{}
	srv := newTestServerWithIntegrations(t, fake, true)
	operator := makeBearer([]string{"operator"})

	body := []byte(`{"addr":"http://loki:3100","query":"{namespace=\"prod\"}","poll_interval":"15s","timeout":"5s","auth_header":"Bearer secret"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/loki", body, operator)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}

	if fake.reloadCalls != 1 {
		t.Fatalf("expected ReloadLoki called once, got %d", fake.reloadCalls)
	}
	if fake.reloadCfg.Addr != "http://loki:3100" {
		t.Errorf("expected reload addr http://loki:3100, got %q", fake.reloadCfg.Addr)
	}

	// Saved settings should round-trip through a fresh GET (secrets redacted).
	getRR := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/loki", nil, operator)
	var got lokiIntegrationResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Configured || got.Addr != "http://loki:3100" || !got.HasAuthHeader {
		t.Errorf("unexpected response: %+v", got)
	}
	if strings.Contains(getRR.Body.String(), "Bearer secret") {
		t.Error("response must not contain the plaintext auth header")
	}
}

func TestSaveLokiIntegration_RejectsMissingAddr(t *testing.T) {
	srv := newTestServerWithIntegrations(t, &fakeIntegrationsControl{}, true)
	operator := makeBearer([]string{"operator"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/loki", []byte(`{}`), operator)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}

func TestSaveLokiIntegration_RejectsBadDuration(t *testing.T) {
	srv := newTestServerWithIntegrations(t, &fakeIntegrationsControl{}, true)
	operator := makeBearer([]string{"operator"})
	body := []byte(`{"addr":"http://loki:3100","poll_interval":"not-a-duration"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/loki", body, operator)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}

func TestSaveLokiIntegration_ViewerForbidden(t *testing.T) {
	srv := newTestServerWithIntegrations(t, &fakeIntegrationsControl{}, true)
	body := []byte(`{"addr":"http://loki:3100"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/loki", body, makeBearer([]string{"viewer"}))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestLokiIntegration_UnreachableAddr(t *testing.T) {
	srv := newTestServerWithIntegrations(t, &fakeIntegrationsControl{}, false)
	body := []byte(`{"addr":"http://127.0.0.1:1","timeout":"1s"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/loki/test", body, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 (test endpoint reports failure in body, not HTTP status), got %d: %s", rr.Code, rr.Body)
	}
	var got ingestor.LokiTestResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OK {
		t.Error("expected OK=false for an unreachable address")
	}
}

func TestTestLokiIntegration_MissingAddr(t *testing.T) {
	srv := newTestServerWithIntegrations(t, &fakeIntegrationsControl{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/loki/test", []byte(`{}`), "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}
