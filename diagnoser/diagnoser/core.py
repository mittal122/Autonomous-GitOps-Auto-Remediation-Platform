"""DiagnosisService — orchestrates LLM provider with RuleBasedProvider fallback.

Provider priority (first available wins):
  1. NimProvider    — if NIM_API_KEY is set (NVIDIA NIM, llama-3.3-70b-instruct)
  2. GeminiProvider — if GEMINI_API_KEY is set (legacy)
  3. RuleBasedProvider — always available, no API key required

This module is ADVISORY ONLY. It never calls the remediator, gitwriter,
policy engine, or any Kubernetes API. The output (Diagnosis) is consumed by
the Go agent's policy engine, which makes the actual allow/deny decision.
"""

from __future__ import annotations

import logging
import os

from diagnoser.contracts import Diagnosis, Incident, LLMProvider
from diagnoser.providers.rule_based import RuleBasedProvider

logger = logging.getLogger(__name__)


class DiagnosisService:
    """Wraps the active LLMProvider and handles fallback transparently."""

    def __init__(
        self,
        primary: LLMProvider | None = None,
        fallback: LLMProvider | None = None,
    ) -> None:
        self._fallback: LLMProvider = fallback or RuleBasedProvider()

        if primary is not None:
            self._primary: LLMProvider | None = primary
        else:
            self._primary = _build_primary_provider()

        if self._primary is None:
            logger.info(
                "DiagnosisService: no LLM API key found; running in fallback-only mode"
            )

    def diagnose(self, incident: Incident) -> Diagnosis:
        """Return a Diagnosis — from the LLM provider or the rule-based fallback."""
        if self._primary is not None:
            try:
                d = self._primary.diagnose(incident)
                logger.info(
                    "diagnosis: llm succeeded (source=%s action=%s confidence=%.2f)",
                    d.source, d.proposed_action, d.confidence,
                )
                return d
            except Exception as exc:
                logger.warning(
                    "diagnosis: llm provider failed; using rule-based fallback. error=%r", exc
                )

        d = self._fallback.diagnose(incident)
        logger.info(
            "diagnosis: fallback result (action=%s confidence=%.2f)",
            d.proposed_action, d.confidence,
        )
        return d


def _build_primary_provider() -> LLMProvider | None:
    """Try NimProvider first, then GeminiProvider; return None if neither configured."""

    # NVIDIA NIM (preferred)
    nim_key = os.getenv("NIM_API_KEY", "")
    if nim_key:
        try:
            from diagnoser.providers.nim import NimProvider  # noqa: PLC0415

            model = os.getenv("NIM_MODEL", "meta/llama-3.3-70b-instruct")
            timeout = int(os.getenv("LLM_TIMEOUT_SECONDS", "30"))
            provider = NimProvider(api_key=nim_key, model=model, timeout_seconds=timeout)
            logger.info("DiagnosisService: primary provider = NIM (model=%s)", model)
            return provider
        except Exception as exc:
            logger.error("DiagnosisService: failed to build NimProvider: %r", exc)

    # Google Gemini (legacy)
    gemini_key = os.getenv("GEMINI_API_KEY", "")
    if gemini_key:
        try:
            from diagnoser.providers.gemini import GeminiProvider  # noqa: PLC0415

            model = os.getenv("GEMINI_MODEL", "gemini-1.5-flash")
            timeout = int(os.getenv("LLM_TIMEOUT_SECONDS", "30"))
            provider = GeminiProvider(api_key=gemini_key, model=model, timeout_seconds=timeout)
            logger.info("DiagnosisService: primary provider = Gemini (model=%s)", model)
            return provider
        except Exception as exc:
            logger.error("DiagnosisService: failed to build GeminiProvider: %r", exc)

    return None
