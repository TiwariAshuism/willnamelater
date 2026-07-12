"""The data-floor gate: never train on nothing."""

from __future__ import annotations

# Minimum labelled, feature-bearing examples required PER CLASS before a
# supervised model may be fit. Below this the service keeps serving the
# unsupervised cold-start models: a classifier fit on a handful of labels is
# confidently wrong — the exact failure this product exists to expose.
FLOOR_PER_CLASS = 50


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
