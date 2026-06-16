"""Tests for diagnoser contract types.

These are compile-time / structural tests: they prove the dataclasses
are importable and constructable before any logic is implemented.
"""

import pytest
from diagnoser.contracts import Diagnosis, Incident, LLMProvider, Signal


def test_signal_construction() -> None:
    sig = Signal(
        id="sig-001",
        source="prometheus",
        namespace="production",
        resource="payment-service",
        severity="critical",
        labels={"team": "payments"},
    )
    assert sig.source == "prometheus"
    assert sig.severity == "critical"
    assert sig.labels["team"] == "payments"
    assert sig.raw_payload == b""


@pytest.mark.parametrize(
    "severity",
    ["critical", "warning", "info"],
)
def test_signal_severities(severity: str) -> None:
    sig = Signal(
        id="sig-x",
        source="loki",
        namespace="default",
        resource="auth-service",
        severity=severity,
    )
    assert sig.severity == severity


def test_incident_embeds_signals() -> None:
    sig = Signal(
        id="sig-002",
        source="k8s",
        namespace="staging",
        resource="order-service",
        severity="warning",
    )
    inc = Incident(
        id="inc-001",
        signals=[sig],
        affected_resources=["order-service"],
        severity="warning",
    )
    assert len(inc.signals) == 1
    assert inc.signals[0].id == "sig-002"
    assert inc.resolved_at is None


@pytest.mark.parametrize(
    "confidence,expected",
    [
        (0.0, True),
        (1.0, True),
        (0.91, True),
    ],
)
def test_diagnosis_confidence_bounds(confidence: float, expected: bool) -> None:
    d = Diagnosis(
        incident_id="inc-001",
        root_cause="memory limit too low",
        failure_mode="OOMKill",
        proposed_action="increase memory limit to 512Mi",
        confidence=confidence,
        blast_radius="pod",
    )
    in_bounds = 0.0 <= d.confidence <= 1.0
    assert in_bounds == expected


def test_llm_provider_raises_not_implemented() -> None:
    provider = LLMProvider()
    inc = Incident(
        id="inc-002",
        signals=[],
        affected_resources=[],
        severity="critical",
    )
    with pytest.raises(NotImplementedError):
        provider.diagnose(inc)
