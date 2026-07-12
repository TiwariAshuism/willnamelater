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


def test_reach_200_rows_from_20_influencers_trains_nothing(tmp_path, monkeypatch):
    """End-to-end: the re-audited-panel dataset must not produce a challenger.

    20 creators re-audited monthly for 10 months is 200 rows and 20 accounts.
    Under a ROW floor of 200 the orchestrator would train, validate against a
    held-out slice containing those same 20 creators, and register a model whose
    G1 score is a measure of memorization. It must instead train nothing and
    leave the registry on 'heuristic' — and it needs no LightGBM to say so,
    because the floor returns before any training.
    """
    rows = _reach_rows(200)
    for i, row in enumerate(rows):
        row["influencer_id"] = f"inf{i % 20}"
    monkeypatch.setattr(retrain, "fetch_feature_rows", lambda *a, **k: rows)

    rc = retrain.run_reach(_args(tmp_path, "reach"))
    assert rc == 0
    assert not (tmp_path / "manifest.json").exists()
    assert not (tmp_path / "shadow").exists()
    assert ModelRegistry(tmp_path).active_version() == HEURISTIC_VERSION


def test_fraud_heuristic_echo_labels_train_nothing(tmp_path, monkeypatch):
    """A whole export of heuristic-echo labels is an export of NO ground truth.

    Enough rows and enough distinct creators to clear every count — but every
    label records that the reviewer observed nothing the heuristic had not
    already computed. Training on them would distil the heuristic and register it
    as an independent model. The fold is empty and nothing is trained.
    """
    rows = _fraud_rows(300)
    for i, row in enumerate(rows):
        row["influencer_id"] = f"inf{i}"
        row["fraud_label_evidence"] = "none_reviewed_heuristic_only"
    monkeypatch.setattr(retrain, "fetch_feature_rows", lambda *a, **k: rows)

    rc = retrain.run_fraud(_args(tmp_path, "fraud"))
    assert rc == 0
    assert not (tmp_path / "manifest.json").exists()
    assert ModelRegistry(tmp_path).active_version() == HEURISTIC_VERSION
