# AutoSRE Operator Runbook

This document is for on-call engineers and SREs responsible for running the AutoSRE platform.

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Architecture Overview](#architecture-overview)
3. [Safety Modes](#safety-modes)
4. [RBAC / SSO Setup](#rbac--sso-setup)
5. [Reading Audit Traces](#reading-audit-traces)
6. [Approving/Rejecting Remediations](#approvingrejecting-remediations)
7. [Kill Switch](#kill-switch)
8. [Enabling Apply Mode](#enabling-apply-mode)
9. [Helm Deployment](#helm-deployment)
10. [Troubleshooting](#troubleshooting)

---

## Quick Start

```bash
# 1. Deploy with Helm (dry-run-only by default)
helm upgrade --install autosre charts/autosre \
  --namespace autosre --create-namespace \
  --set agent.applyEnabled=false

# 2. Port-forward the Web UI + API
kubectl port-forward svc/autosre-agent 8080:8080 -n autosre

# 3. Open the dashboard
open http://localhost:8080
# Or use the API directly:
curl http://localhost:8080/api/v1/status
```

---

## Architecture Overview

```
Kubernetes Events
      │
      ▼
  Ingestor ──► Correlator ──► Orchestrator ──► Policy Engine
                                    │               │
                                    │               ▼
                                    │         Diagnoser (Python/LLM)
                                    │
                              (dry-run only by default)
                                    │
                              Approval Registry ◄── Web UI / Slack
                                    │
                              Remediator ──► GitOps Repo ──► ArgoCD
                                    │
                              Verifier ──► Learner (outcome stats)
                                    │
                              Audit Log (append-only JSONL)
```

**Three services:**
- `agent` (Go): detection → decision → remediation loop; exposes REST API + Web UI
- `diagnoser` (Python/FastAPI): LLM-backed diagnosis (Gemini); falls back to rule engine
- `learner` (Python/FastAPI): advisory outcome statistics; read-only stats endpoint

---

## Safety Modes

### Default (DRY-RUN-ONLY)

By default `ORCHESTRATOR_APPLY_ENABLED=false`. The agent runs the full pipeline (detect, diagnose, decide, dry-run) but never commits to the GitOps repo. This is the safe starting mode.

```
GET /api/v1/status → { "apply_enabled": false, "kill_switch_engaged": false }
```

### Dry-run stage

Even in dry-run mode the pipeline runs completely. Audit events are emitted for every stage. The "DryRun" event describes what _would_ happen if apply were enabled:

```json
{ "stage": "DryRun", "outcome": "ok", "details": { "action": "bump-memory-limit", "description": "would bump memory from 256Mi to 384Mi" } }
```

### Kill Switch

The kill switch halts all apply calls in-flight. It is checked twice: once before building the action, and once before Apply(). Flipping the kill switch is effective immediately for new pipelines; in-flight pipelines that have already passed the kill-switch check will continue to their Apply call.

---

## RBAC / SSO Setup

### Roles

| Role       | Access                                                             |
|------------|-------------------------------------------------------------------|
| `viewer`   | Read all API endpoints (incidents, trace, status, stats)          |
| `operator` | viewer + approve/reject remediations                              |
| `admin`    | operator + toggle kill switch (audited)                           |

### Dev mode (OIDC disabled)

When `API_OIDC_ENABLED=false` (default), every request is treated as `viewer`. The API is accessible without credentials. A loud warning is logged at startup:

```
api: OIDC auth DISABLED — all API requests are granted viewer access (dev mode)
```

**Do not run with OIDC disabled in production.**

### Enabling OIDC

```bash
# Example: Google Workspace OIDC
helm upgrade autosre charts/autosre \
  --set agent.oidc.enabled=true \
  --set agent.oidc.issuerURL=https://accounts.google.com \
  --set agent.oidc.clientID=YOUR_CLIENT_ID \
  --set agent.oidc.rolesClaimKey=roles
```

Your OIDC provider must include a `roles` claim (or the configured key) in the JWT. The value can be a string or array of strings: `"admin"`, `["operator", "viewer"]`, etc.

**Security note:** The current implementation decodes the JWT payload to extract roles but does not cryptographically verify the signature. Before going to production, swap in `coreos/go-oidc/v3` for full JWK verification (see `agent/internal/api/auth.go` TODO comment).

---

## Reading Audit Traces

Every incident generates a linked chain of audit events tagged with a `trace_id`. The Web UI shows these at `http://localhost:8080/incidents/{id}/trace`.

Via API:
```bash
# All events for an incident
curl http://localhost:8080/api/v1/incidents/INC-123/trace

# Filter to a specific trace
curl "http://localhost:8080/api/v1/incidents/INC-123/trace?trace_id=abc-xyz"

# Filter to a stage
curl "http://localhost:8080/api/v1/incidents/INC-123/trace?stage=Decided"
```

Via CLI (if audit file is accessible):
```bash
# All events for an incident
./autosre audit --incident INC-123

# Just the Decided stage
./autosre audit --incident INC-123 --stage Decided

# Since a timestamp
./autosre audit --since 2026-01-01T00:00:00Z

# Raw JSON output
./autosre audit --incident INC-123 --json
```

The audit file is append-only JSONL at `$AUDIT_FILE_PATH` (default: `./data/audit.jsonl`).

---

## Approving/Rejecting Remediations

When the policy engine returns `REQUIRE_APPROVAL`, the orchestrator waits for human input. The request appears in:

1. **Slack** — an interactive message with Approve/Deny buttons (if configured)
2. **Web UI** — `http://localhost:8080/approvals` (requires operator role)
3. **API** — direct POST (requires operator role):

```bash
# Approve
curl -X POST http://localhost:8080/api/v1/approvals/REQ-ID/approve \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason": "reviewed and confirmed safe"}'

# Reject
curl -X POST http://localhost:8080/api/v1/approvals/REQ-ID/reject \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason": "blast radius too large"}'
```

Approvals time out after `NOTIFIER_APPROVAL_TIMEOUT` (default: 30 minutes). Timeouts are fail-closed: the remediation is **not** applied.

Every approval/rejection is recorded in the audit log with the approver identity and reason.

---

## Kill Switch

The kill switch halts all new remediation applies. Use during incidents, maintenance windows, or when you observe unexpected behaviour.

```bash
# Engage (halt all applies)
curl -X POST http://localhost:8080/api/v1/control/kill-switch \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"engaged": true, "reason": "incident 2026-01-15: unexpected OOM cascade"}'

# Disengage
curl -X POST http://localhost:8080/api/v1/control/kill-switch \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"engaged": false, "reason": "incident resolved"}'
```

Requires admin role. Every toggle is written to the audit log with operator identity, previous state, and reason.

---

## Enabling Apply Mode

**Only enable apply after validating dry-run behaviour over at least a few hours.**

```bash
# Via Helm (persistent — survives pod restarts)
helm upgrade autosre charts/autosre \
  --reuse-values \
  --set agent.applyEnabled=true

# Via env var override (ephemeral — resets on pod restart)
kubectl set env deployment/autosre-agent ORCHESTRATOR_APPLY_ENABLED=true -n autosre

# Via CLI flag (one-shot run)
./autosre run --apply
```

When apply is enabled:
- The agent commits YAML patches to `$GITOPS_REPO_PATH` and pushes them.
- ArgoCD (if configured) picks up the commit and syncs it to the cluster.
- The verifier observes the incident resource for `$VERIFIER_WINDOW` and records RECOVERED/FAILED.
- Outcome is posted to the learner service for advisory statistics.

---

## Helm Deployment

```bash
# First install
helm upgrade --install autosre charts/autosre \
  --namespace autosre \
  --create-namespace \
  --values my-values.yaml

# Upgrade
helm upgrade autosre charts/autosre \
  --namespace autosre \
  --reuse-values \
  --set agent.image.tag=v0.9.1

# Rollback
helm rollback autosre 1 -n autosre

# View rendered templates before applying
helm template autosre charts/autosre --values my-values.yaml
```

### Secrets management

Never put tokens in `values.yaml`. Use `agent.envFrom` to reference a Kubernetes Secret:

```yaml
# my-values.yaml
agent:
  envFrom:
    - secretRef:
        name: autosre-credentials
```

```bash
kubectl create secret generic autosre-credentials \
  --namespace autosre \
  --from-literal=SLACK_BOT_TOKEN=xoxb-... \
  --from-literal=SLACK_SIGNING_SECRET=... \
  --from-literal=GITOPS_REPO_PATH=/gitops-repo
```

---

## Troubleshooting

### No audit events

Check the audit file is writable:
```bash
kubectl exec -n autosre deploy/autosre-agent -- ls -la /data/
kubectl logs -n autosre deploy/autosre-agent | grep "audit:"
```

### Kill switch won't disengage

The kill switch toggle requires admin role. Verify your token has `admin` in the roles claim, or disable OIDC for emergency access (restart with `API_OIDC_ENABLED=false`).

### Pipeline stuck in REQUIRE_APPROVAL

Check for expired approvals:
```bash
curl http://localhost:8080/api/v1/approvals/pending
```

If the request has expired (past `deadline`), it will be cleaned from the list on the next list call. The pipeline will have already timed out (fail-closed, no action taken).

### Circuit breaker tripped

```bash
curl http://localhost:8080/api/v1/status | jq '.circuit_breaker_tripped'
```

If true, too many AUTO decisions fired in the rolling window. The breaker resets after `circuit_breaker_window_sec` seconds. Increase `maxActionsPerWindow` in the policy ConfigMap if the limit is too aggressive.

### Diagnoser returning errors

```bash
kubectl logs -n autosre deploy/autosre-diagnoser | tail -50
```

The agent falls back to rule-based diagnosis if the LLM is unavailable. Check `GEMINI_API_KEY` is set in the diagnoser's Secret.
