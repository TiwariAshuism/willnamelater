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

# Minimum DISTINCT INFLUENCERS carrying a real reach label (from OAuth Instagram
# Insights) before the REACH quantile regressor may be fit. Contract §4 G0.
#
# This floor counts INFLUENCERS, not rows, and that distinction is the whole
# point. Rows are keyed by audit_job_id, and the same creator is re-audited on a
# schedule: 25 creators audited monthly for 8 months produce 200 rows while the
# model has only ever seen 25 accounts. A row floor calls that "enough data"; it
# is not — it is 25 accounts, and a model fit on it memorizes 25 creators and
# then reports a beautiful, meaningless held-out number, because the same
# creators are on both sides of the split (see grouped_temporal_split).
#
# The effective sample size of a repeated-measures panel is the number of
# INDEPENDENT UNITS. That is what this floor counts.
MIN_REACH_INFLUENCERS = 200

# Minimum distinct influencers PER CLASS for the fraud classifier. Same argument
# as above: 50 labelled rows drawn from 6 re-audited creators is 6 examples of
# fraud, not 50. Kept alongside FLOOR_PER_CLASS (both must hold).
MIN_FRAUD_INFLUENCERS_PER_CLASS = 25

# Minimum held-out rows in a (tier / niche) stratum before that stratum is
# GATED for regression (G2). Thinner strata are reported but not gated — too few
# rows to judge a regression honestly. Contract §4 G2.
MIN_STRATUM_N = 30


def distinct_count(influencer_ids) -> int:
    """Number of distinct, non-empty influencer ids. An id we do not have is not
    an influencer we can vouch for, so blanks are dropped rather than collapsed
    into one phantom creator."""
    return len({str(i) for i in influencer_ids if str(i or "").strip()})


def class_counts(targets, influencer_ids) -> dict:
    """Label counts AND distinct-influencer counts per class, for reporting.

    ``rows``-per-class is what the Go promote re-check reads today
    (``positive`` / ``negative``); the ``*_influencers`` keys are the honest
    effective sample size and are what this module actually gates on.
    """
    pos_ids = [i for i, t in zip(influencer_ids, targets, strict=True) if t == 1]
    neg_ids = [i for i, t in zip(influencer_ids, targets, strict=True) if t != 1]
    return {
        "positive": len(pos_ids),
        "negative": len(neg_ids),
        "floor": FLOOR_PER_CLASS,
        "positive_influencers": distinct_count(pos_ids),
        "negative_influencers": distinct_count(neg_ids),
        "influencer_floor": MIN_FRAUD_INFLUENCERS_PER_CLASS,
    }


def meets_floor(targets, influencer_ids):
    """Both classes must clear the per-class ROW floor AND the per-class DISTINCT
    INFLUENCER floor. Rows are not independent examples; creators are."""
    counts = class_counts(targets, influencer_ids)
    ok = (
        counts["positive"] >= FLOOR_PER_CLASS
        and counts["negative"] >= FLOOR_PER_CLASS
        and counts["positive_influencers"] >= MIN_FRAUD_INFLUENCERS_PER_CLASS
        and counts["negative_influencers"] >= MIN_FRAUD_INFLUENCERS_PER_CLASS
    )
    return ok, counts


def reach_counts(n_rows: int, influencer_ids) -> dict:
    """Return the reach-label row count, the DISTINCT influencer count, and the
    floor the gate is actually taken against.

    ``rows`` is retained because the Go promote re-check reads it
    (``report.go``: ``floorReachRows``); ``distinct_influencers`` is the floor
    this module enforces, and the Go side should enforce it too — see
    ``floor_basis``.
    """
    return {
        "rows": n_rows,
        "distinct_influencers": distinct_count(influencer_ids),
        "floor": MIN_REACH_INFLUENCERS,
        "floor_basis": "distinct_influencers",
    }


def meets_reach_floor(n_rows: int, influencer_ids):
    """Report whether the reach labels cover >= MIN_REACH_INFLUENCERS DISTINCT
    creators. 200 rows from 20 re-audited creators is 20 creators, and it does
    NOT clear this floor."""
    counts = reach_counts(n_rows, influencer_ids)
    ok = counts["distinct_influencers"] >= MIN_REACH_INFLUENCERS
    return ok, counts
