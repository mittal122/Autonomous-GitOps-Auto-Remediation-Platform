package remediator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/autosre/agent/internal/gitwriter"
)

// RightSizeMemory sets the memory limit for a container to an absolute value
// derived from observed usage data (VPA recommendations or Prometheus metrics).
//
// Unlike BumpMemoryLimit which reactively multiplies by a factor after an OOM event,
// RightSizeMemory is a proactive FinOps action: it sets the limit to exactly
// what the workload actually needs, reducing both cost and OOM risk.
type RightSizeMemory struct {
	Writer     *gitwriter.Writer
	Namespace  string
	Deployment string
	Container  string
	TargetMB   int // absolute target in mebibytes, e.g. 256 → "256Mi"

	dryRun  bool
	applied *gitwriter.Result
	log     *slog.Logger
}

func NewRightSizeMemory(
	w *gitwriter.Writer,
	namespace, deployment, container string,
	targetMB int,
	dryRun bool,
	log *slog.Logger,
) *RightSizeMemory {
	return &RightSizeMemory{
		Writer:     w,
		Namespace:  namespace,
		Deployment: deployment,
		Container:  container,
		TargetMB:   targetMB,
		dryRun:     dryRun,
		log:        log,
	}
}

func (a *RightSizeMemory) Name() string { return "right-size-memory" }

func (a *RightSizeMemory) limitFieldPath() string {
	return "spec.template.spec.containers[name=" + a.Container + "].resources.limits.memory"
}

func (a *RightSizeMemory) requestFieldPath() string {
	return "spec.template.spec.containers[name=" + a.Container + "].resources.requests.memory"
}

// targetValue returns the memory quantity string for the given MB value.
func (a *RightSizeMemory) targetValue() string {
	return fmt.Sprintf("%dMi", a.TargetMB)
}

// requestValue returns 75% of the limit (requests < limits is best practice).
func (a *RightSizeMemory) requestValue() string {
	req := int(float64(a.TargetMB) * 0.75)
	if req < 1 {
		req = 1
	}
	return fmt.Sprintf("%dMi", req)
}

func (a *RightSizeMemory) DryRun(ctx context.Context) (string, error) {
	if a.TargetMB <= 0 {
		return "", fmt.Errorf("right-size-memory: TargetMB must be > 0")
	}
	// Dry-run the limit change to get the diff.
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.limitFieldPath(), a.targetValue(), true)
	if err != nil {
		return "", fmt.Errorf("right-size-memory dry-run: %w", err)
	}
	if res.NoOp {
		return fmt.Sprintf("right-size-memory: no-op — %s/%s[%s] memory limit already %s",
			a.Namespace, a.Deployment, a.Container, a.targetValue()), nil
	}
	return fmt.Sprintf("right-size-memory: would set %s/%s[%s] memory limit %s → %s, request → %s\n%s",
		a.Namespace, a.Deployment, a.Container,
		res.OldValue, a.targetValue(), a.requestValue(), res.Diff), nil
}

func (a *RightSizeMemory) Apply(ctx context.Context) error {
	if a.applied != nil {
		return fmt.Errorf("right-size-memory: already applied (sha=%s)", a.applied.CommitSHA)
	}
	if a.TargetMB <= 0 {
		return fmt.Errorf("right-size-memory: TargetMB must be > 0")
	}

	// Set limit first.
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.limitFieldPath(), a.targetValue(), false)
	if err != nil {
		return fmt.Errorf("right-size-memory: set limit: %w", err)
	}

	// Set request to 75% of limit (best-effort; non-fatal if field path doesn't exist yet).
	_, _ = a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.requestFieldPath(), a.requestValue(), false)

	a.applied = &res
	a.log.InfoContext(ctx, "right-size-memory: applied",
		"namespace", a.Namespace, "deployment", a.Deployment, "container", a.Container,
		"old", res.OldValue, "limit", a.targetValue(), "request", a.requestValue(),
		"sha", res.CommitSHA, "noop", res.NoOp)
	return nil
}

func (a *RightSizeMemory) Rollback(ctx context.Context) error {
	if a.applied == nil {
		return fmt.Errorf("right-size-memory: cannot rollback — Apply has not been called")
	}
	if a.applied.NoOp {
		a.log.InfoContext(ctx, "right-size-memory: rollback skipped — original apply was a no-op")
		return nil
	}
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.limitFieldPath(), a.applied.OldValue, false)
	if err != nil {
		return fmt.Errorf("right-size-memory: rollback edit failed: %w", err)
	}
	a.log.InfoContext(ctx, "right-size-memory: rolled back",
		"namespace", a.Namespace, "deployment", a.Deployment,
		"restored", a.applied.OldValue, "sha", res.CommitSHA)
	return nil
}
