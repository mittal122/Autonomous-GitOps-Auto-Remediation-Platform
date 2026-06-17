# Production Readiness Roadmap
## AutoSRE — Autonomous GitOps & Auto-Remediation Platform

> **Current production readiness: ~35%**
> This document is the authoritative checklist of every gap between the current state and a
> system that can safely run autonomous remediation in a real production Kubernetes cluster.
> Items are grouped by area and ordered by priority within each area.

---

## How to Read This Document

Each item has:
- **Status** — Not Started / Partial / Done
- **Priority** — P0 (blocks everything) → P3 (nice to have)
- **Effort** — S (days) / M (1–2 weeks) / L (3–4 weeks) / XL (>1 month)
- **Where to change** — exact file / package

---

## Area 1 — Critical Blockers (P0)

These must be resolved before ANY production deployment. The system does not work end-to-end without them.

---

### 1.1 Git Push Not Implemented

**Status:** Not Started
**Priority:** P0
**Effort:** M

**Problem:**
The `gitwriter` package commits changes to the local git clone but never pushes to the remote.
ArgoCD polls the remote repository, not the local clone. Without a push, no automated
remediation ever reaches the cluster — the entire `Remediate` step is a no-op in production.

**Evidence:**
```
agent/internal/gitwriter/gitwriter.go — 243 lines, zero calls to PlainPush or .Push()
```

**What to build:**
1. Add `RemoteURL`, `GitHubToken` (or `SSHKeyPath`) fields to `gitwriter.Config`.
2. After every successful `Commit`, call `repo.Push(&git.PushOptions{Auth: ...})`.
3. Support both HTTPS token auth (`github.com/go-git/go-git/v5/plumbing/transport/http`) and SSH key auth.
4. On push failure: log the error, mark the `Result` as failed, trigger escalation — do NOT silently succeed.
5. Add `GIT_REMOTE_URL` and `GIT_TOKEN` env vars to `config/config.go` and `.env.example`.
6. Wire the secret into the Helm chart via `envFrom` referencing a Kubernetes Secret.

**Files to change:**
- `agent/internal/gitwriter/gitwriter.go` — add `pushAfterCommit()`
- `agent/internal/gitwriter/config.go` (extract Config to own file) — add remote auth fields
- `agent/internal/config/config.go` — `GIT_REMOTE_URL`, `GIT_TOKEN`, `GIT_SSH_KEY_PATH`
- `charts/autosre/templates/deployment-agent.yaml` — mount git credentials Secret
- `charts/autosre/values.yaml` — `agent.gitSecret` ref
- `.env.example` — document the new vars
- `agent/internal/gitwriter/gitwriter_test.go` — add push test with bare in-memory repo

---

### 1.2 JWT Signature Not Verified

**Status:** Partial (payload decoded, signature ignored)
**Priority:** P0
**Effort:** M

**Problem:**
`api/auth.go` extracts the JWT Bearer token and base64-decodes the payload to read the `roles`
claim, but it does **not** verify the cryptographic signature. Anyone who can craft a JSON blob
like `{"roles":["admin"]}`, base64-encode it, and wrap it in a JWT header can approve
remediations, toggle the kill switch, or reject pending approvals.

The code itself documents this:
```go
// TODO: replace the base64 decode with a cryptographic signature check using
//       github.com/coreos/go-oidc/v3/oidc when the OIDC provider is configured.
//       The current implementation trusts the token payload without verifying the
//       signature — only safe on a private, trusted network.
```

**What to build:**
1. Add `github.com/coreos/go-oidc/v3` to `agent/go.mod`.
2. In `newAuthMiddleware()`, if `OIDCEnabled=true`, construct an `oidc.Provider` from `OIDCIssuerURL`.
3. Replace the manual base64 decode in `extractRoles()` with `provider.Verifier(...).Verify(ctx, raw)`.
4. Extract the roles claim from the verified `IDToken.Claims(...)`.
5. On verification failure (expired, wrong issuer, bad signature) → return 401, log the rejection.
6. Keep the `OIDCEnabled=false` dev-mode path (grants viewer, audits loudly).

**Files to change:**
- `agent/internal/api/auth.go` — replace `extractRoles()` body
- `agent/go.mod` — add `github.com/coreos/go-oidc/v3 v3.10.0`
- `agent/internal/api/api_test.go` — update tests to use real JWKS mock or test provider

---

### 1.3 All State Is In-Memory

**Status:** Not Started
**Priority:** P0
**Effort:** L

**Problem:**
The `correlator`, notifier approval registry, and circuit breaker live entirely in RAM.
An agent pod restart (OOM kill, node drain, rolling update) silently discards:
- All open incidents and their signal history
- All pending human approval requests (approvals in flight are lost → fall through to timeout → DENIED)
- Circuit breaker counters (resets to zero after every restart, bypassing protection)
- Audit log is written to a local file (survives restart if PVC is mounted, but is not queryable)

**Evidence:**
```
correlator/correlator.go line 13:
  // TODO (future prompt): persist incidents to a store (PostgreSQL/SQLite).
```

**What to build:**
1. **SQLite option (lean):** Embed `github.com/mattn/go-sqlite3` or `modernc.org/sqlite` (pure Go, no CGO).
   - Store incidents in `incidents` table, signals in `signals` table.
   - Correlator reads/writes via a thin repository interface.
   - Circuit breaker persists window start + count to a `circuit_breaker` table.
2. **PostgreSQL option (production):** Use `lib/pq` or `pgx`; same repository interface.
3. Add a `StoreConfig` to `config.go` with `STORE_DSN` env var.
4. Approval registry (`notifier/registry.go`) must persist pending approvals to the store with TTL.
5. Add database migration framework (`golang-migrate` or embedded SQL files).

**Files to change:**
- `agent/internal/correlator/correlator.go` — extract store interface; add SQLite implementation
- `agent/internal/notifier/registry.go` — persist to store
- `agent/internal/policy/circuitbreaker.go` — persist window to store
- `agent/internal/config/config.go` — `STORE_DSN`
- `agent/cmd/autosre/run.go` — open store, pass to components
- `charts/autosre/values.yaml` — add SQLite PVC or PostgreSQL external secret

---

## Area 2 — Security Hardening (P1)

---

### 2.1 No Pod Security Standards in Helm Chart

**Status:** Not Started
**Priority:** P1
**Effort:** S

**Problem:**
All three Helm deployments (agent, diagnoser, learner) run as root with a writable root
filesystem and no seccomp profile. Any RCE vulnerability in the Go or Python code gives
an attacker full root access to the node.

**What to add to every `deployment-*.yaml`:**
```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
containers:
  - securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
```

**Files to change:**
- `charts/autosre/templates/deployment-agent.yaml`
- `charts/autosre/templates/deployment-diagnoser.yaml`
- `charts/autosre/templates/deployment-learner.yaml`
- Add `emptyDir` volumes for any paths the code writes to at runtime

---

### 2.2 No Kubernetes Network Policies

**Status:** Not Started
**Priority:** P1
**Effort:** S

**Problem:**
Any pod in the cluster can reach the agent's API, including the kill-switch endpoint.
Network policies are the defense-in-depth layer that restricts which pods can talk to which.

**What to build:**
Add `charts/autosre/templates/networkpolicy.yaml`:
- Agent → diagnoser: allow port 8001
- Agent → learner: allow port 8002
- Alertmanager → agent: allow port 8080 (webhook)
- Operator browsers → agent: allow port 8080 (UI/API)
- Default deny-all ingress for all autosre pods from outside the autosre namespace

---

### 2.3 Secrets Passed as Plain Env Vars

**Status:** Partial (documented in .env.example, not managed)
**Priority:** P1
**Effort:** M

**Problem:**
`SLACK_BOT_TOKEN`, `PAGERDUTY_ROUTING_KEY`, `GEMINI_API_KEY`, and `GIT_TOKEN` are passed
as environment variables. In Kubernetes, env vars from Secrets are visible in `kubectl describe pod`
and stored unencrypted in etcd by default.

**What to build:**
1. **Short term:** Use Kubernetes Secrets + `envFrom` in the Helm chart (already partially wired via `agent.secretName` in values.yaml but not enforced).
2. **Medium term:** Add AWS Secrets Manager / HashiCorp Vault integration via External Secrets Operator. Add `charts/autosre/templates/externalsecret.yaml` (optional, feature-flagged).
3. Enable etcd encryption at rest in the EKS cluster (`infra/eks.tf` — add `encryptionConfig` to the `aws_eks_cluster` resource).

---

### 2.4 No TLS Between Internal Services

**Status:** Not Started
**Priority:** P1
**Effort:** M

**Problem:**
Agent-to-diagnoser and agent-to-learner HTTP is plain text. On a shared cluster, a
compromised pod on the same node can sniff diagnosis requests (which contain pod logs,
env vars, error messages — potentially sensitive operational data).

**What to build:**
1. Use cert-manager to issue certificates for internal services.
2. Add TLS listener to diagnoser (`diagnoser/server.py`) and learner (`learner/server.py`).
3. Update the Go `diagnosis.Client` and `outcome.Client` to use TLS with cert verification.
4. Add `cert-manager` Helm install to `infra/observability.tf`.
5. Add Certificate + Issuer manifests to `charts/autosre/templates/`.

---

### 2.5 No API Rate Limiting

**Status:** Not Started
**Priority:** P1
**Effort:** S

**Problem:**
The `/api/v1/control/kill-switch` and approval endpoints have no rate limiting. A
compromised operator token could spam approvals or toggle the kill switch thousands of times.

**What to build:**
Add a per-IP and per-user token bucket middleware to `api/api.go` using `golang.org/x/time/rate`.
Limits: 10 req/s per IP on write endpoints; 100 req/s on read endpoints.

**Files to change:**
- `agent/internal/api/api.go` — add `rateLimitMiddleware()`
- `agent/go.mod` — add `golang.org/x/time`

---

## Area 3 — Testing (P1)

---

### 3.1 No Integration Tests

**Status:** Not Started
**Priority:** P1
**Effort:** L

**Problem:**
All tests are unit tests with fakes. The integration between the Go agent, the Python
diagnoser, and a real Kubernetes cluster is completely untested. The gitwriter tests use
a local bare repo — they never test a real remote push + ArgoCD sync.

**What to build:**
1. **kind-based integration test suite** (`tests/integration/`):
   - Spin up a kind cluster (already have `kind-config.yaml`)
   - Deploy a test workload (e.g., a pod that OOMKills itself)
   - Assert the agent detects the incident, calls the diagnoser, and creates a git commit
   - Assert ArgoCD syncs the commit (or mock ArgoCD with a git bare repo)
2. **Contract tests** between agent and diagnoser:
   - Use `pact` or simple HTTP record/replay to assert the `/diagnose` request/response shape
3. Add an integration test job to `.github/workflows/ci.yml` (run on push to main only; requires kind)

---

### 3.2 No End-to-End Remediation Test

**Status:** Not Started
**Priority:** P1
**Effort:** L

**Problem:**
No test exercises the full detect → diagnose → decide → remediate → verify path against
a real (or realistic fake) cluster and git repo. Chaos tests are unit-level with all
external calls faked.

**What to build:**
A test in `tests/e2e/` that:
1. Creates a real bare git repo (local)
2. Seeds it with a Deployment YAML that has a low memory limit
3. Injects a synthetic `OOMKilled` signal directly into the correlator
4. Runs the full orchestrator pipeline (with `ApplyEnabled=true`)
5. Asserts the git repo has a new commit bumping the memory limit
6. Asserts the audit log contains `ApplySucceeded`
7. Asserts the verifier eventually returns `RECOVERED` (using a fake verifier source)

---

### 3.3 No Performance / Load Tests

**Status:** Not Started
**Priority:** P2
**Effort:** M

**Problem:**
The correlator, policy engine, and orchestrator have never been load-tested. Under a real
alert storm (hundreds of signals per second from a misbehaving cluster), the in-memory
correlator could OOM or the orchestrator worker pool could deadlock.

**What to build:**
A benchmark test in `agent/internal/correlator/correlator_bench_test.go` that:
- Generates 10,000 signals in a tight loop
- Measures allocations per signal and throughput
- Asserts no goroutine leak after the correlator is stopped

---

## Area 4 — Infrastructure / Terraform (P1)

---

### 4.1 Terraform Has a Module Reference Bug

**Status:** Not Started
**Priority:** P1
**Effort:** S

**Problem:**
`infra/main.tf` and `infra/observability.tf` reference `module.eks.cluster_endpoint`,
`module.eks.cluster_name`, etc. But there is no Terraform module — `eks.tf` defines
resources inline and stores the results in a `locals` block called `module`. The Terraform
providers will fail with "A managed resource "module" has not been declared" on `terraform init`.

**What to fix:**
Replace all `module.eks.*` references in `main.tf` and `observability.tf` with the correct
direct resource references:
- `module.eks.cluster_endpoint` → `aws_eks_cluster.this.endpoint`
- `module.eks.cluster_name` → `aws_eks_cluster.this.name`
- `module.eks.cluster_certificate_authority_data` → `aws_eks_cluster.this.certificate_authority[0].data`
- `module.eks.oidc_provider_arn` → `aws_iam_openid_connect_provider.eks.arn`

Then delete the `locals { module = { ... } }` block from `eks.tf`.

**Files to change:**
- `infra/main.tf` — fix provider `kubernetes` and `helm` blocks
- `infra/observability.tf` — fix `helm_release.autosre` values
- `infra/eks.tf` — remove the `locals { module = ... }` block

---

### 4.2 Terraform Never Tested Against Real AWS

**Status:** Not Started
**Priority:** P1
**Effort:** M

**What to do:**
1. Fix the module reference bug (4.1 above).
2. Run `terraform init` and `terraform validate` in CI (no AWS credentials needed).
3. Run `terraform plan` against a real AWS sandbox account (one-time manual step; document the output).
4. Add `infra/.github/workflows/terraform-validate.yml` that runs `init + validate + fmt check` on every PR.

---

### 4.3 No EKS Security Groups Defined

**Status:** Not Started
**Priority:** P2
**Effort:** S

**Problem:**
`eks.tf` relies on EKS-managed security groups. No explicit security groups are defined
to restrict inbound traffic to the cluster API endpoint or between node groups.

**What to add to `eks.tf`:**
- `aws_security_group` for the cluster API server (allow 443 from VPN CIDR only)
- Restrict node-to-node traffic to required ports only

---

## Area 5 — Scalability & Reliability (P2)

---

### 5.1 Agent Cannot Run Multiple Replicas

**Status:** Not Started
**Priority:** P2
**Effort:** L

**Problem:**
The correlator and notifier registry hold all state in-memory on a single process. Running
`replicaCount: 2` would cause two agents to independently process the same incident,
potentially applying the same remediation twice to the same deployment.

**What to build:**
1. After implementing persistence (Area 3 / item 1.3), add a distributed lock using the store.
2. **Option A (simpler):** Use a database advisory lock (SQLite WAL or PostgreSQL `pg_advisory_lock`) around the incident processing pipeline. One replica holds the lock; others skip.
3. **Option B (proper):** Add a Kubernetes lease (`coordination.k8s.io/v1 Lease`) for leader election — the agent already has `client-go`, which includes `leaderelection`. Only the leader processes incidents; others are hot standbys.
4. Update `charts/autosre/values.yaml` to allow `replicaCount: 2` once leader election is in place.

---

### 5.2 No Graceful Shutdown for In-Flight Remediations

**Status:** Partial
**Priority:** P2
**Effort:** S

**Problem:**
When a SIGTERM arrives (rolling deploy, node drain), the orchestrator calls `wg.Wait()` but
there is no timeout. An in-flight remediation (e.g. waiting for human approval for 30 minutes)
will block shutdown indefinitely.

**What to build:**
Wrap `wg.Wait()` with a context timeout in `run.go`:
```go
done := make(chan struct{})
go func() { wg.Wait(); close(done) }()
select {
case <-done:
case <-time.After(cfg.ShutdownTimeout):
    log.Warn("shutdown timeout reached; forcing exit")
}
```
Default `SHUTDOWN_TIMEOUT=30s`. Add to `config.go`.

---

### 5.3 Diagnoser Has No Retry / Backoff

**Status:** Not Started
**Priority:** P2
**Effort:** S

**Problem:**
If the diagnoser returns a 500 or times out, the orchestrator pipeline logs the error and
the incident is never remediated. There is no retry with backoff.

**What to build:**
Add exponential backoff (max 3 retries, 2s/4s/8s) to `diagnosis/client.go`'s `Diagnose()` call.
If all retries fail, the orchestrator falls back to `REQUIRE_APPROVAL` (fail-closed).

---

## Area 6 — Operational Readiness (P2)

---

### 6.1 No Log Rotation or Audit Log Retention Policy

**Status:** Not Started
**Priority:** P2
**Effort:** S

**Problem:**
`data/audit.jsonl` grows unbounded. On a busy cluster running 100 remediations/day,
this file is ~5 MB/day = ~1.8 GB/year. There is no rotation, compression, or deletion policy.

**What to build:**
1. Add `lumberjack` log rotation to `audit/file_sink.go` (`MaxSizeMB`, `MaxBackups`, `MaxAgeDays`).
2. Add `AUDIT_MAX_SIZE_MB`, `AUDIT_MAX_BACKUPS`, `AUDIT_MAX_AGE_DAYS` to `config.go`.
3. Add a note in `OPERATOR_RUNBOOK.md` about retention and SIEM export.

---

### 6.2 No Alerting on Agent Itself

**Status:** Not Started
**Priority:** P2
**Effort:** S

**Problem:**
If the autosre agent crashes or becomes unavailable, there is no alert. Operators would
discover the outage only when incidents stop being remediated, which could be hours later.

**What to build:**
Add to `charts/autosre/templates/`:
- `prometheusrule.yaml` with alerts:
  - `AutoSREAgentDown` — agent pod not ready for > 2 minutes
  - `AutoSRECircuitBreakerTripped` — CB tripped for > 10 minutes
  - `AutoSREKillSwitchEngaged` — kill switch on for > 30 minutes (might be forgotten)
  - `AutoSREHighRemediationFailureRate` — > 20% of remediations failing in 1h

---

### 6.3 No OpenAPI / API Documentation

**Status:** Not Started
**Priority:** P3
**Effort:** S

**Problem:**
The REST API (`/api/v1/incidents`, `/api/v1/approvals/pending`, etc.) has no machine-readable
specification. Integrating with external tools (PagerDuty, Grafana annotations, custom scripts)
requires reading the source code.

**What to build:**
Add `docs/openapi.yaml` (OpenAPI 3.1) covering all 10 API endpoints with request/response schemas.
Add a `/api/v1/openapi.json` handler in `api/api.go` that serves it.

---

### 6.4 Gitwriter Assumes Repo Is Already Cloned

**Status:** Not Started
**Priority:** P2
**Effort:** S

**Problem:**
`gitwriter.New()` takes a `RepoPath` and assumes a valid git repo exists there. In a fresh
pod (empty PVC), there is no repo. The first `EditField` call will fail with "repository does not exist".

**What to build:**
In `gitwriter.New()` or a new `gitwriter.Init()` function:
1. If `RepoPath` directory doesn't exist or isn't a git repo, run `git.PlainClone(RepoPath, false, &git.CloneOptions{URL: cfg.RemoteURL, Auth: ...})`.
2. Call this from `run.go` before creating the `orchestrator`.
3. Add an init-container to the Helm chart that clones the repo on first start.

---

## Area 7 — Compliance & Observability (P3)

---

### 7.1 No Metrics Endpoint

**Status:** Not Started
**Priority:** P3
**Effort:** S

**What to build:**
Add a Prometheus `/metrics` endpoint to the agent using `github.com/prometheus/client_golang`.
Key metrics:
- `autosre_incidents_total{failure_mode, outcome}` — counter
- `autosre_remediation_duration_seconds{action}` — histogram
- `autosre_approvals_pending` — gauge
- `autosre_circuit_breaker_state{state}` — gauge
- `autosre_kill_switch_engaged` — gauge

---

### 7.2 No Audit Log Encryption

**Status:** Not Started
**Priority:** P3
**Effort:** S

**Problem:**
`data/audit.jsonl` is written in plain text to a PVC. Audit logs may contain sensitive
information (pod names, namespaces, error messages, approver identities).

**What to build:**
Add optional AES-GCM encryption to `audit/file_sink.go` controlled by `AUDIT_ENCRYPTION_KEY`.

---

### 7.3 No SIEM / Log Export

**Status:** Not Started
**Priority:** P3
**Effort:** M

**Problem:**
Audit logs are local JSONL only. Enterprise compliance requirements (SOC2, ISO27001) typically
require log export to a centralized SIEM (Splunk, DataDog, Elastic, AWS CloudWatch).

**What to build:**
Add a `CloudWatchSink` and `WebhookSink` implementation of `audit.AuditSink`. Configure via `AUDIT_SINK_TYPE=cloudwatch|webhook|file`.

---

## Summary Checklist

| # | Area | Item | Priority | Effort | Status |
|---|---|---|---|---|---|
| 1.1 | Core | Git push to remote | P0 | M | Not Started |
| 1.2 | Security | JWT signature verification | P0 | M | Not Started |
| 1.3 | State | Persistent store (SQLite/PG) | P0 | L | Not Started |
| 2.1 | Security | Pod securityContext in Helm | P1 | S | Not Started |
| 2.2 | Security | Kubernetes NetworkPolicies | P1 | S | Not Started |
| 2.3 | Security | Secrets management (ESO/Vault) | P1 | M | Not Started |
| 2.4 | Security | TLS between internal services | P1 | M | Not Started |
| 2.5 | Security | API rate limiting | P1 | S | Not Started |
| 3.1 | Testing | Integration tests (kind cluster) | P1 | L | Not Started |
| 3.2 | Testing | End-to-end remediation test | P1 | L | Not Started |
| 3.3 | Testing | Load / benchmark tests | P2 | M | Not Started |
| 4.1 | Infra | Fix Terraform module ref bug | P1 | S | Not Started |
| 4.2 | Infra | Run Terraform against real AWS | P1 | M | Not Started |
| 4.3 | Infra | EKS explicit security groups | P2 | S | Not Started |
| 5.1 | Scale | Multi-replica / leader election | P2 | L | Not Started |
| 5.2 | Scale | Graceful shutdown timeout | P2 | S | Not Started |
| 5.3 | Scale | Diagnoser retry / backoff | P2 | S | Not Started |
| 6.1 | Ops | Log rotation + audit retention | P2 | S | Not Started |
| 6.2 | Ops | PrometheusRules for agent alerts | P2 | S | Not Started |
| 6.3 | Ops | OpenAPI documentation | P3 | S | Not Started |
| 6.4 | Ops | Auto-clone repo on empty PVC | P2 | S | Not Started |
| 7.1 | Observability | Prometheus /metrics endpoint | P3 | S | Not Started |
| 7.2 | Compliance | Audit log encryption | P3 | S | Not Started |
| 7.3 | Compliance | SIEM / CloudWatch export | P3 | M | Not Started |

---

## Suggested Implementation Phases

### Phase 1 — "It actually works end-to-end" (4–6 weeks)
Close the three P0 blockers. After this phase, the system can make a real git commit
that ArgoCD syncs, with proper auth.
- 1.1 Git push
- 1.2 JWT signature verification
- 1.3 Persistent store
- 4.1 + 4.2 Fix and validate Terraform

### Phase 2 — "Safe to give to a design partner" (4–6 weeks)
Security and testing hardening. After this phase, the system can be run in a staging
cluster with real workloads under supervision.
- 2.1 Pod securityContext
- 2.2 NetworkPolicies
- 2.3 Secrets management
- 2.5 API rate limiting
- 3.1 Integration tests
- 3.2 End-to-end test
- 5.2 Graceful shutdown
- 5.3 Diagnoser retry
- 6.4 Auto-clone on empty PVC

### Phase 3 — "Production-grade" (6–8 weeks)
Scalability, observability, and compliance. After this phase, the system can be
sold to enterprise customers and run 24/7 without babysitting.
- 5.1 Multi-replica / leader election
- 2.4 TLS between services
- 6.1 Log rotation
- 6.2 PrometheusRules
- 7.1 Metrics endpoint
- 7.3 SIEM export
- 4.3 EKS security groups
- 6.3 OpenAPI docs

### Phase 4 — "Enterprise-ready" (ongoing)
- 7.2 Audit log encryption
- SOC2 Type II audit
- Multi-cloud (GKE/AKS) support
- Marketplace remediation packs
- FinOps right-sizing actions

---

*Last updated: 2026-06-17*
*Current overall readiness: ~35% → Target after Phase 1: ~55% → Phase 2: ~70% → Phase 3: ~85%*
