package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/policy"
)

// runPolicy is the entry point for `autosre policy [flags]`.
// It loads the policy file, evaluates a proposal, and prints the Decision.
// It NEVER executes any remediation — decision only.
func runPolicy(args []string, log *slog.Logger) int {
	fs := flag.NewFlagSet("policy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	policyFile := fs.String("policy", os.Getenv("POLICY_FILE"), "Path to policy.yaml (overrides POLICY_FILE env)")
	failureMode := fs.String("failure-mode", "", "Failure mode (e.g. OOMKilled, CrashLoopBackOff)")
	namespace := fs.String("namespace", "", "Target Kubernetes namespace")
	resource := fs.String("resource", "", "Target resource name")
	action := fs.String("action", "", "Proposed action type: rollback-deployment | scale-deployment | bump-memory-limit")
	confidence := fs.Float64("confidence", 0, "Confidence score from diagnoser [0.0-1.0]")
	replicas := fs.Int("replicas", 0, "Target replicas (for scale-deployment)")
	currentReplicas := fs.Int("current-replicas", 0, "Current replicas (for scale-deployment blast-radius)")
	memFactor := fs.Float64("mem-factor", 1.5, "Memory bump factor (for bump-memory-limit)")
	container := fs.String("container", "app", "Container name (for rollback-deployment and bump-memory-limit)")
	knownGood := fs.String("known-good", "", "Known-good image ref (for rollback-deployment)")
	outputJSON := fs.Bool("json", false, "Output decision as JSON")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *failureMode == "" || *namespace == "" || *action == "" {
		fmt.Fprintln(os.Stderr, "error: --failure-mode, --namespace, and --action are required")
		fs.Usage()
		return 2
	}

	// Load policy — fail-closed on any error.
	cfg, loadErr := policy.LoadPolicyFile(*policyFile)
	if loadErr != nil {
		log.Warn("policy: using fail-closed defaults", "error", loadErr)
		fmt.Fprintf(os.Stderr, "warning: %v\n", loadErr)
	}

	engine := policy.New(cfg, log)

	proposal := contracts.RemediationProposal{
		IncidentID:  "cli-evaluation",
		Namespace:   *namespace,
		Resource:    *resource,
		FailureMode: *failureMode,
		Confidence:  *confidence,
		Params: contracts.ActionParams{
			ActionType:       *action,
			TargetReplicas:   *replicas,
			CurrentReplicas:  *currentReplicas,
			MemoryBumpFactor: *memFactor,
			Container:        *container,
			KnownGoodRef:     *knownGood,
		},
	}

	// TODO (future prompt — orchestrator): in the live loop, proposals arrive from
	// the orchestrator after the diagnoser produces a Diagnosis. Here we evaluate
	// a synthetic proposal supplied by the operator.
	decision := engine.Evaluate(proposal)

	if *outputJSON {
		return printJSON(decision)
	}
	return printText(proposal, decision, loadErr != nil)
}

func printText(p contracts.RemediationProposal, d contracts.Decision, usingDefaults bool) int {
	fmt.Println("=== AutoSRE Policy Decision ===")
	if usingDefaults {
		fmt.Println("! WARNING: policy file missing/invalid; using fail-closed defaults")
	}
	fmt.Printf("Namespace:    %s\n", p.Namespace)
	fmt.Printf("Failure mode: %s\n", p.FailureMode)
	fmt.Printf("Action:       %s\n", p.Params.ActionType)
	fmt.Printf("Confidence:   %.4f\n", p.Confidence)
	fmt.Println()
	fmt.Printf("VERDICT:      %s\n", d.Verdict)
	fmt.Printf("Reason:       %s\n", d.Reason)
	fmt.Printf("DryRunReqd:   %v\n", d.DryRunRequired)
	fmt.Printf("Matched rules:\n")
	for i, r := range d.MatchedRules {
		fmt.Printf("  [%d] %s\n", i+1, r)
	}
	fmt.Println()
	fmt.Println("NOTE: this command evaluates the policy only — no remediation was executed.")
	return 0
}

func printJSON(d contracts.Decision) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
		return 1
	}
	return 0
}
