"""Assemble the frozen fraud feature vector for supervised scoring.

The supervised model's positional inputs are ``training.features.FEATURE_ORDER``
(five signals, v2). This builder maps what the ``/v1/fraud/score`` request can
honestly observe onto those keys and marks everything else **native-missing**
(never zero-filled).

Assembly gap — flagged, not fabricated
--------------------------------------
Three of the five features are *cross-endpoint* outputs the Go ``scoring`` layer
assembles from several ML responses, not signals this per-account endpoint can see
from a :class:`FraudScoreRequest` alone:

* ``clique_count`` / ``clique_membership_fraction`` come from ``/v1/pods/detect``
  (needs comment events, absent here);
* ``engagement_anomaly`` needs a sourced engagement benchmark, which the scoring
  layer owns and does not pass to this endpoint.

``risk_score`` (this endpoint's own composite estimate) and ``confidence`` are
filled. The remaining keys are left missing (NaN at score time). The full assembled
vector IS served — by ``/v1/fraud/refine``, which the Go layer calls once it has
gathered every signal, closing the train/serve skew.

v2 removed two keys that were never measurements: ``fake_follower_rate`` (this
same composite score renamed — nothing ever fetched a follower list) and
``bot_comment_rate`` (a bit-for-bit duplicate of ``clique_membership_fraction`` —
no comment's text was ever classified). The v1 vector therefore carried six
positions holding five distinct values.
"""

from __future__ import annotations

from app.models.heuristics import FraudResult
from training.features import FEATURE_ORDER


def build_fraud_vector(result: FraudResult) -> dict[str, float | None]:
    """Project the cold-start fraud result onto ``FEATURE_ORDER``.

    Returns every frozen key; a key this endpoint cannot observe is ``None``
    (native-missing downstream), never 0.
    """
    observed: dict[str, float | None] = dict.fromkeys(FEATURE_ORDER)
    # The two keys this endpoint genuinely produces: its own composite risk score
    # and the confidence behind it. The coordination keys come from a different
    # model (/v1/pods/detect) and the engagement anomaly needs a sourced benchmark
    # this endpoint is not given — all stay None (native-missing), never zero.
    observed["risk_score"] = result.score
    observed["confidence"] = result.confidence
    return observed
