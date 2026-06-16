"""Integration tests for the FastAPI learner server."""

import pathlib

import pytest
from fastapi.testclient import TestClient

from learner.server import app

client = TestClient(app)

VALID_OUTCOME = {
    "incident_id": "inc-1",
    "trace_id": "trace-abc",
    "failure_mode": "OOMKilled",
    "proposed_action": "bump-memory-limit",
    "verdict": "AUTO",
    "applied": True,
    "verification_outcome": "RECOVERED",
    "timestamp": "2026-06-16T10:00:00Z",
}


# ---------------------------------------------------------------------------
# /healthz
# ---------------------------------------------------------------------------


def test_healthz_returns_ok():
    r = client.get("/healthz")
    assert r.status_code == 200
    assert r.json() == {"status": "ok"}


# ---------------------------------------------------------------------------
# POST /outcome
# ---------------------------------------------------------------------------


def test_post_outcome_valid(tmp_path, monkeypatch):
    monkeypatch.setenv("LEARNER_STATS_PATH", str(tmp_path / "outcomes.jsonl"))
    r = client.post("/outcome", json=VALID_OUTCOME)
    assert r.status_code == 204


def test_post_outcome_missing_required_field():
    bad = {k: v for k, v in VALID_OUTCOME.items() if k != "failure_mode"}
    r = client.post("/outcome", json=bad)
    assert r.status_code == 422


def test_post_outcome_malformed_json():
    r = client.post("/outcome", content=b"not-json", headers={"Content-Type": "application/json"})
    assert r.status_code == 422


def test_post_outcome_extra_fields_ignored(tmp_path, monkeypatch):
    monkeypatch.setenv("LEARNER_STATS_PATH", str(tmp_path / "outcomes.jsonl"))
    payload = {**VALID_OUTCOME, "unknown_field": "ignored"}
    r = client.post("/outcome", json=payload)
    assert r.status_code == 204


def test_post_outcome_empty_verification_outcome(tmp_path, monkeypatch):
    monkeypatch.setenv("LEARNER_STATS_PATH", str(tmp_path / "outcomes.jsonl"))
    payload = {**VALID_OUTCOME, "verification_outcome": ""}
    r = client.post("/outcome", json=payload)
    assert r.status_code == 204


# ---------------------------------------------------------------------------
# GET /stats
# ---------------------------------------------------------------------------


def test_get_stats_empty(tmp_path, monkeypatch):
    monkeypatch.setenv("LEARNER_STATS_PATH", str(tmp_path / "outcomes.jsonl"))
    r = client.get("/stats")
    assert r.status_code == 200
    data = r.json()
    assert data["total_outcomes"] == 0
    assert data["by_failure_mode_action"] == {}
    assert data["updated_at"] is None


def test_get_stats_after_posting(tmp_path, monkeypatch):
    monkeypatch.setenv("LEARNER_STATS_PATH", str(tmp_path / "outcomes.jsonl"))

    for _ in range(3):
        r = client.post("/outcome", json=VALID_OUTCOME)
        assert r.status_code == 204

    r = client.get("/stats")
    assert r.status_code == 200
    data = r.json()
    assert data["total_outcomes"] == 3

    key = "OOMKilled/bump-memory-limit"
    assert key in data["by_failure_mode_action"]
    s = data["by_failure_mode_action"][key]
    assert s["attempts"] == 3
    assert s["recovered"] == 3
    assert s["success_rate"] == pytest.approx(1.0, abs=1e-4)


def test_stats_advisory_only(tmp_path: pathlib.Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """GET /stats must not trigger any write (idempotent read)."""
    monkeypatch.setenv("LEARNER_STATS_PATH", str(tmp_path / "outcomes.jsonl"))
    r1 = client.get("/stats")
    r2 = client.get("/stats")
    assert r1.json()["total_outcomes"] == r2.json()["total_outcomes"]
