# Coding Conventions & Development Standards

## Table of Contents
1. [Go (`agent/`)](#go-agent)
2. [Python (`diagnoser/`, `learner/`)](#python)
3. [Commit Messages](#commit-messages)
4. [Branching Strategy](#branching-strategy)
5. [Secrets & Configuration](#secrets--configuration)
6. [Testing Approach](#testing-approach)
7. [Error Handling](#error-handling)

---

## Go (`agent/`)

### Formatting & Linting
- **`gofmt`** is the canonical formatter. Run `make fmt` before committing.
- **`golangci-lint`** enforces the lint ruleset in `agent/.golangci.yml`. CI blocks merges on lint failures. Run `make lint-go` locally.
- **`goimports`** manages import groups: stdlib → third-party → internal (`github.com/autosre/agent/...`), each separated by a blank line.

### Project Layout
- Follow the [Go Standard Project Layout](https://github.com/golang-standards/project-layout).
- `cmd/autosre/` — binary entrypoint only; minimal logic, just wiring.
- `internal/` — all implementation packages; not importable outside this module.
- `internal/contracts/` — canonical interfaces and types; zero dependencies on other internal packages.

### Package Naming
- One word, lowercase, no underscores: `ingestor`, `correlator`, `remediator`, not `signal_ingestor`.
- Package name must match the directory name.

### Error Handling
- Always wrap errors with context: `fmt.Errorf("ingestor: process signal %s: %w", id, err)`.
- Never swallow errors silently — either return them or log them with the full context.
- Use `errors.Is` / `errors.As` for type-aware error inspection; never string matching.

### Comments
- Exported types and functions must have doc comments (`// TypeName ...`).
- Internal helpers: only comment the **why**, not the what.
- No multi-paragraph comment blocks; one clear sentence is almost always enough.

---

## Python

### Formatting & Linting
- **`black`** (line-length 100) is the canonical formatter. `make fmt` runs it.
- **`ruff`** enforces PEP 8, import order, and common anti-patterns. Config in `pyproject.toml`.
- **`mypy`** enforces strict type hints. All public functions must be annotated.

### Module & Function Naming
- Modules: `snake_case` (e.g., `contracts.py`, `llm_provider.py`).
- Functions: `snake_case`; async functions prefix with no special convention but must be `async def`.
- Classes: `PascalCase` (e.g., `LLMProvider`, `GeminiProvider`).

### Type Hints
- All function signatures must include parameter and return type annotations.
- Use `from __future__ import annotations` for forward references.
- Prefer `X | Y` over `Optional[X]` / `Union[X, Y]` (Python 3.10+ syntax).

### Comments
- Same philosophy as Go: comment the **why**, not the what.
- Prefer descriptive names over explanatory comments.

---

## Commit Messages

We follow **Conventional Commits** (`https://www.conventionalcommits.org`).

```
<type>(<scope>): <short description>

[optional body]

[optional footer: BREAKING CHANGE or closes #issue]
```

**Types:**
| Type | When to use |
|------|-------------|
| `feat` | New feature / capability |
| `fix` | Bug fix |
| `chore` | Tooling, deps, config — no code logic change |
| `docs` | Documentation only |
| `test` | Tests only — no production code change |
| `refactor` | Code restructure without behavior change |
| `ci` | CI/CD pipeline changes |

**Examples:**
```
chore(agent): initialize go module and project skeleton
feat(contracts): add Signal, Incident, Diagnosis types
fix(diagnoser): handle missing incident ID in diagnosis output
docs: add CONVENTIONS.md
```

---

## Branching Strategy

- `main` — always green; protected; requires passing CI and one approval.
- Feature branches: `promptN-<short-desc>` (e.g., `prompt0-foundation`, `prompt1-detection`).
- Branches are short-lived; one branch per prompt or small independent fix.
- Rebase on main before merging; no merge commits on main.

---

## Secrets & Configuration

- **Never commit secrets.** `.env` is gitignored; `.env.example` is the only committed env file.
- All runtime configuration is passed via environment variables (12-factor: `https://12factor.net/config`).
- Document every variable in `.env.example` with a placeholder value and a comment.
- If a variable is sensitive (API key, token), its placeholder value must be obviously fake (e.g., `your-api-key-here`, not a real-looking string).
- In Go, load config at startup via `os.Getenv`; fail fast with a clear message if a required variable is missing.
- In Python, same pattern; use `os.getenv("KEY")` with a `None` guard or default.

---

## Testing Approach

### Go
- Table-driven tests for all non-trivial logic.
- Use the standard `testing` package; no third-party test framework.
- Tests live in `_test.go` files alongside the code they test, or in a separate `package foo_test` for black-box tests.
- Race detector enabled in CI (`go test -race`).
- Target: ≥ 80% coverage for business logic packages.

### Python
- **pytest** is the test runner. Config in `pyproject.toml` under `[tool.pytest.ini_options]`.
- Tests live in `tests/` at the root of each Python service.
- Use `pytest.mark.parametrize` for table-driven cases.
- No mocking of external services at the unit level — stub the interface, not the HTTP call.
- Target: ≥ 80% coverage for business logic modules; contracts and entrypoints at 100%.

### General
- Tests must pass before any code is merged.
- New features require new tests; bug fixes require a regression test.
- Flakey tests are treated as bugs and fixed immediately, not skipped.
