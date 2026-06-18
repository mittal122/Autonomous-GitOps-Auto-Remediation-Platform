"""FastAPI HTTP bridge — exposes POST /diagnose and GET /healthz.

This server is the sole entry point for the Go agent to request a diagnosis.
It accepts an Incident JSON body, validates it with Pydantic, runs the
DiagnosisService, and returns a Diagnosis JSON. It never executes remediation.

TODO (future prompt — orchestrator): the Go agent will call this endpoint from
the orchestration loop after the correlator emits a new incident.
"""

from __future__ import annotations

import logging
from datetime import datetime
from typing import Optional

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field, field_validator

from diagnoser.contracts import (
    Diagnosis as DiagnosisContract,
    Incident as IncidentContract,
    Signal as SignalContract,
)
from diagnoser.core import DiagnosisService

_TEST_INCIDENT = IncidentContract(
    id="settings-test",
    signals=[
        SignalContract(
            id="settings-test-signal",
            source="k8s-event",
            namespace="default",
            resource="settings-test-deployment",
            severity="warning",
            kind="Pod",
            reason="OOMKilled",
            message="Connectivity test triggered from the Settings page — not a real incident.",
        )
    ],
    affected_resources=["settings-test-deployment"],
    severity="warning",
)

logger = logging.getLogger(__name__)

app = FastAPI(
    title="AutoSRE Diagnoser",
    description="Advisory-only diagnosis service. Returns Diagnosis; never executes remediation.",
    version="0.4.0",
)

# Module-level service instance; tests can replace it via app.state.
_service: DiagnosisService | None = None


def get_service() -> DiagnosisService:
    global _service
    if _service is None:
        _service = DiagnosisService()
    return _service


# ---------------------------------------------------------------------------
# Pydantic request / response models (match the Go JSON contract)
# ---------------------------------------------------------------------------


class SignalIn(BaseModel):
    id: str
    source: str
    namespace: str
    resource: str
    severity: str
    kind: str = ""
    reason: str = ""
    message: str = ""
    labels: dict[str, str] = Field(default_factory=dict)
    received_at: Optional[datetime] = None


class IncidentIn(BaseModel):
    id: str
    signals: list[SignalIn]
    affected_resources: list[str]
    severity: str
    opened_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None


class DiagnosisOut(BaseModel):
    incident_id: str
    root_cause: str
    failure_mode: str
    proposed_action: str
    confidence: float
    blast_radius: str
    source: str
    diagnosed_at: datetime

    @field_validator("confidence")
    @classmethod
    def clamp_confidence(cls, v: float) -> float:
        return max(0.0, min(1.0, v))


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/diagnose", response_model=DiagnosisOut)
def diagnose(body: IncidentIn, request: Request) -> DiagnosisOut:
    """Accept an Incident JSON body and return a validated Diagnosis."""
    # Convert Pydantic model → internal contract type.
    signals = [
        SignalContract(
            id=s.id,
            source=s.source,
            namespace=s.namespace,
            resource=s.resource,
            severity=s.severity,
            kind=s.kind,
            reason=s.reason,
            message=s.message,
            labels=s.labels,
            received_at=s.received_at or datetime.utcnow(),
        )
        for s in body.signals
    ]
    incident = IncidentContract(
        id=body.id,
        signals=signals,
        affected_resources=body.affected_resources,
        severity=body.severity,
        opened_at=body.opened_at or datetime.utcnow(),
        updated_at=body.updated_at or datetime.utcnow(),
    )

    try:
        svc = get_service()
        d: DiagnosisContract = svc.diagnose(incident)
    except Exception as exc:
        logger.error("diagnose: unexpected error: %r", exc)
        raise HTTPException(status_code=500, detail="diagnosis service error") from exc

    return DiagnosisOut(
        incident_id=d.incident_id,
        root_cause=d.root_cause,
        failure_mode=d.failure_mode,
        proposed_action=d.proposed_action,
        confidence=d.confidence,
        blast_radius=d.blast_radius,
        source=d.source,
        diagnosed_at=d.diagnosed_at,
    )


# ---------------------------------------------------------------------------
# Runtime LLM provider configuration — set by the Go agent's Settings page so
# the API key never needs to be set via NIM_API_KEY/GEMINI_API_KEY env vars or
# a process restart. The Go agent's encrypted settings store is the source of
# truth; this process holds only an in-memory copy of the active provider.
# ---------------------------------------------------------------------------


class LLMConfigIn(BaseModel):
    provider: str = ""  # "nim" | "gemini" | "" (disable — fallback-only mode)
    api_key: str = ""
    model: str = ""
    timeout_seconds: int = 30


class LLMConfigTestOut(BaseModel):
    ok: bool
    message: str


@app.post("/config/llm")
def set_llm_config(body: LLMConfigIn) -> dict[str, bool]:
    """Rebuild the active LLM provider in place. Never logs body.api_key."""
    try:
        get_service().reconfigure(
            provider=body.provider,
            api_key=body.api_key,
            model=body.model,
            timeout_seconds=body.timeout_seconds,
        )
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:
        logger.error("set_llm_config: failed to configure provider %r: %s", body.provider, type(exc).__name__)
        raise HTTPException(status_code=400, detail=f"failed to configure provider: {exc}") from exc
    return {"ok": True}


@app.post("/config/llm/test", response_model=LLMConfigTestOut)
def test_llm_config(body: LLMConfigIn) -> LLMConfigTestOut:
    """One-shot connectivity test against the submitted (not-yet-saved) credentials."""
    if body.provider not in ("nim", "gemini"):
        return LLMConfigTestOut(ok=False, message="provider must be 'nim' or 'gemini'")
    if not body.api_key:
        return LLMConfigTestOut(ok=False, message="api_key is required")

    try:
        if body.provider == "nim":
            from diagnoser.providers.nim import NimProvider  # noqa: PLC0415

            provider = NimProvider(api_key=body.api_key, model=body.model, timeout_seconds=body.timeout_seconds or 30)
        else:
            from diagnoser.providers.gemini import GeminiProvider  # noqa: PLC0415

            provider = GeminiProvider(api_key=body.api_key, model=body.model, timeout_seconds=body.timeout_seconds or 30)

        diagnosis = provider.diagnose(_TEST_INCIDENT)
    except Exception as exc:
        return LLMConfigTestOut(ok=False, message=f"Test call failed: {exc}")

    return LLMConfigTestOut(
        ok=True,
        message=f"Connected — model responded (action={diagnosis.proposed_action}, confidence={diagnosis.confidence:.2f})",
    )


@app.exception_handler(Exception)
async def global_exception_handler(request: Request, exc: Exception) -> JSONResponse:
    logger.error("unhandled exception: %r", exc)
    return JSONResponse(status_code=500, content={"detail": "internal server error"})
