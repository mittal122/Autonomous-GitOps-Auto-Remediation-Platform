package correlator_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
)

// fastConfig returns a Config with very short windows so tests can observe
// dedup and resolution without long sleeps.
func fastConfig() correlator.Config {
	return correlator.Config{
		CorrelationWindow: 1 * time.Minute,
		ResolveWindow:     50 * time.Millisecond,
		DedupWindow:       50 * time.Millisecond,
	}
}

func newTestCorrelator() *correlator.Correlator {
	return correlator.New(fastConfig(), slog.Default())
}

func makeSignal(ns, resource, reason, severity string) contracts.Signal {
	return contracts.Signal{
		ID:         "sig-" + ns + "-" + resource,
		Source:     "k8s-event",
		Namespace:  ns,
		Kind:       "Pod",
		Resource:   resource,
		Reason:     reason,
		Severity:   severity,
		ReceivedAt: time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Functional tests
// ---------------------------------------------------------------------------

func TestCorrelator_SingleSignal_OpensOneIncident(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("production", "payment-pod", "CrashLoopBackOff", "critical"))

	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	inc := incidents[0]
	if inc.ID == "" {
		t.Error("incident ID should not be empty")
	}
	if inc.Severity != "critical" {
		t.Errorf("severity: got %q, want %q", inc.Severity, "critical")
	}
	if inc.ResolvedAt != (time.Time{}) {
		t.Error("new incident should not be resolved")
	}
	if len(inc.Signals) != 1 {
		t.Errorf("expected 1 signal, got %d", len(inc.Signals))
	}
}

func TestCorrelator_DifferentNamespaces_SeparateIncidents(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("production", "svc", "OOMKilled", "critical"))
	c.Process(makeSignal("staging", "svc", "OOMKilled", "critical"))

	incidents := c.ListIncidents()
	if len(incidents) != 2 {
		t.Fatalf("expected 2 incidents for different namespaces, got %d", len(incidents))
	}
}

func TestCorrelator_DifferentResources_SameNamespace_SeparateIncidents(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("default", "pod-a", "CrashLoopBackOff", "critical"))
	c.Process(makeSignal("default", "pod-b", "CrashLoopBackOff", "critical"))

	incidents := c.ListIncidents()
	if len(incidents) != 2 {
		t.Fatalf("expected 2 incidents for different resources, got %d", len(incidents))
	}
}

func TestCorrelator_SameResourceDifferentReasons_OneIncidentTwoSignals(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("prod", "app", "CrashLoopBackOff", "critical"))
	// Different reason → not deduped, added to same incident.
	c.Process(makeSignal("prod", "app", "OOMKilled", "critical"))

	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	if len(incidents[0].Signals) != 2 {
		t.Errorf("expected 2 signals, got %d", len(incidents[0].Signals))
	}
}

func TestCorrelator_SeverityEscalates(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("prod", "svc", "FailedScheduling", "warning"))
	// Escalate to critical.
	c.Process(makeSignal("prod", "svc", "OOMKilled", "critical"))

	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	if incidents[0].Severity != "critical" {
		t.Errorf("severity should have escalated to critical, got %q", incidents[0].Severity)
	}
}

// ---------------------------------------------------------------------------
// Dedup tests
// ---------------------------------------------------------------------------

func TestCorrelator_SameReasonWithinDedupWindow_Deduped(t *testing.T) {
	c := newTestCorrelator()
	sig := makeSignal("prod", "app", "CrashLoopBackOff", "critical")

	c.Process(sig)
	c.Process(sig) // same reason, same resource, within dedup window

	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	if len(incidents[0].Signals) != 1 {
		t.Errorf("dedup failed: expected 1 signal, got %d", len(incidents[0].Signals))
	}
}

func TestCorrelator_BurstOfIdenticalSignals_OnlyOneIncident(t *testing.T) {
	c := newTestCorrelator()
	sig := makeSignal("prod", "app", "OOMKilled", "critical")
	for range 20 {
		c.Process(sig)
	}
	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident for burst, got %d", len(incidents))
	}
	if len(incidents[0].Signals) != 1 {
		t.Errorf("expected 1 deduped signal, got %d", len(incidents[0].Signals))
	}
}

func TestCorrelator_SameReasonAfterDedupWindow_AddsSignal(t *testing.T) {
	c := newTestCorrelator()
	sig := makeSignal("prod", "app", "CrashLoopBackOff", "critical")

	c.Process(sig)
	// Wait past the dedup window, then send the same reason again.
	time.Sleep(100 * time.Millisecond)

	sig2 := sig
	sig2.ID = "sig-2"
	c.Process(sig2)

	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	// After dedup window, the second signal should be appended.
	if len(incidents[0].Signals) != 2 {
		t.Errorf("expected 2 signals after dedup window, got %d", len(incidents[0].Signals))
	}
}

// ---------------------------------------------------------------------------
// Resolve / close tests
// ---------------------------------------------------------------------------

func TestCorrelator_ResolveStale_ClosesQuietIncident(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("prod", "app", "CrashLoopBackOff", "critical"))

	// Wait past the resolve window.
	time.Sleep(100 * time.Millisecond)
	c.ResolveStale()

	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	if incidents[0].ResolvedAt.IsZero() {
		t.Error("expected incident to be closed after resolve window")
	}
}

func TestCorrelator_NoSignals_ZeroIncidents(t *testing.T) {
	c := newTestCorrelator()
	incidents := c.ListIncidents()
	if len(incidents) != 0 {
		t.Errorf("expected 0 incidents with no signals, got %d", len(incidents))
	}
}

func TestCorrelator_SignalAfterClose_ReopensIncident(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("prod", "app", "CrashLoopBackOff", "critical"))

	time.Sleep(100 * time.Millisecond)
	c.ResolveStale()

	// New signal after the incident was closed.
	time.Sleep(10 * time.Millisecond)
	c.Process(makeSignal("prod", "app", "OOMKilled", "critical"))

	incidents := c.ListIncidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident (reopened), got %d", len(incidents))
	}
	if !incidents[0].ResolvedAt.IsZero() {
		t.Error("reopened incident should not be resolved")
	}
}

// ---------------------------------------------------------------------------
// Event emission tests
// ---------------------------------------------------------------------------

func TestCorrelator_Events_OpenedEventEmitted(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("prod", "svc", "CrashLoopBackOff", "critical"))

	select {
	case ev := <-c.Events():
		if ev.Kind != correlator.EventOpened {
			t.Errorf("expected EventOpened, got %v", ev.Kind)
		}
		if ev.Incident.ID == "" {
			t.Error("incident ID should not be empty")
		}
	default:
		t.Fatal("expected EventOpened to be emitted")
	}
}

func TestCorrelator_Events_ClosedEventEmittedAfterResolve(t *testing.T) {
	c := newTestCorrelator()
	c.Process(makeSignal("prod", "svc", "CrashLoopBackOff", "critical"))
	// Drain the opened event.
	<-c.Events()

	time.Sleep(100 * time.Millisecond)
	c.ResolveStale()

	select {
	case ev := <-c.Events():
		if ev.Kind != correlator.EventClosed {
			t.Errorf("expected EventClosed, got %v", ev.Kind)
		}
	default:
		t.Fatal("expected EventClosed to be emitted")
	}
}
