"""The data-floor gate at the orchestrator level: below the floor it trains
nothing, writes no artifact, and exits 0 — the registry stays 'heuristic'.

Uses a stubbed feature-row fetch with a handful of schema-shaped synthetic rows
(never enough to clear the floor), so no LightGBM is needed: the gate returns
before any training.
"""

from types import SimpleNamespace

from app.registry.registry import HEURISTIC_VERSION, ModelRegistry
from training import retrain


def _args(tmp_path, model):
    return SimpleNamespace(
        model=model, feature_rows_url="http://x/feature-rows", canaries_url=None,
        models_url=None, token=None, out=str(tmp_path), since=None,
        promote=False, override_shadow=False, reason="test",
    )


def _fraud_rows(n):
    return [
        {
            "audit_job_id": f"a{i}", "features": {
                "risk_score": 10.0,
                "engagement_anomaly": 0.1, "clique_count": 0,
                "clique_membership_fraction": 0.0, "confidence": 0.5,
                "niche": "x", "tier": "micro",
            },
            "fraud_label": (i % 2 == 0), "captured_at": f"2026-07-{(i % 28) + 1:02d}",
        }
        for i in range(n)
    ]


def _reach_rows(n):
    rows = []
    for i in range(n):
        rows.append({
            "audit_job_id": f"a{i}",
            "features": {"follower_count": 1000, "niche": "x", "tier": "micro"},
            "reach_label": 900, "captured_at": f"2026-07-{(i % 28) + 1:02d}",
        })
    return rows


def test_fraud_below_floor_writes_no_artifact(tmp_path, monkeypatch):
    monkeypatch.setattr(retrain, "fetch_feature_rows", lambda *a, **k: _fraud_rows(10))
    rc = retrain.run_fraud(_args(tmp_path, "fraud"))
    assert rc == 0
    assert not (tmp_path / "manifest.json").exists()
    assert ModelRegistry(tmp_path).active_version() == HEURISTIC_VERSION


def test_reach_below_floor_writes_no_artifact(tmp_path, monkeypatch):
    monkeypatch.setattr(retrain, "fetch_feature_rows", lambda *a, **k: _reach_rows(10))
    rc = retrain.run_reach(_args(tmp_path, "reach"))
    assert rc == 0
    assert not (tmp_path / "manifest.json").exists()
    assert ModelRegistry(tmp_path).active_version() == HEURISTIC_VERSION
