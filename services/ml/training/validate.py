"""Offline validation gates and the promotion decision.

Pure and dependency-free (no LightGBM): every gate consumes already-computed
prediction arrays, so the orchestrator does the model scoring and this module
does the judging. That keeps the gate criteria unit-testable with tiny synthetic
metric arrays — the tests exercise the *mechanics* of the gates, never assert a
specific fraud/reach verdict is "correct".

The gates implement ``RETRAINING_ARCHITECTURE.md`` §4:

- G1 held-out test (newest temporal slice, GROUPED — never trained on, and no
  influencer appears on both sides; see ``retrain.grouped_temporal_split``)
- G2 stratified per tier / per niche (a per-cohort regression fails the whole)
- G3 canary must-pass (empty set → skipped-with-warning)
- G4 challenger-vs-champion non-inferiority (first-ever model auto-passes)
- G6 beats-the-raw-heuristic (MANDATORY, no auto-pass, no skip)

Each of those returns ``{"pass", ..., "evidence"}`` and every one of them must
pass for ``all_required_pass``.

**``g5_serving_skew`` IS NOT IN THAT LIST, AND IS NOT AN ACCURACY GATE.** It
compares two score DISTRIBUTIONS (offline vs live) and joins NO LABELS AT ALL.
It cannot tell you the challenger is better, worse, or the same on the truth,
and it is written so that it cannot even express such a claim: its result has no
``pass`` key. It is a plumbing check — "the model sees the same kind of input in
production as it did offline" — and it can only VETO, never endorse. The real
label-joined shadow arbiter (``ml_prediction_log`` JOIN ``training_feature_row``
ON ``audit_job_id``, comparing champion and challenger predictions against reach
/ fraud outcomes that arrived AFTER the prediction) is a SEPARATE GATE THAT DOES
NOT EXIST YET. Until it is built, no shadow evidence of accuracy exists, and
nothing in this module should be read as supplying it.
"""

from __future__ import annotations

import math

from training.gate import MIN_STRATUM_N

# Serving decision threshold for the fraud probability (mean ensemble score).
FRAUD_THRESHOLD = 0.5

# G1 fraud: minimum per-class precision and recall on the held-out slice.
FRAUD_MIN_PRECISION = 0.70
FRAUD_MIN_RECALL = 0.70

# G1 reach: max P50 MAPE and the acceptable empirical [P10,P90] coverage band.
REACH_MAX_MAPE = 0.35
REACH_COVERAGE_MIN = 0.70
REACH_COVERAGE_MAX = 0.90

# G2: tolerated per-stratum regression vs the current champion.
FRAUD_RECALL_DROP_TOL = 0.05
REACH_MAPE_INCREASE_TOL = 0.05

# Serving-skew window (NOT an accuracy gate — see the module docstring).
MIN_SHADOW_N = 50
PSI_MAX = 0.25

# G6 beats-the-raw-heuristic. A challenger that cannot outperform the heuristic
# it was trained downstream of is a DISTILLATION of that heuristic, not a model:
# it adds no information, only the appearance of one, and it launders a rule of
# thumb into "our ML says". The margins are the minimum improvement that counts
# as beating it; a tie is a fail.
HEURISTIC_AUC_MARGIN = 0.02
HEURISTIC_MAPE_MARGIN = 0.02
# Minimum usable held-out rows (baseline score present, both classes for AUC)
# before G6 can return a verdict at all. Too few → the gate FAILS, it does not
# skip: an unproven challenger is not a promotable one.
#
# 20 is the largest value the CURRENT fraud data floor can actually satisfy: the
# floor admits 50 labelled rows per class (100 rows), of which the held-out slice
# is 20%. So a comparison against the heuristic on ~20 rows is the most evidence
# the floor can produce — and it is thin. The honest resolution is to RAISE THE
# DATA FLOOR (the Go promote re-check mirrors it, so that is a cross-service
# decision, flagged rather than taken here), not to lower this and pretend.
G6_MIN_N = 20

_EPS = 1e-9


# --------------------------------------------------------------------------- #
# Pure metric helpers
# --------------------------------------------------------------------------- #
def labels_at_threshold(scores, threshold: float = FRAUD_THRESHOLD) -> list[int]:
    return [1 if s >= threshold else 0 for s in scores]


def precision_recall(y_true, y_pred, cls: int) -> dict:
    tp = sum(1 for t, p in zip(y_true, y_pred, strict=True) if t == cls and p == cls)
    fp = sum(1 for t, p in zip(y_true, y_pred, strict=True) if t != cls and p == cls)
    fn = sum(1 for t, p in zip(y_true, y_pred, strict=True) if t == cls and p != cls)
    precision = tp / (tp + fp) if (tp + fp) else 0.0
    recall = tp / (tp + fn) if (tp + fn) else 0.0
    return {"precision": precision, "recall": recall}


def f1(pr: dict) -> float:
    p, r = pr["precision"], pr["recall"]
    return (2 * p * r / (p + r)) if (p + r) else 0.0


def mean_class_f1(y_true, y_pred) -> float:
    return (f1(precision_recall(y_true, y_pred, 1))
            + f1(precision_recall(y_true, y_pred, 0))) / 2


def mape(y_true, y_pred) -> float:
    if not y_true:
        return 0.0
    total = 0.0
    for t, p in zip(y_true, y_pred, strict=True):
        total += abs(t - p) / max(abs(t), _EPS)
    return total / len(y_true)


def coverage(y_true, lo, hi) -> float:
    if not y_true:
        return 0.0
    inside = sum(
        1 for t, a, b in zip(y_true, lo, hi, strict=True) if a <= t <= b
    )
    return inside / len(y_true)


def _finite(v) -> bool:
    return isinstance(v, (int, float)) and not (math.isnan(v) or math.isinf(v))


def roc_auc(y_true, scores) -> float | None:
    """Rank-based AUC (Mann–Whitney U) with tie-averaged ranks.

    Rank-based on purpose: the raw heuristic score is on an arbitrary scale
    (0-100 risk), the challenger emits a probability, and AUC is invariant to
    any monotone rescaling of either. It therefore answers exactly the question
    G6 asks — "does the challenger ORDER the truth better than the heuristic
    does?" — without needing the two to share units or a threshold.

    Returns None when the comparison is undefined (a single class present).
    """
    pairs = [(s, t) for s, t in zip(scores, y_true, strict=True) if _finite(s)]
    n_pos = sum(1 for _, t in pairs if t == 1)
    n_neg = len(pairs) - n_pos
    if n_pos == 0 or n_neg == 0:
        return None
    ranked = sorted(range(len(pairs)), key=lambda i: pairs[i][0])
    ranks = [0.0] * len(pairs)
    i = 0
    while i < len(ranked):
        j = i
        while j + 1 < len(ranked) and pairs[ranked[j + 1]][0] == pairs[ranked[i]][0]:
            j += 1
        avg = (i + j) / 2 + 1  # 1-based, averaged over the tie block
        for k in range(i, j + 1):
            ranks[ranked[k]] = avg
        i = j + 1
    rank_sum_pos = sum(r for r, (_, t) in zip(ranks, pairs, strict=True) if t == 1)
    return (rank_sum_pos - n_pos * (n_pos + 1) / 2) / (n_pos * n_neg)


def psi(expected, actual, *, bins: int = 10) -> float:
    """Population stability index between two score distributions on [0,1].

    Sum over bins of ``(a% - e%) * ln(a% / e%)``. A small floor avoids division
    by / log of zero in empty bins.
    """
    if not expected or not actual:
        return 0.0

    def hist(xs):
        counts = [0] * bins
        for x in xs:
            k = min(bins - 1, max(0, int(x * bins)))
            counts[k] += 1
        return [c / len(xs) for c in counts]

    e_pct, a_pct = hist(expected), hist(actual)
    total = 0.0
    for e, a in zip(e_pct, a_pct, strict=True):
        e = max(e, 1e-4)
        a = max(a, 1e-4)
        total += (a - e) * math.log(a / e)
    return total


# --------------------------------------------------------------------------- #
# G1 — held-out test
# --------------------------------------------------------------------------- #
def g1_fraud(y_true, chal_scores, *, threshold: float = FRAUD_THRESHOLD) -> dict:
    y_pred = labels_at_threshold(chal_scores, threshold)
    pos = precision_recall(y_true, y_pred, 1)
    neg = precision_recall(y_true, y_pred, 0)
    ok = all(
        m["precision"] >= FRAUD_MIN_PRECISION and m["recall"] >= FRAUD_MIN_RECALL
        for m in (pos, neg)
    )
    return {
        "pass": ok,
        "evidence": {"positive": pos, "negative": neg, "n": len(y_true),
                     "min_precision": FRAUD_MIN_PRECISION,
                     "min_recall": FRAUD_MIN_RECALL},
    }


def g1_reach(y_true, p10, p50, p90) -> dict:
    m = mape(y_true, p50)
    cov = coverage(y_true, p10, p90)
    ok = m <= REACH_MAX_MAPE and REACH_COVERAGE_MIN <= cov <= REACH_COVERAGE_MAX
    return {
        "pass": ok,
        "evidence": {"p50_mape": m, "coverage": cov, "n": len(y_true),
                     "max_mape": REACH_MAX_MAPE,
                     "coverage_band": [REACH_COVERAGE_MIN, REACH_COVERAGE_MAX]},
    }


# --------------------------------------------------------------------------- #
# G2 — stratified per tier / per niche
# --------------------------------------------------------------------------- #
def _group_indices(strata):
    """Map each ('tier'|'niche', value) stratum to the row indices in it."""
    groups: dict[tuple[str, str], list[int]] = {}
    for i, s in enumerate(strata):
        for dim, val in (("tier", s.tier), ("niche", s.niche)):
            groups.setdefault((dim, val), []).append(i)
    return groups


def g2_fraud_stratified(
    y_true, chal_scores, champ_scores, strata, *, threshold: float = FRAUD_THRESHOLD
) -> dict:
    """No stratum with >= MIN_STRATUM_N rows may regress recall beyond tolerance.

    ``champ_scores`` None (first-ever model) → auto-pass: there is nothing to
    regress against. Thin strata are reported but not gated.
    """
    if champ_scores is None:
        return {"pass": True, "skipped_no_champion": True, "strata": []}
    chal_pred = labels_at_threshold(chal_scores, threshold)
    champ_pred = labels_at_threshold(champ_scores, threshold)
    reports = []
    ok = True
    for (dim, val), idx in sorted(_group_indices(strata).items()):
        n = len(idx)
        gated = n >= MIN_STRATUM_N
        yt = [y_true[i] for i in idx]
        chal_recall = precision_recall(yt, [chal_pred[i] for i in idx], 1)["recall"]
        champ_recall = precision_recall(yt, [champ_pred[i] for i in idx], 1)["recall"]
        drop = champ_recall - chal_recall
        regressed = gated and drop > FRAUD_RECALL_DROP_TOL
        if regressed:
            ok = False
        reports.append({
            "dimension": dim, "value": val, "n": n, "gated": gated,
            "challenger_recall": chal_recall, "champion_recall": champ_recall,
            "recall_drop": drop, "regressed": regressed,
        })
    return {"pass": ok, "tolerance": FRAUD_RECALL_DROP_TOL, "strata": reports}


def g2_reach_stratified(y_true, chal_p50, champ_p50, strata) -> dict:
    if champ_p50 is None:
        return {"pass": True, "skipped_no_champion": True, "strata": []}
    reports = []
    ok = True
    for (dim, val), idx in sorted(_group_indices(strata).items()):
        n = len(idx)
        gated = n >= MIN_STRATUM_N
        yt = [y_true[i] for i in idx]
        chal_mape = mape(yt, [chal_p50[i] for i in idx])
        champ_mape = mape(yt, [champ_p50[i] for i in idx])
        increase = chal_mape - champ_mape
        regressed = gated and increase > REACH_MAPE_INCREASE_TOL
        if regressed:
            ok = False
        reports.append({
            "dimension": dim, "value": val, "n": n, "gated": gated,
            "challenger_mape": chal_mape, "champion_mape": champ_mape,
            "mape_increase": increase, "regressed": regressed,
        })
    return {"pass": ok, "tolerance": REACH_MAPE_INCREASE_TOL, "strata": reports}


# --------------------------------------------------------------------------- #
# G3 — canary must-pass
# --------------------------------------------------------------------------- #
def g3_canary_fraud(results, *, threshold: float = FRAUD_THRESHOLD) -> dict:
    """``results``: list of {"expected_label": bool, "score": float, "label": str}.

    Empty set → skipped-with-warning (honest cold start). Otherwise every canary
    must be scored to its expected label at the serving threshold (100%).
    """
    if not results:
        return {"pass": True, "skipped": True,
                "warning": "no active canaries — canary gate uncovered", "misses": []}
    misses = []
    for r in results:
        predicted = 1 if r["score"] >= threshold else 0
        expected = 1 if r["expected_label"] else 0
        if predicted != expected:
            misses.append({"label": r.get("label"), "expected": expected,
                           "predicted": predicted, "score": r["score"]})
    return {"pass": not misses, "skipped": False, "count": len(results),
            "misses": misses}


def g3_canary_reach(results) -> dict:
    """``results``: list of {"expected_min","expected_max","p50","label"}."""
    if not results:
        return {"pass": True, "skipped": True,
                "warning": "no active canaries — canary gate uncovered", "misses": []}
    misses = []
    for r in results:
        lo, hi, p50 = r["expected_min"], r["expected_max"], r["p50"]
        if not (lo <= p50 <= hi):
            misses.append({"label": r.get("label"), "expected_min": lo,
                           "expected_max": hi, "p50": p50})
    return {"pass": not misses, "skipped": False, "count": len(results),
            "misses": misses}


# --------------------------------------------------------------------------- #
# G4 — challenger vs champion (aggregate non-inferiority)
# --------------------------------------------------------------------------- #
def g4_fraud(
    y_true, chal_scores, champ_scores, *, threshold: float = FRAUD_THRESHOLD
) -> dict:
    chal_f1 = mean_class_f1(y_true, labels_at_threshold(chal_scores, threshold))
    if champ_scores is None:
        return {"pass": True, "first_model": True,
                "evidence": {"challenger_mean_f1": chal_f1}}
    champ_f1 = mean_class_f1(y_true, labels_at_threshold(champ_scores, threshold))
    return {"pass": chal_f1 >= champ_f1, "first_model": False,
            "evidence": {"challenger_mean_f1": chal_f1, "champion_mean_f1": champ_f1}}


def g4_reach(y_true, chal_p50, champ_p50) -> dict:
    chal_mape = mape(y_true, chal_p50)
    if champ_p50 is None:
        return {"pass": True, "first_model": True,
                "evidence": {"challenger_mape": chal_mape}}
    champ_mape = mape(y_true, champ_p50)
    return {"pass": chal_mape <= champ_mape, "first_model": False,
            "evidence": {"challenger_mape": chal_mape, "champion_mape": champ_mape}}


# --------------------------------------------------------------------------- #
# G6 — the challenger must BEAT THE RAW HEURISTIC (mandatory; no auto-pass)
# --------------------------------------------------------------------------- #
def g6_beats_heuristic_fraud(y_true, chal_scores, heuristic_scores) -> dict:
    """The challenger must ORDER the held-out truth better than the raw heuristic.

    ``heuristic_scores`` is the heuristic's own composite risk score for each
    held-out row, read straight off the frozen feature vector — the very number
    the product shipped before any model existed. If a LightGBM ensemble cannot
    beat it, the ensemble has learned the heuristic and nothing else: it is a
    distillation with a version hash, and promoting it would let us claim a
    model where we have a rule of thumb.

    Compared by AUC (scale-free; see ``roc_auc``). No auto-pass on a first
    model: a first model has MORE to prove, not less. No skip: if the baseline
    cannot be computed on enough rows, the gate FAILS — an unproven challenger
    is not promotable, and "we couldn't check" is not "it passed".
    """
    usable = [i for i, s in enumerate(heuristic_scores) if _finite(s)]
    y_used = [y_true[i] for i in usable]
    chal_auc = roc_auc(y_used, [chal_scores[i] for i in usable])
    heur_auc = roc_auc(y_used, [heuristic_scores[i] for i in usable])
    evidence = {
        "baseline": "raw_heuristic_risk_score",
        "n_usable": len(usable), "n_total": len(y_true), "min_n": G6_MIN_N,
        "challenger_auc": chal_auc, "heuristic_auc": heur_auc,
        "margin": HEURISTIC_AUC_MARGIN,
    }
    if len(usable) < G6_MIN_N or chal_auc is None or heur_auc is None:
        evidence["reason"] = (
            "cannot evaluate the heuristic baseline on the held-out slice "
            "(too few rows carrying a baseline score, or a single class): the "
            "challenger is UNPROVEN against the heuristic, so it is not promotable"
        )
        return {"pass": False, "inconclusive": True, "evidence": evidence}
    beat = chal_auc - heur_auc
    evidence["auc_gain"] = beat
    return {"pass": beat >= HEURISTIC_AUC_MARGIN, "inconclusive": False,
            "evidence": evidence}


def g6_beats_heuristic_reach(y_true, chal_p50, baseline_p50, *, baseline: str) -> dict:
    """The challenger's P50 must beat the non-model baseline's by a real margin.

    NOTE ON THE REACH BASELINE: unlike fraud, the pipeline has never shipped a
    heuristic reach ESTIMATOR — there is no rule-of-thumb reach number anywhere
    in the product to beat. The honest stand-in for "no model" is then the
    trivial constant predictor (the training median), which is what ``retrain``
    passes and what ``baseline`` names. A quantile regressor that cannot beat a
    constant has learned nothing from the features at all. If a real heuristic
    reach estimator is ever shipped, pass ITS predictions here instead and the
    gate becomes the literal beats-the-heuristic check.
    """
    usable = [i for i, s in enumerate(baseline_p50) if _finite(s)]
    y_used = [y_true[i] for i in usable]
    chal_mape = mape(y_used, [chal_p50[i] for i in usable])
    base_mape = mape(y_used, [baseline_p50[i] for i in usable])
    evidence = {
        "baseline": baseline, "n_usable": len(usable), "n_total": len(y_true),
        "min_n": G6_MIN_N, "challenger_mape": chal_mape, "baseline_mape": base_mape,
        "margin": HEURISTIC_MAPE_MARGIN,
    }
    if len(usable) < G6_MIN_N:
        evidence["reason"] = (
            "cannot evaluate the baseline on the held-out slice: the challenger "
            "is UNPROVEN against it, so it is not promotable"
        )
        return {"pass": False, "inconclusive": True, "evidence": evidence}
    gain = base_mape - chal_mape
    evidence["mape_gain"] = gain
    return {"pass": gain >= HEURISTIC_MAPE_MARGIN, "inconclusive": False,
            "evidence": evidence}


# --------------------------------------------------------------------------- #
# Serving skew — a PLUMBING check. NOT a gate. NOT an accuracy verdict.
# --------------------------------------------------------------------------- #
def g5_serving_skew(offline_scores, shadow_scores) -> dict:
    """PSI between the offline held-out and the live shadow score DISTRIBUTIONS.

    **This function joins NO LABELS. It cannot say the challenger is better.**
    Its result deliberately carries NO ``pass`` key and NO champion-vs-challenger
    comparison, so it is structurally incapable of expressing an accuracy verdict
    — the caller cannot read one out of it even by accident, and
    ``should_promote`` raises if handed a dict that pretends otherwise.

    What it measures: whether the inputs the model meets in production resemble
    the inputs it was validated on. A null "plumbing" challenger that merely
    echoes the champion scores identically here and yields PSI ~= 0 — which is
    precisely the point: PSI ~= 0 means "the pipes line up", NOT "the model is
    right". Under the old name (``g5_shadow``, with a ``pass`` key) that zero-
    label result was being read as a shadow-window PASS, which is how a heuristic
    echo could be registered as an ML champion on 50 label-free pairs.

    The real arbiter — ``ml_prediction_log`` JOIN ``training_feature_row`` ON
    ``audit_job_id``, scoring champion and challenger against outcomes that
    landed AFTER the prediction — is A SEPARATE GATE AND IT IS NOT BUILT YET.
    Nothing here substitutes for it.

    Returns ``skew_detected``: True (skewed), False (measured, no skew) or None
    (not enough live traffic to say). Only an explicit False lets a promotion
    through; True and None both veto.
    """
    n = len(shadow_scores)
    if n < MIN_SHADOW_N:
        return {
            "skew_detected": None, "n": n, "min_n": MIN_SHADOW_N,
            "labels_joined": 0, "is_accuracy_verdict": False,
            "reason": "insufficient live shadow scores; the window stays open",
        }
    value = psi(offline_scores, shadow_scores)
    return {
        "skew_detected": value >= PSI_MAX, "n": n, "psi": value, "psi_max": PSI_MAX,
        "labels_joined": 0, "is_accuracy_verdict": False,
        "reason": "train/serve INPUT skew only; says nothing about accuracy",
    }


# --------------------------------------------------------------------------- #
# Report builders + promotion decision
# --------------------------------------------------------------------------- #
def build_fraud_report(
    y_true, chal_scores, champ_scores, strata, canary_results, heuristic_scores,
    *, threshold: float = FRAUD_THRESHOLD,
) -> dict:
    gates = {
        "g1_held_out": g1_fraud(y_true, chal_scores, threshold=threshold),
        "g2_stratified": g2_fraud_stratified(
            y_true, chal_scores, champ_scores, strata, threshold=threshold),
        "g3_canary": g3_canary_fraud(canary_results, threshold=threshold),
        "g4_vs_champion": g4_fraud(
            y_true, chal_scores, champ_scores, threshold=threshold),
        "g6_beats_heuristic": g6_beats_heuristic_fraud(
            y_true, chal_scores, heuristic_scores),
    }
    # Gate verdicts live at the TOP LEVEL of the report (contract §5.2): the Go
    # promote endpoint re-checks them there, not nested under a "gates" key.
    report = {"model_name": "fraud", **gates}
    report["all_required_pass"] = _required_pass(gates)
    return report


def build_reach_report(
    y_true, chal_p10, chal_p50, chal_p90, champ_p50, strata, canary_results,
    baseline_p50, *, baseline: str, within_account_spread=None,
) -> dict:
    gates = {
        "g1_held_out": g1_reach(y_true, chal_p10, chal_p50, chal_p90),
        "g2_stratified": g2_reach_stratified(y_true, chal_p50, champ_p50, strata),
        "g3_canary": g3_canary_reach(canary_results),
        "g4_vs_champion": g4_reach(y_true, chal_p50, champ_p50),
        "g6_beats_heuristic": g6_beats_heuristic_reach(
            y_true, chal_p50, baseline_p50, baseline=baseline),
    }
    report = {"model_name": "reach", **gates}
    report["measurement_disclosure"] = _spread_disclosure(within_account_spread)
    report["all_required_pass"] = _required_pass(gates)
    return report


def _spread_disclosure(within_account_spread) -> dict:
    """Report the observed WITHIN-ACCOUNT post-to-post reach spread, explicitly
    fenced off from the model's predictive band.

    Two different quantities that a reader will otherwise conflate. The model's
    [p10, p90] answers "how uncertain are we about THIS ACCOUNT'S reach?"; this
    answers "how much did that account's own posts vary?". The second is a
    measurement of accounts we already saw; it is never the model's interval and
    was never a training target (see ``challenger.train_reach_challenger``).
    """
    observed = [s for s in (within_account_spread or []) if s]
    return {
        "kind": "within_account_post_spread",
        "is_model_predictive_interval": False,
        "note": "measurement of observed accounts' own post-to-post reach spread; "
                "NOT the model's predictive interval and never a training target",
        "n_rows_with_spread": len(observed),
    }


# The offline gate keys, at the top level of a validation report (contract §5.2).
# ALL of them are required (G3 may be honestly skipped on an empty canary set).
GATE_KEYS = (
    "g1_held_out", "g2_stratified", "g3_canary", "g4_vs_champion",
    "g6_beats_heuristic",
)

# The serving-skew result's key in the report. It is NOT in GATE_KEYS: it is not
# a gate, it carries no "pass", and it can only veto a promotion, never grant one.
SERVING_SKEW_KEY = "g5_serving_skew"


def _required_pass(gates: dict) -> bool:
    """Every offline gate must pass; G3 alone may instead be honestly skipped.

    G6 (beats-the-raw-heuristic) is mandatory and has no skip path: a challenger
    that does not beat the heuristic is a distillation of it, and shipping it as
    a model would be a lie about where the number came from.

    Serving skew is evaluated separately, on live traffic, and is not part of the
    register-time offline verdict — and it is not an accuracy verdict at all.
    """
    return (
        gates["g1_held_out"]["pass"]
        and gates["g2_stratified"]["pass"]
        and gates["g4_vs_champion"]["pass"]
        and gates["g6_beats_heuristic"]["pass"]
        and (gates["g3_canary"]["pass"] or gates["g3_canary"].get("skipped", False))
    )


def should_promote(report: dict, serving_skew: dict | None = None) -> bool:
    """Promote only when every offline gate passes AND, if a serving-skew reading
    was taken, it explicitly found NO skew.

    ``serving_skew`` can only ever REMOVE a promotion. It cannot supply one: a
    skew reading joins no labels, so "no skew" is not evidence of accuracy, and
    an inconclusive reading (too little live traffic) blocks rather than waves
    through. A dict carrying a ``pass`` key is rejected outright — that is the
    shape of the old accuracy-flavoured G5 verdict, and accepting it here is how
    a label-free plumbing check gets mistaken for a shadow-window result.
    """
    if serving_skew is not None and "pass" in serving_skew:
        raise ValueError(
            "serving skew carries no accuracy verdict: a dict with a 'pass' key is "
            "not a skew reading. The label-joined shadow arbiter "
            "(ml_prediction_log JOIN training_feature_row) is a separate gate and "
            "is NOT BUILT — do not fake it with a PSI number."
        )
    if not report.get("all_required_pass"):
        return False
    if serving_skew is not None:
        return serving_skew.get("skew_detected") is False
    return True
