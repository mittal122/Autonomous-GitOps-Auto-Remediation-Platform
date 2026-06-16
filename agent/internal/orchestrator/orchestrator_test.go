package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/policy"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type mockDiagnosisClient struct {
	mu   sync.Mutex
	diag contracts.Diagnosis
	err  error
}

func (m *mockDiagnosisClient) Diagnose(_ context.Context, _ contracts.Incident) (contracts.Diagnosis, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.diag, m.err
}

type mockAction struct {
	mu          sync.Mutex
	dryRunCalls int
	applyCalls  int
	dryRunErr   error
	applyErr    error
	blockDryRun chan struct{} // if non-nil, DryRun blocks until closed or ctx cancelled
}

func (a *mockAction) Name() string { return "mock-action" }

func (a *mockAction) DryRun(ctx context.Context) (string, error) {
	if a.blockDryRun != nil {
		select {
		case <-a.blockDryRun:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dryRunCalls++
	return "mock dry-run output", a.dryRunErr
}

func (a *mockAction) Apply(_ context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applyCalls++
	return a.applyErr
}

func (a *mockAction) Rollback(_ context.Context) error { return nil }

func (a *mockAction) dryRunCount() int { a.mu.Lock(); defer a.mu.Unlock(); return a.dryRunCalls }
func (a *mockAction) applyCount() int  { a.mu.Lock(); defer a.mu.Unlock(); return a.applyCalls }

type mockActionBuilder struct {
	action *mockAction
	err    error
}

func (b *mockActionBuilder) Build(_ contracts.Diagnosis, _ contracts.RemediationProposal, _ bool) (contracts.RemediationAction, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.action, nil
}

type mockVerifiable struct {
	result contracts.VerificationResult
}

func (m *mockVerifiable) Verify(_ context.Context, inc contracts.Incident, ref string) contracts.VerificationResult {
	r := m.result
	r.IncidentID = inc.ID
	r.RemediationRef = ref
	return r
}

// ---------------------------------------------------------------------------
// Policy helpers
// ---------------------------------------------------------------------------

// autoPolicy: full-auto, OOMKilled → bump-memory-limit allowed, confidence threshold 0.80.
func autoPolicy() policy.PolicyConfig {
	return policy.PolicyConfig{
		DefaultAutonomy:     contracts.AutonomyFullAuto,
		ConfidenceThreshold: 0.80,
		RequireDryRun:       false,
		FailureModeRules: map[string]policy.FailureModeRule{
			"OOMKilled": {
				Autonomy:       contracts.AutonomyFullAuto,
				AllowedActions: []string{"bump-memory-limit"},
			},
		},
		NamespaceRules:      map[string]policy.NamespaceRule{},
		ProtectedNamespaces: []string{"kube-system"},
		BlastRadius: policy.BlastRadiusLimits{
			MaxReplicaDelta:     10,
			MaxMemoryBumpFactor: 4.0,
		},
		CircuitBreaker: policy.CircuitBreakerConfig{
			MaxActionsPerWindow: 100,
			WindowSeconds:       300,
		},
	}
}

// requireApprovalPolicy: confidence threshold set above test diagnosis confidence.
func requireApprovalPolicy() policy.PolicyConfig {
	cfg := autoPolicy()
	// AutoWithApproval + threshold above test's 0.95 confidence → REQUIRE_APPROVAL.
	cfg.DefaultAutonomy = contracts.AutonomyAutoWithApproval
	cfg.ConfidenceThreshold = 0.99
	return cfg
}

// blockPolicy: no failure-mode rules → checkActionAllowList returns BLOCK.
func blockPolicy() policy.PolicyConfig {
	cfg := autoPolicy()
	cfg.FailureModeRules = map[string]policy.FailureModeRule{}
	return cfg
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func testIncident(id string) contracts.Incident {
	return contracts.Incident{
		ID:       id,
		Severity: "critical",
		OpenedAt: time.Now(),
		Signals: []contracts.Signal{
			{
				ID:         "sig-1",
				Namespace:  "staging",
				Resource:   "payment-service",
				Kind:       "Pod",
				Reason:     "OOMKilled",
				Severity:   "critical",
				ReceivedAt: time.Now(),
			},
		},
		AffectedResources: []string{"payment-service"},
	}
}

func oomDiagnosis() contracts.Diagnosis {
	return contracts.Diagnosis{
		IncidentID:     "inc-test",
		FailureMode:    "OOMKilled",
		ProposedAction: "bump-memory-limit",
		Confidence:     0.95,
		BlastRadius:    "pod",
		Source:         "gemini",
		DiagnosedAt:    time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Constructor helper
// ---------------------------------------------------------------------------

type orchestratorOpts struct {
	polCfg     policy.PolicyConfig
	builder    ActionBuilder
	verResult  contracts.VerificationResult
	mockNotif  *notifier.MockNotifier
	applyCfg   bool
	killSwitch bool
}

func newTestOrchestrator(opts orchestratorOpts) (*Orchestrator, *notifier.MockNotifier) {
	mock := opts.mockNotif
	if mock == nil {
		mock = &notifier.MockNotifier{}
	}
	pol := policy.New(opts.polCfg, discardLog())
	cfg := config.OrchestratorConfig{
		ApplyEnabled:         opts.applyCfg,
		KillSwitch:           opts.killSwitch,
		MaxWorkers:           4,
		DefaultContainer:     "app",
		DefaultScaleReplicas: 2,
	}
	orch := New(cfg, nil, pol, mock, &mockVerifiable{result: opts.verResult}, opts.builder, discardLog())
	return orch, mock
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---------------------------------------------------------------------------
// Pipeline stage tests
// ---------------------------------------------------------------------------

// DryRun is called; Apply is NOT called when ApplyEnabled=false (the default).
func TestPipeline_DryRunDefault(t *testing.T) {
	action := &mockAction{}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:    autoPolicy(),
		builder:   &mockActionBuilder{action: action},
		verResult: contracts.VerificationResult{Outcome: contracts.VerificationRecovered},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-1"))

	if got := action.dryRunCount(); got != 1 {
		t.Errorf("DryRun calls = %d; want 1", got)
	}
	if got := action.applyCount(); got != 0 {
		t.Errorf("Apply calls = %d; want 0 (apply disabled)", got)
	}
}

// DryRun AND Apply are called when AUTO verdict + ApplyEnabled=true.
func TestPipeline_ApplyEnabled(t *testing.T) {
	action := &mockAction{}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:    autoPolicy(),
		builder:   &mockActionBuilder{action: action},
		verResult: contracts.VerificationResult{Outcome: contracts.VerificationRecovered},
		applyCfg:  true,
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-2"))

	if got := action.dryRunCount(); got != 1 {
		t.Errorf("DryRun calls = %d; want 1", got)
	}
	if got := action.applyCount(); got != 1 {
		t.Errorf("Apply calls = %d; want 1", got)
	}
}

// Kill switch in config prevents Apply even when ApplyEnabled=true.
func TestPipeline_KillSwitchInConfig(t *testing.T) {
	action := &mockAction{}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:     autoPolicy(),
		builder:    &mockActionBuilder{action: action},
		applyCfg:   true,
		killSwitch: true,
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-3"))

	if got := action.applyCount(); got != 0 {
		t.Errorf("Apply calls = %d; want 0 (kill switch)", got)
	}
}

// SetKillSwitch at runtime halts Apply.
func TestPipeline_KillSwitchSetAtRuntime(t *testing.T) {
	action := &mockAction{}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:   autoPolicy(),
		builder:  &mockActionBuilder{action: action},
		applyCfg: true,
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}
	orch.SetKillSwitch(true)

	orch.runPipeline(context.Background(), testIncident("inc-3b"))

	if got := action.applyCount(); got != 0 {
		t.Errorf("Apply calls = %d; want 0 (kill switch)", got)
	}
	if !orch.KillSwitchEngaged() {
		t.Error("KillSwitchEngaged() = false; want true")
	}
}

// BLOCK verdict → no DryRun/Apply, Notify called with blocked message.
func TestPipeline_PolicyBlock(t *testing.T) {
	action := &mockAction{}
	orch, mock := newTestOrchestrator(orchestratorOpts{
		polCfg:  blockPolicy(),
		builder: &mockActionBuilder{action: action},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-4"))

	if got := action.dryRunCount(); got != 0 {
		t.Errorf("DryRun calls = %d; want 0 (blocked)", got)
	}
	if got := action.applyCount(); got != 0 {
		t.Errorf("Apply calls = %d; want 0 (blocked)", got)
	}
	if len(mock.Notified) == 0 {
		t.Error("expected BLOCKED notification to be sent; got none")
	}
}

// REQUIRE_APPROVAL + default DENIED (MockNotifier fail-closed) → no Apply.
func TestPipeline_PolicyRequireApproval_Denied(t *testing.T) {
	action := &mockAction{}
	// MockNotifier zero value → default DENIED
	mock := &notifier.MockNotifier{}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:    requireApprovalPolicy(),
		builder:   &mockActionBuilder{action: action},
		applyCfg:  true,
		mockNotif: mock,
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-5"))

	if got := action.applyCount(); got != 0 {
		t.Errorf("Apply calls = %d; want 0 (denied)", got)
	}
	if len(mock.Approvals) != 1 {
		t.Errorf("approval requests = %d; want 1", len(mock.Approvals))
	}
}

// REQUIRE_APPROVAL + APPROVED → DryRun + Apply + Notify.
func TestPipeline_PolicyRequireApproval_Approved(t *testing.T) {
	action := &mockAction{}
	mock := &notifier.MockNotifier{
		ApprovalResult: contracts.ApprovalResult{
			Decision: contracts.ApprovalApproved,
			Approver: "alice",
		},
	}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:    requireApprovalPolicy(),
		builder:   &mockActionBuilder{action: action},
		verResult: contracts.VerificationResult{Outcome: contracts.VerificationRecovered},
		applyCfg:  true,
		mockNotif: mock,
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-6"))

	if got := action.dryRunCount(); got != 1 {
		t.Errorf("DryRun calls = %d; want 1", got)
	}
	if got := action.applyCount(); got != 1 {
		t.Errorf("Apply calls = %d; want 1 (approved)", got)
	}
}

// Diagnosis error → pipeline aborts before policy/action.
func TestPipeline_DiagnosisFails(t *testing.T) {
	action := &mockAction{}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:  autoPolicy(),
		builder: &mockActionBuilder{action: action},
	})
	orch.diag = &mockDiagnosisClient{err: errors.New("diagnoser unreachable")}

	orch.runPipeline(context.Background(), testIncident("inc-7"))

	if got := action.dryRunCount(); got != 0 {
		t.Errorf("DryRun calls = %d; want 0 (diagnosis failed)", got)
	}
}

// Builder error → pipeline aborts, no DryRun/Apply, no panic.
func TestPipeline_BuilderFails(t *testing.T) {
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:  autoPolicy(),
		builder: &mockActionBuilder{err: errors.New("unsupported action")},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	// Must not panic.
	orch.runPipeline(context.Background(), testIncident("inc-8"))
}

// Verifier returns FAILED → Escalate called, Notify NOT called.
func TestPipeline_VerificationFailed_Escalates(t *testing.T) {
	action := &mockAction{}
	orch, mock := newTestOrchestrator(orchestratorOpts{
		polCfg:  autoPolicy(),
		builder: &mockActionBuilder{action: action},
		verResult: contracts.VerificationResult{
			Outcome:          contracts.VerificationFailed,
			EscalationNeeded: true,
			Reason:           "signals persisted",
		},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-9"))

	if len(mock.Escalated) != 1 {
		t.Errorf("Escalate calls = %d; want 1", len(mock.Escalated))
	}
	if len(mock.Notified) != 0 {
		t.Errorf("Notify calls = %d; want 0 (escalated, not notified)", len(mock.Notified))
	}
}

// Verifier returns RECOVERED → Notify called, Escalate NOT called.
func TestPipeline_VerificationRecovered_Notifies(t *testing.T) {
	action := &mockAction{}
	orch, mock := newTestOrchestrator(orchestratorOpts{
		polCfg:  autoPolicy(),
		builder: &mockActionBuilder{action: action},
		verResult: contracts.VerificationResult{
			Outcome:          contracts.VerificationRecovered,
			EscalationNeeded: false,
		},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	orch.runPipeline(context.Background(), testIncident("inc-10"))

	if len(mock.Notified) != 1 {
		t.Errorf("Notify calls = %d; want 1", len(mock.Notified))
	}
	if len(mock.Escalated) != 0 {
		t.Errorf("Escalate calls = %d; want 0 (recovered)", len(mock.Escalated))
	}
}

// ---------------------------------------------------------------------------
// Idempotency + concurrency tests
// ---------------------------------------------------------------------------

// The in-flight registry prevents the same incident ID from being acquired twice.
func TestInFlightRegistry_Idempotency(t *testing.T) {
	r := inFlightRegistry{ids: make(map[string]struct{})}

	if !r.tryAcquire("abc") {
		t.Fatal("first tryAcquire should succeed")
	}
	if r.tryAcquire("abc") {
		t.Fatal("second tryAcquire of same ID should fail (already in-flight)")
	}
	r.release("abc")
	if !r.tryAcquire("abc") {
		t.Fatal("tryAcquire after release should succeed")
	}
}

// Concurrent call for the same incident ID: DryRun called exactly once.
func TestOrchestrator_DuplicateIncident(t *testing.T) {
	unblock := make(chan struct{})
	action := &mockAction{blockDryRun: unblock}
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:    autoPolicy(),
		builder:   &mockActionBuilder{action: action},
		verResult: contracts.VerificationResult{Outcome: contracts.VerificationRecovered},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	inc := testIncident("inc-dup")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		orch.runPipelineGuarded(context.Background(), inc)
	}()

	// Let the first goroutine acquire the lock before the second arrives.
	time.Sleep(20 * time.Millisecond)

	// Second call for the same ID should be a no-op.
	orch.runPipelineGuarded(context.Background(), inc)

	close(unblock)
	wg.Wait()

	if got := action.dryRunCount(); got != 1 {
		t.Errorf("DryRun calls = %d; want 1 (duplicate should be skipped)", got)
	}
}

// ---------------------------------------------------------------------------
// Run loop tests
// ---------------------------------------------------------------------------

// Cancelling the context causes Run to return promptly.
func TestOrchestrator_Run_ContextCancel(t *testing.T) {
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:  autoPolicy(),
		builder: &mockActionBuilder{action: &mockAction{}},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	events := make(chan correlator.IncidentEvent)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		orch.Run(ctx, events)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("Run did not return within 500ms after context cancel")
	}
}

// Closing the events channel causes Run to return.
func TestOrchestrator_Run_ChannelClose(t *testing.T) {
	orch, _ := newTestOrchestrator(orchestratorOpts{
		polCfg:  autoPolicy(),
		builder: &mockActionBuilder{action: &mockAction{}},
	})
	orch.diag = &mockDiagnosisClient{diag: oomDiagnosis()}

	events := make(chan correlator.IncidentEvent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		orch.Run(context.Background(), events)
	}()

	close(events)
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("Run did not return within 500ms after events channel closed")
	}
}

// ---------------------------------------------------------------------------
// Internal: runPipelineGuarded wraps the in-flight lock so tests can exercise
// the duplicate-incident path without going through the async schedule().
// ---------------------------------------------------------------------------

func (o *Orchestrator) runPipelineGuarded(ctx context.Context, inc contracts.Incident) {
	if !o.inFlight.tryAcquire(inc.ID) {
		return
	}
	defer o.inFlight.release(inc.ID)
	o.runPipeline(ctx, inc)
}
