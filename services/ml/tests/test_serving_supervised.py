"""Supervised inference mechanics with an injected fake loader.

No LightGBM and no trained model: a fake :class:`LoadedModel` lets us prove the
feature-ordering, native-missing handling, caching and clamping contracts
without any real artifact. The fake is a test double implementing the serving
interface (explicitly allowed), not fabricated business data.
"""

from __future__ import annotations

import math

import pytest

from app.registry import ArtifactRef
from app.serving import supervised


@pytest.fixture(autouse=True)
def _reset_loader():
    yield
    supervised.set_loader(supervised._default_loader)  # restore + clear cache


class _EchoModel:
    """Records the row it was asked to score and returns a fixed probability."""

    def __init__(self, prob: float) -> None:
        self.prob = prob
        self.seen_rows: list[list[float]] = []

    def predict_proba(self, row: list[float]) -> float:
        self.seen_rows.append(row)
        return self.prob


def _ref(version: str, feature_order=("a", "b", "c")) -> ArtifactRef:
    from pathlib import Path

    return ArtifactRef(
        version=version, model_path=Path("unused"), feature_order=feature_order
    )


def test_orders_features_and_fills_missing_with_nan() -> None:
    model = _EchoModel(0.42)
    supervised.set_loader(lambda ref: model)
    pred = supervised.predict(_ref("v1"), {"c": 3.0, "a": 1.0})  # "b" absent
    assert pred.version == "v1"
    assert pred.probability == pytest.approx(0.42)
    assert pred.score == pytest.approx(42.0)
    row = model.seen_rows[0]
    assert row[0] == 1.0  # a
    assert math.isnan(row[1])  # b was missing -> native NaN, never 0
    assert row[2] == 3.0  # c


def test_probability_is_clamped_to_unit_interval() -> None:
    supervised.set_loader(lambda ref: _EchoModel(1.5))
    assert supervised.predict(_ref("hi"), {"a": 1, "b": 1, "c": 1}).score == 100.0
    supervised.set_loader(lambda ref: _EchoModel(-0.3))
    assert supervised.predict(_ref("lo"), {"a": 1, "b": 1, "c": 1}).score == 0.0


def test_model_is_cached_by_version() -> None:
    calls = {"n": 0}

    def loader(ref):
        calls["n"] += 1
        return _EchoModel(0.5)

    supervised.set_loader(loader)
    ref = _ref("v-cached")
    supervised.predict(ref, {"a": 1, "b": 1, "c": 1})
    supervised.predict(ref, {"a": 2, "b": 2, "c": 2})
    assert calls["n"] == 1  # loaded once, reused


def test_missing_feature_order_is_unservable() -> None:
    supervised.set_loader(lambda ref: _EchoModel(0.5))
    with pytest.raises(ValueError):
        supervised.predict(_ref("bad", feature_order=None), {"a": 1})
