// Package uid provides a lightweight random ID generator.
// IDs are 16-char lowercase hex strings backed by crypto/rand.
package uid

import (
	"crypto/rand"
	"fmt"
)

// New returns a random 16-char hex string suitable for signal and incident IDs.
func New() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never errors on a supported OS
	return fmt.Sprintf("%x", b)
}
