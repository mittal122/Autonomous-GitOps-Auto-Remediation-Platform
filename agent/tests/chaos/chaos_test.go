// Package chaos_test exercises the full orchestrator pipeline with synthetic
// incidents for every supported failure mode. All tests run with apply disabled
// (DRY-RUN-ONLY). No Kubernetes cluster is needed.
//
// Chaos test contract:
//   - ORCHESTRATOR_APPLY_ENABLED is never set to true in this file.
//   - Every test validates the audit trail, not the remediation result.
//   - Fake DiagnosisClient, Verifiable, and ActionBuilder avoid any external I/O.
package chaos_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/orchestrator"
	"github.com/autosre/agent/internal/policy"
	"github.com/autosre/agent/internal/uid"
)

// ---------------------------------------------------------------------------
// Test doubles (no external I/O)
// ---------------------------------------------------------------------------

type fakeDiagnoser struct {
	// perMode maps failure mode to a canned diagnosis. Defaults to OOMKilled.
	perMode map[string]contracts.Diagnosis
}

func (f *fakeDiagnoser) Diagnose(_ context.Context, inc contracts.Incident) (contracts.Diagnosis, error) {
	fm := "OOMKilled"
	if len(inc.Signals) > 0 {
		fm = inc.Signals[0].Reason
	}
	if d, ok := f.perMode[fm]; ok {
		return d, nil
	}
	return contracts.Diagnosis{
		IncidentID:     inc.ID,
		FailureMode:    fm,
		ProposedAction: "rollback-deployment",
		Confidence:     0.92,
		BlastRadius:    "deployment",
		Source:         "rule-based",
		DiagnosedAt:    time.Now(),
	}, nil
}

type fakeVerifier struct{}

func (f *fakeVerifier) Verify(_ context.Context, _ contracts.Incident, _ string) contracts.VerificationResult {
	return contracts.VerificationResult{
		Outcome:     contracts.VerificationRecovered,
		WindowStart: time.Now(),
		Reason:      "chaos: no signals observed",
	}
}

type noOpAction struct{ name string }

func (n *noOpAction) Name() string                       { return n.name }
func (n *noOpAction) DryRun(_ context.Context) (string, error) {
	return fmt.Sprintf("dry-run: would apply %s", n.name), nil
}
func (n *noOpAction) Apply(_ context.Context) error    { return nil }
func (n *noOpAction) Rollback(_ context.Context) error { return nil }

type fakeBuilder struct{}

func (f *fakeBuilder) Build(
	_ contracts.Diagnosis,
	proposal contracts.RemediationProposal,
	_ bool,
) (contracts.RemediationAction, error) {
	return &noOpAction{name: proposal.Params.ActionType}, nil
}

// ---------------------------------------------------------------------------
// Test setup
// ---------------------------------------------------------------------------

// permissivePolicy returns a policy that allows all failure modes in full-auto
// with a generous circuit breaker. Used in chaos tests only.
func permissivePolicy() policy.PolicyConfig {
	return policy.PolicyConfig{
		DefaultAutonomy:     contracts.AutonomyFullAuto,
		ConfidenceThreshold: 0.5,
		CircuitBreaker: policy.CircuitBreakerConfig{
			MaxActionsPerWindow: 100,
			WindowSeconds:       60,
		},
		FailureModeRules: map[string]policy.FailureModeRule{
			"OOMKilled":        {Autonomy: contracts.AutonomyFullAuto, AllowedActions: []string{"bump-memory-limit", "rollback-deployment"}},
			"CrashLoopBackOff": {Autonomy: contracts.AutonomyFullAuto, AllowedActions: []string{"rollback-deployment"}},
			"ImagePullBackOff": {Autonomy: contracts.AutonomyFullAuto, AllowedActions: []string{"rollback-deployment", "patch-image"}},
			"FailedScheduling": {Autonomy: contracts.AutonomyFullAuto, AllowedActions: []string{"scale-deployment"}},
			"NotReady":         {Autonomy: contracts.AutonomyFullAuto, AllowedActions: []string{"rollback-deployment", "restart-pod"}},
			"BadDeploy":        {Autonomy: contracts.AutonomyFullAuto, AllowedActions: []string{"rollback-deployment"}},
		},
		NamespaceRules: map[string]policy.NamespaceRule{},
	}
}

func buildChaosOrchestrator(t *testing.T, diagnoser orchestrator.DiagnosisClient) (*orchestrator.Orchestrator, *audit.MemorySink) {
	t.Helper()
	sink := &audit.MemorySink{}
	log := slog.Default()
	pol := policy.New(permissivePolicy(), log)
	notif := notifier.New(config.NotifierConfig{ApprovalTimeout: 5 * time.Second, SendTimeout: time.Second}, log)
	orch := orchestrator.New(
		config.OrchestratorConfig{
			ApplyEnabled: false, // CHAOS TESTS MUST NEVER APPLY
			KillSwitch:   false,
			MaxWorkers:   10,
		},
		diagnoser,
		pol,
		notif,
		&fakeVerifier{},
		&fakeBuilder{},
		sink,
		nil, // outcome reporter — not needed for chaos tests
		log,
	)
	return orch, sink
}

// syntheticIncident creates an Incident with one Signal for the given failure mode.
func syntheticIncident(failureMode string) contracts.Incident {
	id := uid.New()
	return contracts.Incident{
		ID:                id,
		Severity:          "high",
		AffectedResources: []string{"default/" + failureMode + "-pod"},
		OpenedAt:          time.Now(),
		UpdatedAt:         time.Now(),
		Signals: []contracts.Signal{
			{
				ID:         uid.New(),
				Source:     "chaos-test",
				Namespace:  "default",
				Resource:   failureMode + "-deployment",
				Reason:     failureMode,
				Severity:   "high",
				ReceivedAt: time.Now(),
			},
		},
	}
}

// runChaosIncident runs a single incident through the pipeline and waits for completion.
// Returns the audit events recorded during the pipeline.
func runChaosIncident(t *testing.T, orch *orchestrator.Orchestrator, sink *audit.MemorySink, inc contracts.Incident) []audit.AuditEvent {
	t.Helper()

	beforeCount := len(sink.All())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := make(chan correlator.IncidentEvent, 1)
	ch <- correlator.IncidentEvent{Kind: correlator.EventClosed, Incident: inc}
	close(ch)

	done := make(chan struct{})
	go func() {
		defer close(done)
		orch.Run(ctx, ch)
	}()

	// Wait for Run() to return (channel closed triggers immediate return).
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("chaos: context expired before Run returned")
	}

	// Poll until all in-flight goroutines finish or timeout.
	deadline := time.Now().Add(5 * time.Second)
	for orch.InFlightCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("chaos: timed out waiting for pipeline goroutines to finish")
		}
		time.Sleep(5 * time.Millisecond)
	}

	all := sink.All()
	return all[beforeCount:]
}

// hasStage checks that at least one event with the given stage exists in events.
func hasStage(events []audit.AuditEvent, stage audit.Stage) bool {
	for _, ev := range events {
		if ev.Stage == stage {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Chaos tests — one per failure mode + apply-disabled contract
// ---------------------------------------------------------------------------

var failureModes = []struct {
	name   string
	action string
}{
	{"OOMKilled", "bump-memory-limit"},
	{"CrashLoopBackOff", "rollback-deployment"},
	{"ImagePullBackOff", "rollback-deployment"},
	{"FailedScheduling", "scale-deployment"},
	{"NotReady", "rollback-deployment"},
	{"BadDeploy", "rollback-deployment"},
}

func TestChaos_AllFailureModes_AuditTrail(t *testing.T) {
	for _, fm := range failureModes {
		fm := fm
		t.Run(fm.name, func(t *testing.T) {
			diag := &fakeDiagnoser{perMode: map[string]contracts.Diagnosis{
				fm.name: {
					FailureMode:    fm.name,
					ProposedAction: fm.action,
					Confidence:     0.92,
					BlastRadius:    "deployment",
					Source:         "chaos-test",
					DiagnosedAt:    time.Now(),
				},
			}}
			orch, sink := buildChaosOrchestrator(t, diag)
			inc := syntheticIncident(fm.name)

			events := runChaosIncident(t, orch, sink, inc)

			if len(events) == 0 {
				t.Fatalf("chaos[%s]: no audit events recorded", fm.name)
			}

			requiredStages := []audit.Stage{
				audit.StageDetected,
				audit.StageDiagnosed,
				audit.StageDecided,
				audit.StageDryRun,
			}
			for _, stage := range requiredStages {
				if !hasStage(events, stage) {
					t.Errorf("chaos[%s]: missing audit stage %q; got stages: %v",
						fm.name, stage, stageNames(events))
				}
			}
		})
	}
}

func TestChaos_ApplyNeverEnabled(t *testing.T) {
	orch, sink := buildChaosOrchestrator(t, &fakeDiagnoser{})
	if orch.ApplyEnabled() {
		t.Fatal("chaos: apply must never be enabled in chaos tests")
	}

	for _, fm := range failureModes {
		inc := syntheticIncident(fm.name)
		events := runChaosIncident(t, orch, sink, inc)

		// Applied stage must NEVER appear when apply is disabled.
		if hasStage(events, audit.StageApplied) {
			t.Errorf("chaos[%s]: Applied stage found — apply must be disabled in chaos tests", fm.name)
		}
	}
}

func TestChaos_KillSwitch_HaltsAllPipelines(t *testing.T) {
	orch, sink := buildChaosOrchestrator(t, &fakeDiagnoser{})
	orch.SetKillSwitch(true)

	inc := syntheticIncident("OOMKilled")
	events := runChaosIncident(t, orch, sink, inc)

	// Pipeline should stop after kill-switch check — no DryRun stage.
	if hasStage(events, audit.StageDryRun) {
		t.Error("chaos: DryRun stage appeared after kill switch was engaged")
	}
	if hasStage(events, audit.StageApplied) {
		t.Error("chaos: Applied stage appeared after kill switch was engaged")
	}
}

func TestChaos_BadDiagnosis_DoesNotPanic(t *testing.T) {
	// Diagnoser that always errors — pipeline must handle gracefully.
	errDiag := &errorDiagnoser{}
	orch, sink := buildChaosOrchestrator(t, errDiag)

	inc := syntheticIncident("OOMKilled")
	events := runChaosIncident(t, orch, sink, inc)

	// Detected stage emitted; Diagnosed stage emitted with "error" outcome.
	if !hasStage(events, audit.StageDetected) {
		t.Error("chaos: Detected stage missing even on diagnosis error")
	}
	// Pipeline must have returned without DryRun or Applied stages.
	if hasStage(events, audit.StageDryRun) {
		t.Error("chaos: DryRun appeared despite diagnosis error")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type errorDiagnoser struct{}

func (e *errorDiagnoser) Diagnose(_ context.Context, _ contracts.Incident) (contracts.Diagnosis, error) {
	return contracts.Diagnosis{}, fmt.Errorf("chaos: synthetic diagnosis failure")
}

func stageNames(events []audit.AuditEvent) []string {
	var names []string
	for _, ev := range events {
		names = append(names, string(ev.Stage))
	}
	return names
}
