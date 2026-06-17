// Package diagnosis provides an HTTP client for the Python diagnoser service.
//
// The client posts an Incident to POST /diagnose and unmarshals the Diagnosis
// response. It is advisory-only: the returned Diagnosis must pass through the
// policy engine before any action is taken.
//
// Retry policy: up to 3 retries with exponential backoff (2s / 4s / 8s).
// 4xx errors are not retried (the request itself is malformed).
// If all retries are exhausted the caller receives an error and the policy
// engine's fail-closed behaviour prevents any autonomous action.
package diagnosis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/autosre/agent/internal/contracts"
)

// Config holds the connection settings for the diagnoser HTTP service.
type Config struct {
	// Addr is the base URL of the diagnoser service, e.g. "http://localhost:8001".
	Addr string
	// Timeout is the per-request deadline. Default: 35s (longer than the LLM timeout).
	Timeout time.Duration
	// MaxRetries is the maximum number of retry attempts after the first failure.
	// Default: 3.  Set to 0 to disable retries.
	MaxRetries int
}

// Client is an HTTP client for the Python diagnoser service.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient creates a Client. If cfg.Timeout is zero, 35 seconds is used.
func NewClient(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 35 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// retryBackoffs contains the sleep durations between successive attempts.
var retryBackoffs = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

// Diagnose posts an Incident to the diagnoser service and returns the Diagnosis.
// Transient failures (network errors, 5xx) are retried with exponential backoff.
// If the service is unreachable after all retries, Diagnose returns an error.
func (c *Client) Diagnose(ctx context.Context, incident contracts.Incident) (contracts.Diagnosis, error) {
	var lastErr error
	maxAttempts := 1 + c.cfg.MaxRetries

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := retryBackoffs[min(attempt-1, len(retryBackoffs)-1)]
			select {
			case <-ctx.Done():
				return contracts.Diagnosis{}, fmt.Errorf("diagnosis: context cancelled during retry: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}

		diag, err, retryable := c.doRequest(ctx, incident)
		if err == nil {
			return diag, nil
		}
		lastErr = err
		if !retryable {
			return contracts.Diagnosis{}, err // 4xx: don't retry
		}
	}
	return contracts.Diagnosis{}, fmt.Errorf("diagnosis: %d attempts exhausted: %w", maxAttempts, lastErr)
}

// doRequest performs a single POST /diagnose call.
// Returns (diagnosis, nil, false) on success.
// Returns (zero, err, retryable) on failure; retryable=false on 4xx.
func (c *Client) doRequest(ctx context.Context, incident contracts.Incident) (contracts.Diagnosis, error, bool) {
	body, err := json.Marshal(incidentToDTO(incident))
	if err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: marshal incident: %w", err), false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.Addr+"/diagnose", bytes.NewReader(body))
	if err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: build request: %w", err), false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: POST /diagnose: %w", err), true // network error → retryable
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return contracts.Diagnosis{},
			fmt.Errorf("diagnosis: service returned %d (client error; not retrying)", resp.StatusCode),
			false
	}
	if resp.StatusCode != http.StatusOK {
		return contracts.Diagnosis{},
			fmt.Errorf("diagnosis: service returned %d", resp.StatusCode),
			true // 5xx → retryable
	}

	var dto diagnosisDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: decode response: %w", err), false
	}
	return dtoToDiagnosis(dto), nil, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// JSON DTOs — snake_case to match the Python FastAPI server
// ---------------------------------------------------------------------------

type signalDTO struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`
	Namespace string            `json:"namespace"`
	Resource  string            `json:"resource"`
	Severity  string            `json:"severity"`
	Kind      string            `json:"kind"`
	Reason    string            `json:"reason"`
	Message   string            `json:"message"`
	Labels    map[string]string `json:"labels"`
}

type incidentDTO struct {
	ID                string      `json:"id"`
	Signals           []signalDTO `json:"signals"`
	AffectedResources []string    `json:"affected_resources"`
	Severity          string      `json:"severity"`
	OpenedAt          time.Time   `json:"opened_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

type diagnosisDTO struct {
	IncidentID     string    `json:"incident_id"`
	RootCause      string    `json:"root_cause"`
	FailureMode    string    `json:"failure_mode"`
	ProposedAction string    `json:"proposed_action"`
	Confidence     float64   `json:"confidence"`
	BlastRadius    string    `json:"blast_radius"`
	Source         string    `json:"source"`
	DiagnosedAt    time.Time `json:"diagnosed_at"`
}

func incidentToDTO(inc contracts.Incident) incidentDTO {
	sigs := make([]signalDTO, len(inc.Signals))
	for i, s := range inc.Signals {
		sigs[i] = signalDTO{
			ID:        s.ID,
			Source:    s.Source,
			Namespace: s.Namespace,
			Resource:  s.Resource,
			Severity:  s.Severity,
			Kind:      s.Kind,
			Reason:    s.Reason,
			Message:   s.Message,
			Labels:    s.Labels,
		}
	}
	return incidentDTO{
		ID:                inc.ID,
		Signals:           sigs,
		AffectedResources: inc.AffectedResources,
		Severity:          inc.Severity,
		OpenedAt:          inc.OpenedAt,
		UpdatedAt:         inc.UpdatedAt,
	}
}

func dtoToDiagnosis(d diagnosisDTO) contracts.Diagnosis {
	conf := d.Confidence
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	return contracts.Diagnosis{
		IncidentID:     d.IncidentID,
		RootCause:      d.RootCause,
		FailureMode:    d.FailureMode,
		ProposedAction: d.ProposedAction,
		Confidence:     conf,
		BlastRadius:    d.BlastRadius,
		Source:         d.Source,
		DiagnosedAt:    d.DiagnosedAt,
	}
}
