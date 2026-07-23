"""End-to-end serving mechanics for /v1/fraud/score.

Covers the three serving regimes with a temp artifact dir + an injected fake
model loader (no LightGBM, no trained model):

* cold start (no artifact) stays on the heuristic path;
* a champion artifact serves the supervised score and records its version;
* a challenger (shadow) is scored, logged, and NEVER alters the served result.

The manifests/model files are schema-shaped fixtures and the loader is a test
double implementing the serving interface — no fabricated business data.
"""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from app.main import create_app
from app.registry import ModelRegistry
from app.serving import shadow, supervised
from training.features import FEATURE_ORDER

_BASE = datetime(2026, 1, 1, tzinfo=UTC)


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


class _FixedModel:
    def __init__(self, prob: float) -> None:
        self._prob = prob

    def predict_proba(self, row: list[float]) -> float:
        return self._prob


def _loader(ref):
    # Champion and challenger score differently so we can prove the challenger's
    # value is logged but never served.
    return _FixedModel(0.9 if "champion" in ref.version else 0.2)


def _payload() -> dict:
    series = [
        {"timestamp": (_BASE + timedelta(days=i)).isoformat(), "count": 10_000 + i}
        for i in range(5)
    ]
    posts = [
        {
            "post_id": f"post_{i}",
            "timestamp": (_BASE + timedelta(days=i)).isoformat(),
            "likes": 200,
            "comments": 20,
        }
        for i in range(5)
    ]
    return {
        "account": {
            "handle": "creator",
            "platform": "instagram",
            "follower_count": 10_004,
            "following_count": 300,
        },
        "follower_series": series,
        "posts": posts,
        "audit_ref": "audit-123",
    }


@pytest.fixture(autouse=True)
def _reset_serving():
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


def test_cold_start_serves_heuristic(monkeypatch, tmp_path, emitted) -> None:
    client = _client(monkeypatch, tmp_path)  # empty dir
    body = client.post("/v1/fraud/score", json=_payload()).json()
    assert body["model_version"] == "heuristic"
    assert body["estimate"] is True
    assert 0.0 <= body["score"] <= 100.0
    assert emitted == []  # no challenger => no shadow log


def test_champion_serves_supervised_score(monkeypatch, tmp_path, emitted) -> None:
    _write_artifact(tmp_path, "lgbm-champion01")
    client = _client(monkeypatch, tmp_path)
    body = client.post("/v1/fraud/score", json=_payload()).json()
    assert body["model_version"] == "lgbm-champion01"
    assert body["score"] == 90.0  # 0.9 probability -> 0-100 scale
    assert body["estimate"] is True
    assert body["signals"]  # heuristic breakdown still attached
    assert emitted == []  # champion only, no shadow


def test_shadow_logs_challenger_without_changing_served_result(
    monkeypatch, tmp_path, emitted
) -> None:
    _write_artifact(tmp_path, "lgbm-champion01")
    _write_artifact(tmp_path / "shadow", "lgbm-challenger1")
    client = _client(monkeypatch, tmp_path)

    body = client.post("/v1/fraud/score", json=_payload()).json()

    # Served result is the CHAMPION's, unchanged by the presence of a challenger.
    assert body["model_version"] == "lgbm-champion01"
    assert body["score"] == 90.0
    # The challenger value appears nowhere in the response.
    assert "lgbm-challenger1" not in json.dumps(body)
    assert body["score"] != 20.0

    # ...but the pair WAS logged for the shadow comparison.
    assert len(emitted) == 1
    record = emitted[0]
    assert record.model_name == "fraud"
    assert record.champion_version == "lgbm-champion01"
    assert record.champion_score == 90.0
    assert record.challenger_version == "lgbm-challenger1"
    assert record.challenger_score == 20.0
    assert record.audit_job_id == "audit-123"
    assert record.features_hash.startswith("sha256:")


def test_first_model_shadow_over_heuristic_champion(
    monkeypatch, tmp_path, emitted
) -> None:
    # First-ever model: champion still heuristic, challenger in shadow. The
    # champion score logged is the heuristic score; challenger is logged too.
    _write_artifact(tmp_path / "shadow", "lgbm-challenger1")
    client = _client(monkeypatch, tmp_path)

    body = client.post("/v1/fraud/score", json=_payload()).json()
    assert body["model_version"] == "heuristic"
    assert len(emitted) == 1
    assert emitted[0].champion_version == "heuristic"
    assert emitted[0].challenger_version == "lgbm-challenger1"
    assert emitted[0].champion_score == body["score"]
