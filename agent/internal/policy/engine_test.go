package policy_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/policy"
)

// fullAutoPolicy returns a maximally permissive policy for testing AUTO paths.
func fullAutoPolicy() policy.PolicyConfig {
	return policy.PolicyConfig{
		DefaultAutonomy:     contracts.AutonomyFullAuto,
		ConfidenceThreshold: 0.85,
		RequireDryRun:       false,
		ProtectedNamespaces: []string{"kube-system"},
		BlastRadius: policy.BlastRadiusLimits{
			MaxReplicaDelta:     3,
			MaxMemoryBumpFactor: 2.0,
		},
		CircuitBreaker: policy.CircuitBreakerConfig{
			MaxActionsPerWindow: 10,
			WindowSeconds:       60,
		},
		FailureModeRules: map[string]policy.FailureModeRule{
			"OOMKilled": {
				Autonomy:       contracts.AutonomyFullAuto,
				AllowedActions: []string{"bump-memory-limit"},
			},
			"CrashLoopBackOff": {
				Autonomy:       contracts.AutonomyFullAuto,
				AllowedActions: []string{"rollback-deployment"},
			},
			"BadDeploy": {
				Autonomy:       contracts.AutonomyFullAuto,
				AllowedActions: []string{"rollback-deployment", "scale-deployment"},
			},
		},
		NamespaceRules: map[string]policy.NamespaceRule{},
	}
}

func newEngine(cfg policy.PolicyConfig) *policy.Engine {
	return policy.New(cfg, slog.Default())
}

func proposal(failureMode, action, namespace string, confidence float64) contracts.RemediationProposal {
	return contracts.RemediationProposal{
		IncidentID:  "inc-test",
		Namespace:   namespace,
		Resource:    "payment-service",
		FailureMode: failureMode,
		Confidence:  confidence,
		Params: contracts.ActionParams{
			ActionType:       action,
			TargetReplicas:   5,
			CurrentReplicas:  3,
			MemoryBumpFactor: 1.5,
			Container:        "app",
		},
	}
}

// ---------------------------------------------------------------------------
// Functional tests
// ---------------------------------------------------------------------------

func TestAuto_HighConfidenceFullAuto(t *testing.T) {
	e := newEngine(fullAutoPolicy())
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	if d.Verdict != contracts.VerdictAuto {
		t.Errorf("want AUTO, got %s: %s", d.Verdict, d.Reason)
	}
}

func TestRequireApproval_ConfidenceBelowThreshold(t *testing.T) {
	e := newEngine(fullAutoPolicy())
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.70))
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Errorf("want REQUIRE_APPROVAL, got %s", d.Verdict)
	}
	assertRulePresent(t, d, "confidence-below-threshold")
}

func TestBlock_ObserveAutonomy(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.DefaultAutonomy = contracts.AutonomyObserve
	cfg.FailureModeRules = map[string]policy.FailureModeRule{} // no overrides
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.99))
	if d.Verdict != contracts.VerdictBlock {
		t.Errorf("want BLOCK, got %s", d.Verdict)
	}
	assertRulePresent(t, d, "autonomy-observe-block")
}

func TestRequireApproval_ProposeAutonomy(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.DefaultAutonomy = contracts.AutonomyPropose
	// Override failure-mode to also be propose.
	cfg.FailureModeRules = map[string]policy.FailureModeRule{
		"OOMKilled": {Autonomy: contracts.AutonomyPropose, AllowedActions: []string{"bump-memory-limit"}},
	}
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.99))
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Errorf("want REQUIRE_APPROVAL, got %s", d.Verdict)
	}
	assertRulePresent(t, d, "autonomy-propose")
}

func TestBlock_DisallowedActionForFailureMode(t *testing.T) {
	e := newEngine(fullAutoPolicy())
	// OOMKilled only allows bump-memory-limit; scale-deployment should be blocked.
	d := e.Evaluate(proposal("OOMKilled", "scale-deployment", "production", 0.99))
	if d.Verdict != contracts.VerdictBlock {
		t.Errorf("want BLOCK, got %s", d.Verdict)
	}
	assertRulePresent(t, d, "action-not-allowed[OOMKilled→scale-deployment]")
}

func TestBlock_UnknownFailureMode(t *testing.T) {
	e := newEngine(fullAutoPolicy())
	d := e.Evaluate(proposal("UnknownFailure", "bump-memory-limit", "production", 0.99))
	if d.Verdict != contracts.VerdictBlock {
		t.Errorf("want BLOCK for unknown failure mode, got %s", d.Verdict)
	}
	assertRuleContains(t, d, "action-allowlist-no-rule")
}

func TestBlock_ProtectedNamespace(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.FailureModeRules["OOMKilled"] = policy.FailureModeRule{
		Autonomy: contracts.AutonomyFullAuto, AllowedActions: []string{"bump-memory-limit"},
	}
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "kube-system", 0.99))
	// Protected namespace downgrades AUTO → REQUIRE_APPROVAL, not BLOCK.
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Errorf("want REQUIRE_APPROVAL for protected namespace, got %s", d.Verdict)
	}
	assertRuleContains(t, d, "protected-namespace")
}

func TestRequireApproval_BlastRadiusReplicaDelta(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.BlastRadius.MaxReplicaDelta = 1
	e := newEngine(cfg)
	// delta = |5-3| = 2 > 1
	d := e.Evaluate(proposal("BadDeploy", "scale-deployment", "production", 0.99))
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Errorf("want REQUIRE_APPROVAL for blast-radius, got %s", d.Verdict)
	}
	assertRuleContains(t, d, "blast-radius-replicas")
}

func TestRequireApproval_BlastRadiusMemoryFactor(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.BlastRadius.MaxMemoryBumpFactor = 1.2
	e := newEngine(cfg)
	p := proposal("OOMKilled", "bump-memory-limit", "production", 0.99)
	p.Params.MemoryBumpFactor = 1.5 // > 1.2
	d := e.Evaluate(p)
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Errorf("want REQUIRE_APPROVAL for memory factor, got %s", d.Verdict)
	}
	assertRuleContains(t, d, "blast-radius-memory[")
}

func TestDryRunRequired(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.RequireDryRun = true
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	if d.Verdict != contracts.VerdictAuto {
		t.Fatalf("want AUTO, got %s", d.Verdict)
	}
	if !d.DryRunRequired {
		t.Error("expected DryRunRequired=true")
	}
	assertRulePresent(t, d, "dry-run-required-by-policy")
}

func TestMatchedRulesAndReasonPopulated(t *testing.T) {
	e := newEngine(fullAutoPolicy())
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	if len(d.MatchedRules) == 0 {
		t.Error("expected non-empty MatchedRules")
	}
	if d.Reason == "" {
		t.Error("expected non-empty Reason")
	}
}

// ---------------------------------------------------------------------------
// Edge / boundary tests (fail-closed)
// ---------------------------------------------------------------------------

func TestBlock_ConfidenceOutOfRangeNegative(t *testing.T) {
	e := newEngine(fullAutoPolicy())
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", -0.1))
	if d.Verdict != contracts.VerdictBlock {
		t.Errorf("want BLOCK for negative confidence, got %s", d.Verdict)
	}
	assertRulePresent(t, d, "confidence-range-invalid")
}

func TestBlock_ConfidenceOutOfRangeAboveOne(t *testing.T) {
	e := newEngine(fullAutoPolicy())
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 1.01))
	if d.Verdict != contracts.VerdictBlock {
		t.Errorf("want BLOCK for confidence>1, got %s", d.Verdict)
	}
}

func TestBoundary_ConfidenceExactlyAtThreshold(t *testing.T) {
	// Confidence >= threshold must pass (inclusive boundary).
	cfg := fullAutoPolicy()
	cfg.ConfidenceThreshold = 0.85
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.85))
	if d.Verdict != contracts.VerdictAuto {
		t.Errorf("confidence exactly at threshold should AUTO, got %s: %s", d.Verdict, d.Reason)
	}
}

func TestBoundary_ConfidenceJustBelowThreshold(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.ConfidenceThreshold = 0.85
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.849))
	if d.Verdict == contracts.VerdictAuto {
		t.Error("confidence just below threshold must not AUTO")
	}
}

func TestLoadPolicyFile_MissingFile(t *testing.T) {
	cfg, err := policy.LoadPolicyFile("/nonexistent/policy.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
	// Must still return fail-closed defaults.
	if cfg.DefaultAutonomy == "" {
		t.Error("expected fail-closed defaults when file is missing")
	}
	// Engine must not AUTO with default config on a random proposal.
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.99))
	if d.Verdict == contracts.VerdictAuto {
		t.Errorf("missing policy file must not produce AUTO, got %s", d.Verdict)
	}
}

func TestLoadPolicyFile_EmptyPath(t *testing.T) {
	cfg, err := policy.LoadPolicyFile("")
	if err == nil {
		t.Error("expected error for empty path")
	}
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.99))
	if d.Verdict == contracts.VerdictAuto {
		t.Errorf("empty policy path must not produce AUTO, got %s", d.Verdict)
	}
}

func TestCircuitBreaker_TripsAfterMaxActions(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.CircuitBreaker.MaxActionsPerWindow = 3
	cfg.CircuitBreaker.WindowSeconds = 60
	e := newEngine(cfg)

	// First 3 should be AUTO.
	for i := 0; i < 3; i++ {
		d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
		if d.Verdict != contracts.VerdictAuto {
			t.Fatalf("decision %d: want AUTO, got %s", i+1, d.Verdict)
		}
	}
	// 4th must be downgraded.
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Errorf("4th decision: want REQUIRE_APPROVAL after circuit trip, got %s", d.Verdict)
	}
	assertRulePresent(t, d, "circuit-breaker-tripped")
}

func TestCircuitBreaker_ClearsAfterWindow(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.CircuitBreaker.MaxActionsPerWindow = 2
	cfg.CircuitBreaker.WindowSeconds = 1 // very short window for testing
	e := newEngine(cfg)

	// Fill the breaker.
	e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))

	// Trip it.
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Fatalf("expected tripped breaker, got %s", d.Verdict)
	}

	// Wait for window to expire.
	time.Sleep(1100 * time.Millisecond)

	// Should be AUTO again.
	d = e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	if d.Verdict != contracts.VerdictAuto {
		t.Errorf("after window clear: want AUTO, got %s: %s", d.Verdict, d.Reason)
	}
}

func TestAutoWithApproval_AboveThreshold(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.FailureModeRules["OOMKilled"] = policy.FailureModeRule{
		Autonomy:       contracts.AutonomyAutoWithApproval,
		AllowedActions: []string{"bump-memory-limit"},
	}
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.95))
	if d.Verdict != contracts.VerdictAuto {
		t.Errorf("auto-with-approval above threshold: want AUTO, got %s", d.Verdict)
	}
}

func TestAutoWithApproval_BelowThreshold(t *testing.T) {
	cfg := fullAutoPolicy()
	cfg.FailureModeRules["OOMKilled"] = policy.FailureModeRule{
		Autonomy:       contracts.AutonomyAutoWithApproval,
		AllowedActions: []string{"bump-memory-limit"},
	}
	e := newEngine(cfg)
	d := e.Evaluate(proposal("OOMKilled", "bump-memory-limit", "production", 0.70))
	if d.Verdict != contracts.VerdictRequireApproval {
		t.Errorf("auto-with-approval below threshold: want REQUIRE_APPROVAL, got %s", d.Verdict)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertRulePresent(t *testing.T, d contracts.Decision, rule string) {
	t.Helper()
	for _, r := range d.MatchedRules {
		if r == rule {
			return
		}
	}
	t.Errorf("rule %q not found in MatchedRules: %v", rule, d.MatchedRules)
}

func assertRuleContains(t *testing.T, d contracts.Decision, substr string) {
	t.Helper()
	for _, r := range d.MatchedRules {
		if strings.Contains(r, substr) {
			return
		}
	}
	t.Errorf("no rule containing %q found in MatchedRules: %v", substr, d.MatchedRules)
}
