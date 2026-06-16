package remediator

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/autosre/agent/internal/gitwriter"
)

// PatchHPA stabilizes an oscillating HorizontalPodAutoscaler by raising its
// minReplicas to the observed current replica count, preventing rapid scale-down.
// All changes are committed to the GitOps repo; ArgoCD syncs the change to the cluster.
type PatchHPA struct {
	Writer      *gitwriter.Writer
	Namespace   string
	HPAName     string
	MinReplicas int // the stable floor to set

	dryRun  bool
	applied *gitwriter.Result
	log     *slog.Logger
}

const (
	hpaMinReplicasField = "spec.minReplicas"
	defaultMinReplicas  = 2
)

func NewPatchHPA(
	w *gitwriter.Writer,
	namespace, hpaName string,
	minReplicas int,
	dryRun bool,
	log *slog.Logger,
) *PatchHPA {
	if minReplicas <= 0 {
		minReplicas = defaultMinReplicas
	}
	return &PatchHPA{
		Writer:      w,
		Namespace:   namespace,
		HPAName:     hpaName,
		MinReplicas: minReplicas,
		dryRun:      dryRun,
		log:         log,
	}
}

func (a *PatchHPA) Name() string { return "patch-hpa" }

func (a *PatchHPA) DryRun(ctx context.Context) (string, error) {
	res, err := a.Writer.EditField(ctx,
		a.Namespace, a.HPAName, "HorizontalPodAutoscaler",
		hpaMinReplicasField,
		strconv.Itoa(a.MinReplicas),
		true,
	)
	if err != nil {
		return "", fmt.Errorf("patch-hpa: dry-run: %w", err)
	}
	if res.NoOp {
		return fmt.Sprintf("patch-hpa: no-op — %s/%s minReplicas already %d",
			a.Namespace, a.HPAName, a.MinReplicas), nil
	}
	return fmt.Sprintf("patch-hpa: would set %s/%s minReplicas %s → %d (stabilizes oscillation)\n%s",
		a.Namespace, a.HPAName, res.OldValue, a.MinReplicas, res.Diff), nil
}

func (a *PatchHPA) Apply(ctx context.Context) error {
	if a.applied != nil {
		return fmt.Errorf("patch-hpa: already applied (sha=%s)", a.applied.CommitSHA)
	}
	res, err := a.Writer.EditField(ctx,
		a.Namespace, a.HPAName, "HorizontalPodAutoscaler",
		hpaMinReplicasField,
		strconv.Itoa(a.MinReplicas),
		false,
	)
	if err != nil {
		return fmt.Errorf("patch-hpa: apply: %w", err)
	}
	a.applied = &res
	a.log.InfoContext(ctx, "patch-hpa: applied",
		"namespace", a.Namespace, "hpa", a.HPAName,
		"min_replicas", a.MinReplicas, "sha", res.CommitSHA, "noop", res.NoOp)
	return nil
}

func (a *PatchHPA) Rollback(ctx context.Context) error {
	if a.applied == nil {
		return fmt.Errorf("patch-hpa: cannot rollback — Apply has not been called")
	}
	if a.applied.NoOp {
		a.log.InfoContext(ctx, "patch-hpa: rollback skipped — original apply was a no-op")
		return nil
	}
	prevMin, err := strconv.Atoi(a.applied.OldValue)
	if err != nil {
		return fmt.Errorf("patch-hpa: cannot parse previous minReplicas %q: %w", a.applied.OldValue, err)
	}
	rollback := NewPatchHPA(a.Writer, a.Namespace, a.HPAName, prevMin, false, a.log)
	if err := rollback.Apply(ctx); err != nil {
		return fmt.Errorf("patch-hpa: rollback failed: %w", err)
	}
	a.log.InfoContext(ctx, "patch-hpa: rolled back",
		"namespace", a.Namespace, "hpa", a.HPAName, "min_replicas", prevMin)
	return nil
}
