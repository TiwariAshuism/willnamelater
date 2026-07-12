from training.features import FEATURE_ORDER, to_dataset


def _row(label, *, has_features=True, present=True, **feats):
    base = {
        "fake_follower_rate": 0.1,
        "bot_comment_rate": 0.1,
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


def test_feature_order_is_six_frozen_columns():
    assert FEATURE_ORDER == (
        "fake_follower_rate",
        "bot_comment_rate",
        "engagement_anomaly",
        "clique_count",
        "clique_membership_fraction",
        "confidence",
    )


def test_drops_rows_without_features_and_never_zero_fills():
    labels = [_row(True), _row(True, has_features=False), _row(False, present=False)]
    features, targets, _ = to_dataset(labels)
    # Only the one genuinely feature-bearing row survives; the has_features=false
    # and present=false rows are dropped, not zero-filled.
    assert len(features) == 1
    assert len(features[0]) == 6
    assert targets == [1]


def test_label_mapping():
    _, targets, _ = to_dataset([_row(True), _row(False)])
    assert targets == [1, 0]


def test_columns_follow_feature_order():
    labels = [_row(True, fake_follower_rate=0.9, clique_count=7, confidence=0.6)]
    features, _, _ = to_dataset(labels)
    assert features[0][0] == 0.9  # fake_follower_rate first
    assert features[0][3] == 7.0  # clique_count fourth
    assert features[0][5] == 0.6  # confidence last
