# Autonomous GitOps & Auto-Remediation Platform

A closed-loop SRE control plane that watches a Kubernetes cluster, diagnoses failures with an LLM, and applies safe GitOps-native auto-remediations with rollback — escalating to humans only when it can't fix something.

**Core loop:** Detect → Diagnose → Decide → Remediate → Verify

## Architecture

| Component | Language | Purpose |
|-----------|----------|---------|
| `agent/` | Go | Long-running controller: ingestion, correlation, policy, remediation, verification |
| `diagnoser/` | Python | LLM-powered root-cause diagnosis |
| `learner/` | Python | Outcome tracking and learning loop |
| `infra/` | Terraform | Cloud infrastructure (EKS, IAM, networking) |
| `charts/` | Helm | Kubernetes application packaging |
| `gitops/` | YAML | ArgoCD application definitions |

## Quick Start

```bash
# Install toolchain dependencies
make setup

# Spin up a local kind cluster
make kind-up

# Run linters
make lint

# Run tests
make test

# Tear down the local cluster
make kind-down
```

## Development

See [docs/CONVENTIONS.md](docs/CONVENTIONS.md) for coding standards.

Copy `.env.example` to `.env` and fill in values before running any service.

## Status

**Current phase:** Prompt 0 — Foundation (scaffolding only, no feature logic yet)

See [docs/PROJECT_PROGRESS.md](docs/PROJECT_PROGRESS.md) for the full roadmap.
