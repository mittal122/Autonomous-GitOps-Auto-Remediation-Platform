package contracts_test

import (
	"testing"
	"time"

	"github.com/autosre/agent/internal/contracts"
)

func TestSignalFields(t *testing.T) {
	tests := []struct {
		name    string
		signal  contracts.Signal
		wantSrc string
		wantSev string
		wantRsn string
	}{
		{
			name: "k8s OOMKilled signal",
			signal: contracts.Signal{
				ID:         "sig-001",
				Source:     "k8s-event",
				Namespace:  "production",
				Kind:       "Pod",
				Resource:   "payment-service-abc123",
				Reason:     "OOMKilled",
				Message:    "container was OOM killed",
				Severity:   "critical",
				Labels:     map[string]string{"container": "app"},
				ReceivedAt: time.Now(),
			},
			wantSrc: "k8s-event",
			wantSev: "critical",
			wantRsn: "OOMKilled",
		},
		{
			name: "prometheus alert signal",
			signal: contracts.Signal{
				ID:         "sig-002",
				Source:     "prometheus-alert",
				Namespace:  "staging",
				Kind:       "Pod",
				Resource:   "auth-service",
				Reason:     "HighErrorRate",
				Severity:   "warning",
				ReceivedAt: time.Now(),
			},
			wantSrc: "prometheus-alert",
			wantSev: "warning",
			wantRsn: "HighErrorRate",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.signal.Source != tc.wantSrc {
				t.Errorf("Source: got %q, want %q", tc.signal.Source, tc.wantSrc)
			}
			if tc.signal.Severity != tc.wantSev {
				t.Errorf("Severity: got %q, want %q", tc.signal.Severity, tc.wantSev)
			}
			if tc.signal.Reason != tc.wantRsn {
				t.Errorf("Reason: got %q, want %q", tc.signal.Reason, tc.wantRsn)
			}
		})
	}
}

func TestIncidentHasSignals(t *testing.T) {
	sig := contracts.Signal{
		ID:       "sig-003",
		Source:   "k8s-event",
		Kind:     "Pod",
		Reason:   "CrashLoopBackOff",
		Severity: "critical",
	}
	now := time.Now()
	inc := contracts.Incident{
		ID:        "inc-001",
		Signals:   []contracts.Signal{sig},
		Severity:  "critical",
		OpenedAt:  now,
		UpdatedAt: now,
	}

	if len(inc.Signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(inc.Signals))
	}
	if inc.Signals[0].ID != "sig-003" {
		t.Errorf("signal ID mismatch: got %q", inc.Signals[0].ID)
	}
	if !inc.ResolvedAt.IsZero() {
		t.Error("new incident should not be resolved")
	}
}

func TestIncidentUpdatedAt(t *testing.T) {
	opened := time.Now().Add(-5 * time.Minute)
	updated := time.Now()
	inc := contracts.Incident{
		ID:        "inc-002",
		OpenedAt:  opened,
		UpdatedAt: updated,
	}
	if inc.UpdatedAt.Before(inc.OpenedAt) {
		t.Error("UpdatedAt should not be before OpenedAt")
	}
}

func TestDiagnosisConfidenceBounds(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
	}{
		{"zero confidence", 0.0},
		{"full confidence", 1.0},
		{"typical confidence", 0.91},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := contracts.Diagnosis{
				IncidentID: "inc-001",
				Confidence: tc.confidence,
			}
			if d.Confidence < 0 || d.Confidence > 1 {
				t.Errorf("confidence %f out of [0,1]", d.Confidence)
			}
		})
	}
}
