"""Champion-challenger training mechanics + determinism.

These fit real LightGBM models, so they need the training extra and skip cleanly
without it. The synthetic, separable data is a fixture exercising the ensemble /
quantile mechanics — not a claim any account is fraudulent or has a given reach.
"""

import pytest

pytest.importorskip("lightgbm")

from training import challenger as ch  # noqa: E402


def _fraud_data(n=40):
    features, targets = [], []
    for _ in range(n):
        features.append([0.8, 0.7, 0.6, 9.0, 0.5, 0.6])
        targets.append(1)
        features.append([0.05, 0.05, 0.05, 0.0, 0.0, 0.55])
        targets.append(0)
    return features, targets


def _reach_data(n=60):
    features, targets = [], []
    for i in range(n):
        f = float(1000 + i * 10)
        features.append([0.1] * 6 + [f, f / 2, 2.0, 0.03, 0.0001, 0.02, 3.0, 400.0,
                                     20.0, 0.0])
        targets.append(f * 0.8)
    return features, targets


def test_fraud_ensemble_is_byte_deterministic():
    features, targets = _fraud_data()
    a = ch.train_fraud_challenger(features, targets)
    b = ch.train_fraud_challenger(features, targets)
    # Same data + seed → byte-identical ensemble, so the version hash is stable.
    assert a.to_bytes() == b.to_bytes()


def test_fraud_ensemble_reports_mean_and_variance():
    features, targets = _fraud_data()
    model = ch.train_fraud_challenger(features, targets)
    preds = model.predict([[0.8, 0.7, 0.6, 9.0, 0.5, 0.6]])
    assert 0.0 <= preds[0].mean <= 1.0
    assert preds[0].variance >= 0.0  # cross-ensemble disagreement signal


def test_fraud_ensemble_roundtrips_through_bytes():
    features, targets = _fraud_data()
    model = ch.train_fraud_challenger(features, targets)
    reloaded = ch.load_fraud_challenger(model.to_bytes())
    x = [[0.8, 0.7, 0.6, 9.0, 0.5, 0.6]]
    assert reloaded.scores(x)[0] == pytest.approx(model.scores(x)[0])


def test_reach_quantiles_are_ordered_and_deterministic():
    features, targets = _reach_data()
    a = ch.train_reach_challenger(features, targets, tuple(f"f{i}" for i in range(16)))
    b = ch.train_reach_challenger(features, targets, tuple(f"f{i}" for i in range(16)))
    assert a.to_bytes() == b.to_bytes()
    pred = a.predict([features[0]])[0]
    # The band is always honest: P10 <= P50 <= P90 (crossing is sorted away).
    assert pred.p10 <= pred.p50 <= pred.p90
