"""The data-floor gate: never train on nothing.

This is gate **G0** in the champion–challenger contract
(``RETRAINING_ARCHITECTURE.md`` §4). Below the floor the trainer produces no
challenger and the serving registry stays on the honest cold-start
``heuristic`` state — a model fit on a handful of labels is confidently wrong,
the exact failure this product exists to expose.
"""

from __future__ import annotations

# Minimum labelled, feature-bearing examples required PER CLASS before a
# supervised FRAUD model may be fit. Below this the service keeps serving the
# unsupervised cold-start models: a classifier fit on a handful of labels is
# confidently wrong — the exact failure this product exists to expose.
FLOOR_PER_CLASS = 50

# Minimum rows carrying a real reach label (from OAuth Instagram Insights)
# before the REACH quantile regressor may be fit. Reach has no class balance to
# enforce, only a volume floor. Contract §4 G0.
MIN_REACH_ROWS = 200

# Minimum held-out rows in a (tier / niche) stratum before that stratum is
# GATED for regression (G2). Thinner strata are reported but not gated — too few
# rows to judge a regression honestly. Contract §4 G2.
MIN_STRATUM_N = 30


def class_counts(targets):
    """Return the positive/negative label counts and the floor, for reporting."""
    positive = sum(1 for v in targets if v == 1)
    return {
        "positive": positive,
        "negative": len(targets) - positive,
        "floor": FLOOR_PER_CLASS,
    }


def meets_floor(targets):
    """Report whether both classes clear the per-class floor, with the counts."""
    counts = class_counts(targets)
    ok = counts["positive"] >= FLOOR_PER_CLASS and counts["negative"] >= FLOOR_PER_CLASS
    return ok, counts


def reach_counts(n_rows: int) -> dict:
    """Return the reach-label row count and its floor, for reporting."""
    return {"rows": n_rows, "floor": MIN_REACH_ROWS}


def meets_reach_floor(n_rows: int):
    """Report whether the reach-label row count clears the volume floor."""
    counts = reach_counts(n_rows)
    return n_rows >= MIN_REACH_ROWS, counts
