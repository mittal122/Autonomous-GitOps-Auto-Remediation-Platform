"""Shared data contracts mirrored from the Go agent layer.

These Python dataclasses are the canonical types for data that crosses the
agent→diagnoser boundary (typically via HTTP/JSON).

TODO (future prompt): generate from a protobuf/OpenAPI spec so Go and Python
stay in sync automatically.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime


@dataclass
class Signal:
    """Normalized telemetry data point from any source."""

    id: str
    source: str  # "k8s-event" | "prometheus-alert" | "loki-log"
    namespace: str
    resource: str
    severity: str  # "critical" | "warning" | "info"
    kind: str = ""  # Kubernetes resource kind e.g. "Pod", "Node"
    reason: str = ""  # normalized failure indicator e.g. "OOMKilled"
    message: str = ""  # human-readable description from the source
    labels: dict[str, str] = field(default_factory=dict)
    raw_payload: bytes = b""
    received_at: datetime = field(default_factory=datetime.utcnow)


@dataclass
class Incident:
    """A correlated set of Signals describing a single operational problem."""

    id: str
    signals: list[Signal]
    affected_resources: list[str]
    severity: str
    opened_at: datetime = field(default_factory=datetime.utcnow)
    updated_at: datetime = field(default_factory=datetime.utcnow)
    resolved_at: datetime | None = None


@dataclass
class Diagnosis:
    """Structured output from the diagnosis layer for an Incident.

    The diagnoser produces this; the policy engine (Go) consumes it.
    This type is advisory-only — it NEVER triggers action by itself.
    """

    incident_id: str
    root_cause: str
    failure_mode: str
    proposed_action: str  # constrained to ALLOWED_ACTIONS whitelist
    confidence: float  # [0.0, 1.0]; clamped on output
    blast_radius: str  # "pod" | "deployment" | "namespace" | "cluster"
    source: str = "fallback"  # "gemini" | "fallback"
    diagnosed_at: datetime = field(default_factory=datetime.utcnow)


class LLMProvider:
    """Abstract base for LLM backends (Gemini, rule-based, etc.).

    Concrete implementations live in diagnoser/providers/.
    """

    def diagnose(self, incident: Incident) -> Diagnosis:  # pragma: no cover
        raise NotImplementedError
