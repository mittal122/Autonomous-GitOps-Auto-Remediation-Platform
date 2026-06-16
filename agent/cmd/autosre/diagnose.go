package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/diagnosis"
)

// runDiagnose is the entry point for `autosre diagnose [flags]`.
// It posts an Incident to the diagnoser service and prints the Diagnosis.
// It NEVER executes any remediation — advisory output only.
//
// TODO (future prompt — orchestrator): in the live loop, the orchestrator calls
// the diagnoser automatically after the correlator emits an open incident.
func runDiagnose(args []string, cfg config.Config, log *slog.Logger) int {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	addr := fs.String("diagnoser", cfg.Diagnoser.Addr, "Diagnoser HTTP service URL (overrides DIAGNOSER_ADDR)")
	incidentFile := fs.String("incident-file", "", "Path to incident JSON file (omit to use built-in fixture)")
	outputJSON := fs.Bool("json", false, "Output diagnosis as JSON")
	timeout := fs.Duration("timeout", cfg.Diagnoser.Timeout, "Request timeout")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *addr == "" {
		fmt.Fprintln(os.Stderr, "error: --diagnoser or DIAGNOSER_ADDR must be set")
		return 2
	}

	incident, err := loadIncident(*incidentFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading incident: %v\n", err)
		return 1
	}

	client := diagnosis.NewClient(diagnosis.Config{
		Addr:    *addr,
		Timeout: *timeout,
	})

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+5*time.Second)
	defer cancel()

	log.Info("diagnose: posting incident to diagnoser",
		"incident_id", incident.ID, "addr", *addr)

	d, err := client.Diagnose(ctx, incident)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diagnosis error: %v\n", err)
		log.Error("diagnose: failed", "error", err)
		return 1
	}

	if *outputJSON {
		return printDiagnosisJSON(d)
	}
	return printDiagnosisText(d)
}

func loadIncident(path string) (contracts.Incident, error) {
	if path == "" {
		// Use the built-in fixture.
		path = "internal/diagnosis/testdata/sample_incident.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return contracts.Incident{}, fmt.Errorf("read %s: %w", path, err)
	}
	// The JSON uses snake_case (matching Python), so we need a temporary DTO.
	var dto struct {
		ID                string `json:"id"`
		Severity          string `json:"severity"`
		AffectedResources []string `json:"affected_resources"`
		Signals           []struct {
			ID        string            `json:"id"`
			Source    string            `json:"source"`
			Namespace string            `json:"namespace"`
			Resource  string            `json:"resource"`
			Severity  string            `json:"severity"`
			Kind      string            `json:"kind"`
			Reason    string            `json:"reason"`
			Message   string            `json:"message"`
			Labels    map[string]string `json:"labels"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(data, &dto); err != nil {
		return contracts.Incident{}, fmt.Errorf("parse incident JSON: %w", err)
	}
	sigs := make([]contracts.Signal, len(dto.Signals))
	for i, s := range dto.Signals {
		sigs[i] = contracts.Signal{
			ID:        s.ID,
			Source:    s.Source,
			Namespace: s.Namespace,
			Resource:  s.Resource,
			Severity:  s.Severity,
			Kind:      s.Kind,
			Reason:    s.Reason,
			Message:   s.Message,
			Labels:    s.Labels,
			ReceivedAt: time.Now(),
		}
	}
	return contracts.Incident{
		ID:                dto.ID,
		Signals:           sigs,
		AffectedResources: dto.AffectedResources,
		Severity:          dto.Severity,
		OpenedAt:          time.Now(),
		UpdatedAt:         time.Now(),
	}, nil
}

func printDiagnosisText(d contracts.Diagnosis) int {
	fmt.Println("=== AutoSRE Diagnosis ===")
	fmt.Printf("Incident:        %s\n", d.IncidentID)
	fmt.Printf("Source:          %s\n", d.Source)
	fmt.Printf("Failure mode:    %s\n", d.FailureMode)
	fmt.Printf("Root cause:      %s\n", d.RootCause)
	fmt.Printf("Proposed action: %s\n", d.ProposedAction)
	fmt.Printf("Confidence:      %.4f\n", d.Confidence)
	fmt.Printf("Blast radius:    %s\n", d.BlastRadius)
	fmt.Printf("Diagnosed at:    %s\n", d.DiagnosedAt.Format(time.RFC3339))
	fmt.Println()
	fmt.Println("NOTE: this is advisory only — no action has been taken.")
	fmt.Println("      Pass the Diagnosis to `autosre policy` to get a verdict.")
	return 0
}

func printDiagnosisJSON(d contracts.Diagnosis) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
		return 1
	}
	return 0
}
