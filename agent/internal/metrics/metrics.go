// Package metrics registers all Prometheus metrics for the autosre agent.
// Call Register() once at startup; after that the metric helpers can be called
// from any goroutine without additional synchronisation.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ---------------------------------------------------------------------------
// Metric definitions (item 7.1)
// ---------------------------------------------------------------------------

var (
	// incidentsTotal counts incidents by failure_mode and outcome.
	incidentsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "autosre",
		Name:      "incidents_total",
		Help:      "Total number of incidents processed, partitioned by failure_mode and outcome.",
	}, []string{"failure_mode", "outcome"})

	// remediationDurationSeconds measures the wall-clock duration of each
	// pipeline run from detection to verification, partitioned by action type.
	remediationDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "autosre",
		Name:      "remediation_duration_seconds",
		Help:      "End-to-end remediation pipeline duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"action"})

	// approvalsPending is the current number of outstanding human-approval requests.
	approvalsPending = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "autosre",
		Name:      "approvals_pending",
		Help:      "Number of remediation approvals currently waiting for a human decision.",
	})

	// circuitBreakerState is 1 when the circuit breaker is tripped, 0 otherwise.
	circuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "autosre",
		Name:      "circuit_breaker_state",
		Help:      "Circuit breaker state: 1=tripped (AUTO blocked), 0=healthy.",
	}, []string{"state"})

	// killSwitchEngaged is 1 when the kill switch is on, 0 otherwise.
	killSwitchEngaged = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "autosre",
		Name:      "kill_switch_engaged",
		Help:      "Kill switch state: 1=engaged (all apply calls blocked), 0=normal.",
	})

	// auditEventsTotal counts audit events written per sink type.
	auditEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "autosre",
		Name:      "audit_events_total",
		Help:      "Total audit events recorded, partitioned by sink.",
	}, []string{"sink"})
)

// ---------------------------------------------------------------------------
// Helper functions — called by pipeline and component code
// ---------------------------------------------------------------------------

// IncIncident increments the incidents counter for the given failure mode and outcome.
// outcome is typically "applied", "dry-run", "blocked", "approval-denied", "error".
func IncIncident(failureMode, outcome string) {
	incidentsTotal.WithLabelValues(failureMode, outcome).Inc()
}

// ObserveRemediation records the duration (in seconds) of one complete pipeline run.
func ObserveRemediation(action string, seconds float64) {
	remediationDurationSeconds.WithLabelValues(action).Observe(seconds)
}

// SetApprovalsPending sets the current pending-approvals gauge.
func SetApprovalsPending(n float64) {
	approvalsPending.Set(n)
}

// SetCircuitBreakerTripped updates the circuit-breaker gauge.
// tripped=true → "tripped" label = 1, "healthy" label = 0 (and vice-versa).
func SetCircuitBreakerTripped(tripped bool) {
	if tripped {
		circuitBreakerState.WithLabelValues("tripped").Set(1)
		circuitBreakerState.WithLabelValues("healthy").Set(0)
	} else {
		circuitBreakerState.WithLabelValues("tripped").Set(0)
		circuitBreakerState.WithLabelValues("healthy").Set(1)
	}
}

// SetKillSwitchEngaged updates the kill-switch gauge.
func SetKillSwitchEngaged(engaged bool) {
	if engaged {
		killSwitchEngaged.Set(1)
	} else {
		killSwitchEngaged.Set(0)
	}
}

// IncAuditEvent increments the audit events counter for the given sink name.
func IncAuditEvent(sink string) {
	auditEventsTotal.WithLabelValues(sink).Inc()
}
