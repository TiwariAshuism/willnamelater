"""Offline validation gates G1–G5 and the promotion decision.

Pure and dependency-free (no LightGBM): every gate consumes already-computed
prediction arrays, so the orchestrator does the model scoring and this module
does the judging. That keeps the gate criteria unit-testable with tiny synthetic
metric arrays — the tests exercise the *mechanics* of the gates, never assert a
specific fraud/reach verdict is "correct".

The gates implement ``RETRAINING_ARCHITECTURE.md`` §4:

- G1 held-out test (newest temporal slice, never trained on)
- G2 stratified per tier / per niche (a per-cohort regression fails the whole)
- G3 canary must-pass (empty set → skipped-with-warning)
- G4 challenger-vs-champion non-inferiority (first-ever model auto-passes)
- G5 shadow-window train/serve-skew (PSI on live shadow scores)

Each gate returns ``{"pass", ..., "evidence"}``; the report the promotion gate
consumes carries every gate's verdict and evidence.
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

# G5 shadow window.
MIN_SHADOW_N = 50
PSI_MAX = 0.25

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
# G5 — shadow window (train/serve skew)
# --------------------------------------------------------------------------- #
def g5_shadow(offline_scores, shadow_scores) -> dict:
    """PSI between the offline held-out and live shadow challenger scores.

    Fewer than MIN_SHADOW_N live pairs → inconclusive: the window cannot close,
    the model stays in shadow (gated on real traffic), and this is NOT a pass.
    """
    n = len(shadow_scores)
    if n < MIN_SHADOW_N:
        return {"pass": False, "inconclusive": True, "n": n,
                "min_n": MIN_SHADOW_N,
                "reason": "insufficient live shadow scores; window stays open"}
    value = psi(offline_scores, shadow_scores)
    return {"pass": value < PSI_MAX, "inconclusive": False, "n": n,
            "psi": value, "psi_max": PSI_MAX}


# --------------------------------------------------------------------------- #
# Report builders + promotion decision
# --------------------------------------------------------------------------- #
def build_fraud_report(
    y_true, chal_scores, champ_scores, strata, canary_results,
    *, threshold: float = FRAUD_THRESHOLD,
) -> dict:
    gates = {
        "g1_held_out": g1_fraud(y_true, chal_scores, threshold=threshold),
        "g2_stratified": g2_fraud_stratified(
            y_true, chal_scores, champ_scores, strata, threshold=threshold),
        "g3_canary": g3_canary_fraud(canary_results, threshold=threshold),
        "g4_vs_champion": g4_fraud(
            y_true, chal_scores, champ_scores, threshold=threshold),
    }
    # Gate verdicts live at the TOP LEVEL of the report (contract §5.2): the Go
    # promote endpoint re-checks g1_held_out/g2_stratified/g3_canary/g4_vs_champion
    # there, not nested under a "gates" key.
    report = {"model_name": "fraud", **gates}
    report["all_required_pass"] = _required_pass(gates)
    return report


def build_reach_report(
    y_true, chal_p10, chal_p50, chal_p90, champ_p50, strata, canary_results,
) -> dict:
    gates = {
        "g1_held_out": g1_reach(y_true, chal_p10, chal_p50, chal_p90),
        "g2_stratified": g2_reach_stratified(y_true, chal_p50, champ_p50, strata),
        "g3_canary": g3_canary_reach(canary_results),
        "g4_vs_champion": g4_reach(y_true, chal_p50, champ_p50),
    }
    report = {"model_name": "reach", **gates}
    report["all_required_pass"] = _required_pass(gates)
    return report


# The offline gate keys, at the top level of a validation report (contract §5.2).
GATE_KEYS = ("g1_held_out", "g2_stratified", "g3_canary", "g4_vs_champion")


def _required_pass(gates: dict) -> bool:
    """Every offline gate (G1, G2, G4) must pass; G3 must pass OR be skipped.

    G5 is evaluated separately after a real shadow window; it is not part of the
    register-time offline verdict.
    """
    return (
        gates["g1_held_out"]["pass"]
        and gates["g2_stratified"]["pass"]
        and gates["g4_vs_champion"]["pass"]
        and (gates["g3_canary"]["pass"] or gates["g3_canary"].get("skipped", False))
    )


def should_promote(report: dict, shadow: dict | None = None) -> bool:
    """Promote only when all offline gates pass AND (if a shadow verdict is
    supplied) G5 passes. No shadow verdict → offline gates only (the caller is
    responsible for gating promotion on a real shadow window)."""
    if not report.get("all_required_pass"):
        return False
    if shadow is not None:
        return bool(shadow.get("pass"))
    return True
