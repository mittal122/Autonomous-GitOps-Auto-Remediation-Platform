package ingestor

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autosre/agent/internal/contracts"
)

func makeWebhookHandler(ch chan contracts.Signal) http.Handler {
	return alertmanagerHandler(ch, slog.Default())
}

// ---------------------------------------------------------------------------
// Happy-path tests
// ---------------------------------------------------------------------------

func TestWebhook_ValidFiringAlert_EmitsSignal(t *testing.T) {
	ch := make(chan contracts.Signal, 10)
	h := makeWebhookHandler(ch)

	body := `{
		"version": "4",
		"status": "firing",
		"commonLabels": {"namespace": "production"},
		"commonAnnotations": {"summary": "high error rate"},
		"alerts": [{
			"status": "firing",
			"labels": {
				"alertname": "HighErrorRate",
				"namespace": "production",
				"pod":       "payment-svc-abc",
				"severity":  "critical"
			},
			"annotations": {},
			"startsAt": "2024-01-01T00:00:00Z"
		}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	select {
	case sig := <-ch:
		assertEqual(t, "prometheus-alert", sig.Source)
		assertEqual(t, "HighErrorRate", sig.Reason)
		assertEqual(t, "critical", sig.Severity)
		assertEqual(t, "production", sig.Namespace)
		assertEqual(t, "payment-svc-abc", sig.Resource)
	default:
		t.Fatal("expected one signal in channel, got none")
	}
}

func TestWebhook_MultipleAlerts_EmitsAll(t *testing.T) {
	ch := make(chan contracts.Signal, 10)
	h := makeWebhookHandler(ch)

	body := `{
		"version": "4",
		"status": "firing",
		"commonLabels": {},
		"commonAnnotations": {},
		"alerts": [
			{"status":"firing","labels":{"alertname":"AlertA","severity":"warning"},"startsAt":"2024-01-01T00:00:00Z"},
			{"status":"firing","labels":{"alertname":"AlertB","severity":"critical"},"startsAt":"2024-01-01T00:00:00Z"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(ch) != 2 {
		t.Fatalf("expected 2 signals, got %d", len(ch))
	}
}

func TestWebhook_EmptyAlerts_Returns200_NoSignals(t *testing.T) {
	ch := make(chan contracts.Signal, 10)
	h := makeWebhookHandler(ch)

	body := `{"version":"4","status":"firing","alerts":[]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(ch) != 0 {
		t.Errorf("expected no signals for empty alerts, got %d", len(ch))
	}
}

func TestWebhook_MissingSeverityLabel_DefaultsToWarning(t *testing.T) {
	ch := make(chan contracts.Signal, 10)
	h := makeWebhookHandler(ch)

	body := `{"version":"4","status":"firing","commonLabels":{},"commonAnnotations":{},"alerts":[{"status":"firing","labels":{"alertname":"MissSeverity"},"startsAt":"2024-01-01T00:00:00Z"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	sig := <-ch
	assertEqual(t, "warning", sig.Severity)
}

// ---------------------------------------------------------------------------
// Error / edge case tests
// ---------------------------------------------------------------------------

func TestWebhook_MalformedJSON_Returns400(t *testing.T) {
	ch := make(chan contracts.Signal, 10)
	h := makeWebhookHandler(ch)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader("not json {{{"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(ch) != 0 {
		t.Error("expected no signals after malformed JSON")
	}
}

func TestWebhook_EmptyBody_Returns400(t *testing.T) {
	ch := make(chan contracts.Signal, 10)
	h := makeWebhookHandler(ch)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(""))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty body, got %d", w.Code)
	}
}

func TestWebhook_SeverityNormalisedToLowercase(t *testing.T) {
	ch := make(chan contracts.Signal, 10)
	h := makeWebhookHandler(ch)

	body := `{"version":"4","status":"firing","commonLabels":{},"commonAnnotations":{},"alerts":[{"status":"firing","labels":{"alertname":"X","severity":"CRITICAL"},"startsAt":"2024-01-01T00:00:00Z"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	sig := <-ch
	assertEqual(t, "critical", sig.Severity)
}
