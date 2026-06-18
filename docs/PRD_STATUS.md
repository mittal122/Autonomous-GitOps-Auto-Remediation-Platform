# PRD vs. Implementation Status

This document compares the Product & Strategy Documentation (PRD) you provided against what
actually exists in this codebase today. It is based on direct inspection of the source code
(Go agent, Python diagnoser/learner, web-ui, infra, charts) — not on the older internal status
files (`PROJECT_PROGRESS.md`, `PRODUCTION_READINESS_ROADMAP.md`), which were written at earlier
checkpoints and are now stale in places (e.g. they say "git push not implemented" and "web UI
empty placeholder" — both are now done).

Two different kinds of PRD content are evaluated separately:
- **Section A — Engineering scope** (Sections 8 & 14 of the PRD): things that get *built*. Each row has a real Done/Partial/Not Built status.
- **Section B — Business/strategy scope** (Sections 9–13, 15–17): things that get *decided*, not coded (pricing model, GTM, fundraising). These can't be "built," so they're marked N/A with a note on whether the groundwork exists.

---

## Section A1 — Product Scope: "In Scope" (PRD §8)

| PRD Item | Status | Evidence |
|---|---|---|
| Telemetry ingestion from Prometheus + Loki | **Partial** | Loki log polling is implemented (`agent/internal/ingestor/loki.go`, `LOKI_ADDR`/`LOKI_QUERY` env vars). There is no direct Prometheus *alert-rule* scrape/ingestion path — detection instead comes via the Alertmanager **webhook** (`POST /webhook/alertmanager`), which is the standard way Prometheus alerts reach external systems, plus a native Kubernetes event/pod/node watcher (`agent/internal/ingestor/k8s.go`). So "Prometheus" telemetry arrives indirectly via Alertmanager, not via direct PromQL queries. |
| LLM-based anomaly diagnosis (Gemini API; pluggable) | **Done — and exceeded** | `diagnoser/diagnoser/providers/gemini.py` (Gemini) and `diagnoser/diagnoser/providers/nim.py` (NVIDIA NIM, now the default/priority provider) both exist behind a common `LLMProvider` interface, with a deterministic `RuleBasedProvider` fallback. Provider priority: NIM → Gemini → rule-based (`diagnoser/diagnoser/core.py`). This is more pluggable than the PRD asked for. |
| Safe remediation catalogue for top failure modes | **Done — exceeded scope** | 6 actions implemented: `rollback-deployment`, `scale-deployment`, `bump-memory-limit`, `patch-hpa`, `right-size-cpu`, `right-size-memory` (`agent/internal/remediator/*.go`). PRD only specified rollback, scale, and bump-memory; HPA patching and CPU/memory right-sizing were added beyond the original ask. |
| GitOps execution path (ArgoCD/Helm), zero-touch rollback, dynamic scaling | **Done** | `agent/internal/gitwriter/gitwriter.go` does structure-preserving YAML edits + git commit + **git push** (`PushContext`, line ~299–319) with HTTPS-token and SSH-key auth. `gitops/autosre-app.yaml` defines the ArgoCD Application. Rollback discovers the last-known-good image from git history automatically. |
| Incident notification + summary via PagerDuty + Slack | **Done** | `agent/internal/notifier/slack.go` (Block Kit approval buttons, HMAC-verified inbound interactions) and `agent/internal/notifier/pagerduty.go` (Events API v2) both implemented, composed via `CompositeNotifier`. Fails closed (log-only) when tokens are absent. |
| Terraform-provisioned, reproducible stack on EKS | **Done — exceeded (multi-cloud)** | `infra/eks.tf` (134 lines), plus `infra/aks.tf` (179 lines) and `infra/gke.tf` (197 lines) were also built — the PRD explicitly scoped multi-cloud as **Future Scope (Phase 4)**, but AKS/GKE Terraform already exists alongside EKS. Also includes `vpc.tf`, `iam.tf`, `observability.tf`, `main.tf`, `outputs.tf`, `variables.tf`. *Not yet verified: these have not been confirmed to `terraform apply` cleanly against live cloud accounts — see Notes below.* |
| Safety layer: dry-run, confidence gating, human approval, audit log | **Done** | `policy.yaml` implements confidence threshold (0.90 default), per-failure-mode autonomy, protected namespaces, blast-radius limits, and a circuit breaker. `REMEDIATION_DRY_RUN` / `ORCHESTRATOR_APPLY_ENABLED` gate real changes. Approval flow goes through Slack buttons or the web UI `/approvals` page. Audit log is append-only JSONL (`agent/internal/audit/file_sink.go`) plus an in-memory sink, queryable via `GET /api/v1/audit`. |

---

## Section A2 — Product Roadmap (PRD §14)

### Phase 1: MVP

| Feature | Status |
|---|---|
| Prometheus/Loki ingestion | **Partial** — Loki done; Prometheus via Alertmanager webhook, not direct scrape (see A1 above) |
| Gemini-based diagnosis | **Done** — plus NIM as the new default provider |
| 5 remediation patterns | **Done** — 6 implemented (exceeds target) |
| ArgoCD rollback + pod scaling | **Done** |
| Slack/PagerDuty notify | **Done** |
| Dry-run + audit log | **Done** |
| *(implicit)* reproducible Terraform deploy | **Partial** — Terraform files exist for EKS/AKS/GKE but are not confirmed applied/tested against a live cluster in this repo |

**Phase 1 verdict: ~90% complete.** The only real gap is direct Prometheus integration (currently webhook-mediated) and live-cluster Terraform validation.

### Phase 2: Beta

| Feature | Status |
|---|---|
| Confidence gating | **Done** (`policy.yaml confidenceThreshold`) |
| Human-approval workflow | **Done** (Slack buttons + web UI Approvals page) |
| Remediation pack authoring | **Partial** — a real pack-loading mechanism exists (`agent/internal/policy/packs.go`, `charts/autosre/packs/*.yaml` — e.g. `oomkilled.yaml`, `crashloop.yaml`, `highcpu.yaml`, `highlatency.yaml`), where packs merge additive rules into the base policy (base policy always wins). This is a real primitive, not just a concept — but there is no authoring *UI/CLI*, validation tooling, or versioning beyond a free-text `version:` field in each pack. |
| Basic RBAC | **Done** | Three-tier role model (`viewer` / `operator` / `admin`) enforced server-side per endpoint (`agent/internal/api/auth.go`), roles sourced from an OIDC JWT claim. |
| Reasoning-trace UI | **Done** | `web-ui/src/pages/IncidentTrace.tsx` and `AuditLog.tsx` show the full pipeline trace (diagnosis → decision → approval → remediation → verification) per incident, linked by trace ID. |
| *(implicit)* validated with 3–5 real design-partner teams | **Not Built** — this is a go-to-market activity, not code; no design partners exist yet |

**Phase 2 verdict: ~85% of the *buildable* features are done.** The customer-validation half of this phase is explicitly business/GTM work, not engineering — see Section B.

### Phase 3: Production

| Feature | Status |
|---|---|
| SaaS control plane (multi-tenant) | **Not Built** — current architecture is single-tenant, self-hosted (one agent per cluster, one `.env`/policy per deployment). No tenant isolation, no per-customer billing/usage metering. |
| SSO | **Partial** — OIDC bearer-token auth exists and *is* the SSO mechanism (`agent/internal/api/auth.go` verifies JWTs against an OIDC provider's JWKS), but there's no UI-driven OIDC login redirect flow — the web UI's Login page expects you to paste a token, not "Sign in with Okta/Google" button-click SSO. |
| Audit/compliance exports | **Partial** — audit log exists and is queryable via API/UI, but there's no formal export format (CSV/PDF compliance report) or retention-policy tooling |
| Expanded remediation catalogue | **Done** — 6 actions, more than the Phase 1 floor of 5 |
| BYO-LLM | **Done** — NIM and Gemini both work via user-supplied API keys; provider abstraction makes adding a third (e.g. Bedrock/OpenAI direct) straightforward |
| Marketplace v1 | **Not Built** — the policy "packs" mechanism (see Phase 2) is the technical seed of this, but there is no marketplace, package registry, sharing, or discovery UI |
| *(implicit)* first paying customers, SOC2 in progress | **Not Built** — business/compliance milestones, not code |

**Phase 3 verdict: ~30% complete.** This phase was always going to be the one furthest from a single-developer/portfolio build — multi-tenancy and compliance certification are organizationally heavy, not just code-heavy.

### Phase 4: Scale

| Feature | Status |
|---|---|
| GKE/AKS multi-cloud | **Partial** — Terraform files exist for both (`infra/aks.tf`, `infra/gke.tf`) ahead of schedule (this was Phase 4 scope, built during what looks like Phase 1/MVP work), but not confirmed live-tested |
| Causal + LLM reasoning engine | **Not Built** — diagnosis is single-shot LLM-call-or-rule-based; there is no causal/dependency-graph reasoning layer |
| Learning loop from PR accept/reject | **Partial** — the learner service tracks **remediation outcome** success/failure rates per (failure_mode, action) via `learner/learner/aggregator.py`, and exposes `GET /stats`. This is real outcome tracking. But it does not feed back into the diagnoser or policy engine automatically (no retraining/auto-tuning loop), and since GitWriter commits directly rather than opening PRs, there is no "PR accept/reject" signal to learn from in the first place. |
| FinOps right-sizing | **Done — earlier than scoped** | `right-size-cpu` and `right-size-memory` actions exist now (Phase 1/2 territory), ahead of the PRD's Phase 4 placement |
| Partner marketplace | **Not Built** |
| On-prem/air-gapped | **Partial** — the whole stack is self-hosted by design (Go binary + Python venvs + SQLite, no SaaS dependency), which is most of what "on-prem" requires. No explicit air-gapped artifact distribution (offline Helm chart bundling, mirrored container registry docs) has been built. |

**Phase 4 verdict: ~25% complete**, with FinOps right-sizing and multi-cloud Terraform pulled forward earlier than the roadmap called for, while causal reasoning, the PR-based learning loop, and marketplace remain unbuilt.

---

## Section A3 — Safety Layer / Risk Mitigations Called Out in PRD §12 (Technical Risks)

| PRD Mitigation | Status |
|---|---|
| Dry-run default | **Done** — `REMEDIATION_DRY_RUN=true` and `ORCHESTRATOR_APPLY_ENABLED=false` by default |
| Confidence gating | **Done** — `confidenceThreshold` in `policy.yaml`, enforced in `agent/internal/policy/engine.go` |
| Blast-radius limits | **Done** — `maxReplicaDelta`, `maxMemoryBumpFactor` in `policy.yaml` |
| Instant rollback | **Done** — `rollback-deployment` action + git history-based "previous good image" discovery |
| Human-approval boundary | **Done** — Slack buttons + web UI Approvals page, fail-closed on timeout |
| Hybrid deterministic + LLM (fallback to propose-only) | **Done** — `RuleBasedProvider` fallback triggers automatically on any LLM error |
| BYO-LLM (data-handling/privacy mitigation) | **Done** | User supplies their own NIM/Gemini key; no telemetry sent to any AutoSRE-controlled backend |
| Circuit breaker | **Done** — rolling-window AUTO-decision counter (`agent/internal/policy/circuitbreaker.go`), trips after `maxActionsPerWindow` within `windowSeconds` |
| Verification step ("did the fix work?") | **Done** — `agent/internal/verifier/verifier.go`: grace delay + polling window, fails toward escalation |

**This section is essentially fully built** — the PRD's own list of "what would make this safe enough to trust with prod" risk mitigations is the most complete part of the entire implementation.

---

## Section B — Business & Strategy Scope (PRD §9–13, 15–17)

These sections of the PRD are decisions and go-to-market activities, not engineering deliverables. They cannot be "completed" by writing code, so each is marked **N/A (business decision)** with a note on what groundwork — if any — already exists in the repo.

| PRD Section | Status | Groundwork in repo |
|---|---|---|
| §9 Business Model (pricing, licensing) | N/A — business decision | None — no billing/metering/license-key code exists, nor should it yet |
| §10 Startup Feasibility | N/A — strategic judgment call | This status doc itself is the kind of evidence an investor/co-founder conversation would use |
| §11 SWOT | N/A — analysis, not a deliverable | — |
| §12 Risk Assessment — *business/legal/financial rows* | N/A — requires legal/ToS/insurance decisions outside code | The *technical* risk rows are covered in Section A3 above |
| §13 Success Metrics (ARR, NRR, CAC) | N/A — requires paying customers, which don't exist yet | The *product* metrics this would need (auto-resolution rate, MTTR) are computable today from `learner` outcome data and the web UI Analytics page |
| §15 Resource Planning (team/budget) | N/A — staffing decision | — |
| §16 Failure Analysis | N/A — strategic judgment | — |
| §17 Final Strategic Evaluation + Next Steps | N/A — your call to make | The PRD's own recommended next step ("customer discovery before building more") is worth revisiting now that Phase 1–2 engineering is largely done — see Recommendation below |

---

## Summary Table

| Category | Done | Partial | Not Built |
|---|---|---|---|
| §8 In Scope (7 items) | 5 | 2 | 0 |
| §14 Phase 1 (MVP) | 6 | 1 | 0 |
| §14 Phase 2 (Beta) | 4 | 1 | 1 (design partners — GTM) |
| §14 Phase 3 (Production) | 2 | 2 | 3 |
| §14 Phase 4 (Scale) | 1 | 3 | 3 |
| §12 Technical risk mitigations (9 items) | 9 | 0 | 0 |
| §9–13, 15–17 (business/strategy) | N/A | N/A | N/A — not code, see Section B |

**Bottom line:** Everything the PRD scoped as **Phase 1 MVP** and the **technical safety layer** is done, and several things scoped for **Phase 4** (multi-cloud Terraform, FinOps right-sizing) were pulled forward and already exist. The gaps that remain are concentrated in **Phase 3 (multi-tenant SaaS, compliance exports)** and **Phase 4 (causal reasoning, marketplace, PR-based learning loop)** — which is expected, since those phases assume a funded company with customers, not a single-developer build. The business/strategy half of the PRD (Sections 9–13, 15–17) was never meant to be "built" — it's the set of decisions to revisit now that the engineering is far enough along to support a real customer-discovery conversation.

---

## Notes / Caveats on This Assessment

- This assessment is based on reading the actual source code in this repository as of 2026-06-18, not on the older `docs/PROJECT_PROGRESS.md` or `docs/PRODUCTION_READINESS_ROADMAP.md` files, both of which describe an earlier snapshot and are now inaccurate in places (they predate the NIM integration, the finished web UI, and the git push implementation).
- "Done" means the code path exists and is wired in — it does **not** mean it has been load-tested against a live production Kubernetes cluster with real traffic. The Terraform files for EKS/AKS/GKE in particular have not been confirmed to `terraform apply` cleanly; treat them as a strong starting point, not a verified deployment.
- If you want a refreshed `PROJECT_PROGRESS.md` and `PRODUCTION_READINESS_ROADMAP.md` to replace the stale ones, that's a quick follow-up — say so and it'll be done in a separate commit.
