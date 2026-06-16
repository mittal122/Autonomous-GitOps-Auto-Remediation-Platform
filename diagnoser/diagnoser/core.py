"""DiagnosisService — orchestrates GeminiProvider with RuleBasedProvider fallback.

This module is ADVISORY ONLY. It never calls the remediator, gitwriter,
policy engine, or any Kubernetes API. The output (Diagnosis) is consumed by
the Go agent's policy engine, which makes the actual allow/deny decision.

TODO (future prompt — orchestrator): the Go agent will call POST /diagnose
after the correlator opens an incident; the resulting Diagnosis feeds the
policy engine.
TODO (future prompt — verifier): the verifier re-queries telemetry to confirm
recovery after a remediation is applied.
"""

from __future__ import annotations

import logging
import os

from diagnoser.contracts import Diagnosis, Incident, LLMProvider
from diagnoser.providers.rule_based import RuleBasedProvider

logger = logging.getLogger(__name__)


class DiagnosisService:
    """Wraps the active LLMProvider and handles fallback transparently.

    Instantiation chooses the provider:
    - If GEMINI_API_KEY is set and use_gemini is not False → try GeminiProvider.
    - If the key is missing or use_gemini=False → RuleBasedProvider only.
    - If GeminiProvider raises on construction (missing SDK, bad key) → fallback.
    """

    def __init__(
        self,
        primary: LLMProvider | None = None,
        fallback: LLMProvider | None = None,
    ) -> None:
        self._fallback: LLMProvider = fallback or RuleBasedProvider()

        if primary is not None:
            self._primary: LLMProvider | None = primary
        else:
            self._primary = _build_gemini_provider()

        if self._primary is None:
            logger.info(
                "DiagnosisService: GEMINI_API_KEY not set; running in fallback-only mode"
            )

    def diagnose(self, incident: Incident) -> Diagnosis:
        """Return a Diagnosis, always — either from Gemini or the rule-based fallback."""
        if self._primary is not None:
            try:
                d = self._primary.diagnose(incident)
                logger.info(
                    "diagnosis: gemini succeeded",
                    extra={
                        "incident_id": incident.id,
                        "action": d.proposed_action,
                        "confidence": d.confidence,
                    },
                )
                return d
            except Exception as exc:
                logger.warning(
                    "diagnosis: gemini failed; using fallback. error=%r", exc
                )

        d = self._fallback.diagnose(incident)
        logger.info(
            "diagnosis: fallback result",
            extra={
                "incident_id": incident.id,
                "action": d.proposed_action,
                "confidence": d.confidence,
            },
        )
        return d


def _build_gemini_provider() -> LLMProvider | None:
    """Attempt to build a GeminiProvider; return None if key is absent."""
    api_key = os.getenv("GEMINI_API_KEY", "")
    if not api_key:
        return None

    try:
        from diagnoser.providers.gemini import GeminiProvider  # noqa: PLC0415

        model = os.getenv("GEMINI_MODEL", "gemini-1.5-flash")
        timeout = int(os.getenv("LLM_TIMEOUT_SECONDS", "30"))
        return GeminiProvider(api_key=api_key, model=model, timeout_seconds=timeout)
    except Exception as exc:
        logger.error("DiagnosisService: failed to build GeminiProvider: %r", exc)
        return None
