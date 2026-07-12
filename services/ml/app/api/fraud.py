"""POST /v1/fraud/score — authenticity risk estimate for one account.

Serving policy (RETRAINING_ARCHITECTURE §5.6):

* **Cold start** (no champion artifact) — serve the unsupervised heuristic score
  and report ``model_version = "heuristic"``. Nothing is fabricated.
* **Champion present** — a real trained artifact serves the headline score;
  ``model_version`` is the champion's version, recorded per score. The heuristic
  signals remain as the explainable per-signal breakdown.
* **Challenger present** (shadow window) — the challenger is scored in parallel,
  **never shown**, and logged best-effort to the backend prediction log for the
  champion-vs-challenger comparison.

Either way the honesty envelope (``estimate=True``, ``confidence``,
``model_version``) is preserved, and every served score is fed to the drift
monitor.
"""

from __future__ import annotations

import contextlib
from datetime import UTC, datetime

from fastapi import APIRouter, BackgroundTasks

from app.features.fraud_vector import build_fraud_vector
from app.models.heuristics import score_fraud
from app.registry import get_registry
from app.schemas import FraudScoreRequest, FraudScoreResponse
from app.serving import shadow, supervised
from app.serving.drift import get_drift_monitor

router = APIRouter(prefix="/v1/fraud", tags=["fraud"])

# The model whose champion/challenger this endpoint serves. Names the row set in
# the prediction log (§5.4) and the registry family.
_MODEL_NAME = "fraud"


@router.post("/score", response_model=FraudScoreResponse)
def score(request: FraudScoreRequest, background_tasks: BackgroundTasks):
    registry = get_registry()

    # The unsupervised result is always computed: it is the cold-start score, and
    # it supplies the explainable per-signal breakdown + confidence in every era.
    heuristic = score_fraud(request)
    features = build_fraud_vector(heuristic)

    # Default to the honest cold-start score; a champion overrides it only if it
    # loads and scores cleanly. A champion that cannot be loaded (e.g. a bad or
    # incompatible artifact) must NEVER fail the request — it falls back to the
    # heuristic score rather than 500-ing a paying caller.
    served_score = heuristic.score
    model_version = registry.active_version()  # "heuristic" in cold start
    champion = registry.active_ref()
    if champion is not None:
        try:
            prediction = supervised.predict(champion, features)
            served_score = prediction.score
            model_version = champion.version
        except Exception:  # noqa: BLE001 — serving must degrade, not fail
            served_score = heuristic.score
            model_version = registry.active_version()

    # Prediction-drift signal over the served score (real input, never faked).
    get_drift_monitor().record(served_score)

    # Shadow: score the challenger in parallel, never show it, log the pair. It is
    # strictly best-effort — a challenger error is dropped and never touches the
    # served response.
    challenger = registry.shadow_ref()
    if challenger is not None:
        with contextlib.suppress(Exception):
            challenger_pred = supervised.predict(challenger, features)
            record = shadow.ShadowRecord(
                model_name=_MODEL_NAME,
                champion_version=model_version,
                champion_score=served_score,
                challenger_version=challenger_pred.version,
                challenger_score=challenger_pred.score,
                features_hash=shadow.features_hash(features),
                scored_at=datetime.now(UTC).isoformat(),
                audit_job_id=request.audit_ref,
            )
            shadow.enqueue(background_tasks, record)

    return FraudScoreResponse(
        score=served_score,
        confidence=heuristic.confidence,
        model_version=model_version,
        signals=heuristic.signals,
        flags=heuristic.flags,
        generated_at=datetime.now(UTC),
    )
