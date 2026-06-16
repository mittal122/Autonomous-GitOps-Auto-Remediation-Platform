package ingestor

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/uid"
)

// ---------------------------------------------------------------------------
// Alertmanager webhook payload types (v4 format)
// ---------------------------------------------------------------------------

// amPayload is the top-level Alertmanager webhook POST body.
type amPayload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"` // "firing" | "resolved"
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []amAlert         `json:"alerts"`
}

// amAlert is a single alert within an Alertmanager payload.
type amAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// ---------------------------------------------------------------------------
// HTTP handler
// ---------------------------------------------------------------------------

// alertmanagerHandler returns an http.HandlerFunc that parses an Alertmanager
// webhook POST body and emits one Signal per alert onto out.
//
// Validation rules:
//   - Body must be valid JSON → 400 on parse failure
//   - "alerts" field may be absent or empty → 200, 0 signals emitted
//   - Individual alerts with missing labels are mapped with empty strings
func alertmanagerHandler(out chan<- contracts.Signal, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload amPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			log.Warn("alertmanager webhook: failed to parse body", "error", err)
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if len(payload.Alerts) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}

		emitted := 0
		for _, alert := range payload.Alerts {
			sig := mapAlert(alert, payload)
			select {
			case out <- sig:
				emitted++
			default:
				// Signal buffer full — log and drop rather than blocking the HTTP handler.
				log.Warn("signal buffer full, dropping alertmanager signal",
					"alertname", alert.Labels["alertname"])
			}
		}
		log.Info("alertmanager webhook received", "alerts", len(payload.Alerts), "emitted", emitted)
		w.WriteHeader(http.StatusOK)
	}
}

// mapAlert converts one amAlert + its parent payload into a normalized Signal.
func mapAlert(alert amAlert, payload amPayload) contracts.Signal {
	labels := mergeLabels(payload.CommonLabels, alert.Labels)

	severity := strings.ToLower(labels["severity"])
	if severity == "" {
		severity = "warning" // safe default when Alertmanager omits severity
	}

	reason := labels["alertname"]
	if reason == "" {
		reason = "UnknownAlert"
	}

	namespace := labels["namespace"]
	resource := firstNonEmpty(labels["pod"], labels["deployment"], labels["service"], labels["job"])

	ts := alert.StartsAt
	if ts.IsZero() {
		ts = time.Now()
	}

	// Build a clean label set for the Signal (exclude high-cardinality noise).
	sigLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		sigLabels[k] = v
	}

	raw, _ := json.Marshal(alert)

	return contracts.Signal{
		ID:         uid.New(),
		Source:     "prometheus-alert",
		Namespace:  namespace,
		Kind:       inferKind(labels),
		Resource:   resource,
		Reason:     reason,
		Message:    payload.CommonAnnotations["summary"],
		Severity:   severity,
		Labels:     sigLabels,
		RawPayload: raw,
		ReceivedAt: time.Now(),
	}
}

// inferKind guesses the Kubernetes resource kind from alert labels.
func inferKind(labels map[string]string) string {
	if labels["pod"] != "" {
		return "Pod"
	}
	if labels["deployment"] != "" {
		return "Deployment"
	}
	if labels["node"] != "" {
		return "Node"
	}
	return ""
}

// mergeLabels returns a new map with base values overridden by override values.
func mergeLabels(base, override map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// firstNonEmpty returns the first non-empty string from the candidates.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
