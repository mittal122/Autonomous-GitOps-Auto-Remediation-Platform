package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/gitwriter"
	"github.com/autosre/agent/internal/remediator"
)

// runRemediate is the entry point for `autosre remediate [flags]`.
// It constructs the requested action, prints a dry-run diff (default), and
// optionally commits the change with --apply. The cluster is never touched.
func runRemediate(args []string, cfg config.Config, log *slog.Logger) int {
	fs := flag.NewFlagSet("remediate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	action := fs.String("action", "", "Action to perform: rollback | scale | bump-memory")
	namespace := fs.String("namespace", "", "Kubernetes namespace of the target Deployment")
	deployment := fs.String("deployment", "", "Deployment name")
	container := fs.String("container", "app", "Container name (for rollback and bump-memory)")
	replicas := fs.Int("replicas", 0, "Target replica count (for scale)")
	knownGood := fs.String("known-good", "", "Known-good image ref for rollback; if empty, discovered from git history")
	factor := fs.Float64("factor", 0, "Memory bump factor (for bump-memory; default 1.5)")
	apply := fs.Bool("apply", false, "Commit the change; default is dry-run only")
	repoPath := fs.String("repo", cfg.Remediator.RepoPath, "Path to the local GitOps repo clone (overrides GITOPS_REPO_PATH)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *action == "" {
		fmt.Fprintln(os.Stderr, "error: --action is required (rollback | scale | bump-memory)")
		fs.Usage()
		return 2
	}
	if *namespace == "" || *deployment == "" {
		fmt.Fprintln(os.Stderr, "error: --namespace and --deployment are required")
		return 2
	}
	if *repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo or GITOPS_REPO_PATH must be set")
		return 2
	}

	dryRun := !*apply
	if dryRun {
		fmt.Fprintln(os.Stderr, "note: running in dry-run mode (pass --apply to commit)")
	}

	writerCfg := gitwriter.Config{
		RepoPath: *repoPath,
		BotName:  cfg.Remediator.BotName,
		BotEmail: cfg.Remediator.BotEmail,
		Branch:   cfg.Remediator.Branch,
	}
	w := gitwriter.New(writerCfg, log)

	ctx := context.Background()

	switch *action {
	case "rollback":
		a := remediator.NewRollbackDeployment(w, *namespace, *deployment, *container, *knownGood, dryRun, log)
		return execAction(ctx, a, dryRun)

	case "scale":
		if *replicas <= 0 {
			fmt.Fprintln(os.Stderr, "error: --replicas must be a positive integer for scale action")
			return 2
		}
		a := remediator.NewScaleDeployment(w, *namespace, *deployment, *replicas, dryRun, log)
		return execAction(ctx, a, dryRun)

	case "bump-memory":
		bumpFactor := *factor
		if bumpFactor <= 0 {
			bumpFactor = cfg.Remediator.MemoryBumpFactor
		}
		a := remediator.NewBumpMemoryLimit(w, *namespace, *deployment, *container, bumpFactor, dryRun, log)
		return execAction(ctx, a, dryRun)

	default:
		fmt.Fprintf(os.Stderr, "error: unknown action %q; want rollback | scale | bump-memory\n", *action)
		return 2
	}
}

// execAction runs DryRun or Apply depending on the dryRun flag and prints the result.
func execAction(ctx context.Context, a interface {
	DryRun(context.Context) (string, error)
	Apply(context.Context) error
}, dryRun bool) int {
	if dryRun {
		desc, err := a.DryRun(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dry-run error: %v\n", err)
			return 1
		}
		fmt.Println(desc)
		return 0
	}
	if err := a.Apply(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "apply error: %v\n", err)
		return 1
	}
	fmt.Println("applied successfully")
	return 0
}
