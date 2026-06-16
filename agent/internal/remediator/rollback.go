package remediator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/autosre/agent/internal/gitwriter"
)

// RollbackDeployment reverts a container image to a known-good tag by editing
// the image field in the GitOps config repo.
//
// If KnownGoodRef is empty, the previous value is discovered from git history
// via Writer.GetPreviousValue. This avoids hard-coding the tag in the policy rule.
type RollbackDeployment struct {
	Writer       *gitwriter.Writer
	Namespace    string
	Deployment   string
	Container    string
	KnownGoodRef string // if empty, discovered from git history

	dryRun  bool
	applied *gitwriter.Result
	log     *slog.Logger
}

func NewRollbackDeployment(
	w *gitwriter.Writer,
	namespace, deployment, container, knownGoodRef string,
	dryRun bool,
	log *slog.Logger,
) *RollbackDeployment {
	return &RollbackDeployment{
		Writer:       w,
		Namespace:    namespace,
		Deployment:   deployment,
		Container:    container,
		KnownGoodRef: knownGoodRef,
		dryRun:       dryRun,
		log:          log,
	}
}

func (a *RollbackDeployment) Name() string { return "rollback-deployment" }

func (a *RollbackDeployment) fieldPath() string {
	return "spec.template.spec.containers[name=" + a.Container + "].image"
}

func (a *RollbackDeployment) resolveTarget(ctx context.Context) (string, error) {
	if a.KnownGoodRef != "" {
		return a.KnownGoodRef, nil
	}
	prev, err := a.Writer.GetPreviousValue(a.Namespace, a.Deployment, "Deployment", a.fieldPath())
	if err != nil {
		return "", fmt.Errorf("rollback-deployment: cannot discover previous image: %w", err)
	}
	a.log.InfoContext(ctx, "rollback-deployment: discovered previous image from git history",
		"namespace", a.Namespace, "deployment", a.Deployment, "image", prev)
	return prev, nil
}

func (a *RollbackDeployment) DryRun(ctx context.Context) (string, error) {
	target, err := a.resolveTarget(ctx)
	if err != nil {
		return "", err
	}
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.fieldPath(), target, true)
	if err != nil {
		return "", err
	}
	if res.NoOp {
		return fmt.Sprintf("rollback-deployment: no-op — %s/%s container %q already at %s",
			a.Namespace, a.Deployment, a.Container, target), nil
	}
	return fmt.Sprintf("rollback-deployment: would set %s/%s[%s] image %s → %s\n%s",
		a.Namespace, a.Deployment, a.Container, res.OldValue, target, res.Diff), nil
}

func (a *RollbackDeployment) Apply(ctx context.Context) error {
	if a.applied != nil {
		return fmt.Errorf("rollback-deployment: already applied (sha=%s)", a.applied.CommitSHA)
	}
	target, err := a.resolveTarget(ctx)
	if err != nil {
		return err
	}
	res, err := a.Writer.EditField(ctx, a.Namespace, a.Deployment, "Deployment",
		a.fieldPath(), target, false)
	if err != nil {
		return err
	}
	a.applied = &res
	a.log.InfoContext(ctx, "rollback-deployment: applied",
		"namespace", a.Namespace, "deployment", a.Deployment, "container", a.Container,
		"image", target, "sha", res.CommitSHA, "noop", res.NoOp)
	return nil
}

func (a *RollbackDeployment) Rollback(ctx context.Context) error {
	if a.applied == nil {
		return fmt.Errorf("rollback-deployment: cannot rollback — Apply has not been called")
	}
	if a.applied.NoOp {
		a.log.InfoContext(ctx, "rollback-deployment: rollback skipped — original apply was a no-op")
		return nil
	}
	// Re-apply the image we replaced (OldValue is the "bad" image we previously reverted away from).
	// This re-applies it intentionally to undo our rollback commit.
	undo := NewRollbackDeployment(a.Writer, a.Namespace, a.Deployment, a.Container,
		a.applied.OldValue, false, a.log)
	if err := undo.Apply(ctx); err != nil {
		return fmt.Errorf("rollback-deployment: undo failed: %w", err)
	}
	a.log.InfoContext(ctx, "rollback-deployment: rolled back to pre-remediation image",
		"image", a.applied.OldValue)
	return nil
}
