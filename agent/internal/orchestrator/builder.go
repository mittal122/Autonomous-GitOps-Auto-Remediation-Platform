package orchestrator

import (
	"fmt"
	"log/slog"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/gitwriter"
	"github.com/autosre/agent/internal/remediator"
)

// ActionBuilder constructs a concrete RemediationAction from a Diagnosis + proposal.
// The dryRun flag is threaded through to the action so it never calls Apply internally.
type ActionBuilder interface {
	Build(diag contracts.Diagnosis, proposal contracts.RemediationProposal, dryRun bool) (contracts.RemediationAction, error)
}

type defaultActionBuilder struct {
	writer       *gitwriter.Writer
	defaultCont  string
	defaultScale int
	memFactor    float64
	log          *slog.Logger
}

// NewDefaultBuilder returns the production ActionBuilder backed by the real remediator actions.
func NewDefaultBuilder(
	w *gitwriter.Writer,
	defaultContainer string,
	defaultScaleReplicas int,
	memBumpFactor float64,
	log *slog.Logger,
) ActionBuilder {
	if defaultContainer == "" {
		defaultContainer = "app"
	}
	if defaultScaleReplicas <= 0 {
		defaultScaleReplicas = 2
	}
	if memBumpFactor <= 0 {
		memBumpFactor = 1.5
	}
	return &defaultActionBuilder{
		writer:       w,
		defaultCont:  defaultContainer,
		defaultScale: defaultScaleReplicas,
		memFactor:    memBumpFactor,
		log:          log,
	}
}

func (b *defaultActionBuilder) Build(
	diag contracts.Diagnosis,
	proposal contracts.RemediationProposal,
	dryRun bool,
) (contracts.RemediationAction, error) {
	ns := proposal.Namespace
	res := proposal.Resource

	// Container: prefer proposal param, fall back to default.
	cont := proposal.Params.Container
	if cont == "" {
		cont = b.defaultCont
	}

	switch diag.ProposedAction {
	case "bump-memory-limit":
		factor := proposal.Params.MemoryBumpFactor
		if factor <= 0 {
			factor = b.memFactor
		}
		return remediator.NewBumpMemoryLimit(b.writer, ns, res, cont, factor, dryRun, b.log), nil

	case "rollback-deployment":
		ref := proposal.Params.KnownGoodRef
		if ref == "" {
			return nil, fmt.Errorf("builder: rollback-deployment requires KnownGoodRef in proposal params")
		}
		return remediator.NewRollbackDeployment(b.writer, ns, res, cont, ref, dryRun, b.log), nil

	case "scale-deployment":
		target := proposal.Params.TargetReplicas
		if target <= 0 {
			target = b.defaultScale
		}
		return remediator.NewScaleDeployment(b.writer, ns, res, target, dryRun, b.log), nil

	default:
		return nil, fmt.Errorf("builder: unknown action type %q for failure mode %q",
			diag.ProposedAction, diag.FailureMode)
	}
}
