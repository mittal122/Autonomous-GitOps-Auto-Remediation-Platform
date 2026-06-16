# Project Progress

## Project Overview

The Autonomous GitOps & Auto-Remediation Platform is a closed-loop SRE control plane that watches a Kubernetes cluster, diagnoses failures with an LLM (Gemini), and applies safe, GitOps-native auto-remediations (with rollback) — escalating to humans only when it cannot resolve the issue automatically. The core control loop is: **Detect → Diagnose → Decide → Remediate → Verify**. The Go `agent` handles the long-running controller logic; Python `diagnoser` and `learner` services handle LLM interaction and outcome learning respectively.

---

## Current Development Phase

**Prompt 8 — Audit + Learning (COMPLETE)**

---

## Completed Prompts

| Prompt | Name | Status |
|--------|------|--------|
| Prompt 0 | Foundation setup | COMPLETE |
| Prompt 1 | Detection — signal ingestion + correlation | COMPLETE |
| Prompt 2 | GitOps remediation primitives — gitwriter + 3 actions + CLI | COMPLETE |
| Prompt 3 | Decision / policy engine — deterministic, fail-closed | COMPLETE |
| Prompt 4 | LLM Diagnosis — GeminiProvider + RuleBasedProvider + HTTP bridge + Go client | COMPLETE |
| Prompt 5 | Verifier — read-only recovery check, fail-toward-escalation, VerificationResult | COMPLETE |
| Prompt 6 | Notifier — Slack + PagerDuty, fail-closed approvals, signed inbound endpoint | COMPLETE |
| Prompt 7 | Orchestrator — reconcile loop, dry-run-only default, kill switch, bounded concurrency | COMPLETE |
| Prompt 8 | Audit + Learning — append-only JSONL audit trail, observational stats service | COMPLETE |

---

## Completed Modules

| Module | Package | Description |
|--------|---------|-------------|
| Ingestor | `agent/internal/ingestor` | Normalizes k8s events/pod/node states + Alertmanager webhooks into Signal objects |
| Correlator | `agent/internal/correlator` | Groups Signals into Incidents; deduplicates; opens/updates/closes incidents |
| Config loader | `agent/internal/config` | Loads all env vars with documented defaults; now includes RemediatorConfig |
| UID generator | `agent/internal/uid` | crypto/rand-backed ID generator |
| Git writer | `agent/internal/gitwriter` | Structure-preserving YAML editor + go-git commit engine; BumpQuantity helper |
| RollbackDeployment | `agent/internal/remediator` | Reverts container image to a known-good tag via git commit |
| ScaleDeployment | `agent/internal/remediator` | Changes spec.replicas via git commit |
| BumpMemoryLimit | `agent/internal/remediator` | Increases resources.limits.memory by a factor via git commit |
| Policy Engine | `agent/internal/policy` | Deterministic Evaluate(proposal)→Decision with 6 gates; fail-closed; circuit breaker |
| GeminiProvider | `diagnoser/diagnoser/providers/gemini.py` | Calls Gemini API; validates whitelist/confidence; falls back on any error |
| RuleBasedProvider | `diagnoser/diagnoser/providers/rule_based.py` | Deterministic failure-mode→action mapping; no LLM; always available |
| DiagnosisService | `diagnoser/diagnoser/core.py` | Wraps providers with fallback; no LLM if key absent |
| FastAPI bridge | `diagnoser/diagnoser/server.py` | POST /diagnose, GET /healthz; Pydantic validation; advisory-only |
| Go diagnosis client | `agent/internal/diagnosis/client.go` | HTTP client to Python diagnoser; confidence clamping; snake_case JSON |
| RecoverySource | `agent/internal/verifier/source.go` | Interface + CorrelatorSource; decouples verifier from concrete correlator |
| Verifier | `agent/internal/verifier/verifier.go` | Grace delay + polling window; FAIL TOWARD ESCALATION; produces VerificationResult |
| SlackNotifier | `agent/internal/notifier/slack.go` | Notify, RequestApproval (fail-closed, block-kit buttons), Escalate; HMAC signature verification |
| PagerDutyClient | `agent/internal/notifier/pagerduty.go` | Events API v2 trigger; log-only degradation; capped retries |
| CompositeNotifier | `agent/internal/notifier/composite.go` | Routes Notify/RequestApproval to Slack; Escalate to Slack+PD |
| MockNotifier | `agent/internal/notifier/mock.go` | Records calls; configurable outcome; satisfies contracts.Notifier; no network |
| Approval Registry | `agent/internal/notifier/registry.go` | Thread-safe in-memory pending approvals; keyed by request ID |
| ActionBuilder | `agent/internal/orchestrator/builder.go` | Constructs real remediator actions from Diagnosis + proposal; defaultActionBuilder |
| Orchestrator | `agent/internal/orchestrator/orchestrator.go` | Reconcile loop: bounded concurrency, kill switch, per-incident idempotency, graceful shutdown |
| Pipeline | `agent/internal/orchestrator/pipeline.go` | 7-stage runPipeline: diagnose→propose→decide→check→DryRun→Apply→Verify→Notify; emits audit events; posts outcomes |
| AuditSink | `agent/internal/audit/` | Append-only JSONL event log + in-memory sink + NoOp; QueryFilter; TraceID-linked pipeline events |
| OutcomeClient | `agent/internal/outcome/` | Posts completed pipeline outcomes to learner POST /outcome; non-fatal; advisory |
| Learner service | `learner/learner/` | FastAPI: POST /outcome (append-only), GET /stats (read-only advisory), GET /healthz |

---

## Implemented Features

| Feature | Status | Notes |
|---------|--------|-------|
| Kubernetes event watcher | Done | Watches Warning events via client-go SharedInformerFactory; detects OOMKilled, CrashLoopBackOff, ImagePullBackOff, FailedScheduling, NodeNotReady |
| Pod crash state watcher | Done | Secondary detection path via pod informer; catches OOMKilled/CrashLoopBackOff from container status |
| Node NotReady watcher | Done | Node condition informer |
| Alertmanager webhook receiver | Done | `POST /webhook/alertmanager`; validates JSON; maps alerts to Signals |
| Signal deduplication | Done | Configurable `DEDUP_WINDOW` suppresses repeated (resource+reason) signals |
| Incident correlation | Done | `namespace/resource` key groups signals; configurable `CORRELATION_WINDOW` |
| Incident auto-close | Done | Background resolver closes quiet incidents after `RESOLVE_WINDOW` |
| Incident inspection endpoint | Done | `GET /incidents` returns JSON snapshot of all incidents |
| Health endpoint | Done | `GET /health` |
| Graceful shutdown | Done | SIGINT/SIGTERM cancel the root context; HTTP server shuts down cleanly |
| Structured logging | Done | `log/slog` JSON handler; logs incident lifecycle events |

---

## Pending Features

| Feature | Target Prompt |
|---------|---------------|
| Loki log-tail ingestion | Deferred |
| Human-in-the-loop (Slack approval, PagerDuty escalation) | COMPLETE (Prompt 6) |
| Full orchestration loop (correlator→diagnose→decide→remediate→verify) | COMPLETE (Prompt 7) |
| Audit & Learning loop (outcome store, model feedback) | Deferred |
| Web UI (dashboard, incident timeline) | Deferred |
| Real EKS deployment (Terraform, Helm, ArgoCD) | Prompt 8 |

---

## Folder Structure Summary

```
autosre/
├── infra/                          # Terraform (empty — future prompt)
├── charts/                         # Helm charts (empty skeleton)
├── gitops/
│   └── apps/production/
│       └── payment-service.yaml    # Fixture Deployment for local testing
├── agent/                          # Go service
│   ├── go.mod                      # + go-git v5.11.0, yaml.v3 v3.0.1
│   ├── .golangci.yml
│   ├── cmd/autosre/
│   │   ├── main.go                 # Subcommand dispatch + detection wiring
│   │   ├── remediate.go            # `autosre remediate` CLI subcommand
│   │   ├── policy.go               # `autosre policy` CLI subcommand
│   │   ├── diagnose.go             # `autosre diagnose` CLI subcommand
│   │   ├── verify.go               # `autosre verify` CLI subcommand
│   │   ├── notify.go               # `autosre notify` CLI subcommand
│   │   └── run.go                  # `autosre run` CLI subcommand (full reconcile loop)
│   └── internal/
│       ├── uid/uid.go              # Random ID generator
│       ├── config/config.go        # Env-var config + new RemediatorConfig
│       ├── contracts/
│       │   ├── contracts.go        # Signal, Incident, RemediationAction interface, etc.
│       │   └── contracts_test.go
│       ├── ingestor/
│       │   ├── ingestor.go
│       │   ├── k8s.go              # 14 unit tests
│       │   ├── k8s_test.go
│       │   ├── webhook.go
│       │   └── webhook_test.go     # 7 unit tests
│       ├── correlator/
│       │   ├── correlator.go
│       │   └── correlator_test.go  # 13 unit tests
│       ├── gitwriter/
│       │   ├── quantity.go         # BumpQuantity() + ParseQuantityBytes()
│       │   ├── manifest.go         # FindManifest(), GetField(), SetField(), YAML nav helpers
│       │   ├── gitwriter.go        # Writer: EditField(), GetCurrentValue(), GetPreviousValue()
│       │   └── gitwriter_test.go   # 12 integration tests (temp git repo)
│       ├── remediator/
│       │   ├── doc.go              # Package doc + compile-time interface checks
│       │   ├── scale.go            # ScaleDeployment action
│       │   ├── rollback.go         # RollbackDeployment action
│       │   ├── bump_memory.go      # BumpMemoryLimit action
│       │   └── remediator_test.go  # 9 integration tests
│       ├── policy/
│       │   ├── config.go           # PolicyConfig YAML loader; fail-closed defaults
│       │   ├── circuitbreaker.go   # Thread-safe rolling-window AUTO decision counter
│       │   ├── engine.go           # Engine.Evaluate() — 6-gate deterministic decision
│       │   └── engine_test.go      # 20 unit tests (functional + edge + boundary)
│       ├── verifier/
│       │   ├── source.go           # RecoverySource interface + CorrelatorSource
│       │   ├── verifier.go         # Verifier: grace delay + polling + FAIL TOWARD ESCALATION
│       │   └── verifier_test.go    # 11 tests (functional + edge + boundary)
│       ├── notifier/
│       │   ├── doc.go              # Package doc + compile-time interface assertions
│       │   ├── registry.go         # Thread-safe in-memory pending approvals
│       │   ├── slack.go            # SlackNotifier + HMAC signature verification + Block Kit
│       │   ├── pagerduty.go        # PagerDutyClient (Events API v2)
│       │   ├── composite.go        # CompositeNotifier + New() constructor
│       │   ├── mock.go             # MockNotifier for tests
│       │   └── notifier_test.go    # 16 tests (all transport-mocked, no real sends)
│       ├── orchestrator/
│       │   ├── orchestrator.go     # Orchestrator: Run(), schedule(), kill switch, in-flight registry
│       │   ├── pipeline.go         # 7-stage runPipeline(); buildProposal()
│       │   ├── builder.go          # ActionBuilder interface + defaultActionBuilder
│       │   └── orchestrator_test.go # 15 tests (pipeline stages, idempotency, Run loop)
│       ├── audit/                  # (empty)
│       └── controller/             # (empty)
├── diagnoser/                      # Python LLM service (Prompt 0 scaffolding)
├── learner/                        # Python learning service (Prompt 0 scaffolding)
├── web-ui/                         # (empty placeholder)
├── runbooks/                       # (empty skeleton)
├── policy.yaml                     # Sample/default policy-as-code config
├── .github/workflows/ci.yml        # go mod tidy before lint/test
├── docs/
├── kind-config.yaml
├── Makefile
├── .gitignore
├── .env.example                    # + GitOps remediation vars
└── README.md
```

---

## Important Architectural Decisions

| Decision | Rationale |
|----------|-----------|
| **Monorepo** | All services share contracts; easier cross-service refactoring in early stages |
| **Go for agent** | Long-running controllers with low memory footprint; strong concurrency primitives |
| **Python for diagnoser/learner** | Rich LLM SDK ecosystem; easier prompt engineering iteration |
| **GitOps via ArgoCD** | All changes go through Git; full audit trail; declarative reconciliation |
| **Pluggable `LLMProvider` interface** | Swap Gemini for another model without touching the decision layer |
| **Safety-first: dry-run, rollback, confidence gating** | Never apply a remediation without a Rollback path and a confidence floor |
| **Local dev on kind before real EKS** | Zero cloud cost during development; identical API surface |
| **In-memory incident store** | Simplest path for Prompt 1; no DB dependency. Incidents lost on restart. Persistence is Prompt 6. |
| **Correlation by namespace/resource** | Simple, correct, fast. Future: refine to Deployment owner references. |
| **Dedup by (resource, reason)** | Prevents single failure from generating 100 incidents; configurable window. |
| **SharedInformerFactory for k8s watch** | Reconnect-resilient; local cache; handles event aggregation better than raw Watch. |
| **yaml.Node API for YAML edits** | Preserves existing comments and indentation; avoids marshal/unmarshal round-trip information loss. |
| **go-git for commits (no shell)** | No `exec.Command("git", ...)` — avoids shell injection, more portable, better error handling. |
| **Atomic file restore on commit failure** | Original content snapshotted before write; restored on any git error — no half-committed state. |
| **NoOp detection before commit** | Compares old/new values; skips git commit when field already has the target value. |
| **Previous image discovery from git log** | RollbackDeployment can find the last-known-good image from git history without caller supplying it. |
| **Policy engine is decision-only** | `internal/policy` never calls remediator, gitwriter, or k8s. Grep-verified. Orchestrator wiring is a future prompt. |
| **Fail-closed policy defaults** | Missing/invalid `policy.yaml` returns conservative defaults (`propose`); engine never crashes and never auto-acts. |
| **Most-restrictive-wins autonomy** | Global default < failure-mode rule < namespace rule; a namespace override can only tighten, never loosen. |
| **Confidence is an input, not computed** | Policy engine takes confidence as a pre-computed value. Gemini diagnoser (Prompt 4) will supply it in production. |
| **Circuit breaker in-memory** | Rolling window of AUTO decision timestamps; thread-safe with sync.Mutex; cleared when window expires. |

---

## Signal / Incident Schema Changes (Prompt 1)

Added to `contracts.Signal` (backward-compatible):
- `Kind string` — Kubernetes resource kind (e.g. "Pod", "Node")
- `Reason string` — normalized failure indicator (e.g. "OOMKilled", "CrashLoopBackOff")
- `Message string` — human-readable description from the source system

Added to `contracts.Incident` (backward-compatible):
- `UpdatedAt time.Time` — when the most recent Signal was appended

---

## Reusable Components Created

| Component | Location | Description |
|-----------|----------|-------------|
| `Signal` | `agent/internal/contracts/contracts.go` | Normalized telemetry data point (updated with Kind, Reason, Message) |
| `Incident` | `agent/internal/contracts/contracts.go` | Correlated problem from multiple signals (updated with UpdatedAt) |
| `Diagnosis` | `agent/internal/contracts/contracts.go` | LLM output: root cause, action, confidence |
| `RemediationAction` | `agent/internal/contracts/contracts.go` | Interface: DryRun / Apply / Rollback |
| `LLMProvider` | `agent/internal/contracts/contracts.go` | Interface: Diagnose(ctx, incident) Diagnosis |
| `Notifier` | `agent/internal/contracts/contracts.go` | Interface: Notify / RequestApproval / Escalate |
| Python contracts | `diagnoser/diagnoser/contracts.py` | Python mirror of the above types |
| `uid.New()` | `agent/internal/uid/uid.go` | Crypto-random 16-char hex ID generator |
| `config.Load()` | `agent/internal/config/config.go` | Env-var config with documented defaults |
| `Ingestor` | `agent/internal/ingestor/ingestor.go` | Signal aggregator: k8s watcher + webhook handler |
| `Correlator` | `agent/internal/correlator/correlator.go` | Incident lifecycle manager with dedup and auto-close |

---

## New Environment Variables (Prompt 1)

| Variable | Default | Purpose |
|----------|---------|---------|
| `IN_CLUSTER` | `false` | Use in-cluster service-account credentials |
| `WEBHOOK_ADDR` | `:8080` | HTTP server address |
| `CORRELATION_WINDOW` | `5m` | Group signals for same resource within this window |
| `RESOLVE_WINDOW` | `10m` | Close incident after this long without new signals |
| `DEDUP_WINDOW` | `1m` | Suppress duplicate (resource+reason) signals |

## New Environment Variables (Prompt 7)

| Variable | Default | Purpose |
|----------|---------|---------|
| `ORCHESTRATOR_APPLY_ENABLED` | `false` | Enable real GitOps commits; `false` = dry-run-only |
| `ORCHESTRATOR_KILL_SWITCH` | `false` | Halt all Apply calls immediately when `true` |
| `ORCHESTRATOR_MAX_WORKERS` | `5` | Max concurrent incident pipelines |
| `ORCHESTRATOR_DEFAULT_CONTAINER` | `app` | Container name for rollback/bump-memory when not specified |
| `ORCHESTRATOR_DEFAULT_SCALE_REPLICAS` | `2` | Target replica count for scale-deployment when not in diagnosis |

## New Environment Variables (Prompt 6)

| Variable | Default | Purpose |
|----------|---------|---------|
| `SLACK_BOT_TOKEN` | (empty) | Slack bot token (`xoxb-...`); empty → Notify/RequestApproval log-only |
| `SLACK_SIGNING_SECRET` | (empty) | HMAC signing secret for verifying inbound `/slack/interactions`; empty → reject all |
| `SLACK_CHANNEL_ID` | (empty) | Slack channel for notifications and approval requests |
| `PAGERDUTY_ROUTING_KEY` | (empty) | PagerDuty Events API v2 routing key; empty → PD escalations skipped |
| `NOTIFIER_APPROVAL_TIMEOUT` | `30m` | How long to wait for human approval before TIMEOUT (fail-closed) |
| `NOTIFIER_SEND_TIMEOUT` | `10s` | Per-call HTTP timeout for Slack/PagerDuty sends |
| `NOTIFIER_MAX_RETRIES` | `3` | Max retries for failed outbound sends (exponential backoff) |

## New Environment Variables (Prompt 5)

| Variable | Default | Purpose |
|----------|---------|---------|
| `VERIFIER_GRACE_DELAY` | `30s` | Wait after remediation before observing (ArgoCD sync time) |
| `VERIFIER_WINDOW` | `5m` | Total observation window after grace delay |
| `VERIFIER_POLL_INTERVAL` | `15s` | How often to query the recovery source during the window |
| `VERIFIER_FAILURE_THRESHOLD` | `0` | Max matching signals before FAILED (>threshold → FAIL) |

## New Environment Variables (Prompt 4)

| Variable | Default | Purpose |
|----------|---------|---------|
| `GEMINI_API_KEY` | (optional) | Gemini API key; if absent, diagnoser runs fallback-only |
| `GEMINI_MODEL` | `gemini-1.5-flash` | Gemini model name |
| `LLM_TIMEOUT_SECONDS` | `30` | Gemini API call timeout (Python side) |
| `DIAGNOSER_PORT` | `8001` | Port for the FastAPI diagnoser service |
| `DIAGNOSER_HOST` | `0.0.0.0` | Bind host for the FastAPI diagnoser service |
| `DIAGNOSER_ADDR` | `http://localhost:8001` | URL the Go agent uses to reach the diagnoser |
| `DIAGNOSER_TIMEOUT` | `35s` | Go→Python HTTP request timeout |

## New Environment Variables (Prompt 3)

| Variable | Default | Purpose |
|----------|---------|---------|
| `POLICY_FILE` | (optional) | Path to `policy.yaml`; if unset, engine uses fail-closed defaults |

## New Environment Variables (Prompt 2)

| Variable | Default | Purpose |
|----------|---------|---------|
| `GITOPS_REPO_PATH` | (required) | Absolute path to local GitOps config repo clone |
| `GIT_BOT_NAME` | `autosre-bot` | Git commit author name |
| `GIT_BOT_EMAIL` | `autosre-bot@localhost` | Git commit author email |
| `GIT_BRANCH` | `main` | Branch to commit remediation changes on |
| `REMEDIATION_DRY_RUN` | `true` | When true, all actions are dry-run only |
| `MEMORY_BUMP_FACTOR` | `1.5` | Multiplier for BumpMemoryLimit action |

---

## API Endpoints Created (Prompt 1)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/webhook/alertmanager` | Receive Alertmanager webhook; emit Signals |
| `GET` | `/incidents` | JSON snapshot of all incidents (open + closed) |
| `GET` | `/health` | Returns `{"status":"ok"}` |
| `POST` | `/slack/interactions` | Slack interactive message handler (approval approve/deny); wired in `autosre run` |

---

## Database / Schema Changes

*(None — in-memory store only)*

---

## Deployment Status

- **Local dev only (kind).** No cloud resources provisioned.
- CI runs on GitHub Actions (lint + test; `go mod tidy` added before Go steps).
- Real EKS deployment is a future prompt.
