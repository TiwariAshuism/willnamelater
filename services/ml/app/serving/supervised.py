"""Supervised inference from a resolved model artifact.

This path is reached *only* when the registry resolves a real trained artifact
(champion or challenger). In cold start there is no artifact and this module is
never called — the service keeps serving the unsupervised heuristic path.

LightGBM is imported lazily inside the default loader so the runtime service and
the pure tests never require the training extra unless an artifact is actually
present. The loader is injectable (:func:`set_loader`) so the champion/challenger
serving mechanics can be exercised with a test fake that implements the
:class:`LoadedModel` protocol, without pulling in LightGBM.
"""

from __future__ import annotations

import json
import math
import threading
from dataclasses import dataclass
from typing import Protocol

from app.registry import ArtifactRef

#: The artifact "kind" the fraud champion serializes: a bootstrap ensemble of
#: LightGBM boosters (training.challenger.FRAUD_KIND). The served probability is
#: the mean over the ensemble.
FRAUD_ENSEMBLE_KIND = "lgbm-fraud-ensemble"

#: Value used for a feature the caller could not observe. Never zero — a missing
#: feature is native-missing (NaN), which LightGBM consumes as such. Zero-filling
#: would teach/tell the model that "no signal" is a specific point in feature
#: space, which is false (project no-zero-fill rule).
MISSING = float("nan")


class LoadedModel(Protocol):
    """A model that maps one ordered feature row to a probability in [0, 1]."""

    def predict_proba(self, row: list[float]) -> float: ...


@dataclass(frozen=True)
class SupervisedPrediction:
    """A single supervised inference result."""

    version: str
    #: Authenticity-risk probability in [0, 1] (1 == most likely inauthentic).
    probability: float
    #: The same estimate on the 0-100 scale the response envelope uses.
    score: float


class _EnsembleModel:
    """A mean over a bootstrap ensemble of LightGBM boosters — the fraud champion
    the trainer produces. predict_proba averages the members, matching the
    trainer's own scoring (training.challenger.FraudChallenger.scores)."""

    def __init__(self, boosters: list) -> None:
        self._boosters = boosters

    def predict_proba(self, row: list[float]) -> float:
        preds = [float(b.predict([row])[0]) for b in self._boosters]
        return sum(preds) / len(preds)


def _default_loader(ref: ArtifactRef) -> LoadedModel:
    """Load the trained fraud artifact from disk. The trainer serializes a JSON
    wrapper (a bootstrap ensemble of LightGBM boosters), NOT a bare booster, so
    the loader parses it and reconstructs the mean-ensemble. LightGBM is imported
    lazily so the runtime and pure tests stay LightGBM-free unless an artifact is
    actually served."""
    import lightgbm as lgb

    payload = json.loads(ref.model_path.read_text(encoding="utf-8"))
    kind = payload.get("kind")
    members = payload.get("models")
    if not isinstance(members, list) or not members:
        # A reach (quantile) artifact, or anything else, cannot serve a fraud
        # probability. Raising here is caught by the serving guard, which falls
        # back to the heuristic score rather than failing the request.
        raise ValueError(
            f"artifact {ref.version} (kind={kind!r}) is not a servable fraud "
            "ensemble"
        )
    boosters = [lgb.Booster(model_str=m) for m in members]
    return _EnsembleModel(boosters)


_loader = _default_loader
_cache: dict[str, LoadedModel] = {}
_lock = threading.Lock()


def set_loader(loader) -> None:
    """Override the artifact loader (test seam). Clears the cache."""
    global _loader
    with _lock:
        _loader = loader
        _cache.clear()


def clear_cache() -> None:
    """Drop cached loaded models (test hygiene / post-promotion refresh)."""
    with _lock:
        _cache.clear()


def _load(ref: ArtifactRef) -> LoadedModel:
    # Cache by version: a version pins exact model bytes, so re-loading the same
    # version is wasteful. A promotion writes a new version, so the key changes.
    model = _cache.get(ref.version)
    if model is None:
        with _lock:
            model = _cache.get(ref.version)
            if model is None:
                model = _loader(ref)
                _cache[ref.version] = model
    return model


def predict(
    ref: ArtifactRef, features: dict[str, float | None]
) -> SupervisedPrediction:
    """Score ``features`` with the artifact at ``ref``.

    Features are laid out in the artifact's frozen ``feature_order``; a key the
    caller did not supply becomes native-missing (never zero). A model without a
    ``feature_order`` in its manifest is a malformed artifact and cannot be
    served — raising here is correct, the registry only hands back refs whose
    files exist, and a missing feature order is a build error, not a runtime one.
    """
    if ref.feature_order is None:
        raise ValueError(
            f"artifact {ref.version} has no feature_order; cannot score supervised"
        )
    row = [_coerce(features.get(name)) for name in ref.feature_order]
    model = _load(ref)
    prob = model.predict_proba(row)
    # Clamp defensively: a caller-supplied fake or a miscalibrated booster must
    # not leak a value outside the response contract's [0, 1] / [0, 100] bounds.
    prob = min(max(prob, 0.0), 1.0)
    return SupervisedPrediction(
        version=ref.version, probability=prob, score=round(prob * 100.0, 4)
    )


def _coerce(value: float | None) -> float:
    if value is None:
        return MISSING
    value = float(value)
    return value if math.isfinite(value) else MISSING
