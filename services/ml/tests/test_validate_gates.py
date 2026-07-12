"""Pure gate mechanics G1–G5 and the promotion decision.

No LightGBM: the gates judge already-computed prediction arrays. The synthetic
arrays exercise the gate *logic* — they never assert a specific account's
fraud/reach verdict is correct.
"""

from training import validate
from training.feature_store import Stratum
from training.gate import MIN_STRATUM_N


def _balanced(n=20):
    """n positives + n negatives, challenger predicts them perfectly."""
    y_true = [1] * n + [0] * n
    scores = [0.9] * n + [0.1] * n
    strata = [Stratum(tier="micro", niche="fitness")] * (2 * n)
    return y_true, scores, strata


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


def test_g5_shadow_inconclusive_below_min_n_is_not_a_pass():
    result = validate.g5_shadow([0.2] * 100, [0.2] * 10)
    assert result["pass"] is False
    assert result["inconclusive"] is True


def test_g5_shadow_passes_when_distributions_match():
    offline = [i / 100 for i in range(100)]
    shadow = [i / 100 for i in range(100)]
    result = validate.g5_shadow(offline, shadow)
    assert result["inconclusive"] is False
    assert result["pass"] is True


def test_promotion_decision_pass_promotes_fail_discards():
    y_true, scores, strata = _balanced()
    passing = validate.build_fraud_report(y_true, scores, None, strata, [])
    assert passing["all_required_pass"] is True
    assert validate.should_promote(passing) is True

    bad_scores = [0.1] * len(y_true)  # positive class never recalled → G1 fails
    failing = validate.build_fraud_report(y_true, bad_scores, None, strata, [])
    assert failing["all_required_pass"] is False
    assert validate.should_promote(failing) is False


def test_promotion_requires_shadow_when_supplied():
    y_true, scores, strata = _balanced()
    report = validate.build_fraud_report(y_true, scores, None, strata, [])
    failed_shadow = {"pass": False, "inconclusive": True}
    assert validate.should_promote(report, shadow=failed_shadow) is False
    passed_shadow = {"pass": True, "inconclusive": False}
    assert validate.should_promote(report, shadow=passed_shadow) is True
