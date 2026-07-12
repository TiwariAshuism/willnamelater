"""Feature-store projection: label-gating and never-zero-fill.

Fixtures are schema-derived (they mirror the §5.1 export shape), not fabricated
business data — they exercise the projection mechanics only.
"""

import math

from training.feature_store import (
    REACH_FEATURE_ORDER,
    to_fraud_dataset,
    to_reach_dataset,
)
from training.features import FEATURE_ORDER


def _row(**over):
    feats = {
        "fake_follower_rate": 0.1, "bot_comment_rate": 0.1, "engagement_anomaly": 0.1,
        "clique_count": 0, "clique_membership_fraction": 0.0, "confidence": 0.5,
        "follower_count": 1000, "following_count": None,
        "follower_following_ratio": None, "engagement_rate": 0.03,
        "engagement_rate_variance": 0.0001, "comment_like_ratio": 0.02,
        "posting_cadence_per_week": 3.0, "account_age_days_proxy": 400.0,
        "post_count": 20, "niche": "fitness", "tier": "micro",
        "verified": None, "platform": "instagram",
    }
    row = {
        "audit_job_id": "a1", "influencer_id": "i1", "platform": "instagram",
        "features": feats, "fraud_label": None, "reach_label": None,
        "quality_ok": True, "captured_at": "2026-07-01T00:00:00Z",
    }
    row.update(over)
    return row


def test_fraud_dataset_keeps_only_rows_with_a_fraud_label():
    rows = [_row(fraud_label=True), _row(fraud_label=None), _row(fraud_label=False)]
    ds = to_fraud_dataset(rows)
    assert ds.targets == [1, 0]
    assert len(ds.features) == 2
    assert len(ds.features[0]) == len(FEATURE_ORDER)


def test_reach_dataset_keeps_only_rows_with_a_reach_label():
    rows = [_row(reach_label=15234), _row(reach_label=None)]
    ds = to_reach_dataset(rows)
    assert ds.targets == [15234.0]
    assert len(ds.features) == 1
    assert len(ds.features[0]) == len(REACH_FEATURE_ORDER)


def test_missing_feature_becomes_nan_never_zero():
    ds = to_reach_dataset([_row(reach_label=100)])
    idx = REACH_FEATURE_ORDER.index("following_count")
    # following_count was null in the export → NaN (native missing), not 0.0.
    assert math.isnan(ds.features[0][idx])


def test_strata_default_unknown_when_absent():
    ds = to_fraud_dataset([_row(fraud_label=True, features={"niche": None})])
    assert ds.strata[0].tier == "unknown"
    assert ds.strata[0].niche == "unknown"
