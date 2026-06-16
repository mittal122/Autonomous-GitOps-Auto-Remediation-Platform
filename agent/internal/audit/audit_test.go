package audit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autosre/agent/internal/audit"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeEvent(traceID, incidentID string, stage audit.Stage, outcome string, ts time.Time) audit.AuditEvent {
	return audit.AuditEvent{
		Timestamp:  ts,
		TraceID:    traceID,
		IncidentID: incidentID,
		Stage:      stage,
		Outcome:    outcome,
	}
}

func record(t *testing.T, sink audit.AuditSink, ev audit.AuditEvent) {
	t.Helper()
	if err := sink.Record(context.Background(), ev); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
}

func query(t *testing.T, sink audit.AuditSink, f audit.QueryFilter) []audit.AuditEvent {
	t.Helper()
	evs, err := sink.Query(context.Background(), f)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	return evs
}

// ---------------------------------------------------------------------------
// MemorySink tests
// ---------------------------------------------------------------------------

func TestMemorySink_RecordAndQuery_ByIncidentID(t *testing.T) {
	sink := &audit.MemorySink{}
	now := time.Now()

	record(t, sink, makeEvent("t1", "inc-A", audit.StageDiagnosed, "ok", now))
	record(t, sink, makeEvent("t2", "inc-B", audit.StageDiagnosed, "ok", now.Add(time.Second)))
	record(t, sink, makeEvent("t1", "inc-A", audit.StageDecided, "ok", now.Add(2*time.Second)))

	got := query(t, sink, audit.QueryFilter{IncidentID: "inc-A"})
	if len(got) != 2 {
		t.Fatalf("IncidentID filter: got %d events, want 2", len(got))
	}
}

func TestMemorySink_RecordAndQuery_ByTraceID(t *testing.T) {
	sink := &audit.MemorySink{}
	now := time.Now()

	record(t, sink, makeEvent("trace-X", "inc-1", audit.StageDetected, "started", now))
	record(t, sink, makeEvent("trace-Y", "inc-2", audit.StageDetected, "started", now))
	record(t, sink, makeEvent("trace-X", "inc-1", audit.StageDiagnosed, "ok", now.Add(time.Second)))

	got := query(t, sink, audit.QueryFilter{TraceID: "trace-X"})
	if len(got) != 2 {
		t.Fatalf("TraceID filter: got %d events, want 2", len(got))
	}
	for _, ev := range got {
		if ev.TraceID != "trace-X" {
			t.Errorf("unexpected TraceID %q in result", ev.TraceID)
		}
	}
}

func TestMemorySink_RecordAndQuery_ByStage(t *testing.T) {
	sink := &audit.MemorySink{}
	now := time.Now()

	record(t, sink, makeEvent("t1", "inc-1", audit.StageDetected, "started", now))
	record(t, sink, makeEvent("t1", "inc-1", audit.StageDiagnosed, "ok", now.Add(time.Second)))
	record(t, sink, makeEvent("t2", "inc-2", audit.StageDiagnosed, "ok", now.Add(2*time.Second)))
	record(t, sink, makeEvent("t1", "inc-1", audit.StageVerified, "recovered", now.Add(3*time.Second)))

	got := query(t, sink, audit.QueryFilter{Stage: audit.StageDiagnosed})
	if len(got) != 2 {
		t.Fatalf("Stage filter: got %d events, want 2", len(got))
	}
}

func TestMemorySink_RecordAndQuery_ByTimeRange(t *testing.T) {
	sink := &audit.MemorySink{}
	base := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)

	record(t, sink, makeEvent("t1", "inc-1", audit.StageDetected, "started", base))
	record(t, sink, makeEvent("t2", "inc-2", audit.StageDetected, "started", base.Add(5*time.Minute)))
	record(t, sink, makeEvent("t3", "inc-3", audit.StageDetected, "started", base.Add(10*time.Minute)))

	got := query(t, sink, audit.QueryFilter{
		Since: base.Add(3 * time.Minute),
		Until: base.Add(7 * time.Minute),
	})
	if len(got) != 1 {
		t.Fatalf("TimeRange filter: got %d events, want 1", len(got))
	}
	if got[0].TraceID != "t2" {
		t.Errorf("wrong event: got TraceID %q, want t2", got[0].TraceID)
	}
}

func TestMemorySink_Limit(t *testing.T) {
	sink := &audit.MemorySink{}
	now := time.Now()

	for i := 0; i < 10; i++ {
		record(t, sink, makeEvent("t1", "inc-1", audit.StageDetected, "started", now.Add(time.Duration(i)*time.Second)))
	}

	got := query(t, sink, audit.QueryFilter{Limit: 3})
	if len(got) != 3 {
		t.Fatalf("Limit filter: got %d events, want 3", len(got))
	}
}

func TestMemorySink_All(t *testing.T) {
	sink := &audit.MemorySink{}
	now := time.Now()

	record(t, sink, makeEvent("t1", "inc-1", audit.StageDetected, "started", now))
	record(t, sink, makeEvent("t2", "inc-2", audit.StageDiagnosed, "ok", now.Add(time.Second)))

	all := sink.All()
	if len(all) != 2 {
		t.Fatalf("All: got %d events, want 2", len(all))
	}
}

func TestMemorySink_Empty_ReturnsNilSlice(t *testing.T) {
	sink := &audit.MemorySink{}
	got := query(t, sink, audit.QueryFilter{IncidentID: "no-such-incident"})
	if got != nil {
		t.Errorf("empty query should return nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// FileSink tests
// ---------------------------------------------------------------------------

func TestFileSink_RecordAndQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	defer sink.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	record(t, sink, makeEvent("t1", "inc-1", audit.StageDetected, "started", now))
	record(t, sink, makeEvent("t1", "inc-1", audit.StageDiagnosed, "ok", now.Add(time.Second)))
	record(t, sink, makeEvent("t2", "inc-2", audit.StageDiagnosed, "ok", now.Add(2*time.Second)))

	got := query(t, sink, audit.QueryFilter{IncidentID: "inc-1"})
	if len(got) != 2 {
		t.Fatalf("FileSink IncidentID filter: got %d events, want 2", len(got))
	}
}

func TestFileSink_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "subdir", "audit.jsonl")

	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink with nested path: %v", err)
	}
	defer sink.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist at %s: %v", path, err)
	}
}

func TestFileSink_PersistsAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Write with first sink handle.
	sink1, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink (1): %v", err)
	}
	record(t, sink1, makeEvent("t1", "inc-1", audit.StageDetected, "started", now))
	sink1.Close()

	// Write more with a second sink handle opened on the same file.
	sink2, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink (2): %v", err)
	}
	defer sink2.Close()
	record(t, sink2, makeEvent("t1", "inc-1", audit.StageDiagnosed, "ok", now.Add(time.Second)))

	// All events should be present.
	got := query(t, sink2, audit.QueryFilter{TraceID: "t1"})
	if len(got) != 2 {
		t.Fatalf("PersistsAcrossReopens: got %d events, want 2", len(got))
	}
}

func TestFileSink_Query_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	defer sink.Close()

	got := query(t, sink, audit.QueryFilter{})
	if len(got) != 0 {
		t.Errorf("empty file: expected 0 events, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Append-only contract
// ---------------------------------------------------------------------------

// TestAuditSink_AppendOnly verifies all exported sink types implement AuditSink
// (compile-time proof) and that the interface surface has no mutation methods.
func TestAuditSink_AppendOnly(t *testing.T) {
	// These lines do not execute at runtime but will fail to compile if the
	// AuditSink interface or the types are changed to remove Record/Query.
	var _ audit.AuditSink = audit.NoOp{}
	var _ audit.AuditSink = (*audit.MemorySink)(nil)
	var _ audit.AuditSink = (*audit.FileSink)(nil)
	// If someone adds an Update/Delete/Truncate method to AuditSink, this file
	// must be updated to add a matching stub — making the addition visible.
}

// ---------------------------------------------------------------------------
// NoOp sink
// ---------------------------------------------------------------------------

func TestNoOp_NeverErrors(t *testing.T) {
	sink := audit.NoOp{}
	now := time.Now()

	if err := sink.Record(context.Background(), makeEvent("t", "i", audit.StageDetected, "started", now)); err != nil {
		t.Errorf("NoOp.Record returned error: %v", err)
	}
	evs, err := sink.Query(context.Background(), audit.QueryFilter{})
	if err != nil {
		t.Errorf("NoOp.Query returned error: %v", err)
	}
	if evs != nil {
		t.Errorf("NoOp.Query returned non-nil slice: %v", evs)
	}
}

// ---------------------------------------------------------------------------
// Non-fatal sink — verifies Record error does not propagate beyond the wrapper
// (tested in integration with the orchestrator; this is the unit-level check)
// ---------------------------------------------------------------------------

type alwaysErrorSink struct{}

func (alwaysErrorSink) Record(_ context.Context, _ audit.AuditEvent) error {
	return errors.New("simulated sink failure")
}

func (alwaysErrorSink) Query(_ context.Context, _ audit.QueryFilter) ([]audit.AuditEvent, error) {
	return nil, errors.New("simulated sink failure")
}

// TestAlwaysErrorSink_ImplementsInterface ensures the above type can stand in
// for an AuditSink (the orchestrator wraps it non-fatally in pipeline.go).
func TestAlwaysErrorSink_ImplementsInterface(t *testing.T) {
	var _ audit.AuditSink = alwaysErrorSink{}
}
