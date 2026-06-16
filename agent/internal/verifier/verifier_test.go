package verifier_test

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/verifier"
)

// ---------------------------------------------------------------------------
// Mock RecoverySource
// ---------------------------------------------------------------------------

// mockSource is a thread-safe RecoverySource whose signal list can be mutated
// mid-test to simulate recovery or persistence of signals.
type mockSource struct {
	mu      sync.Mutex
	signals []contracts.Signal
	active  map[string]bool
	err     bool // if true, RecentSignalsFor panics (simulates unreachable source)
}

func (m *mockSource) RecentSignalsFor(target string, since time.Time) []contracts.Signal {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err {
		return nil
	}
	var out []contracts.Signal
	for _, s := range m.signals {
		if s.ReceivedAt.After(since) {
			out = append(out, s)
		}
	}
	return out
}

func (m *mockSource) IsIncidentActive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id]
}

func (m *mockSource) addSignal(s contracts.Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = append(m.signals, s)
}

func (m *mockSource) clearSignals() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func fastCfg(grace, window, poll time.Duration) config.VerifierConfig {
	return config.VerifierConfig{
		GraceDelay:       grace,
		Window:           window,
		PollInterval:     poll,
		FailureThreshold: 0,
	}
}

func testIncident(id, ns, resource string) contracts.Incident {
	return contracts.Incident{
		ID:                id,
		AffectedResources: []string{ns + "/" + resource},
		Severity:          "warning",
		OpenedAt:          time.Now().Add(-1 * time.Minute),
		UpdatedAt:         time.Now().Add(-30 * time.Second),
	}
}

// sig returns a signal with ReceivedAt = time.Now() (before any Verify() window opens).
// Used when the signal should NOT appear inside the observation window.
func sig(id, ns, resource, reason string) contracts.Signal {
	return contracts.Signal{
		ID:         id,
		Namespace:  ns,
		Resource:   resource,
		Reason:     reason,
		ReceivedAt: time.Now(),
	}
}

// futureSig returns a signal stamped 1 hour in the future so it is always
// visible inside any observation window created after this call.
func futureSig(id, ns, resource, reason string) contracts.Signal {
	s := sig(id, ns, resource, reason)
	s.ReceivedAt = time.Now().Add(time.Hour)
	return s
}

func newVerifier(cfg config.VerifierConfig, src verifier.RecoverySource) *verifier.Verifier {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return verifier.New(cfg, src, log)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// Signals stop before window ends → RECOVERED; EscalationNeeded = false.
func TestVerify_Recovered(t *testing.T) {
	src := &mockSource{}
	cfg := fastCfg(0, 80*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)
	inc := testIncident("inc-1", "production", "payment-service")

	result := v.Verify(context.Background(), inc, "abc123")

	if result.Outcome != contracts.VerificationRecovered {
		t.Errorf("expected RECOVERED, got %s: %s", result.Outcome, result.Reason)
	}
	if result.EscalationNeeded {
		t.Error("EscalationNeeded must be false on RECOVERED")
	}
	if result.IncidentID != "inc-1" {
		t.Errorf("IncidentID mismatch: %s", result.IncidentID)
	}
	if result.RemediationRef != "abc123" {
		t.Errorf("RemediationRef mismatch: %s", result.RemediationRef)
	}
	if result.WindowStart.IsZero() || result.WindowEnd.IsZero() {
		t.Error("WindowStart/WindowEnd must be populated")
	}
	if result.Reason == "" {
		t.Error("Reason must be non-empty")
	}
}

// Signals persist past the window → FAILED; EscalationNeeded = true.
func TestVerify_Failed(t *testing.T) {
	src := &mockSource{}
	cfg := fastCfg(0, 60*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)
	inc := testIncident("inc-2", "production", "payment-service")

	// Pre-load a signal stamped in the future so it is visible inside the window.
	src.addSignal(futureSig("s1", "production", "payment-service", "OOMKilled"))

	result := v.Verify(context.Background(), inc, "def456")

	if result.Outcome != contracts.VerificationFailed {
		t.Errorf("expected FAILED, got %s: %s", result.Outcome, result.Reason)
	}
	if !result.EscalationNeeded {
		t.Error("EscalationNeeded must be true on FAILED")
	}
	if len(result.ObservedSignals) == 0 {
		t.Error("ObservedSignals must be non-empty on FAILED")
	}
}

// Grace delay is respected: no verdict fires before the grace period elapses.
func TestVerify_GraceDelayRespected(t *testing.T) {
	src := &mockSource{}
	// Grace = 50ms, window = 60ms, poll = 20ms.
	// If grace is skipped, the verifier would finish in ~60ms.
	// With grace it takes ~110ms. We cancel at 70ms and expect INCONCLUSIVE.
	cfg := fastCfg(50*time.Millisecond, 60*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)
	inc := testIncident("inc-3", "staging", "order-service")

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()

	result := v.Verify(ctx, inc, "")

	if result.Outcome != contracts.VerificationInconclusive {
		t.Errorf("expected INCONCLUSIVE (cancelled in grace period), got %s", result.Outcome)
	}
	if !result.EscalationNeeded {
		t.Error("EscalationNeeded must be true on INCONCLUSIVE")
	}
}

// Context cancel during observation window → INCONCLUSIVE, EscalationNeeded.
func TestVerify_ContextCancelledDuringWindow(t *testing.T) {
	src := &mockSource{}
	cfg := fastCfg(0, 500*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)
	inc := testIncident("inc-4", "staging", "auth-service")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := v.Verify(ctx, inc, "")

	if result.Outcome != contracts.VerificationInconclusive {
		t.Errorf("expected INCONCLUSIVE on context cancel, got %s", result.Outcome)
	}
	if !result.EscalationNeeded {
		t.Error("EscalationNeeded must be true on INCONCLUSIVE")
	}
}

// Partial recovery: signal added mid-window → not RECOVERED.
func TestVerify_PartialRecovery(t *testing.T) {
	src := &mockSource{}
	cfg := fastCfg(0, 100*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)
	inc := testIncident("inc-5", "production", "cart-service")

	// Add a future-stamped signal mid-window so it appears during observation.
	go func() {
		time.Sleep(40 * time.Millisecond)
		src.addSignal(futureSig("s2", "production", "cart-service", "CrashLoopBackOff"))
	}()

	result := v.Verify(context.Background(), inc, "")

	if result.Outcome == contracts.VerificationRecovered {
		t.Error("partial recovery must not produce RECOVERED")
	}
	if !result.EscalationNeeded {
		t.Error("EscalationNeeded must be true when not RECOVERED")
	}
}

// VerificationResult fields are fully populated on every path.
func TestVerify_FieldsPopulated(t *testing.T) {
	src := &mockSource{}
	cfg := fastCfg(0, 60*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)
	inc := testIncident("inc-6", "production", "billing-service")

	result := v.Verify(context.Background(), inc, "ref-xyz")

	if result.IncidentID == "" {
		t.Error("IncidentID must be set")
	}
	if result.Outcome == "" {
		t.Error("Outcome must be set")
	}
	if result.Reason == "" {
		t.Error("Reason must be set")
	}
	if result.WindowStart.IsZero() {
		t.Error("WindowStart must be set")
	}
	if result.WindowEnd.IsZero() {
		t.Error("WindowEnd must be set")
	}
}

// Unknown/already-closed incident: verifier handles gracefully, no crash.
func TestVerify_UnknownIncident(t *testing.T) {
	src := &mockSource{}
	cfg := fastCfg(0, 60*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)

	inc := contracts.Incident{
		ID:       "unknown-99",
		Severity: "warning",
		OpenedAt: time.Now(),
	}

	// Should not panic; outcome depends on signal state (empty → RECOVERED).
	result := v.Verify(context.Background(), inc, "")
	_ = result.Outcome // any non-panic outcome is acceptable
}

// Zero/negative window/poll defaults to safe values without crashing.
func TestVerify_ZeroConfig(t *testing.T) {
	src := &mockSource{}
	cfg := config.VerifierConfig{
		GraceDelay:       0,
		Window:           0,  // should default to 5m internally → too long for test; cancel early
		PollInterval:     0,  // should default to 15s internally
		FailureThreshold: -1, // should default to 0
	}
	v := newVerifier(cfg, src)
	inc := testIncident("inc-z", "default", "nginx")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should not panic; must return INCONCLUSIVE (context cancelled before 5m window).
	result := v.Verify(ctx, inc, "")
	if result.Outcome != contracts.VerificationInconclusive {
		t.Errorf("expected INCONCLUSIVE with zero config (cancelled), got %s", result.Outcome)
	}
}

// Multiple signals observed → all returned in ObservedSignals.
func TestVerify_MultipleSignalsObserved(t *testing.T) {
	src := &mockSource{
		signals: []contracts.Signal{
			futureSig("s-a", "production", "payment-service", "OOMKilled"),
			futureSig("s-b", "production", "payment-service", "OOMKilled"),
		},
	}
	cfg := fastCfg(0, 60*time.Millisecond, 20*time.Millisecond)
	v := newVerifier(cfg, src)
	inc := testIncident("inc-7", "production", "payment-service")

	result := v.Verify(context.Background(), inc, "")

	if result.Outcome != contracts.VerificationFailed {
		t.Errorf("expected FAILED with persistent signals, got %s", result.Outcome)
	}
	if len(result.ObservedSignals) < 2 {
		t.Errorf("expected ≥2 observed signals, got %d", len(result.ObservedSignals))
	}
}

// Signals exactly at threshold → FAILED (conservative: > threshold means fail).
func TestVerify_ThresholdBoundary(t *testing.T) {
	// futureSig ensures the signal is visible inside the window.
	src := &mockSource{
		signals: []contracts.Signal{futureSig("s-t", "ns", "svc", "CrashLoopBackOff")},
	}
	// threshold=1 means 1 signal is allowed; 2+ → FAILED.
	cfg := config.VerifierConfig{
		GraceDelay:       0,
		Window:           60 * time.Millisecond,
		PollInterval:     20 * time.Millisecond,
		FailureThreshold: 1, // one signal is tolerated
	}
	v := newVerifier(cfg, src)
	inc := testIncident("inc-8", "ns", "svc")

	result := v.Verify(context.Background(), inc, "")

	// 1 signal == threshold (not >), so should be RECOVERED.
	if result.Outcome != contracts.VerificationRecovered {
		t.Errorf("expected RECOVERED when signals == threshold, got %s: %s", result.Outcome, result.Reason)
	}
}

// New contract types compile and are usable from outside the package.
func TestContractTypes(t *testing.T) {
	r := contracts.VerificationResult{
		IncidentID:       "x",
		RemediationRef:   "sha",
		Outcome:          contracts.VerificationRecovered,
		EscalationNeeded: false,
		ObservedSignals:  nil,
		WindowStart:      time.Now(),
		WindowEnd:        time.Now(),
		Reason:           "ok",
	}
	if r.Outcome != contracts.VerificationRecovered {
		t.Fail()
	}
	outcomes := []contracts.VerificationOutcome{
		contracts.VerificationRecovered,
		contracts.VerificationFailed,
		contracts.VerificationInconclusive,
	}
	if len(outcomes) != 3 {
		t.Fail()
	}
}
