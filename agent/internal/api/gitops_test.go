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
	"github.com/autosre/agent/internal/gitwriter"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/policy"
	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/store"
)

// fakeGitOpsReloader is a test double for GitOpsReloader.
type fakeGitOpsReloader struct {
	reloadCalls int
	reloadedCfg gitwriter.Config
	reloadErr   error
}

func (f *fakeGitOpsReloader) Reload(_ context.Context, cfg gitwriter.Config) error {
	f.reloadCalls++
	f.reloadedCfg = cfg
	return f.reloadErr
}

func newTestServerWithGitOps(t *testing.T, gitops GitOpsReloader, oidcEnabled bool) *Server {
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
		&audit.MemorySink{}, notif, pol, "", nil, nil, nil, gitops, settingsStore, log)
}

func TestGetGitOpsIntegration_NotConfigured(t *testing.T) {
	srv := newTestServerWithGitOps(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/gitops", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got gitOpsIntegrationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Configured {
		t.Errorf("expected Configured=false, got %+v", got)
	}
}

func TestSaveGitOpsIntegration_PersistsAndReloads(t *testing.T) {
	fake := &fakeGitOpsReloader{}
	srv := newTestServerWithGitOps(t, fake, true)
	operator := makeBearer([]string{"operator"})

	body := []byte(`{"repo_path":"/data/gitops","remote_url":"https://github.com/example/gitops.git","auth_token":"ghp_secret","branch":"main"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/gitops", body, operator)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}

	if fake.reloadCalls != 1 {
		t.Fatalf("expected Reload called once, got %d", fake.reloadCalls)
	}
	if fake.reloadedCfg.RepoPath != "/data/gitops" || fake.reloadedCfg.AuthToken != "ghp_secret" {
		t.Errorf("unexpected reloaded config: %+v", fake.reloadedCfg)
	}

	getRR := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/gitops", nil, operator)
	var got gitOpsIntegrationResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Configured || got.RepoPath != "/data/gitops" || !got.HasAuthToken {
		t.Errorf("unexpected response: %+v", got)
	}
	if strings.Contains(getRR.Body.String(), "ghp_secret") {
		t.Error("response must not contain the plaintext auth token")
	}
}

func TestSaveGitOpsIntegration_RejectsMissingRepoPath(t *testing.T) {
	srv := newTestServerWithGitOps(t, &fakeGitOpsReloader{}, true)
	operator := makeBearer([]string{"operator"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/gitops", []byte(`{}`), operator)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}

func TestSaveGitOpsIntegration_ViewerForbidden(t *testing.T) {
	srv := newTestServerWithGitOps(t, &fakeGitOpsReloader{}, true)
	viewer := makeBearer([]string{"viewer"})
	body := []byte(`{"repo_path":"/data/gitops"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/gitops", body, viewer)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestGitOpsIntegration_UnreachableRemote(t *testing.T) {
	srv := newTestServerWithGitOps(t, &fakeGitOpsReloader{}, false)
	body := []byte(`{"remote_url":"https://127.0.0.1:1/not-a-real-repo.git"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/gitops/test", body, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 (test endpoint reports failure in body, not HTTP status), got %d: %s", rr.Code, rr.Body)
	}
	var got gitOpsTestResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OK {
		t.Error("expected ok=false for an unreachable remote")
	}
}

func TestTestGitOpsIntegration_MissingRemoteURL(t *testing.T) {
	srv := newTestServerWithGitOps(t, &fakeGitOpsReloader{}, false)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/gitops/test", []byte(`{}`), "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}
