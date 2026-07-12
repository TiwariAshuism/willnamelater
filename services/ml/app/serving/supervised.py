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

import math
import threading
from dataclasses import dataclass
from typing import Protocol

from app.registry import ArtifactRef

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


class _LgbmModel:
    """Adapts a LightGBM booster to :class:`LoadedModel`."""

    def __init__(self, booster: object) -> None:
        self._booster = booster

    def predict_proba(self, row: list[float]) -> float:
        # booster.predict returns a length-1 array for a single row.
        return float(self._booster.predict([row])[0])


def _default_loader(ref: ArtifactRef) -> LoadedModel:
    """Load a LightGBM booster from disk. Imported lazily to keep the runtime
    lean and the pure tests LightGBM-free."""
    import lightgbm as lgb

    booster = lgb.Booster(model_file=str(ref.model_path))
    return _LgbmModel(booster)


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
