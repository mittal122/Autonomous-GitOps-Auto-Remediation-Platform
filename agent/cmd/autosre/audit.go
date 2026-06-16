package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
)

// runAudit implements `autosre audit` — queries the append-only JSONL audit log.
//
// Usage:
//
//	autosre audit [--incident <id>] [--trace <id>] [--since <duration>]
//	              [--stage <stage>] [--limit <n>] [--json]
func runAudit(args []string, cfg config.Config, _ *slog.Logger) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	incidentID := fs.String("incident", "", "filter by incident ID")
	traceID := fs.String("trace", "", "filter by trace ID")
	since := fs.String("since", "", "filter events since this duration ago (e.g. 1h, 24h)")
	stage := fs.String("stage", "", "filter by stage (Detected, Diagnosed, Decided, DryRun, Applied, Verified, Notified, Escalated, ApprovalRequested, ApprovalResolved)")
	limit := fs.Int("limit", 100, "maximum number of events to return (0 = no limit)")
	asJSON := fs.Bool("json", false, "output raw JSON lines instead of human-readable format")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "audit: %v\n", err)
		return 1
	}

	// Build filter.
	f := audit.QueryFilter{
		IncidentID: *incidentID,
		TraceID:    *traceID,
		Stage:      audit.Stage(*stage),
		Limit:      *limit,
	}
	if *since != "" {
		d, err := time.ParseDuration(*since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "audit: invalid --since %q: %v\n", *since, err)
			return 1
		}
		f.Since = time.Now().Add(-d)
	}

	// Open the file sink in read-only mode using a fresh FileSink (Query only).
	path := cfg.Audit.FilePath
	sink, err := audit.NewFileSink(path)
	if err != nil {
		// If the file does not exist yet, treat as empty.
		fmt.Fprintln(os.Stderr, "no audit records found (file does not exist or cannot be opened)")
		return 0
	}
	defer sink.Close()

	events, err := sink.Query(context.Background(), f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit: query failed: %v\n", err)
		return 1
	}

	if len(events) == 0 {
		fmt.Fprintln(os.Stdout, "no audit records match the given filter")
		return 0
	}

	for _, ev := range events {
		if *asJSON {
			b, _ := json.Marshal(ev)
			fmt.Fprintln(os.Stdout, string(b))
		} else {
			printAuditEvent(ev)
		}
	}
	return 0
}

func printAuditEvent(ev audit.AuditEvent) {
	fmt.Printf("[%s] trace=%-18s incident=%-20s stage=%-22s outcome=%s\n",
		ev.Timestamp.Format(time.RFC3339),
		ev.TraceID,
		ev.IncidentID,
		ev.Stage,
		ev.Outcome,
	)
	for k, v := range ev.Details {
		fmt.Printf("  %-20s = %s\n", k, v)
	}
}
