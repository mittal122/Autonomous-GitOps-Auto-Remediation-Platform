package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Pack is a named set of FailureModeRules distributed as a YAML file.
// Packs provide ready-made remediation rules for common failure patterns so
// operators do not need to write policy.yaml from scratch.
//
// Pack rules are additive defaults: they are merged into the base policy only
// when the failure mode is NOT already defined there. The base policy always
// wins over packs, giving operators full override control.
type Pack struct {
	Name        string                     `yaml:"name"`
	Version     string                     `yaml:"version"`
	Description string                     `yaml:"description"`
	Rules       map[string]FailureModeRule `yaml:"rules"`
}

// LoadPackDir reads all *.yaml files from dir, parses them as Packs, and
// merges their rules into dst. Rules from a pack are skipped when the failure
// mode is already defined in dst (base policy wins).
//
// Returns the number of new rules merged and any non-fatal errors (one error
// per bad file; valid files are still applied).
func LoadPackDir(dir string, dst *PolicyConfig) (int, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, []error{fmt.Errorf("packs: cannot read dir %q: %w", dir, err)}
	}

	if dst.FailureModeRules == nil {
		dst.FailureModeRules = map[string]FailureModeRule{}
	}

	var errs []error
	merged := 0

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(dir, name)
		n, err := loadPackFile(path, dst)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		merged += n
	}

	return merged, errs
}

// loadPackFile parses a single pack YAML file and merges its rules.
func loadPackFile(path string, dst *PolicyConfig) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("packs: read %q: %w", path, err)
	}

	var pack Pack
	if err := yaml.Unmarshal(data, &pack); err != nil {
		return 0, fmt.Errorf("packs: parse %q: %w", path, err)
	}

	if pack.Rules == nil {
		return 0, nil // empty pack — not an error
	}

	merged := 0
	for failureMode, rule := range pack.Rules {
		if _, exists := dst.FailureModeRules[failureMode]; exists {
			continue // base policy wins
		}
		dst.FailureModeRules[failureMode] = rule
		merged++
	}
	return merged, nil
}
