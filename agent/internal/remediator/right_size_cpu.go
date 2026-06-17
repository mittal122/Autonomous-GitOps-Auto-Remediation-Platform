package remediator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/autosre/agent/internal/gitwriter"
)

// RightSizeCPU sets the CPU request (and optionally limit) for a container to
// an absolute millicore value derived from observed usage data.
//
// This is a proactive FinOps action: it right-sizes over-provisioned CPU
// resources to reduce cost without affecting workload performance.
// CPU limits are set to 2× the request to allow burst capacity.
type RightSizeCPU struct {
	Writer       *gitwriter.Writer
	Namespace    string
	Deployment   string
	Container    string
	TargetMillis int // target CPU in millicores, e.g. 250 → "250m"

	dryRun  bool
	applied *gitwriter.Result
	log     *slog.Logger
}

func NewRightSizeCPU(
	w *gitwriter.Writer,
	namespace, deployment, container string,
	targetMillis int,
	dryRun bool,
	log *slog.Logger,
) *RightSizeCPU {
	return &RightSizeCPU{
		Writer:       w,
		Namespace:    namespace,
		Deployment:   deployment,
		Container:    container,
		TargetMillis: targetMillis,
		dryRun:       dryRun,
		log:          log,
	}
}

func (a *RightSizeCPU) Name() string { return "right-size-cpu" }

func (a *RightSizeCPU) requestFieldPath() string {
	return "spec.template.spec.containers[name=" + a.Container + "].resources.requests.cpu"
}

func (a *RightSizeCPU) limitFieldPath() string {
	return "spec.template.spec.containers[name=" + a.Container + "].resources.limits.cpu"
}

func (a *RightSizeCPU) requestValue() string {
	return fmt.Sprintf("%dm", a.TargetMillis)
}

// limitValue sets CPU limit to 2× request to allow burst without throttling.
func (a *RightSizeCPU) limitValue() string {
	return fmt.Sprintf("%dm", a.TargetMillis*2)
}

func (a *RightSizeCPU) DryRun(ctx context.Context) (string, error) {
	if a.TargetMillis <= 0 {
		return "", fmt.Errorf("right-size-cpu: TargetMillis must be > 0")
	}
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.requestFieldPath(), a.requestValue(), true)
	if err != nil {
		return "", fmt.Errorf("right-size-cpu dry-run: %w", err)
	}
	if res.NoOp {
		return fmt.Sprintf("right-size-cpu: no-op — %s/%s[%s] CPU request already %s",
			a.Namespace, a.Deployment, a.Container, a.requestValue()), nil
	}
	return fmt.Sprintf("right-size-cpu: would set %s/%s[%s] CPU request %s → %s, limit → %s\n%s",
		a.Namespace, a.Deployment, a.Container,
		res.OldValue, a.requestValue(), a.limitValue(), res.Diff), nil
}

func (a *RightSizeCPU) Apply(ctx context.Context) error {
	if a.applied != nil {
		return fmt.Errorf("right-size-cpu: already applied (sha=%s)", a.applied.CommitSHA)
	}
	if a.TargetMillis <= 0 {
		return fmt.Errorf("right-size-cpu: TargetMillis must be > 0")
	}

	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.requestFieldPath(), a.requestValue(), false)
	if err != nil {
		return fmt.Errorf("right-size-cpu: set request: %w", err)
	}

	// Set limit to 2× request (best-effort).
	_, _ = a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.limitFieldPath(), a.limitValue(), false)

	a.applied = &res
	a.log.InfoContext(ctx, "right-size-cpu: applied",
		"namespace", a.Namespace, "deployment", a.Deployment, "container", a.Container,
		"old", res.OldValue, "request", a.requestValue(), "limit", a.limitValue(),
		"sha", res.CommitSHA, "noop", res.NoOp)
	return nil
}

func (a *RightSizeCPU) Rollback(ctx context.Context) error {
	if a.applied == nil {
		return fmt.Errorf("right-size-cpu: cannot rollback — Apply has not been called")
	}
	if a.applied.NoOp {
		a.log.InfoContext(ctx, "right-size-cpu: rollback skipped — original apply was a no-op")
		return nil
	}
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.requestFieldPath(), a.applied.OldValue, false)
	if err != nil {
		return fmt.Errorf("right-size-cpu: rollback edit failed: %w", err)
	}
	a.log.InfoContext(ctx, "right-size-cpu: rolled back",
		"namespace", a.Namespace, "deployment", a.Deployment,
		"restored", a.applied.OldValue, "sha", res.CommitSHA)
	return nil
}
