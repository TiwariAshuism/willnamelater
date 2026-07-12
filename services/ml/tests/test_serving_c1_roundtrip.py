"""C1 regression: a trainer-produced fraud artifact must load through the
serving loader. The trainer serializes a JSON bootstrap-ensemble wrapper; serving
must reconstruct it (not treat model.txt as a bare LightGBM booster). This test
round-trips a real trained ensemble through app.serving.supervised — the exact
gap that let the format mismatch slip. Needs the training extra."""

import pytest

pytest.importorskip("lightgbm")

from app.registry import ArtifactRef  # noqa: E402
from app.serving import supervised  # noqa: E402
from training.challenger import train_fraud_challenger  # noqa: E402
from training.features import FEATURE_ORDER  # noqa: E402


def test_default_loader_serves_a_trained_fraud_ensemble(tmp_path):
    # A tiny, cleanly-separable slice — fixtures exercise the load/predict
    # mechanics, not any claim about a specific account's fraud verdict.
    fraudulent = [[0.8, 0.7, 0.6, 9.0, 0.5, 0.6]] * 20
    legitimate = [[0.05, 0.05, 0.05, 0.0, 0.0, 0.55]] * 20
    features = fraudulent + legitimate
    targets = [1] * 20 + [0] * 20

    model = train_fraud_challenger(features, targets, seed=42)

    path = tmp_path / "model.txt"
    path.write_bytes(model.to_bytes())
    ref = ArtifactRef(
        version="lgbm-roundtrip",
        model_path=path,
        feature_order=tuple(FEATURE_ORDER),
    )

    # Use the REAL default loader (not a test fake), and isolate the cache.
    supervised.set_loader(supervised._default_loader)
    supervised.clear_cache()
    try:
        prediction = supervised.predict(
            ref, dict(zip(FEATURE_ORDER, features[0], strict=True))
        )
        assert 0.0 <= prediction.probability <= 1.0
        assert prediction.score == round(prediction.probability * 100.0, 4)
        # The ensemble mean must match the trainer's own scoring of the same row.
        assert prediction.probability == pytest.approx(
            model.scores([features[0]])[0], abs=1e-9
        )
    finally:
        supervised.set_loader(supervised._default_loader)
        supervised.clear_cache()
