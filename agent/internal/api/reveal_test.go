package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autosre/agent/internal/audit"
)

func TestReveal_RequiresAdmin(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, true)
	operator := makeBearer([]string{"operator"})
	body := []byte(`{"category":"llm","field":"api_key"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/reveal", body, operator)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body)
	}
}

func TestReveal_NotFoundWhenNothingSaved(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, true)
	admin := makeBearer([]string{"admin"})
	body := []byte(`{"category":"llm","field":"api_key"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/reveal", body, admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body)
	}
}

func TestReveal_LLMAPIKey(t *testing.T) {
	fake := &fakeLLMConfigPusher{}
	srv := newTestServerWithLLM(t, fake, true)
	admin := makeBearer([]string{"admin"})

	saveBody := []byte(`{"provider":"nim","api_key":"nvapi-the-real-secret"}`)
	if rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm", saveBody, admin); rr.Code != http.StatusOK {
		t.Fatalf("save failed: %d: %s", rr.Code, rr.Body)
	}

	revealBody := []byte(`{"category":"llm","field":"api_key"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/reveal", revealBody, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got revealResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Value != "nvapi-the-real-secret" {
		t.Errorf("expected revealed value to match saved secret, got %q", got.Value)
	}
}

func TestReveal_UnknownCategoryReturnsNotFound(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, true)
	admin := makeBearer([]string{"admin"})
	body := []byte(`{"category":"bogus","field":"x"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/reveal", body, admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body)
	}
}

func TestReveal_RecordsAuditEvent(t *testing.T) {
	fake := &fakeLLMConfigPusher{}
	srv := newTestServerWithLLM(t, fake, true)
	admin := makeBearer([]string{"admin"})

	saveBody := []byte(`{"provider":"nim","api_key":"nvapi-secret"}`)
	doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/llm", saveBody, admin)

	revealBody := []byte(`{"category":"llm","field":"api_key"}`)
	if rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/reveal", revealBody, admin); rr.Code != http.StatusOK {
		t.Fatalf("reveal failed: %d: %s", rr.Code, rr.Body)
	}

	events, err := srv.sink.Query(context.Background(), audit.QueryFilter{Stage: "SecretRevealed"})
	if err != nil {
		t.Fatalf("audit query failed: %v", err)
	}
	if len(events) == 0 {
		t.Error("expected a SecretRevealed audit event")
	}
}
