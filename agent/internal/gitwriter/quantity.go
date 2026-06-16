package gitwriter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// knownSuffixes lists all supported Kubernetes memory-quantity suffixes,
// longest first so that "Mi" is matched before "M".
var knownSuffixes = []struct {
	suffix    string
	bytesEach int64
}{
	{"Ki", 1024},
	{"Mi", 1024 * 1024},
	{"Gi", 1024 * 1024 * 1024},
	{"Ti", 1024 * 1024 * 1024 * 1024},
	{"K", 1000},
	{"M", 1000 * 1000},
	{"G", 1000 * 1000 * 1000},
	{"T", 1000 * 1000 * 1000 * 1000},
}

// BumpQuantity multiplies a Kubernetes memory quantity string by factor and
// returns a new quantity string using the same suffix as the input.
// The numeric result is rounded up to the nearest integer (ceil).
//
//	BumpQuantity("256Mi", 1.5)  →  "384Mi"
//	BumpQuantity("1Gi",   2.0)  →  "2Gi"
//	BumpQuantity("512",   1.5)  →  "768"  (raw bytes)
func BumpQuantity(current string, factor float64) (string, error) {
	if factor <= 0 {
		return "", fmt.Errorf("factor must be positive, got %g", factor)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return "", fmt.Errorf("empty quantity")
	}

	var suffix string
	for _, u := range knownSuffixes {
		if strings.HasSuffix(current, u.suffix) {
			suffix = u.suffix
			break
		}
	}

	numStr := strings.TrimSuffix(current, suffix)
	val, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil {
		return "", fmt.Errorf("cannot parse numeric part of quantity %q: %w", current, err)
	}
	if val <= 0 {
		return "", fmt.Errorf("quantity value must be positive, got %g in %q", val, current)
	}

	newVal := int64(math.Ceil(val * factor))
	if suffix == "" {
		return strconv.FormatInt(newVal, 10), nil
	}
	return fmt.Sprintf("%d%s", newVal, suffix), nil
}

// ParseQuantityBytes converts a Kubernetes memory quantity string to bytes (int64).
// Used for validation and comparison; not required by BumpQuantity itself.
func ParseQuantityBytes(q string) (int64, error) {
	q = strings.TrimSpace(q)
	for _, u := range knownSuffixes {
		if strings.HasSuffix(q, u.suffix) {
			numStr := strings.TrimSuffix(q, u.suffix)
			val, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
			if err != nil {
				return 0, fmt.Errorf("cannot parse %q: %w", q, err)
			}
			return int64(val * float64(u.bytesEach)), nil
		}
	}
	// Raw bytes
	val, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as bytes: %w", q, err)
	}
	return val, nil
}
