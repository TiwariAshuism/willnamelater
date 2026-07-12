"""Reproducibility hash + register payload shape (pure, no network, no LightGBM)."""

import base64

from training import promotion
from training.gate import MIN_REACH_INFLUENCERS, meets_reach_floor


def test_snapshot_hash_is_deterministic_and_order_sensitive():
    ids = ["a", "b"]
    feats = [[1.0, 2.0], [3.0, 4.0]]
    targets = [1, 0]
    h1 = promotion.snapshot_hash(ids, feats, targets)
    h2 = promotion.snapshot_hash(ids, feats, targets)
    assert h1 == h2 and h1.startswith("sha256:")
    # A reordered fold is a different snapshot.
    h3 = promotion.snapshot_hash(list(reversed(ids)), list(reversed(feats)),
                                 list(reversed(targets)))
    assert h3 != h1


def test_snapshot_hash_handles_nan_missing_features():
    h = promotion.snapshot_hash(["a"], [[float("nan"), 1.0]], [1])
    assert h.startswith("sha256:")  # NaN is rendered stably, not an error


def test_register_payload_shape_matches_contract():
    payload = promotion.build_register_payload(
        model_name="fraud",
        manifest={"version": "lgbm-deadbeef0000", "model_file": "model.txt"},
        model_bytes=b"ensemble-bytes",
        metrics={"positive": {"precision": 1.0}},
        validation_report={"all_required_pass": True},
        feature_snapshot={"row_count": 3, "content_hash": "sha256:x"},
        data_floor_counts={"positive": 61, "negative": 74, "floor": 50},
    )
    assert payload["version"] == "lgbm-deadbeef0000"
    assert payload["model_file_name"] == "model.txt"
    assert base64.b64decode(payload["model_file_b64"]) == b"ensemble-bytes"
    assert payload["validation_report"]["all_required_pass"] is True


def test_reach_floor_gate():
    # The floor is taken over DISTINCT INFLUENCERS, not rows (see
    # test_training_gate.py for the 200-rows-from-20-creators regression guard).
    enough = [f"inf{i}" for i in range(MIN_REACH_INFLUENCERS)]
    assert meets_reach_floor(len(enough), enough)[0] is True

    one_short = enough[:-1]
    ok, counts = meets_reach_floor(len(one_short), one_short)
    assert ok is False
    assert counts["distinct_influencers"] == MIN_REACH_INFLUENCERS - 1
    assert counts["floor"] == MIN_REACH_INFLUENCERS
