package remediator

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/autosre/agent/internal/gitwriter"
)

// ScaleDeployment changes spec.replicas in the GitOps config repo.
// It never writes to the cluster; ArgoCD syncs the change.
type ScaleDeployment struct {
	Writer         *gitwriter.Writer
	Namespace      string
	Deployment     string
	TargetReplicas int

	dryRun  bool
	applied *gitwriter.Result
	log     *slog.Logger
}

func NewScaleDeployment(
	w *gitwriter.Writer,
	namespace, deployment string,
	targetReplicas int,
	dryRun bool,
	log *slog.Logger,
) *ScaleDeployment {
	return &ScaleDeployment{
		Writer:         w,
		Namespace:      namespace,
		Deployment:     deployment,
		TargetReplicas: targetReplicas,
		dryRun:         dryRun,
		log:            log,
	}
}

func (a *ScaleDeployment) Name() string { return "scale-deployment" }

func (a *ScaleDeployment) DryRun(ctx context.Context) (string, error) {
	res, err := a.Writer.EditField(ctx,
		a.Namespace, a.Deployment, "Deployment",
		"spec.replicas",
		strconv.Itoa(a.TargetReplicas),
		true,
	)
	if err != nil {
		return "", err
	}
	if res.NoOp {
		return fmt.Sprintf("scale-deployment: no-op — %s/%s already has %d replicas",
			a.Namespace, a.Deployment, a.TargetReplicas), nil
	}
	return fmt.Sprintf("scale-deployment: would set %s/%s replicas %s → %d\n%s",
		a.Namespace, a.Deployment, res.OldValue, a.TargetReplicas, res.Diff), nil
}

func (a *ScaleDeployment) Apply(ctx context.Context) error {
	if a.applied != nil {
		return fmt.Errorf("scale-deployment: already applied (sha=%s)", a.applied.CommitSHA)
	}
	res, err := a.Writer.EditField(ctx,
		a.Namespace, a.Deployment, "Deployment",
		"spec.replicas",
		strconv.Itoa(a.TargetReplicas),
		false,
	)
	if err != nil {
		return err
	}
	a.applied = &res
	a.log.InfoContext(ctx, "scale-deployment: applied",
		"namespace", a.Namespace, "deployment", a.Deployment,
		"replicas", a.TargetReplicas, "sha", res.CommitSHA, "noop", res.NoOp)
	return nil
}

func (a *ScaleDeployment) Rollback(ctx context.Context) error {
	if a.applied == nil {
		return fmt.Errorf("scale-deployment: cannot rollback — Apply has not been called")
	}
	if a.applied.NoOp {
		a.log.InfoContext(ctx, "scale-deployment: rollback skipped — original apply was a no-op")
		return nil
	}
	prevReplicas, err := strconv.Atoi(a.applied.OldValue)
	if err != nil {
		return fmt.Errorf("scale-deployment: cannot parse previous replicas %q: %w", a.applied.OldValue, err)
	}
	rollback := NewScaleDeployment(a.Writer, a.Namespace, a.Deployment, prevReplicas, false, a.log)
	if err := rollback.Apply(ctx); err != nil {
		return fmt.Errorf("scale-deployment: rollback failed: %w", err)
	}
	a.log.InfoContext(ctx, "scale-deployment: rolled back",
		"namespace", a.Namespace, "deployment", a.Deployment, "replicas", prevReplicas)
	return nil
}
