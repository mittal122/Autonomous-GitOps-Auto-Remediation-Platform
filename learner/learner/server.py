"""FastAPI service for the AutoSRE Learner.

Endpoints:
  POST /outcome  — record a completed pipeline outcome (append-only, advisory)
  GET  /stats    — return aggregated success-rate stats (read-only, advisory)
  GET  /healthz  — liveness probe

Safety contract:
  - This service never writes to git, never calls the policy engine, and never
    modifies the diagnosis layer. Stats are for human observability only.
  - POST /outcome is idempotent in effect (duplicate posts update stats but do
    not remove existing records — the store is append-only).
  - GET /stats never alters state.
"""

import os

from fastapi import FastAPI

from .aggregator import compute_stats
from .contracts import Outcome, StatsResponse
from .store import OutcomeStore

app = FastAPI(
    title="AutoSRE Learner",
    description="Observational learning layer — records pipeline outcomes and surfaces advisory stats.",
    version="0.8.0",
)


def _store() -> OutcomeStore:
    """Return a store pointed at the configured outcomes file.

    The path is re-read from the environment on every request so that tests
    can override it with monkeypatch without restarting the server.
    """
    path = os.getenv("LEARNER_STATS_PATH", "./data/outcomes.jsonl")
    return OutcomeStore(path)


@app.post("/outcome", status_code=204)
async def post_outcome(outcome: Outcome) -> None:
    """Record a completed pipeline outcome.

    Append-only: this endpoint never modifies or deletes existing records.
    The Go agent calls this non-fatally — a 5xx response is logged and ignored.
    """
    _store().append(outcome)


@app.get("/stats", response_model=StatsResponse)
async def get_stats() -> StatsResponse:
    """Return aggregated success-rate stats by (failure_mode, action).

    Advisory only — these stats are for human observability and are never
    fed back into the policy engine or diagnosis layer.

    // TODO: wire opt-in confidence hints into the response once >= 100 outcomes
    //        per key are recorded and the hint has been validated in production.
    """
    outcomes = _store().load_all()
    return compute_stats(outcomes)


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok"}
