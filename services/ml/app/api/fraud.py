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

from app.features.fraud_vector import FEATURE_ORDER, build_fraud_vector
from app.models.heuristics import score_fraud
from app.registry import get_registry
from app.schemas import (
    FraudRefineRequest,
    FraudRefineResponse,
    FraudScoreRequest,
    FraudScoreResponse,
)
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

    # Not one signal was observable. There is nothing to score and nothing to
    # refine — return the honest absence rather than a 0 that reads as "clean".
    if not heuristic.observed:
        return FraudScoreResponse(
            score=None,
            observed=False,
            confidence=0.0,
            model_version=registry.active_version(),
            signals=[],
            flags=[],
            generated_at=datetime.now(UTC),
        )

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


@router.post("/refine", response_model=FraudRefineResponse)
def refine(request: FraudRefineRequest, background_tasks: BackgroundTasks):
    """Serve the fraud champion on the FULL assembled feature vector.

    The Go ``scoring`` layer assembles all six ``FEATURE_ORDER`` signals across
    the per-account fraud model and the co-commenter clique model, then calls
    this to score them together — the exact vector the champion trained on, so
    there is no train/serve skew (unlike ``/score``, whose single-account payload
    can only observe ``confidence``). This is the honest full-vector serving path
    and where shadow scoring belongs.

    In cold start there is no champion: ``refined=False`` and the Go caller keeps
    its heuristic authenticity aggregate. A champion that cannot load/score falls
    back the same way — refinement never fails the caller, only declines.
    """
    registry = get_registry()
    champion = registry.active_ref()
    if champion is None:
        # Cold start: nothing to refine. The heuristic aggregate stands.
        return FraudRefineResponse(
            refined=False,
            model_version=registry.active_version(),
            generated_at=datetime.now(UTC),
        )

    # The full vector, laid out in the frozen order. A signal the audit could not
    # observe arrives null and stays native-missing (never zero-filled).
    features = {name: getattr(request, name) for name in FEATURE_ORDER}
    try:
        prediction = supervised.predict(champion, features)
    except Exception:  # noqa: BLE001 — refinement must decline, not fail
        return FraudRefineResponse(
            refined=False,
            model_version=registry.active_version(),
            generated_at=datetime.now(UTC),
        )

    # Prediction-drift signal over the served (refined) score — real input.
    get_drift_monitor().record(prediction.score)

    # Shadow the challenger on the same full vector, best-effort, never shown.
    challenger = registry.shadow_ref()
    if challenger is not None:
        with contextlib.suppress(Exception):
            challenger_pred = supervised.predict(challenger, features)
            record = shadow.ShadowRecord(
                model_name=_MODEL_NAME,
                champion_version=champion.version,
                champion_score=prediction.score,
                challenger_version=challenger_pred.version,
                challenger_score=challenger_pred.score,
                features_hash=shadow.features_hash(features),
                scored_at=datetime.now(UTC).isoformat(),
                audit_job_id=request.audit_ref,
            )
            shadow.enqueue(background_tasks, record)

    return FraudRefineResponse(
        refined=True,
        score=prediction.score,
        model_version=champion.version,
        generated_at=datetime.now(UTC),
    )
