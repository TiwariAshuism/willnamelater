"""Serving mechanics for /v1/fraud/refine — the full-vector champion path.

Unlike /score (a single-account payload that can only observe ``confidence``),
/refine receives the full assembled FEATURE_ORDER vector the Go scoring layer
built across the fraud + clique models, and serves the champion on exactly the
columns it trained on. Covered here with a temp artifact dir + an injected fake
loader (no LightGBM):

* cold start (no artifact) declines: ``refined=False``, ``score=None``;
* a champion refines and returns its 0-100 score + version;
* a challenger (shadow) is scored on the same full vector, logged, and NEVER
  alters the served result;
* the full vector actually reaches the model (not a confidence-only row).

Manifests/model files are schema-shaped fixtures and the loader is a test double
— no fabricated business data.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from app.main import create_app
from app.registry import ModelRegistry
from app.serving import shadow, supervised
from training.features import FEATURE_ORDER


def _write_artifact(directory: Path, version: str) -> None:
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "model.txt").write_text("stub", encoding="utf-8")
    (directory / "manifest.json").write_text(
        json.dumps(
            {
                "version": version,
                "model_file": "model.txt",
                "feature_order": list(FEATURE_ORDER),
            }
        ),
        encoding="utf-8",
    )


class _RowCapturingModel:
    """Returns a fixed probability and records the exact row it was handed, so a
    test can prove the full assembled vector (not a confidence-only row) reached
    the model in the frozen column order."""

    def __init__(self, prob: float) -> None:
        self._prob = prob
        self.rows: list[list[float]] = []

    def predict_proba(self, row: list[float]) -> float:
        self.rows.append(list(row))
        return self._prob


_champion = _RowCapturingModel(0.8)
_challenger = _RowCapturingModel(0.1)


def _loader(ref):
    return _champion if "champion" in ref.version else _challenger


def _vector() -> dict:
    # A full assembled vector — every FEATURE_ORDER key populated with a distinct
    # value so column order is observable in the captured row.
    #
    # EXPECTATION CHANGED: the vector is FIVE columns now, keyed on `risk_score`
    # (the 0-100 composite estimate) instead of the removed `fake_follower_rate`
    # (which was that same composite renamed — nothing ever fetched a follower
    # list) and `bot_comment_rate` (a bit-for-bit duplicate of
    # clique_membership_fraction — no comment text was ever classified).
    return {
        "risk_score": 40.0,
        "engagement_anomaly": 0.2,
        "clique_count": 7,
        "clique_membership_fraction": 0.5,
        "confidence": 0.6,
        "audit_ref": "audit-xyz",
    }


#: The row `_vector()` must produce, in the frozen FEATURE_ORDER column order.
_EXPECTED_ROW = [40.0, 0.2, 7.0, 0.5, 0.6]


@pytest.fixture(autouse=True)
def _reset_serving():
    _champion.rows.clear()
    _challenger.rows.clear()
    supervised.set_loader(_loader)
    yield
    supervised.set_loader(supervised._default_loader)


@pytest.fixture
def emitted(monkeypatch) -> list[shadow.ShadowRecord]:
    captured: list[shadow.ShadowRecord] = []
    monkeypatch.setattr(shadow, "emit", lambda record: captured.append(record))
    return captured


def _client(monkeypatch, artifact_dir: Path) -> TestClient:
    registry = ModelRegistry(artifact_dir=artifact_dir)
    monkeypatch.setattr("app.api.fraud.get_registry", lambda: registry)
    return TestClient(create_app())


def test_cold_start_declines_refinement(monkeypatch, tmp_path, emitted) -> None:
    client = _client(monkeypatch, tmp_path)  # empty dir
    body = client.post("/v1/fraud/refine", json=_vector()).json()
    assert body["refined"] is False
    assert body["score"] is None
    assert body["model_version"] == "heuristic"
    assert emitted == []


def test_champion_refines_full_vector(monkeypatch, tmp_path, emitted) -> None:
    _write_artifact(tmp_path, "lgbm-champion01")
    client = _client(monkeypatch, tmp_path)

    body = client.post("/v1/fraud/refine", json=_vector()).json()
    assert body["refined"] is True
    assert body["score"] == 80.0  # 0.8 -> 0-100
    assert body["model_version"] == "lgbm-champion01"
    assert emitted == []  # champion only, no shadow

    # The FULL vector reached the model, in the frozen column order — not a
    # confidence-only row (the /score skew this endpoint exists to close).
    assert _champion.rows == [_EXPECTED_ROW]


def test_shadow_logs_challenger_without_changing_result(
    monkeypatch, tmp_path, emitted
) -> None:
    _write_artifact(tmp_path, "lgbm-champion01")
    _write_artifact(tmp_path / "shadow", "lgbm-challenger1")
    client = _client(monkeypatch, tmp_path)

    body = client.post("/v1/fraud/refine", json=_vector()).json()

    # Served result is the champion's, unchanged by the challenger's presence.
    assert body["score"] == 80.0
    assert "lgbm-challenger1" not in json.dumps(body)

    # ...but the pair WAS logged, and the challenger saw the same full vector.
    assert len(emitted) == 1
    record = emitted[0]
    assert record.champion_version == "lgbm-champion01"
    assert record.champion_score == 80.0
    assert record.challenger_version == "lgbm-challenger1"
    assert record.challenger_score == 10.0
    assert record.audit_job_id == "audit-xyz"
    assert _challenger.rows == [_EXPECTED_ROW]


def test_missing_signals_are_native_missing_not_zero(
    monkeypatch, tmp_path, emitted
) -> None:
    _write_artifact(tmp_path, "lgbm-champion01")
    client = _client(monkeypatch, tmp_path)

    # Only confidence observed; the rest omitted => null => native-missing (NaN),
    # never zero-filled.
    client.post("/v1/fraud/refine", json={"confidence": 0.6}).json()
    row = _champion.rows[0]
    assert row[-1] == 0.6  # confidence is last in FEATURE_ORDER
    assert all(r != r for r in row[:-1])  # NaN != NaN for every unobserved key
