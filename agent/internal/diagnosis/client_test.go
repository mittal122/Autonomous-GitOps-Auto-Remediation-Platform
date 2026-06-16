package diagnosis_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/diagnosis"
)

// sampleIncident is a typical open incident for use in tests.
var sampleIncident = contracts.Incident{
	ID: "inc-go-test",
	Signals: []contracts.Signal{
		{
			ID:        "sig-001",
			Source:    "k8s-event",
			Namespace: "production",
			Resource:  "payment-service",
			Severity:  "critical",
			Kind:      "Pod",
			Reason:    "OOMKilled",
			Message:   "container was OOM killed",
		},
	},
	AffectedResources: []string{"payment-service"},
	Severity:          "critical",
	OpenedAt:          time.Now().Add(-2 * time.Minute),
	UpdatedAt:         time.Now(),
}

func validDiagnosisResponse() map[string]interface{} {
	return map[string]interface{}{
		"incident_id":     "inc-go-test",
		"root_cause":      "Memory limit too low",
		"failure_mode":    "OOMKilled",
		"proposed_action": "bump-memory-limit",
		"confidence":      0.90,
		"blast_radius":    "pod",
		"source":          "fallback",
		"diagnosed_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func serveDiagnosis(t *testing.T, responseBody interface{}, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/diagnose" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(responseBody); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
}

func newClient(addr string) *diagnosis.Client {
	return diagnosis.NewClient(diagnosis.Config{
		Addr:    addr,
		Timeout: 5 * time.Second,
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDiagnose_HappyPath(t *testing.T) {
	srv := serveDiagnosis(t, validDiagnosisResponse(), http.StatusOK)
	defer srv.Close()

	d, err := newClient(srv.URL).Diagnose(context.Background(), sampleIncident)
	if err != nil {
		t.Fatalf("Diagnose: unexpected error: %v", err)
	}
	if d.IncidentID != "inc-go-test" {
		t.Errorf("IncidentID: got %q", d.IncidentID)
	}
	if d.ProposedAction != "bump-memory-limit" {
		t.Errorf("ProposedAction: got %q", d.ProposedAction)
	}
	if d.Confidence < 0 || d.Confidence > 1 {
		t.Errorf("Confidence out of [0,1]: %f", d.Confidence)
	}
	if d.Source == "" {
		t.Error("Source must not be empty")
	}
}

func TestDiagnose_ServiceReturns500(t *testing.T) {
	srv := serveDiagnosis(t, map[string]string{"detail": "error"}, http.StatusInternalServerError)
	defer srv.Close()

	_, err := newClient(srv.URL).Diagnose(context.Background(), sampleIncident)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestDiagnose_ServiceUnreachable(t *testing.T) {
	// Use a port that nothing is listening on.
	c := diagnosis.NewClient(diagnosis.Config{
		Addr:    "http://127.0.0.1:19999",
		Timeout: 500 * time.Millisecond,
	})
	_, err := c.Diagnose(context.Background(), sampleIncident)
	if err == nil {
		t.Error("expected error for unreachable service")
	}
}

func TestDiagnose_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("this is not valid json"))
	}))
	defer srv.Close()

	_, err := newClient(srv.URL).Diagnose(context.Background(), sampleIncident)
	if err == nil {
		t.Error("expected error for malformed JSON response")
	}
}

func TestDiagnose_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay long enough for the test to cancel.
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := newClient(srv.URL).Diagnose(ctx, sampleIncident)
	if err == nil {
		t.Error("expected error when context is cancelled")
	}
}

func TestDiagnose_ConfidenceClampedAboveOne(t *testing.T) {
	resp := validDiagnosisResponse()
	resp["confidence"] = 1.5 // out of range
	srv := serveDiagnosis(t, resp, http.StatusOK)
	defer srv.Close()

	d, err := newClient(srv.URL).Diagnose(context.Background(), sampleIncident)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Confidence > 1.0 {
		t.Errorf("confidence %f should have been clamped to 1.0", d.Confidence)
	}
}

func TestDiagnose_ConfidenceClampedBelowZero(t *testing.T) {
	resp := validDiagnosisResponse()
	resp["confidence"] = -0.5
	srv := serveDiagnosis(t, resp, http.StatusOK)
	defer srv.Close()

	d, err := newClient(srv.URL).Diagnose(context.Background(), sampleIncident)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Confidence < 0.0 {
		t.Errorf("confidence %f should have been clamped to 0.0", d.Confidence)
	}
}

func TestDiagnose_RoundTrip_IncidentFieldsSent(t *testing.T) {
	var receivedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(validDiagnosisResponse())
	}))
	defer srv.Close()

	_, err := newClient(srv.URL).Diagnose(context.Background(), sampleIncident)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	// Verify snake_case field names were sent.
	if receivedBody["id"] != "inc-go-test" {
		t.Errorf("incident id not sent correctly: %v", receivedBody["id"])
	}
	if _, ok := receivedBody["affected_resources"]; !ok {
		t.Error("affected_resources field missing in request body")
	}
	signals, _ := receivedBody["signals"].([]interface{})
	if len(signals) != 1 {
		t.Errorf("expected 1 signal in request, got %d", len(signals))
	}
}
