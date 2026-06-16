"""Deterministic rule-based fallback provider.

Maps known failure modes to remediation actions with fixed, conservative
confidence scores. No LLM call is made. Used when:
  - GEMINI_API_KEY is absent (fallback-only mode)
  - Gemini returns an error / timeout / malformed output
  - Gemini proposes an action not in the allow-list
"""

from __future__ import annotations

from datetime import datetime

from diagnoser.contracts import Diagnosis, Incident, LLMProvider

# Maps the normalized failure reason (from Signal.reason or inferred from signals)
# to (proposed_action, conservative_confidence).
_RULE_MAP: dict[str, tuple[str, float]] = {
    "OOMKilled": ("bump-memory-limit", 0.85),
    "OOMKilling": ("bump-memory-limit", 0.85),
    "CrashLoopBackOff": ("rollback-deployment", 0.75),
    "ImagePullBackOff": ("rollback-deployment", 0.90),
    "ErrImagePull": ("rollback-deployment", 0.88),
    "BadDeploy": ("rollback-deployment", 0.80),
    "FailedScheduling": ("scale-deployment", 0.60),
    "NodeNotReady": ("scale-deployment", 0.65),
    # HPA oscillation: raise minReplicas to stabilize the replica floor.
    "HPAOscillation": ("patch-hpa", 0.78),
    # DNS saturation: scale the coredns deployment (resource = "coredns", ns = "kube-system").
    # kube-system is a protected namespace so verdict is REQUIRE_APPROVAL, not AUTO.
    "DNSSaturation": ("scale-deployment", 0.72),
}

_DEFAULT_BLAST_RADIUS: dict[str, str] = {
    "bump-memory-limit": "pod",
    "rollback-deployment": "deployment",
    "scale-deployment": "deployment",
    "patch-hpa": "deployment",
}


class RuleBasedProvider(LLMProvider):
    """Deterministic provider — no network calls, always available."""

    def diagnose(self, incident: Incident) -> Diagnosis:
        reason, action, confidence = self._classify(incident)
        blast_radius = _DEFAULT_BLAST_RADIUS.get(action, "deployment")

        return Diagnosis(
            incident_id=incident.id,
            root_cause=f"Rule-based classification: detected signal reason '{reason}'",
            failure_mode=reason,
            proposed_action=action,
            confidence=confidence,
            blast_radius=blast_radius,
            source="fallback",
            diagnosed_at=datetime.utcnow(),
        )

    def _classify(self, incident: Incident) -> tuple[str, str, float]:
        """Return (reason, action, confidence) by inspecting the incident's signals."""
        # Prioritize critical severity signals; fall back to any signal with a known reason.
        for signal in incident.signals:
            reason = signal.reason.strip()
            if reason in _RULE_MAP:
                action, confidence = _RULE_MAP[reason]
                return reason, action, confidence

        # No known reason in any signal — return low-confidence rollback as a safe default.
        return "Unknown", "rollback-deployment", 0.10
