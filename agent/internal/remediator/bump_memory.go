package remediator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/autosre/agent/internal/gitwriter"
)

const defaultBumpFactor = 1.5

// BumpMemoryLimit increases the memory limit for a container by multiplying
// the current value by Factor. The change is committed to the GitOps config
// repo; the cluster is never touched directly.
type BumpMemoryLimit struct {
	Writer     *gitwriter.Writer
	Namespace  string
	Deployment string
	Container  string
	Factor     float64 // multiplier; defaults to defaultBumpFactor (1.5)

	dryRun  bool
	applied *gitwriter.Result
	log     *slog.Logger
}

func NewBumpMemoryLimit(
	w *gitwriter.Writer,
	namespace, deployment, container string,
	factor float64,
	dryRun bool,
	log *slog.Logger,
) *BumpMemoryLimit {
	if factor <= 0 {
		factor = defaultBumpFactor
	}
	return &BumpMemoryLimit{
		Writer:     w,
		Namespace:  namespace,
		Deployment: deployment,
		Container:  container,
		Factor:     factor,
		dryRun:     dryRun,
		log:        log,
	}
}

func (a *BumpMemoryLimit) Name() string { return "bump-memory-limit" }

func (a *BumpMemoryLimit) fieldPath() string {
	return "spec.template.spec.containers[name=" + a.Container + "].resources.limits.memory"
}

func (a *BumpMemoryLimit) computeNewValue(ctx context.Context) (current, bumped string, err error) {
	current, err = a.Writer.GetCurrentValue(a.Namespace, a.Deployment, "Deployment", a.fieldPath())
	if err != nil {
		return "", "", fmt.Errorf("bump-memory-limit: read current value: %w", err)
	}
	bumped, err = gitwriter.BumpQuantity(current, a.Factor)
	if err != nil {
		return "", "", fmt.Errorf("bump-memory-limit: compute new value: %w", err)
	}
	return current, bumped, nil
}

func (a *BumpMemoryLimit) DryRun(ctx context.Context) (string, error) {
	current, bumped, err := a.computeNewValue(ctx)
	if err != nil {
		return "", err
	}
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.fieldPath(), bumped, true)
	if err != nil {
		return "", err
	}
	if res.NoOp {
		return fmt.Sprintf("bump-memory-limit: no-op — %s/%s[%s] memory limit already %s",
			a.Namespace, a.Deployment, a.Container, current), nil
	}
	return fmt.Sprintf("bump-memory-limit: would set %s/%s[%s] memory limit %s → %s (×%.2f)\n%s",
		a.Namespace, a.Deployment, a.Container, current, bumped, a.Factor, res.Diff), nil
}

func (a *BumpMemoryLimit) Apply(ctx context.Context) error {
	if a.applied != nil {
		return fmt.Errorf("bump-memory-limit: already applied (sha=%s)", a.applied.CommitSHA)
	}
	_, bumped, err := a.computeNewValue(ctx)
	if err != nil {
		return err
	}
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.fieldPath(), bumped, false)
	if err != nil {
		return err
	}
	a.applied = &res
	a.log.InfoContext(ctx, "bump-memory-limit: applied",
		"namespace", a.Namespace, "deployment", a.Deployment, "container", a.Container,
		"old", res.OldValue, "new", res.NewValue, "sha", res.CommitSHA, "noop", res.NoOp)
	return nil
}

func (a *BumpMemoryLimit) Rollback(ctx context.Context) error {
	if a.applied == nil {
		return fmt.Errorf("bump-memory-limit: cannot rollback — Apply has not been called")
	}
	if a.applied.NoOp {
		a.log.InfoContext(ctx, "bump-memory-limit: rollback skipped — original apply was a no-op")
		return nil
	}
	undo := NewBumpMemoryLimit(a.Writer, a.Namespace, a.Deployment, a.Container,
		0 /* unused — we set target directly */, false, a.log)
	// Restore previous value directly instead of using the factor again.
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.fieldPath(), a.applied.OldValue, false)
	if err != nil {
		return fmt.Errorf("bump-memory-limit: rollback edit failed: %w", err)
	}
	_ = undo
	a.log.InfoContext(ctx, "bump-memory-limit: rolled back",
		"namespace", a.Namespace, "deployment", a.Deployment,
		"restored", a.applied.OldValue, "sha", res.CommitSHA)
	return nil
}
