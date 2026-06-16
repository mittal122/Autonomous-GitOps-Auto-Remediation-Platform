"""Tests for compute_stats — pure function, no I/O."""

from datetime import datetime, timezone

import pytest

from learner.aggregator import compute_stats
from learner.contracts import Outcome


def make_outcome(
    failure_mode: str = "OOMKilled",
    proposed_action: str = "bump-memory-limit",
    verification_outcome: str = "RECOVERED",
    applied: bool = True,
) -> Outcome:
    return Outcome(
        incident_id="inc-1",
        trace_id="trace-1",
        failure_mode=failure_mode,
        proposed_action=proposed_action,
        verdict="AUTO",
        applied=applied,
        verification_outcome=verification_outcome,
        timestamp=datetime(2026, 6, 16, 10, 0, 0, tzinfo=timezone.utc),
    )


def test_empty_outcomes():
    stats = compute_stats([])
    assert stats.total_outcomes == 0
    assert stats.updated_at is None
    assert stats.by_failure_mode_action == {}


def test_single_recovered():
    stats = compute_stats([make_outcome()])
    assert stats.total_outcomes == 1
    key = "OOMKilled/bump-memory-limit"
    assert key in stats.by_failure_mode_action
    s = stats.by_failure_mode_action[key]
    assert s.attempts == 1
    assert s.recovered == 1
    assert s.failed == 0
    assert s.inconclusive == 0
    assert s.success_rate == 1.0


def test_mixed_outcomes():
    outcomes = [
        make_outcome(verification_outcome="RECOVERED"),
        make_outcome(verification_outcome="RECOVERED"),
        make_outcome(verification_outcome="FAILED"),
        make_outcome(verification_outcome="INCONCLUSIVE"),
    ]
    stats = compute_stats(outcomes)
    key = "OOMKilled/bump-memory-limit"
    s = stats.by_failure_mode_action[key]
    assert s.attempts == 4
    assert s.recovered == 2
    assert s.failed == 1
    assert s.inconclusive == 1
    assert s.success_rate == pytest.approx(0.5, abs=1e-4)


def test_multiple_keys():
    outcomes = [
        make_outcome(failure_mode="OOMKilled", proposed_action="bump-memory-limit", verification_outcome="RECOVERED"),
        make_outcome(failure_mode="CrashLoopBackOff", proposed_action="rollback-deployment", verification_outcome="FAILED"),
        make_outcome(failure_mode="OOMKilled", proposed_action="bump-memory-limit", verification_outcome="FAILED"),
    ]
    stats = compute_stats(outcomes)
    assert len(stats.by_failure_mode_action) == 2
    assert stats.total_outcomes == 3

    oom = stats.by_failure_mode_action["OOMKilled/bump-memory-limit"]
    assert oom.attempts == 2
    assert oom.recovered == 1
    assert oom.success_rate == pytest.approx(0.5, abs=1e-4)

    crash = stats.by_failure_mode_action["CrashLoopBackOff/rollback-deployment"]
    assert crash.attempts == 1
    assert crash.recovered == 0
    assert crash.success_rate == 0.0


def test_block_verdict_no_verification():
    """BLOCK outcomes have no verification; they should still count as attempts."""
    o = make_outcome(verification_outcome="", applied=False)
    o = o.model_copy(update={"verdict": "BLOCK"})
    stats = compute_stats([o])
    key = "OOMKilled/bump-memory-limit"
    s = stats.by_failure_mode_action[key]
    assert s.attempts == 1
    assert s.recovered == 0
    assert s.success_rate == 0.0


def test_case_insensitive_verification_outcome():
    """The aggregator should normalise verification outcome casing."""
    o_lower = make_outcome(verification_outcome="recovered")
    stats = compute_stats([o_lower])
    key = "OOMKilled/bump-memory-limit"
    assert stats.by_failure_mode_action[key].recovered == 1


def test_updated_at_is_set():
    stats = compute_stats([make_outcome()])
    assert stats.updated_at is not None
