// Package orchestrator wires detect → diagnose → decide → (dry-run) remediate → verify → notify
// into a single bounded reconcile loop.
//
// Safety contract:
//   - DRY-RUN-ONLY BY DEFAULT: Apply() is never called unless cfg.ApplyEnabled is explicitly true.
//   - Kill switch: an atomic bool halts all apply calls immediately, regardless of verdict.
//   - Per-incident idempotency: the inFlightRegistry prevents the same incident ID from being
//     processed twice concurrently.
//   - Bounded concurrency: a buffered semaphore (chan struct{}) caps parallel pipeline goroutines.
//   - This package never bypasses any policy gate, never writes to the cluster directly, and
//     never changes the safety semantics of any underlying component.
package orchestrator

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
	"github.com/autosre/agent/internal/outcome"
	"github.com/autosre/agent/internal/policy"
)

// DiagnosisClient abstracts the HTTP call to the Python diagnoser service.
// The real implementation is *diagnosis.Client; tests supply a mock.
type DiagnosisClient interface {
	Diagnose(ctx context.Context, incident contracts.Incident) (contracts.Diagnosis, error)
}

// Verifiable abstracts the post-remediation recovery checker.
// The real implementation is *verifier.Verifier; tests supply a mock.
type Verifiable interface {
	Verify(ctx context.Context, incident contracts.Incident, remediationRef string) contracts.VerificationResult
}

// Orchestrator is the top-level control loop. Create one per process via New and call Run.
type Orchestrator struct {
	cfg      config.OrchestratorConfig
	diag     DiagnosisClient
	policy   *policy.Engine
	notifier contracts.Notifier
	verifier Verifiable
	builder  ActionBuilder
	log      *slog.Logger

	// sink records every pipeline stage event (non-fatal; nil → no-op).
	sink audit.AuditSink
	// outcomes posts completed pipeline summaries to the learner service (non-fatal; nil → no-op).
	outcomes outcome.Reporter

	// kill is the global halt; set via SetKillSwitch. Checked before every Apply call.
	kill atomic.Bool

	// sem bounds the number of concurrent pipeline goroutines.
	sem chan struct{}

	// inFlight prevents the same incident from being processed twice concurrently.
	inFlight inFlightRegistry
}

// New creates a fully wired Orchestrator. The ApplyEnabled flag in cfg is the ONLY path to
// real GitOps commits; leaving it false (the default) is safe and the recommended starting point.
//
// sink and reporter may both be nil; in that case audit recording and outcome posting are disabled.
func New(
	cfg config.OrchestratorConfig,
	diag DiagnosisClient,
	pol *policy.Engine,
	notif contracts.Notifier,
	ver Verifiable,
	builder ActionBuilder,
	sink audit.AuditSink,
	reporter outcome.Reporter,
	log *slog.Logger,
) *Orchestrator {
	workers := cfg.MaxWorkers
	if workers <= 0 {
		workers = 5
	}
	o := &Orchestrator{
		cfg:      cfg,
		diag:     diag,
		policy:   pol,
		notifier: notif,
		verifier: ver,
		builder:  builder,
		sink:     sink,
		outcomes: reporter,
		log:      log,
		sem:      make(chan struct{}, workers),
		inFlight: inFlightRegistry{ids: make(map[string]struct{})},
	}
	o.kill.Store(cfg.KillSwitch)
	return o
}

// record emits one audit event. Errors from the sink are logged and swallowed;
// a failing sink must never break the pipeline.
func (o *Orchestrator) record(ctx context.Context, traceID, incidentID string, stage audit.Stage, outcomeStr string, details map[string]string) {
	if o.sink == nil {
		return
	}
	ev := audit.AuditEvent{
		Timestamp:  time.Now(),
		TraceID:    traceID,
		IncidentID: incidentID,
		Stage:      stage,
		Outcome:    outcomeStr,
		Details:    details,
	}
	if err := o.sink.Record(ctx, ev); err != nil {
		o.log.WarnContext(ctx, "audit: record failed (non-fatal)", "error", err, "stage", stage)
	}
}

// reportOutcome posts a completed pipeline summary to the learner service.
// Errors are logged and swallowed; a failing learner must never break the pipeline.
func (o *Orchestrator) reportOutcome(ctx context.Context, rec outcome.Record) {
	if o.outcomes == nil {
		return
	}
	if err := o.outcomes.Report(ctx, rec); err != nil {
		o.log.WarnContext(ctx, "outcome: report failed (non-fatal)", "error", err,
			"incident_id", rec.IncidentID, "trace_id", rec.TraceID)
	}
}

// Run consumes IncidentEvents from the correlator and schedules a pipeline goroutine
// for each EventClosed incident. It returns when ctx is cancelled or events is closed.
func (o *Orchestrator) Run(ctx context.Context, events <-chan correlator.IncidentEvent) {
	o.log.InfoContext(ctx, "orchestrator: run loop started",
		"apply_enabled", o.cfg.ApplyEnabled,
		"kill_switch", o.kill.Load(),
		"max_workers", cap(o.sem),
	)
	for {
		select {
		case <-ctx.Done():
			o.log.InfoContext(ctx, "orchestrator: context cancelled; stopping run loop")
			return
		case ev, ok := <-events:
			if !ok {
				o.log.InfoContext(ctx, "orchestrator: events channel closed; stopping run loop")
				return
			}
			if ev.Kind == correlator.EventClosed {
				o.schedule(ctx, ev.Incident)
			}
		}
	}
}

// schedule acquires the semaphore and the in-flight lock, then launches a pipeline goroutine.
// It is non-blocking: if the semaphore is full, the goroutine waits; if ctx is already done,
// the resources are released and no goroutine is started.
func (o *Orchestrator) schedule(ctx context.Context, inc contracts.Incident) {
	if !o.inFlight.tryAcquire(inc.ID) {
		o.log.InfoContext(ctx, "orchestrator: incident already in-flight; skipping",
			"incident_id", inc.ID)
		return
	}
	// Acquire concurrency slot; release in-flight if ctx already cancelled.
	select {
	case o.sem <- struct{}{}:
	case <-ctx.Done():
		o.inFlight.release(inc.ID)
		return
	}
	go func() {
		defer func() {
			<-o.sem
			o.inFlight.release(inc.ID)
		}()
		o.runPipeline(ctx, inc)
	}()
}

// SetKillSwitch atomically engages or disengages the global halt.
// When true, no Apply calls are made regardless of the verdict or ApplyEnabled flag.
func (o *Orchestrator) SetKillSwitch(engaged bool) { o.kill.Store(engaged) }

// ApplyEnabled reports whether real GitOps commits are currently permitted.
func (o *Orchestrator) ApplyEnabled() bool { return o.cfg.ApplyEnabled }

// KillSwitchEngaged reports whether the kill switch is currently on.
func (o *Orchestrator) KillSwitchEngaged() bool { return o.kill.Load() }

// InFlightCount returns the number of pipeline goroutines currently running.
func (o *Orchestrator) InFlightCount() int { return o.inFlight.count() }

// ---------------------------------------------------------------------------
// inFlightRegistry — per-incident idempotency guard
// ---------------------------------------------------------------------------

type inFlightRegistry struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func (r *inFlightRegistry) tryAcquire(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.ids[id]; exists {
		return false
	}
	r.ids[id] = struct{}{}
	return true
}

func (r *inFlightRegistry) release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ids, id)
}

func (r *inFlightRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}
