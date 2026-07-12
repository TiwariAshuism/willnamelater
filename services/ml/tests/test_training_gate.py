from training.gate import FLOOR_PER_CLASS, meets_floor
from training.train import train


def test_below_floor():
    ok, counts = meets_floor([1] * 10 + [0] * 10)
    assert not ok
    assert counts["positive"] == 10
    assert counts["negative"] == 10


def test_at_floor():
    ok, _ = meets_floor([1] * FLOOR_PER_CLASS + [0] * FLOOR_PER_CLASS)
    assert ok


def test_imbalanced_stays_below_floor():
    # One class short of the floor blocks training, even if the other clears it.
    ok, _ = meets_floor([1] * FLOOR_PER_CLASS + [0] * (FLOOR_PER_CLASS - 1))
    assert not ok


def test_train_below_floor_emits_nothing_and_needs_no_lightgbm():
    # A tiny label set never reaches the lazy lightgbm import: the gate returns
    # first, so this runs even when the training extra is not installed.
    labels = [
        {
            "label": True,
            "has_features": True,
            "features": {"present": True},
            "resolved_at": "2026-01-01",
        }
    ]
    result = train(labels)
    assert result.trained is False
    assert result.model_bytes is None
