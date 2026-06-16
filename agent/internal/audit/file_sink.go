package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileSink is an append-only JSONL audit log backed by a single file.
// Each call to Record writes one JSON line. Query re-reads the file from disk.
//
// Append-only guarantee: only Record (O_APPEND|O_CREATE|O_WRONLY) is used for
// writes; there are no seek, truncate, or rewrite operations in this file.
type FileSink struct {
	mu   sync.Mutex
	path string
	f    *os.File
	enc  *json.Encoder
}

// NewFileSink opens (or creates) the JSONL file at path and returns a FileSink.
// Parent directories are created automatically.
func NewFileSink(path string) (*FileSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("audit: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	return &FileSink{
		path: path,
		f:    f,
		enc:  json.NewEncoder(f),
	}, nil
}

// Record appends ev as a single JSON line. The mutex serialises concurrent writes.
func (s *FileSink) Record(_ context.Context, ev AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.enc.Encode(ev); err != nil {
		return fmt.Errorf("audit: encode: %w", err)
	}
	return nil
}

// Query reads the file from the beginning and returns matching events.
// It opens a separate read-only file descriptor so writes can continue concurrently.
func (s *FileSink) Query(_ context.Context, f QueryFilter) ([]AuditEvent, error) {
	rf, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("audit: open for query: %w", err)
	}
	defer rf.Close()

	var out []AuditEvent
	scanner := bufio.NewScanner(rf)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev AuditEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip malformed lines rather than aborting the query
		}
		if f.matches(ev) {
			out = append(out, ev)
			if f.Limit > 0 && len(out) >= f.Limit {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return out, fmt.Errorf("audit: scan: %w", err)
	}
	return out, nil
}

// Close flushes and closes the underlying file. No further writes should be made.
func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}

// compile-time interface assertion
var _ AuditSink = (*FileSink)(nil)
