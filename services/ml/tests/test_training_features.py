from training.features import FEATURE_ORDER, FEATURE_ORDER_VERSION, to_dataset


def _row(label, *, has_features=True, present=True, **feats):
    base = {
        "risk_score": 10.0,
        "engagement_anomaly": 0.1,
        "clique_count": 0,
        "clique_membership_fraction": 0.0,
        "confidence": 0.5,
    }
    base.update(feats)
    base["present"] = present
    return {
        "label": label,
        "has_features": has_features,
        "features": base,
        "resolved_at": "2026-01-01T00:00:00Z",
    }


def test_feature_order_is_five_frozen_columns():
    # EXPECTATION CHANGED (was test_feature_order_is_six_frozen_columns). The
    # vector is five columns at version 2. The two dropped columns were never
    # measurements: `fake_follower_rate` was the composite risk score under
    # another name (nothing in the pipeline has ever fetched a follower list), and
    # `bot_comment_rate` was a bit-for-bit copy of `clique_membership_fraction`
    # (no comment text was ever classified). The old six-column vector therefore
    # carried only five distinct values, and handed the model a perfectly
    # collinear pair dressed up as independent evidence.
    assert FEATURE_ORDER == (
        "risk_score",
        "engagement_anomaly",
        "clique_count",
        "clique_membership_fraction",
        "confidence",
    )
    assert FEATURE_ORDER_VERSION == 2


def test_feature_order_carries_no_duplicate_or_renamed_columns():
    # The columns must be distinct names AND distinct measurements: neither
    # fabricated column may creep back in. `fake_follower_rate` would duplicate
    # `risk_score`; `bot_comment_rate` would duplicate
    # `clique_membership_fraction`.
    assert len(set(FEATURE_ORDER)) == len(FEATURE_ORDER)
    assert "fake_follower_rate" not in FEATURE_ORDER
    assert "bot_comment_rate" not in FEATURE_ORDER

    # A row of genuinely distinct inputs must project to genuinely distinct
    # values — no column is a mirror of another.
    labels = [
        _row(
            True,
            risk_score=80.0,
            engagement_anomaly=0.6,
            clique_count=9,
            clique_membership_fraction=0.5,
            confidence=0.55,
        )
    ]
    features, _, _ = to_dataset(labels)
    assert len(set(features[0])) == len(FEATURE_ORDER)


def test_drops_rows_without_features_and_never_zero_fills():
    labels = [_row(True), _row(True, has_features=False), _row(False, present=False)]
    features, targets, _ = to_dataset(labels)
    # Only the one genuinely feature-bearing row survives; the has_features=false
    # and present=false rows are dropped, not zero-filled.
    assert len(features) == 1
    assert len(features[0]) == 5  # EXPECTATION CHANGED: five columns, not six
    assert targets == [1]


def test_label_mapping():
    _, targets, _ = to_dataset([_row(True), _row(False)])
    assert targets == [1, 0]


def test_columns_follow_feature_order():
    labels = [_row(True, risk_score=90.0, clique_count=7, confidence=0.6)]
    features, _, _ = to_dataset(labels)
    assert features[0][0] == 90.0  # risk_score first (the 0-100 composite estimate)
    assert features[0][2] == 7.0  # clique_count third
    assert features[0][4] == 0.6  # confidence last
