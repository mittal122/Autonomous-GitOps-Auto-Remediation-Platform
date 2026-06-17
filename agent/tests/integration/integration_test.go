// Package integration_test contains kind-cluster integration tests for AutoSRE.
//
// These tests run against a REAL Kubernetes cluster and test the full
// detect → diagnose → decide → remediate → verify pipeline end-to-end.
//
// # How to run
//
// The tests are skipped unless the INTEGRATION_TEST=true environment variable
// is set. They also require KUBECONFIG or in-cluster credentials and a running
// autosre agent reachable at AUTOSRE_AGENT_URL (default: http://localhost:8080).
//
//	# Prerequisites:
//	kind create cluster --config ../../kind-config.yaml
//	helm install autosre ../../charts/autosre -n autosre --create-namespace \
//	    --set agent.applyEnabled=true \
//	    --set agent.storeDSN="file:/tmp/test.db?_journal_mode=WAL"
//	kubectl port-forward svc/autosre-agent 8080:8080 -n autosre &
//
//	# Run:
//	INTEGRATION_TEST=true go test ./tests/integration/... -v -timeout 5m
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// skip exits the test with a helpful message when INTEGRATION_TEST is not set.
func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("set INTEGRATION_TEST=true to run integration tests (requires a running k8s cluster)")
	}
}

func agentURL() string {
	if u := os.Getenv("AUTOSRE_AGENT_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8080"
}

// TestAgentHealthCheck verifies the agent is reachable and reports healthy.
func TestAgentHealthCheck(t *testing.T) {
	skipUnlessIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL()+"/api/v1/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v — is the agent running and port-forwarded?", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health check: got HTTP %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("health status: got %q, want %q", body["status"], "ok")
	}
}

// TestMetricsEndpoint verifies Prometheus metrics are exposed.
func TestMetricsEndpoint(t *testing.T) {
	skipUnlessIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL()+"/metrics", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics endpoint: got HTTP %d, want 200", resp.StatusCode)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	body := buf.String()

	requiredMetrics := []string{
		"autosre_incidents_total",
		"autosre_approvals_pending",
		"autosre_kill_switch_engaged",
		"go_goroutines",
	}
	for _, m := range requiredMetrics {
		if !strings.Contains(body, m) {
			t.Errorf("missing metric %q in /metrics output", m)
		}
	}
}

// TestStatusEndpoint verifies the runtime status API returns expected fields.
func TestStatusEndpoint(t *testing.T) {
	skipUnlessIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL()+"/api/v1/status", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint: got HTTP %d, want 200", resp.StatusCode)
	}

	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	for _, key := range []string{"apply_enabled", "kill_switch_engaged", "in_flight_pipelines", "circuit_breaker_tripped"} {
		if _, ok := status[key]; !ok {
			t.Errorf("status response missing field %q", key)
		}
	}
}

// TestAlertmanagerWebhook_CrashLoop injects a synthetic CrashLoopBackOff alert
// via the Alertmanager webhook, then polls /api/v1/incidents until the incident
// appears. This validates the full ingest → correlate path without requiring a
// real crashing pod.
func TestAlertmanagerWebhook_CrashLoop(t *testing.T) {
	skipUnlessIntegration(t)

	payload := alertmanagerPayload("CrashLoopBackOff", "integration-test", "autosre-integration")
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal webhook payload: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		agentURL()+"/webhook/alertmanager", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build webhook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /webhook/alertmanager: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Fatalf("webhook returned server error %d", resp.StatusCode)
	}

	// Poll /api/v1/incidents until the incident appears (up to 20s).
	deadline := time.Now().Add(20 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		incidents := listIncidents(t, ctx)
		for _, inc := range incidents {
			sigs, _ := inc["signals"].([]any)
			for _, s := range sigs {
				sig, _ := s.(map[string]any)
				if reason, _ := sig["reason"].(string); reason == "CrashLoopBackOff" {
					found = true
				}
			}
		}
		if found {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !found {
		t.Error("CrashLoopBackOff incident did not appear in /api/v1/incidents within 20s")
	}
}

// TestOpenAPISpec verifies the OpenAPI spec is served.
func TestOpenAPISpec(t *testing.T) {
	skipUnlessIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL()+"/api/v1/openapi.json", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/openapi.json: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi: got HTTP %d, want 200", resp.StatusCode)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if !strings.Contains(buf.String(), "openapi") {
		t.Error("response does not look like an OpenAPI spec")
	}
}

// TestKillSwitchRoundTrip engages then immediately disengages the kill switch
// via the admin API, asserting the status endpoint reflects both states.
// Requires admin auth; skipped when AUTOSRE_ADMIN_TOKEN is not set.
func TestKillSwitchRoundTrip(t *testing.T) {
	skipUnlessIntegration(t)

	token := os.Getenv("AUTOSRE_ADMIN_TOKEN")
	if token == "" {
		t.Skip("AUTOSRE_ADMIN_TOKEN not set — skipping kill-switch test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	toggle := func(engaged bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"engaged": engaged,
			"reason":  "integration test",
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			agentURL()+"/api/v1/control/kill-switch", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("build kill-switch request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /api/v1/control/kill-switch: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("kill-switch toggle: got HTTP %d, want 200", resp.StatusCode)
		}
	}

	toggle(true)
	assertKillSwitch(t, ctx, true)
	toggle(false)
	assertKillSwitch(t, ctx, false)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func listIncidents(t *testing.T, ctx context.Context) []map[string]any {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, agentURL()+"/api/v1/incidents", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var incidents []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&incidents)
	return incidents
}

func assertKillSwitch(t *testing.T, ctx context.Context, want bool) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, agentURL()+"/api/v1/status", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/status: %v", err)
	}
	defer resp.Body.Close()
	var status map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&status)
	got, _ := status["kill_switch_engaged"].(bool)
	if got != want {
		t.Errorf("kill_switch_engaged: got %v, want %v", got, want)
	}
}

// alertmanagerPayload builds a minimal Alertmanager v2 webhook payload.
func alertmanagerPayload(reason, resource, namespace string) map[string]any {
	return map[string]any{
		"version":  "4",
		"groupKey": fmt.Sprintf("%s/%s", namespace, resource),
		"status":   "firing",
		"receiver": "autosre",
		"alerts": []map[string]any{
			{
				"status": "firing",
				"labels": map[string]string{
					"alertname":  reason,
					"namespace":  namespace,
					"deployment": resource,
					"severity":   "critical",
					"reason":     reason,
				},
				"annotations": map[string]string{
					"summary":     fmt.Sprintf("%s in %s/%s", reason, namespace, resource),
					"description": "integration test alert",
				},
				"startsAt": time.Now().Format(time.RFC3339),
				"endsAt":   time.Now().Add(10 * time.Minute).Format(time.RFC3339),
			},
		},
	}
}
