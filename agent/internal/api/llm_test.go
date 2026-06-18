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
	"github.com/autosre/agent/internal/diagnosis"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/policy"
	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/store"
)

// fakeLLMConfigPusher is a test double for LLMConfigPusher.
type fakeLLMConfigPusher struct {
	pushCalls  int
	pushedCfg  diagnosis.LLMConfig
	pushErr    error
	testResult diagnosis.LLMTestResult
	testErr    error
}

func (f *fakeLLMConfigPusher) PushConfig(_ context.Context, cfg diagnosis.LLMConfig) error {
	f.pushCalls++
	f.pushedCfg = cfg
	return f.pushErr
}

func (f *fakeLLMConfigPusher) TestConfig(_ context.Context, _ diagnosis.LLMConfig) (diagnosis.LLMTestResult, error) {
	return f.testResult, f.testErr
}

func newTestServerWithLLM(t *testing.T, llm LLMConfigPusher, oidcEnabled bool) *Server {
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
		&audit.MemorySink{}, notif, pol, "", nil, nil, llm, nil, settingsStore, log)
}

func TestGetLLMIntegration_NotConfigured(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/llm", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got llmIntegrationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Configured {
		t.Errorf("expected Configured=false, got %+v", got)
	}
}

func TestSaveLLMIntegration_PersistsAndPushes(t *testing.T) {
	fake := &fakeLLMConfigPusher{}
	srv := newTestServerWithLLM(t, fake, true)
	operator := makeBearer([]string{"operator"})

	body := []byte(`{"provider":"nim","api_key":"nvapi-secret","model":"meta/llama-3.3-70b-instruct","timeout_seconds":30}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm", body, operator)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}

	if fake.pushCalls != 1 {
		t.Fatalf("expected PushConfig called once, got %d", fake.pushCalls)
	}
	if fake.pushedCfg.Provider != "nim" || fake.pushedCfg.APIKey != "nvapi-secret" {
		t.Errorf("unexpected pushed config: %+v", fake.pushedCfg)
	}

	getRR := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/llm", nil, operator)
	var got llmIntegrationResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Configured || got.Provider != "nim" || !got.HasAPIKey {
		t.Errorf("unexpected response: %+v", got)
	}
	if strings.Contains(getRR.Body.String(), "nvapi-secret") {
		t.Error("response must not contain the plaintext API key")
	}
}

func TestSaveLLMIntegration_RejectsMissingKeyForProvider(t *testing.T) {
	srv := newTestServerWithLLM(t, &fakeLLMConfigPusher{}, true)
	operator := makeBearer([]string{"operator"})
	body := []byte(`{"provider":"nim"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm", body, operator)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}

func TestSaveLLMIntegration_RejectsBadProvider(t *testing.T) {
	srv := newTestServerWithLLM(t, &fakeLLMConfigPusher{}, true)
	operator := makeBearer([]string{"operator"})
	body := []byte(`{"provider":"chatgpt","api_key":"x"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm", body, operator)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}

func TestSaveLLMIntegration_EmptyProviderDisables(t *testing.T) {
	fake := &fakeLLMConfigPusher{}
	srv := newTestServerWithLLM(t, fake, true)
	operator := makeBearer([]string{"operator"})
	body := []byte(`{"provider":""}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm", body, operator)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	if fake.pushedCfg.Provider != "" {
		t.Errorf("expected empty provider pushed, got %q", fake.pushedCfg.Provider)
	}
}

func TestSaveLLMIntegration_ViewerForbidden(t *testing.T) {
	srv := newTestServerWithLLM(t, &fakeLLMConfigPusher{}, true)
	viewer := makeBearer([]string{"viewer"})
	body := []byte(`{"provider":"nim","api_key":"x"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm", body, viewer)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestLLMIntegration_NoPusher(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, false)
	body := []byte(`{"provider":"nim","api_key":"x"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm/test", body, "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestLLMIntegration_MissingAPIKey(t *testing.T) {
	srv := newTestServerWithLLM(t, &fakeLLMConfigPusher{}, false)
	body := []byte(`{"provider":"nim"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm/test", body, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestLLMIntegration_Success(t *testing.T) {
	fake := &fakeLLMConfigPusher{testResult: diagnosis.LLMTestResult{OK: true, Message: "Connected"}}
	srv := newTestServerWithLLM(t, fake, false)
	body := []byte(`{"provider":"nim","api_key":"nvapi-x"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm/test", body, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got diagnosis.LLMTestResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK {
		t.Errorf("expected ok=true, got %+v", got)
	}
}
