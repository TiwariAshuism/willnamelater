"""G0 data floors and the GROUPED temporal split.

The floors count DISTINCT INFLUENCERS, not rows, and the split never puts one
influencer on both sides. Both exist to kill the same bug: a re-audited panel of
a handful of creators looks like a large dataset to a row counter, and a row-wise
split then lets the model memorize creators it has already seen — so the held-out
gate measures recall of a memory instead of generalization, and reports a
beautiful, meaningless number.
"""

from training.gate import (
    FLOOR_PER_CLASS,
    MIN_FRAUD_INFLUENCERS_PER_CLASS,
    MIN_REACH_INFLUENCERS,
    meets_floor,
    meets_reach_floor,
)
from training.train import grouped_temporal_split, train


def _ids(prefix, n):
    return [f"{prefix}{i}" for i in range(n)]


# --------------------------------------------------------------------------- #
# Fraud floor
# --------------------------------------------------------------------------- #
def test_below_floor():
    ok, counts = meets_floor([1] * 10 + [0] * 10, _ids("inf", 20))
    assert not ok
    assert counts["positive"] == 10
    assert counts["negative"] == 10


def test_at_floor():
    targets = [1] * FLOOR_PER_CLASS + [0] * FLOOR_PER_CLASS
    ok, _ = meets_floor(targets, _ids("inf", 2 * FLOOR_PER_CLASS))
    assert ok


def test_imbalanced_stays_below_floor():
    # One class short of the floor blocks training, even if the other clears it.
    targets = [1] * FLOOR_PER_CLASS + [0] * (FLOOR_PER_CLASS - 1)
    ok, _ = meets_floor(targets, _ids("inf", 2 * FLOOR_PER_CLASS - 1))
    assert not ok


def test_fraud_floor_counts_influencers_not_rows():
    """100 labelled rows per class — but from 5 re-audited creators per class.

    A row counter calls this "clear of the floor". It is 5 examples of fraud and
    5 of clean, re-measured 20 times each, and a classifier fit on it learns
    those ten accounts, not fraud.
    """
    targets = [1] * 100 + [0] * 100
    influencers = (
        [f"pos{i % 5}" for i in range(100)] + [f"neg{i % 5}" for i in range(100)]
    )
    ok, counts = meets_floor(targets, influencers)
    assert ok is False
    assert counts["positive"] == 100  # the ROWS clear the row floor …
    assert counts["positive_influencers"] == 5  # … and the CREATORS do not
    assert counts["influencer_floor"] == MIN_FRAUD_INFLUENCERS_PER_CLASS


# --------------------------------------------------------------------------- #
# Reach floor — the primary regression guard
# --------------------------------------------------------------------------- #
def test_reach_floor_rejects_200_rows_drawn_from_20_influencers():
    """THE bug this floor exists for.

    Re-auditing 20 creators monthly produces 200 rows. Under a ROW floor of 200
    that clears G0, and a challenger gets trained, validated and promoted on what
    is really 20 accounts. It must NOT clear the floor.
    """
    influencers = [f"inf{i % 20}" for i in range(200)]
    ok, counts = meets_reach_floor(200, influencers)
    assert ok is False
    assert counts["rows"] == 200
    assert counts["distinct_influencers"] == 20
    assert counts["floor"] == MIN_REACH_INFLUENCERS
    assert counts["floor_basis"] == "distinct_influencers"


def test_reach_floor_clears_only_on_enough_distinct_influencers():
    influencers = _ids("inf", MIN_REACH_INFLUENCERS)
    ok, counts = meets_reach_floor(len(influencers), influencers)
    assert ok is True
    assert counts["distinct_influencers"] == MIN_REACH_INFLUENCERS

    # Plenty of rows, one creator short: still below the floor.
    ok, _ = meets_reach_floor(999, _ids("inf", MIN_REACH_INFLUENCERS - 1))
    assert ok is False


def test_blank_influencer_ids_do_not_count_as_a_creator():
    # An id we do not have is not a creator we can vouch for, and blanks must not
    # collapse into one phantom influencer.
    ok, counts = meets_reach_floor(3, ["", None, "  "])
    assert ok is False
    assert counts["distinct_influencers"] == 0


# --------------------------------------------------------------------------- #
# Grouped temporal split
# --------------------------------------------------------------------------- #
def test_grouped_split_never_puts_an_influencer_on_both_sides():
    # 20 creators, each audited 10 times over 10 months: the exact panel shape
    # that leaks under a row-wise split.
    captured_at, influencers = [], []
    for month in range(1, 11):
        for creator in range(20):
            captured_at.append(f"2026-{month:02d}-01T00:00:00Z")
            influencers.append(f"inf{creator}")

    train_idx, held_idx = grouped_temporal_split(captured_at, influencers)
    train_creators = {influencers[i] for i in train_idx}
    held_creators = {influencers[i] for i in held_idx}

    assert train_creators & held_creators == set()  # no leakage, ever
    assert held_creators  # and the held-out side is not empty
    assert len(train_idx) + len(held_idx) == len(captured_at)  # nothing dropped


def test_grouped_split_holds_out_whole_creators_not_rows():
    captured_at = ["2026-01-01", "2026-02-01", "2026-03-01", "2026-04-01"]
    influencers = ["a", "a", "b", "b"]
    train_idx, held_idx = grouped_temporal_split(captured_at, influencers)
    # Every row of a creator lands on the same side, and the newest creator is
    # the one held out.
    assert {influencers[i] for i in train_idx} == {"a"}
    assert {influencers[i] for i in held_idx} == {"b"}


def test_grouped_split_keeps_unidentified_rows_apart():
    # Blank ids must not be merged into a single group: that would be an identity
    # claim we cannot make, and it would drag unrelated rows onto one side.
    captured_at = ["2026-01-01", "2026-02-01", "2026-03-01", "2026-04-01"]
    train_idx, held_idx = grouped_temporal_split(captured_at, ["", "", "", ""])
    assert len(train_idx) + len(held_idx) == 4
    assert held_idx


def test_train_below_floor_emits_nothing_and_needs_no_lightgbm():
    # A tiny label set never reaches the lazy lightgbm import: the gate returns
    # first, so this runs even when the training extra is not installed.
    labels = [
        {
            "label": True,
            "has_features": True,
            "features": {"present": True},
            "resolved_at": "2026-01-01",
            "influencer_id": "i1",
        }
    ]
    result = train(labels)
    assert result.trained is False
    assert result.model_bytes is None
