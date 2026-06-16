package outcome

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Client posts outcome records to the learner's POST /outcome endpoint.
// If Addr is empty, Report is a no-op (learning disabled).
type Client struct {
	addr string
	http *http.Client
	log  *slog.Logger
}

// NewClient creates an outcome Client targeting addr (e.g. "http://localhost:8002").
// timeout is the per-request deadline; 5s is a reasonable default.
func NewClient(addr string, timeout time.Duration, log *slog.Logger) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		addr: addr,
		http: &http.Client{Timeout: timeout},
		log:  log,
	}
}

// Report serialises rec and POSTs it to addr/outcome.
// Errors are returned so callers (the orchestrator's non-fatal wrapper) can log them.
func (c *Client) Report(ctx context.Context, rec Record) error {
	if c.addr == "" {
		return nil // learning disabled
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("outcome: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.addr+"/outcome",
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("outcome: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("outcome: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("outcome: learner returned %d", resp.StatusCode)
	}
	c.log.DebugContext(ctx, "outcome: posted",
		"incident_id", rec.IncidentID, "trace_id", rec.TraceID,
		"failure_mode", rec.FailureMode, "applied", rec.Applied,
		"verification", rec.VerificationOutcome,
	)
	return nil
}

// compile-time interface assertion
var _ Reporter = (*Client)(nil)
