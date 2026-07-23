"""Pure gate mechanics and the promotion decision.

No LightGBM: the gates judge already-computed prediction arrays. The synthetic
arrays exercise the gate *logic* — they never assert a specific account's
fraud/reach verdict is correct.
"""

import pytest

from training import validate
from training.feature_store import Stratum
from training.gate import MIN_STRATUM_N


def _balanced(n=20):
    """n positives + n negatives, challenger predicts them perfectly."""
    y_true = [1] * n + [0] * n
    scores = [0.9] * n + [0.1] * n
    strata = [Stratum(tier="micro", niche="fitness")] * (2 * n)
    return y_true, scores, strata


def _weak_heuristic(n=20):
    """A raw heuristic that orders the truth no better than a coin flip (AUC 0.5):
    it scores half of each class high and half low. Parallel to ``_balanced``."""
    alternating = [0.8 if i % 2 == 0 else 0.2 for i in range(n)]
    return alternating + list(alternating)


def test_g1_fraud_passes_on_separable_scores():
    y_true, scores, _ = _balanced()
    assert validate.g1_fraud(y_true, scores)["pass"] is True


def test_g1_fraud_fails_when_a_class_is_never_recalled():
    y_true = [1] * 20 + [0] * 20
    scores = [0.1] * 40  # predicts everything negative → positive recall 0
    assert validate.g1_fraud(y_true, scores)["pass"] is False


def test_g2_stratified_catches_a_per_tier_regression():
    # A micro-influencer stratum of exactly the gating size where the challenger
    # regresses recall to 0 while the champion had it at 1.0 must FAIL the gate —
    # a model that improves on average but regresses for micro must not ship.
    n = MIN_STRATUM_N
    y_true = [1] * n
    strata = [Stratum(tier="micro", niche="x")] * n
    champ_scores = [0.9] * n  # champion recalls all
    chal_scores = [0.1] * n   # challenger recalls none
    result = validate.g2_fraud_stratified(y_true, chal_scores, champ_scores, strata)
    assert result["pass"] is False
    micro = next(s for s in result["strata"]
                 if s["dimension"] == "tier" and s["value"] == "micro")
    assert micro["regressed"] is True


def test_g2_no_champion_auto_passes():
    y_true, scores, strata = _balanced()
    result = validate.g2_fraud_stratified(y_true, scores, None, strata)
    assert result["pass"] is True
    assert result["skipped_no_champion"] is True


def test_g3_canary_fails_on_a_miss():
    results = [
        {"label": "known bought-follower", "expected_label": True, "score": 0.1},
    ]
    assert validate.g3_canary_fraud(results)["pass"] is False


def test_g3_canary_empty_is_skipped_with_warning():
    result = validate.g3_canary_fraud([])
    assert result["pass"] is True
    assert result["skipped"] is True
    assert "warning" in result


def test_g4_first_model_auto_passes():
    y_true, scores, _ = _balanced()
    result = validate.g4_fraud(y_true, scores, None)
    assert result["pass"] is True
    assert result["first_model"] is True


# --------------------------------------------------------------------------- #
# G6 — beats the raw heuristic (mandatory)
# --------------------------------------------------------------------------- #
def test_g6_fails_a_challenger_that_only_reproduces_the_heuristic():
    """A distillation is not a model.

    The challenger's scores are a monotone rescaling of the heuristic's — it has
    learned the heuristic and nothing else. Its AUC is IDENTICAL, so it adds zero
    information, and G6 must refuse to promote it however good G1 looks.
    """
    y_true, _, _ = _balanced()
    heuristic = _weak_heuristic()
    distilled = [h / 100 for h in heuristic]  # same ordering, prettier units
    result = validate.g6_beats_heuristic_fraud(y_true, distilled, heuristic)
    assert result["pass"] is False
    assert result["evidence"]["auc_gain"] == pytest.approx(0.0)


def test_g6_passes_only_when_the_challenger_orders_the_truth_better():
    y_true, perfect, _ = _balanced()
    result = validate.g6_beats_heuristic_fraud(y_true, perfect, _weak_heuristic())
    assert result["pass"] is True
    assert result["evidence"]["challenger_auc"] > result["evidence"]["heuristic_auc"]


def test_g6_fails_closed_when_the_baseline_cannot_be_scored():
    # No heuristic score on the held-out rows → the challenger is UNPROVEN
    # against the baseline. Unproven is not promotable, and it is not a "skip".
    y_true, scores, _ = _balanced()
    nan = float("nan")
    result = validate.g6_beats_heuristic_fraud(y_true, scores, [nan] * len(y_true))
    assert result["pass"] is False
    assert result["inconclusive"] is True
    assert result["evidence"]["n_usable"] == 0


def test_g6_reach_fails_a_regressor_that_cannot_beat_a_constant():
    y_true = [1000.0 + i for i in range(40)]
    baseline = [1020.0] * 40           # the constant train-median predictor
    challenger = [1020.0] * 40         # learned nothing from the features
    result = validate.g6_beats_heuristic_reach(
        y_true, challenger, baseline, baseline="train_median_constant")
    assert result["pass"] is False
    assert result["evidence"]["mape_gain"] == pytest.approx(0.0)


def test_g6_is_mandatory_in_the_report_and_has_no_skip_path():
    y_true, scores, strata = _balanced()
    # Every other gate passes (perfect separation, no champion, no canaries) but
    # the challenger merely echoes the heuristic → the report must NOT pass.
    heuristic = _weak_heuristic()
    echo = [h / 100 for h in heuristic]
    report = validate.build_fraud_report(y_true, echo, None, strata, [], heuristic)
    assert report["g6_beats_heuristic"]["pass"] is False
    assert report["all_required_pass"] is False
    assert validate.should_promote(report) is False
    assert "g6_beats_heuristic" in validate.GATE_KEYS


# --------------------------------------------------------------------------- #
# Serving skew — NOT a gate, NOT an accuracy verdict
# --------------------------------------------------------------------------- #
def test_serving_skew_cannot_express_an_accuracy_verdict():
    """The structural guarantee: no 'pass', no better/worse, no labels.

    A null 'plumbing' challenger that simply echoes the champion produces an
    identical score distribution and PSI ~= 0. Under the old g5_shadow that read
    as a shadow-window PASS on 50 label-free pairs, which is how a heuristic echo
    could be registered as an ML champion. The result must now be incapable of
    saying anything about accuracy at all.
    """
    offline = [i / 100 for i in range(100)]
    identical = list(offline)
    result = validate.g5_serving_skew(offline, identical)
    assert "pass" not in result           # cannot be read as a verdict
    assert result["skew_detected"] is False  # only ever a statement about INPUTS
    assert result["labels_joined"] == 0
    assert result["is_accuracy_verdict"] is False
    assert not hasattr(validate, "g5_shadow")  # the misleading name is gone


def test_serving_skew_inconclusive_below_min_n_is_not_a_pass():
    result = validate.g5_serving_skew([0.2] * 100, [0.2] * 10)
    assert result["skew_detected"] is None
    assert "pass" not in result


def test_serving_skew_flags_a_shifted_distribution():
    offline = [0.1] * 100
    shifted = [0.9] * validate.MIN_SHADOW_N
    assert validate.g5_serving_skew(offline, shifted)["skew_detected"] is True


def test_promotion_decision_pass_promotes_fail_discards():
    y_true, scores, strata = _balanced()
    heuristic = _weak_heuristic()
    passing = validate.build_fraud_report(y_true, scores, None, strata, [], heuristic)
    assert passing["all_required_pass"] is True
    assert validate.should_promote(passing) is True

    bad = [0.1] * len(y_true)  # positive class never recalled → G1 fails
    failing = validate.build_fraud_report(y_true, bad, None, strata, [], heuristic)
    assert failing["all_required_pass"] is False
    assert validate.should_promote(failing) is False


def test_serving_skew_can_only_veto_a_promotion_never_grant_one():
    y_true, scores, strata = _balanced()
    report = validate.build_fraud_report(
        y_true, scores, None, strata, [], _weak_heuristic())

    # Skew found, or not enough live traffic to say → blocked.
    assert validate.should_promote(report, {"skew_detected": True}) is False
    assert validate.should_promote(report, {"skew_detected": None}) is False
    # Measured, no skew → the offline gates still decide; skew adds no evidence.
    assert validate.should_promote(report, {"skew_detected": False}) is True

    bad = [0.1] * len(y_true)
    failing = validate.build_fraud_report(
        y_true, bad, None, strata, [], _weak_heuristic())
    # A clean skew reading cannot rescue a report that failed its gates.
    assert validate.should_promote(failing, {"skew_detected": False}) is False


def test_should_promote_rejects_a_skew_dict_pretending_to_be_a_verdict():
    y_true, scores, strata = _balanced()
    report = validate.build_fraud_report(
        y_true, scores, None, strata, [], _weak_heuristic())
    with pytest.raises(ValueError, match="no accuracy verdict"):
        validate.should_promote(report, {"pass": True})
