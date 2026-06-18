package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGetAlertmanagerIntegration_NoKubernetesControl(t *testing.T) {
	srv := newTestServerWithIntegrations(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/alertmanager", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got alertmanagerIntegrationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OperatorDetected {
		t.Error("expected OperatorDetected=false with no KubernetesControl wired")
	}
	if !strings.HasSuffix(got.WebhookURL, "/webhook/alertmanager") {
		t.Errorf("unexpected webhook URL: %q", got.WebhookURL)
	}
	if !strings.Contains(got.YAMLSnippet, got.WebhookURL) {
		t.Error("expected the YAML snippet to embed the webhook URL")
	}
}

func TestTestAlertmanagerIntegration_NoIngestor(t *testing.T) {
	srv := newTestServerWithIntegrations(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/alertmanager/test", nil, "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestAlertmanagerIntegration_ExercisesWebhookHandler(t *testing.T) {
	fake := &fakeIntegrationsControl{}
	srv := newTestServerWithIntegrations(t, fake, false)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/alertmanager/test", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
}

func TestApplyAlertmanagerIntegration_NoKubernetesControl(t *testing.T) {
	srv := newTestServerWithIntegrations(t, nil, true)
	operator := makeBearer([]string{"operator"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/alertmanager/apply", nil, operator)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if applied, _ := got["applied"].(bool); applied {
		t.Error("expected applied=false with no KubernetesControl wired")
	}
}

func TestApplyAlertmanagerIntegration_ViewerForbidden(t *testing.T) {
	srv := newTestServerWithIntegrations(t, nil, true)
	viewer := makeBearer([]string{"viewer"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/alertmanager/apply", nil, viewer)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body)
	}
}
