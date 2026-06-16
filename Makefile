# =============================================================================
# AutoSRE Platform — Makefile
# =============================================================================
# Usage:
#   make setup       Install all toolchain dependencies (Go, Python, kind, etc.)
#   make lint        Run all linters (Go + Python)
#   make test        Run all tests (Go + Python)
#   make kind-up     Create the local kind cluster
#   make kind-down   Destroy the local kind cluster
# =============================================================================

SHELL := /bin/bash
.DEFAULT_GOAL := help

GO_VERSION     := 1.22
PYTHON_VERSION := 3.11
KIND_CLUSTER   := autosre-dev
KUBECONFIG_OUT := kubeconfig-kind.yaml

# Detect whether we're in CI (GitHub Actions sets CI=true)
CI ?= false

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------
.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Setup — install toolchain dependencies
# ---------------------------------------------------------------------------
.PHONY: setup
setup: ## Install Go, Python deps, kind, kubectl, golangci-lint
	@echo "==> Checking Go..."
	@command -v go >/dev/null 2>&1 || { \
		echo "Go not found. Install Go $(GO_VERSION) from https://go.dev/dl/ then re-run make setup."; \
		exit 1; \
	}
	@echo "==> Installing Go tools..."
	cd agent && go mod download
	@command -v golangci-lint >/dev/null 2>&1 || \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | \
		sh -s -- -b $$(go env GOPATH)/bin v1.59.1
	@echo "==> Installing Python deps (diagnoser)..."
	cd diagnoser && python$(PYTHON_VERSION) -m venv .venv && \
		.venv/bin/pip install -q --upgrade pip && \
		.venv/bin/pip install -q -e ".[dev]"
	@echo "==> Installing Python deps (learner)..."
	cd learner && python$(PYTHON_VERSION) -m venv .venv && \
		.venv/bin/pip install -q --upgrade pip && \
		.venv/bin/pip install -q -e ".[dev]"
	@echo "==> Checking kind..."
	@command -v kind >/dev/null 2>&1 || { \
		echo "kind not found. Install from https://kind.sigs.k8s.io/docs/user/quick-start/#installation"; \
		exit 1; \
	}
	@echo "==> Setup complete."

# ---------------------------------------------------------------------------
# Lint
# ---------------------------------------------------------------------------
.PHONY: lint lint-go lint-python
lint: lint-go lint-python ## Run all linters

lint-go: ## Run golangci-lint on agent/
	@echo "==> Go lint..."
	cd agent && golangci-lint run ./...

lint-python: ## Run black + ruff + mypy on diagnoser/ and learner/
	@echo "==> Python lint (diagnoser)..."
	cd diagnoser && .venv/bin/black --check . && .venv/bin/ruff check . && .venv/bin/mypy diagnoser/
	@echo "==> Python lint (learner)..."
	cd learner  && .venv/bin/black --check . && .venv/bin/ruff check . && .venv/bin/mypy learner/

# ---------------------------------------------------------------------------
# Format (auto-fix, for local dev)
# ---------------------------------------------------------------------------
.PHONY: fmt
fmt: ## Auto-format Go and Python sources
	@echo "==> gofmt..."
	cd agent && gofmt -w .
	@echo "==> black + ruff fix (diagnoser)..."
	cd diagnoser && .venv/bin/black . && .venv/bin/ruff check --fix .
	@echo "==> black + ruff fix (learner)..."
	cd learner   && .venv/bin/black . && .venv/bin/ruff check --fix .

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------
.PHONY: test test-go test-python
test: test-go test-python ## Run all tests

test-go: ## Run Go tests with race detector
	@echo "==> Go tests..."
	cd agent && go test ./... -race -count=1

test-python: ## Run pytest for diagnoser and learner
	@echo "==> Python tests (diagnoser)..."
	cd diagnoser && .venv/bin/pytest
	@echo "==> Python tests (learner)..."
	cd learner   && .venv/bin/pytest

# ---------------------------------------------------------------------------
# Local Kubernetes cluster (kind)
# ---------------------------------------------------------------------------
.PHONY: kind-up
kind-up: ## Create the local kind cluster (autosre-dev)
	@echo "==> Starting kind cluster '$(KIND_CLUSTER)'..."
	kind create cluster --name $(KIND_CLUSTER) --config kind-config.yaml \
		--kubeconfig $(KUBECONFIG_OUT)
	@echo "==> Cluster ready. Export KUBECONFIG with:"
	@echo "    export KUBECONFIG=\$$PWD/$(KUBECONFIG_OUT)"

.PHONY: kind-down
kind-down: ## Destroy the local kind cluster
	@echo "==> Deleting kind cluster '$(KIND_CLUSTER)'..."
	kind delete cluster --name $(KIND_CLUSTER)
	@rm -f $(KUBECONFIG_OUT)
	@echo "==> Cluster deleted."

.PHONY: kind-status
kind-status: ## Show status of the local kind cluster
	kind get clusters | grep $(KIND_CLUSTER) && \
		kubectl --kubeconfig=$(KUBECONFIG_OUT) get nodes || \
		echo "Cluster '$(KIND_CLUSTER)' is not running."

# ---------------------------------------------------------------------------
# Build (placeholder — no Docker images yet)
# ---------------------------------------------------------------------------
.PHONY: build
build: ## Build Go binary (no Docker yet)
	@echo "==> Building Go agent..."
	cd agent && go build -o bin/autosre ./cmd/autosre/

# ---------------------------------------------------------------------------
# Clean
# ---------------------------------------------------------------------------
.PHONY: clean
clean: ## Remove build artifacts
	rm -rf agent/bin/ agent/coverage.out
	rm -rf diagnoser/.venv diagnoser/.pytest_cache diagnoser/.mypy_cache
	rm -rf learner/.venv learner/.pytest_cache learner/.mypy_cache
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
