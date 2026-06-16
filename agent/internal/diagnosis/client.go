// Package diagnosis provides an HTTP client for the Python diagnoser service.
//
// The client posts an Incident to POST /diagnose and unmarshals the Diagnosis
// response. It is advisory-only: the returned Diagnosis must pass through the
// policy engine before any action is taken.
//
// Fallback logic lives entirely in the Python service — this client returns
// an error when the service is unreachable so callers can decide what to do.
// The policy engine's fail-closed behaviour ensures no auto-action occurs
// without a valid Diagnosis.
//
// TODO (future prompt — orchestrator): wire this client into the control loop
// after the correlator emits a new Incident.
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
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// Diagnose posts an Incident to the diagnoser service and returns the Diagnosis.
// If the service is unreachable or returns an error status, Diagnose returns
// (zero Diagnosis, error). The caller must not auto-act on the error case.
func (c *Client) Diagnose(ctx context.Context, incident contracts.Incident) (contracts.Diagnosis, error) {
	body, err := json.Marshal(incidentToDTO(incident))
	if err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: marshal incident: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.Addr+"/diagnose", bytes.NewReader(body))
	if err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: POST /diagnose: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: service returned %d", resp.StatusCode)
	}

	var dto diagnosisDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return contracts.Diagnosis{}, fmt.Errorf("diagnosis: decode response: %w", err)
	}

	return dtoToDiagnosis(dto), nil
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
	// Clamp confidence defensively; the Python layer already does this,
	// but be explicit on the Go side too.
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
