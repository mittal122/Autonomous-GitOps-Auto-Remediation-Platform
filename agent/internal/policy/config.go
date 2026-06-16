package policy

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/autosre/agent/internal/contracts"
)

// PolicyConfig is the declarative policy loaded from policy.yaml.
// All fields have conservative defaults (fail-closed).
type PolicyConfig struct {
	// DefaultAutonomy applies when no per-failure-mode or per-namespace rule matches.
	// Default: "propose" (always require human approval).
	DefaultAutonomy contracts.AutonomyLevel `yaml:"defaultAutonomy"`

	// ConfidenceThreshold is the minimum confidence required for AUTO verdicts.
	// A proposal whose confidence is strictly below this value is never AUTO.
	// Boundary: confidence >= threshold passes. Default: 0.90.
	ConfidenceThreshold float64 `yaml:"confidenceThreshold"`

	// RequireDryRun forces DryRunRequired=true on every AUTO decision.
	RequireDryRun bool `yaml:"requireDryRun"`

	// FailureModeRules maps failure modes (e.g. "OOMKilled") to their rules.
	FailureModeRules map[string]FailureModeRule `yaml:"failureModeRules"`

	// NamespaceRules maps namespace names to autonomy overrides.
	NamespaceRules map[string]NamespaceRule `yaml:"namespaceRules"`

	// ProtectedNamespaces is a list of namespaces where AUTO is never allowed
	// (verdict is forced to REQUIRE_APPROVAL). Block-level protection uses NamespaceRules.
	ProtectedNamespaces []string `yaml:"protectedNamespaces"`

	// BlastRadius limits apply globally across all actions.
	BlastRadius BlastRadiusLimits `yaml:"blastRadius"`

	// CircuitBreaker limits the rate of AUTO decisions.
	CircuitBreaker CircuitBreakerConfig `yaml:"circuitBreaker"`
}

// FailureModeRule defines which actions are allowed and what autonomy level applies
// when a specific failure mode is diagnosed.
type FailureModeRule struct {
	// Autonomy overrides the global default for this failure mode.
	// If empty, the global default is used.
	Autonomy contracts.AutonomyLevel `yaml:"autonomy"`
	// AllowedActions is the explicit allow-list of action types permitted for this failure mode.
	// A proposal with an action not in this list is BLOCKED.
	// If empty, all actions are blocked (fail-closed).
	AllowedActions []string `yaml:"allowedActions"`
}

// NamespaceRule defines autonomy overrides for a specific namespace.
type NamespaceRule struct {
	// Autonomy overrides the global default for resources in this namespace.
	Autonomy contracts.AutonomyLevel `yaml:"autonomy"`
}

// BlastRadiusLimits defines the upper bounds on action scope.
type BlastRadiusLimits struct {
	// MaxReplicaDelta is the maximum absolute change in replica count permitted
	// for AUTO. Exceeding it downgrades to REQUIRE_APPROVAL.
	MaxReplicaDelta int `yaml:"maxReplicaDelta"`
	// MaxMemoryBumpFactor is the maximum allowed memory multiplier for AUTO.
	// Exceeding it downgrades to REQUIRE_APPROVAL.
	MaxMemoryBumpFactor float64 `yaml:"maxMemoryBumpFactor"`
}

// CircuitBreakerConfig limits how many AUTO decisions can fire in a rolling window.
type CircuitBreakerConfig struct {
	// MaxActionsPerWindow is the number of AUTO decisions after which subsequent
	// decisions are downgraded to REQUIRE_APPROVAL until the window clears.
	MaxActionsPerWindow int `yaml:"maxActionsPerWindow"`
	// WindowSeconds is the rolling window duration in seconds.
	WindowSeconds int `yaml:"windowSeconds"`
}

// defaultPolicy returns a conservative fail-closed policy used when no file is found.
func defaultPolicy() PolicyConfig {
	return PolicyConfig{
		DefaultAutonomy:     contracts.AutonomyPropose,
		ConfidenceThreshold: 0.90,
		RequireDryRun:       true,
		FailureModeRules:    map[string]FailureModeRule{},
		NamespaceRules:      map[string]NamespaceRule{},
		ProtectedNamespaces: []string{"kube-system", "kube-public", "kube-node-lease"},
		BlastRadius: BlastRadiusLimits{
			MaxReplicaDelta:     2,
			MaxMemoryBumpFactor: 2.0,
		},
		CircuitBreaker: CircuitBreakerConfig{
			MaxActionsPerWindow: 5,
			WindowSeconds:       300,
		},
	}
}

// LoadPolicyFile reads and parses a policy YAML file.
// On any error (missing file, invalid YAML, invalid values) it returns the
// fail-closed default config plus the error. The engine never crashes.
func LoadPolicyFile(path string) (PolicyConfig, error) {
	def := defaultPolicy()
	if path == "" {
		return def, fmt.Errorf("policy: POLICY_FILE env var not set; using fail-closed defaults")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return def, fmt.Errorf("policy: cannot read %q: %w; using fail-closed defaults", path, err)
	}

	var cfg PolicyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return def, fmt.Errorf("policy: cannot parse %q: %w; using fail-closed defaults", path, err)
	}

	if err := validatePolicyConfig(cfg); err != nil {
		return def, fmt.Errorf("policy: invalid config in %q: %w; using fail-closed defaults", path, err)
	}

	// Ensure maps are non-nil after parse.
	if cfg.FailureModeRules == nil {
		cfg.FailureModeRules = map[string]FailureModeRule{}
	}
	if cfg.NamespaceRules == nil {
		cfg.NamespaceRules = map[string]NamespaceRule{}
	}
	return cfg, nil
}

func validatePolicyConfig(cfg PolicyConfig) error {
	if cfg.ConfidenceThreshold < 0 || cfg.ConfidenceThreshold > 1 {
		return fmt.Errorf("confidenceThreshold %g is out of [0,1]", cfg.ConfidenceThreshold)
	}
	validAutonomy := map[contracts.AutonomyLevel]bool{
		contracts.AutonomyObserve:          true,
		contracts.AutonomyPropose:          true,
		contracts.AutonomyAutoWithApproval: true,
		contracts.AutonomyFullAuto:         true,
		"": true, // empty means "use default"
	}
	if !validAutonomy[cfg.DefaultAutonomy] {
		return fmt.Errorf("unknown defaultAutonomy %q", cfg.DefaultAutonomy)
	}
	for fm, rule := range cfg.FailureModeRules {
		if !validAutonomy[rule.Autonomy] {
			return fmt.Errorf("failureModeRules[%q].autonomy %q is unknown", fm, rule.Autonomy)
		}
	}
	for ns, rule := range cfg.NamespaceRules {
		if !validAutonomy[rule.Autonomy] {
			return fmt.Errorf("namespaceRules[%q].autonomy %q is unknown", ns, rule.Autonomy)
		}
	}
	if cfg.BlastRadius.MaxReplicaDelta < 0 {
		return fmt.Errorf("blastRadius.maxReplicaDelta must be >= 0")
	}
	if cfg.BlastRadius.MaxMemoryBumpFactor < 0 {
		return fmt.Errorf("blastRadius.maxMemoryBumpFactor must be >= 0")
	}
	if cfg.CircuitBreaker.MaxActionsPerWindow < 0 {
		return fmt.Errorf("circuitBreaker.maxActionsPerWindow must be >= 0")
	}
	if cfg.CircuitBreaker.WindowSeconds <= 0 {
		return fmt.Errorf("circuitBreaker.windowSeconds must be > 0")
	}
	return nil
}
