"""FastAPI application factory for the InfluAudit ML service."""

from __future__ import annotations

from fastapi import FastAPI
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from app.api import comments, fraud, ml, pods
from app.registry import get_registry
from app.schemas import ErrorResponse, HealthResponse
from app.serving.drift import get_drift_monitor


def create_app() -> FastAPI:
    app = FastAPI(
        title="InfluAudit ML",
        version="0.1.0",
        summary="Cold-start fraud, pod and comment-quality estimation.",
    )

    app.include_router(fraud.router)
    app.include_router(pods.router)
    app.include_router(comments.router)
    app.include_router(ml.router)

    @app.exception_handler(RequestValidationError)
    async def _on_validation_error(_, exc: RequestValidationError) -> JSONResponse:
        # Match the Go errs envelope (KindInvalid -> 400 code "ml.invalid").
        body = ErrorResponse(code="ml.invalid", message=str(exc.errors()))
        return JSONResponse(status_code=400, content=body.model_dump())

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        registry = get_registry()
        return HealthResponse(
            status="ok",
            model_version=registry.active_version(),
            challenger_version=registry.shadow_version(),
            drift_status=get_drift_monitor().status(),
        )

    return app


app = create_app()
