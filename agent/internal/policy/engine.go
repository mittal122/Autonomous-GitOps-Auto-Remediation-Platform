// Package policy implements the deterministic Decision/Policy engine.
// It takes a RemediationProposal and returns a Decision (verdict + reason).
//
// This package is DECISION-ONLY: it never calls the remediator, gitwriter,
// or any Kubernetes API. A grep for Apply/Commit/Scale must return nothing here.
//
// TODO (future prompt — orchestrator): wire the policy engine into the detect→diagnose→decide→act loop.
// TODO (future prompt — diagnoser): replace synthetic confidence values with Gemini-produced ones.
// TODO (future prompt — notifier): route REQUIRE_APPROVAL verdicts to Slack/PagerDuty for human approval.
package policy

import (
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/autosre/agent/internal/contracts"
)

// Engine evaluates RemediationProposals against a loaded PolicyConfig.
// Create one per process and reuse it; the circuit breaker state is in-memory.
type Engine struct {
	cfg PolicyConfig
	cb  *circuitBreaker
	log *slog.Logger
}

// New creates a policy Engine. If cfg was loaded with an error, the engine
// still operates safely using the fail-closed default config.
func New(cfg PolicyConfig, log *slog.Logger) *Engine {
	return &Engine{
		cfg: cfg,
		cb:  newCircuitBreaker(cfg.CircuitBreaker),
		log: log,
	}
}

// CircuitBreakerTripped reports whether the circuit breaker has fired.
func (e *Engine) CircuitBreakerTripped() bool { return e.cb.tripped() }

// CircuitBreakerCount returns the number of AUTO decisions recorded in the current window.
func (e *Engine) CircuitBreakerCount() int { return e.cb.count() }

// CircuitBreakerMax returns the limit before the circuit breaker trips.
func (e *Engine) CircuitBreakerMax() int { return e.cfg.CircuitBreaker.MaxActionsPerWindow }

// CircuitBreakerWindowSeconds returns the rolling window size in seconds.
func (e *Engine) CircuitBreakerWindowSeconds() int { return e.cfg.CircuitBreaker.WindowSeconds }

// Evaluate applies all policy gates to a proposal and returns a Decision.
// Gates are evaluated in order; each gate can only downgrade the verdict,
// never upgrade it. Every gate that changes the verdict appends to MatchedRules.
//
// Gate order:
//  1. Input validation (confidence range)
//  2. Autonomy level (global default → failure-mode override → namespace override)
//  3. Action allow-list per failure mode
//  4. Blast-radius limits
//  5. Protected namespaces
//  6. Circuit breaker
//  7. Dry-run policy annotation
func (e *Engine) Evaluate(proposal contracts.RemediationProposal) contracts.Decision {
	// Start optimistic; gates only downgrade.
	d := contracts.Decision{
		Verdict: contracts.VerdictAuto,
	}
	rules := make([]string, 0, 4)

	// -----------------------------------------------------------------------
	// Gate 1 — Confidence validation
	// -----------------------------------------------------------------------
	if proposal.Confidence < 0 || proposal.Confidence > 1 {
		d.Verdict = contracts.VerdictBlock
		d.Reason = fmt.Sprintf("confidence %g is outside [0,1]; rejecting as invalid input", proposal.Confidence)
		rules = append(rules, "confidence-range-invalid")
		d.MatchedRules = rules
		e.log.Warn("policy: blocked — confidence out of range",
			"confidence", proposal.Confidence, "incident", proposal.IncidentID)
		return d
	}

	// -----------------------------------------------------------------------
	// Gate 2 — Autonomy level resolution (most restrictive wins)
	// -----------------------------------------------------------------------
	autonomy := e.resolveAutonomy(proposal.Namespace, proposal.FailureMode, &rules)

	switch autonomy {
	case contracts.AutonomyObserve:
		d.Verdict = contracts.VerdictBlock
		d.Reason = "autonomy=observe: agent may not act"
		rules = append(rules, "autonomy-observe-block")
		d.MatchedRules = rules
		return d

	case contracts.AutonomyPropose:
		downgrade(&d, contracts.VerdictRequireApproval,
			"autonomy=propose: human approval always required", &rules, "autonomy-propose")

	case contracts.AutonomyAutoWithApproval:
		if proposal.Confidence < e.cfg.ConfidenceThreshold {
			downgrade(&d, contracts.VerdictRequireApproval,
				fmt.Sprintf("autonomy=auto-with-approval: confidence %.2f < threshold %.2f",
					proposal.Confidence, e.cfg.ConfidenceThreshold),
				&rules, "confidence-below-threshold")
		} else {
			rules = append(rules, fmt.Sprintf("autonomy-auto-with-approval: confidence %.2f >= %.2f",
				proposal.Confidence, e.cfg.ConfidenceThreshold))
		}

	case contracts.AutonomyFullAuto:
		if proposal.Confidence < e.cfg.ConfidenceThreshold {
			downgrade(&d, contracts.VerdictRequireApproval,
				fmt.Sprintf("autonomy=full-auto: confidence %.2f < threshold %.2f",
					proposal.Confidence, e.cfg.ConfidenceThreshold),
				&rules, "confidence-below-threshold")
		} else {
			rules = append(rules, fmt.Sprintf("autonomy-full-auto: confidence %.2f >= %.2f",
				proposal.Confidence, e.cfg.ConfidenceThreshold))
		}

	default:
		// Unknown autonomy level — fail closed.
		downgrade(&d, contracts.VerdictRequireApproval,
			fmt.Sprintf("unknown autonomy level %q; defaulting to require-approval", autonomy),
			&rules, "autonomy-unknown")
	}

	// -----------------------------------------------------------------------
	// Gate 3 — Action allow-list per failure mode
	// -----------------------------------------------------------------------
	e.checkActionAllowList(proposal, &d, &rules)
	if d.Verdict == contracts.VerdictBlock {
		d.MatchedRules = rules
		return d
	}

	// -----------------------------------------------------------------------
	// Gate 4 — Blast-radius limits
	// -----------------------------------------------------------------------
	e.checkBlastRadius(proposal, &d, &rules)

	// -----------------------------------------------------------------------
	// Gate 5 — Protected namespaces
	// -----------------------------------------------------------------------
	e.checkProtectedNamespace(proposal.Namespace, &d, &rules)

	// -----------------------------------------------------------------------
	// Gate 6 — Circuit breaker
	// -----------------------------------------------------------------------
	if d.Verdict == contracts.VerdictAuto && e.cb.tripped() {
		downgrade(&d, contracts.VerdictRequireApproval,
			fmt.Sprintf("circuit breaker tripped: %d AUTO decisions in window (%ds)",
				e.cb.count(), e.cfg.CircuitBreaker.WindowSeconds),
			&rules, "circuit-breaker-tripped")
	}

	// -----------------------------------------------------------------------
	// Gate 7 — Dry-run annotation
	// -----------------------------------------------------------------------
	if e.cfg.RequireDryRun && d.Verdict == contracts.VerdictAuto {
		d.DryRunRequired = true
		rules = append(rules, "dry-run-required-by-policy")
	}

	// Record the AUTO decision in the circuit breaker only after all gates pass.
	if d.Verdict == contracts.VerdictAuto {
		e.cb.record()
	}

	if d.Reason == "" {
		d.Reason = fmt.Sprintf("all policy gates passed: verdict=%s", d.Verdict)
	}
	d.MatchedRules = rules

	e.log.Info("policy: evaluated",
		"incident", proposal.IncidentID,
		"namespace", proposal.Namespace,
		"failure_mode", proposal.FailureMode,
		"action", proposal.Params.ActionType,
		"confidence", proposal.Confidence,
		"verdict", d.Verdict,
		"rules", strings.Join(rules, "; "),
	)
	return d
}

// resolveAutonomy returns the effective AutonomyLevel for a proposal by applying
// the most-restrictive-wins precedence: global default < failure-mode rule < namespace rule.
// Appends a rule entry for the source that determined the level.
func (e *Engine) resolveAutonomy(namespace, failureMode string, rules *[]string) contracts.AutonomyLevel {
	level := e.cfg.DefaultAutonomy
	if level == "" {
		level = contracts.AutonomyPropose // absolute fallback
	}
	source := "global-default"

	if rule, ok := e.cfg.FailureModeRules[failureMode]; ok && rule.Autonomy != "" {
		// Only override if this is more restrictive than current level.
		if autonomyRank(rule.Autonomy) < autonomyRank(level) {
			level = rule.Autonomy
			source = fmt.Sprintf("failure-mode-rule[%s]", failureMode)
		}
	}

	if rule, ok := e.cfg.NamespaceRules[namespace]; ok && rule.Autonomy != "" {
		if autonomyRank(rule.Autonomy) < autonomyRank(level) {
			level = rule.Autonomy
			source = fmt.Sprintf("namespace-rule[%s]", namespace)
		}
	}

	*rules = append(*rules, fmt.Sprintf("autonomy=%s (from %s)", level, source))
	return level
}

// autonomyRank maps autonomy levels to a numeric rank for comparison.
// Lower rank = more restrictive.
func autonomyRank(a contracts.AutonomyLevel) int {
	switch a {
	case contracts.AutonomyObserve:
		return 0
	case contracts.AutonomyPropose:
		return 1
	case contracts.AutonomyAutoWithApproval:
		return 2
	case contracts.AutonomyFullAuto:
		return 3
	default:
		return -1 // unknown → most restrictive
	}
}

// checkActionAllowList blocks the action if the failure mode has an explicit
// allow-list that does not include the proposed action type.
func (e *Engine) checkActionAllowList(
	proposal contracts.RemediationProposal,
	d *contracts.Decision,
	rules *[]string,
) {
	fmRule, ok := e.cfg.FailureModeRules[proposal.FailureMode]
	if !ok {
		// No rule for this failure mode → block (fail-closed: unknown mode = no allowed actions).
		downgrade(d, contracts.VerdictBlock,
			fmt.Sprintf("no failure-mode rule for %q; action not explicitly allowed",
				proposal.FailureMode),
			rules, fmt.Sprintf("action-allowlist-no-rule[%s]", proposal.FailureMode))
		return
	}
	if len(fmRule.AllowedActions) == 0 {
		downgrade(d, contracts.VerdictBlock,
			fmt.Sprintf("failure-mode %q has empty allowedActions list; blocking all actions",
				proposal.FailureMode),
			rules, fmt.Sprintf("action-allowlist-empty[%s]", proposal.FailureMode))
		return
	}
	for _, allowed := range fmRule.AllowedActions {
		if allowed == proposal.Params.ActionType {
			rules = append(*rules, fmt.Sprintf("action-allowed[%s→%s]",
				proposal.FailureMode, proposal.Params.ActionType))
			*rules = rules
			return
		}
	}
	downgrade(d, contracts.VerdictBlock,
		fmt.Sprintf("action %q is not in allowedActions for failure mode %q",
			proposal.Params.ActionType, proposal.FailureMode),
		rules, fmt.Sprintf("action-not-allowed[%s→%s]",
			proposal.FailureMode, proposal.Params.ActionType))
}

// checkBlastRadius downgrades the verdict when the computed action scope
// exceeds the configured limits.
func (e *Engine) checkBlastRadius(
	proposal contracts.RemediationProposal,
	d *contracts.Decision,
	rules *[]string,
) {
	p := proposal.Params
	switch p.ActionType {
	case "scale-deployment":
		delta := int(math.Abs(float64(p.TargetReplicas - p.CurrentReplicas)))
		limit := e.cfg.BlastRadius.MaxReplicaDelta
		if limit > 0 && delta > limit {
			downgrade(d, contracts.VerdictRequireApproval,
				fmt.Sprintf("replica delta %d exceeds maxReplicaDelta %d", delta, limit),
				rules, fmt.Sprintf("blast-radius-replicas[delta=%d,limit=%d]", delta, limit))
		} else {
			*rules = append(*rules, fmt.Sprintf("blast-radius-replicas-ok[delta=%d]", delta))
		}

	case "bump-memory-limit":
		factor := p.MemoryBumpFactor
		limit := e.cfg.BlastRadius.MaxMemoryBumpFactor
		if limit > 0 && factor > limit {
			downgrade(d, contracts.VerdictRequireApproval,
				fmt.Sprintf("memory bump factor %.2f exceeds maxMemoryBumpFactor %.2f", factor, limit),
				rules, fmt.Sprintf("blast-radius-memory[factor=%.2f,limit=%.2f]", factor, limit))
		} else {
			*rules = append(*rules, fmt.Sprintf("blast-radius-memory-ok[factor=%.2f]", factor))
		}

	case "rollback-deployment":
		// Rollback scope is inherently bounded to a single container image change.
		*rules = append(*rules, "blast-radius-rollback-ok[scope=single-container]")

	default:
		// Unknown action type — treat as unchecked blast radius, require approval.
		downgrade(d, contracts.VerdictRequireApproval,
			fmt.Sprintf("unknown action type %q; cannot compute blast radius", p.ActionType),
			rules, fmt.Sprintf("blast-radius-unknown-action[%s]", p.ActionType))
	}
}

// checkProtectedNamespace downgrades AUTO to REQUIRE_APPROVAL for namespaces
// on the protected list.
func (e *Engine) checkProtectedNamespace(
	namespace string,
	d *contracts.Decision,
	rules *[]string,
) {
	for _, protected := range e.cfg.ProtectedNamespaces {
		if namespace == protected {
			downgrade(d, contracts.VerdictRequireApproval,
				fmt.Sprintf("namespace %q is protected; AUTO not permitted", namespace),
				rules, fmt.Sprintf("protected-namespace[%s]", namespace))
			return
		}
	}
}

// downgrade sets the decision verdict to v only if v is more restrictive than
// the current verdict. It always appends rule to rules and updates Reason.
func downgrade(d *contracts.Decision, v contracts.Verdict, reason string, rules *[]string, rule string) {
	*rules = append(*rules, rule)
	if verdictRank(v) < verdictRank(d.Verdict) {
		d.Verdict = v
		d.Reason = reason
	}
}

// verdictRank maps verdicts to a numeric rank. Lower = more restrictive.
func verdictRank(v contracts.Verdict) int {
	switch v {
	case contracts.VerdictBlock:
		return 0
	case contracts.VerdictRequireApproval:
		return 1
	case contracts.VerdictAuto:
		return 2
	default:
		return -1
	}
}
