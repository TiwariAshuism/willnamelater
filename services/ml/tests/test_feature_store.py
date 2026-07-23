"""Feature-store projection: label-gating and never-zero-fill.

Fixtures are schema-derived (they mirror the §5.1 export shape), not fabricated
business data — they exercise the projection mechanics only.
"""

import math

from training.feature_store import (
    FRAUD_EVIDENCE_HEURISTIC_ECHO,
    REACH_FEATURE_ORDER,
    to_fraud_dataset,
    to_reach_dataset,
)
from training.features import FEATURE_ORDER

# A label kind the heuristic could not have produced on its own — an admin who
# saw the platform actually take the account down observed something new.
OBSERVED = "platform_enforcement_action"


def _row(**over):
    feats = {
        # FEATURE_ORDER v2 keys: the renamed-composite `fake_follower_rate` and the
        # duplicate `bot_comment_rate` are gone; `risk_score` (0-100) is the real
        # composite estimate. A stale key here would leave risk_score unobserved.
        "risk_score": 10.0, "engagement_anomaly": 0.1,
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
    rows = [
        _row(fraud_label=True, fraud_label_evidence=OBSERVED),
        _row(fraud_label=None, fraud_label_evidence=OBSERVED),
        _row(fraud_label=False, fraud_label_evidence=OBSERVED),
    ]
    ds = to_fraud_dataset(rows)
    assert ds.targets == [1, 0]
    assert len(ds.features) == 2
    assert len(ds.features[0]) == len(FEATURE_ORDER)


def test_heuristic_echo_labels_never_become_y():
    """`none_reviewed_heuristic_only` means the admin observed NOTHING the
    heuristic had not already computed. Such a label IS the heuristic's output,
    and training on it produces a distillation that every gate then certifies as
    an independent model. It is UNLABELLED — not a positive, and not a negative.
    """
    rows = [
        _row(fraud_label=True, fraud_label_evidence=FRAUD_EVIDENCE_HEURISTIC_ECHO),
        _row(fraud_label=False, fraud_label_evidence=FRAUD_EVIDENCE_HEURISTIC_ECHO),
        _row(fraud_label=True, fraud_label_evidence=OBSERVED),
    ]
    ds = to_fraud_dataset(rows)
    assert ds.targets == [1]  # only the observed row survives
    assert ds.excluded["heuristic_echo"] == 2  # and the echoes are counted, not y=0


def test_missing_evidence_is_not_trainable():
    # Absence of a stated observation is not an observation. A label with no
    # evidence kind (or an unknown one) is UNLABELLED, never a negative.
    rows = [
        _row(fraud_label=True),                                  # field absent
        _row(fraud_label=False, fraud_label_evidence=None),      # explicitly null
        _row(fraud_label=True, fraud_label_evidence="vibes"),    # not in the enum
    ]
    ds = to_fraud_dataset(rows)
    assert ds.targets == []
    assert ds.features == []
    assert ds.excluded["evidence_missing_or_unknown"] == 3


def test_fraud_dataset_carries_the_influencer_group_key():
    ds = to_fraud_dataset(
        [_row(fraud_label=True, fraud_label_evidence=OBSERVED, influencer_id="inf-9")]
    )
    assert ds.influencer_ids == ["inf-9"]


def test_reach_dataset_keeps_only_rows_with_a_reach_label():
    rows = [_row(reach_label=15234), _row(reach_label=None)]
    ds = to_reach_dataset(rows)
    assert ds.targets == [15234.0]
    assert len(ds.features) == 1
    assert len(ds.features[0]) == len(REACH_FEATURE_ORDER)
    assert ds.influencer_ids == ["i1"]


def test_within_account_spread_is_a_disclosure_never_a_target():
    """The reach target is the SINGLE median, even when the export states the
    account's own p10/p90. Training the quantile heads on those would swap
    cross-account predictive uncertainty for within-account post spread — a
    silently narrower interval that G1's coverage check cannot catch.
    """
    ds = to_reach_dataset([
        _row(reach_label=15234, reach_label_p10=9000, reach_label_p90=21000),
    ])
    assert ds.targets == [15234.0]  # the median, and only the median
    assert ds.within_account_spread == [
        {"within_account_p10": 9000.0, "within_account_p90": 21000.0}
    ]


def test_missing_feature_becomes_nan_never_zero():
    ds = to_reach_dataset([_row(reach_label=100)])
    idx = REACH_FEATURE_ORDER.index("following_count")
    # following_count was null in the export → NaN (native missing), not 0.0.
    assert math.isnan(ds.features[0][idx])


def test_strata_default_unknown_when_absent():
    ds = to_fraud_dataset([
        _row(fraud_label=True, fraud_label_evidence=OBSERVED, features={"niche": None})
    ])
    assert ds.strata[0].tier == "unknown"
    assert ds.strata[0].niche == "unknown"
