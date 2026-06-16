package gitwriter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FindManifest walks repoPath searching for a YAML file that matches
// the given Kubernetes resource (kind, namespace, name).
// Returns the absolute file path of the first match.
func FindManifest(repoPath, namespace, kind, name string) (string, error) {
	var found string
	err := filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir // skip .git, .github, etc.
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // skip unreadable files
		}

		if manifestMatches(data, namespace, kind, name) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking %s: %w", repoPath, err)
	}
	if found == "" {
		return "", fmt.Errorf("no manifest found for %s/%s/%s in %s", kind, namespace, name, repoPath)
	}
	return found, nil
}

// manifestMatches returns true if the YAML document has matching kind, namespace, and name.
func manifestMatches(data []byte, namespace, kind, name string) bool {
	var doc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	return strings.EqualFold(doc.Kind, kind) &&
		doc.Metadata.Namespace == namespace &&
		doc.Metadata.Name == name
}

// GetField parses data and navigates to fieldPath, returning the scalar value.
//
// Supported paths:
//
//	"spec.replicas"
//	"spec.template.spec.containers[name=app].image"
//	"spec.template.spec.containers[name=app].resources.limits.memory"
func GetField(data []byte, fieldPath string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", fmt.Errorf("empty or invalid YAML document")
	}
	root := doc.Content[0]

	parts := splitPath(fieldPath)
	node, err := navigatePath(root, parts)
	if err != nil {
		return "", fmt.Errorf("field %q: %w", fieldPath, err)
	}
	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("field %q is not a scalar (kind=%v)", fieldPath, node.Kind)
	}
	return node.Value, nil
}

// SetField edits fieldPath in data to newValue using the yaml.Node API so
// comments and indentation are preserved. Returns (newData, oldValue, error).
func SetField(data []byte, fieldPath, newValue string) ([]byte, string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, "", fmt.Errorf("parse YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, "", fmt.Errorf("empty or invalid YAML document")
	}
	root := doc.Content[0]

	parts := splitPath(fieldPath)
	node, err := navigatePath(root, parts)
	if err != nil {
		return nil, "", fmt.Errorf("field %q: %w", fieldPath, err)
	}
	if node.Kind != yaml.ScalarNode {
		return nil, "", fmt.Errorf("field %q is not a scalar", fieldPath)
	}

	oldValue := node.Value
	node.Value = newValue
	// Preserve tag for integer-like fields (replicas) as !!int; drop to plain string otherwise.
	if node.Tag == "!!int" {
		if _, convErr := fmt.Sscanf(newValue, "%d", new(int)); convErr != nil {
			node.Tag = "!!str"
		}
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, "", fmt.Errorf("marshal YAML: %w", err)
	}
	return out, oldValue, nil
}

// generateDiff returns a human-readable single-field diff string.
func generateDiff(relFile, field, oldValue, newValue string) string {
	return fmt.Sprintf("--- %s\n+++ %s\n@@ field: %s @@\n- %s\n+ %s\n",
		relFile, relFile, field, oldValue, newValue)
}

// splitPath splits a dot-separated field path, keeping array selectors intact.
// "spec.template.spec.containers[name=app].image" →
//
//	["spec", "template", "spec", "containers[name=app]", "image"]
func splitPath(fieldPath string) []string {
	var parts []string
	for _, p := range strings.Split(fieldPath, ".") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// navigatePath walks a yaml.Node tree following the given path parts.
func navigatePath(current *yaml.Node, parts []string) (*yaml.Node, error) {
	if len(parts) == 0 {
		return current, nil
	}
	part := parts[0]
	rest := parts[1:]

	key, selectorVal, hasSelector := parseArraySelector(part)
	if hasSelector {
		// Navigate to key first (e.g. "containers"), then find item by name.
		if current.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("expected mapping node for key %q, got kind %v", key, current.Kind)
		}
		seqNode := findMappingValue(current, key)
		if seqNode == nil {
			return nil, fmt.Errorf("key %q not found", key)
		}
		if seqNode.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("expected sequence for %q, got kind %v", key, seqNode.Kind)
		}
		item := findItemByName(seqNode, selectorVal)
		if item == nil {
			return nil, fmt.Errorf("no item with name=%q in %q", selectorVal, key)
		}
		return navigatePath(item, rest)
	}

	// Plain key navigation.
	if current.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node for key %q, got kind %v", part, current.Kind)
	}
	child := findMappingValue(current, part)
	if child == nil {
		return nil, fmt.Errorf("key %q not found", part)
	}
	return navigatePath(child, rest)
}

// findMappingValue returns the value node for the given key in a MappingNode.
// YAML mappings are stored as alternating key/value pairs in Content.
func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// findItemByName finds the first mapping in a SequenceNode whose "name" field
// equals nameVal. Used for containers[name=app] style selectors.
func findItemByName(seq *yaml.Node, nameVal string) *yaml.Node {
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		n := findMappingValue(item, "name")
		if n != nil && n.Value == nameVal {
			return item
		}
	}
	return nil
}

// parseArraySelector parses "containers[name=app]" into ("containers", "app", true).
// Returns ("", "", false) if the part has no array selector.
func parseArraySelector(part string) (key, nameVal string, ok bool) {
	idx := strings.Index(part, "[")
	if idx < 0 {
		return "", "", false
	}
	key = part[:idx]
	inner := strings.TrimSuffix(part[idx+1:], "]")
	// Only support "name=<value>" selectors.
	eqIdx := strings.Index(inner, "=")
	if eqIdx < 0 {
		return "", "", false
	}
	attrKey := inner[:eqIdx]
	if attrKey != "name" {
		return "", "", false
	}
	nameVal = inner[eqIdx+1:]
	return key, nameVal, true
}
