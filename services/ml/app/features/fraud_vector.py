"""Assemble the frozen fraud feature vector for supervised scoring.

The supervised model's positional inputs are ``training.features.FEATURE_ORDER``
(the six fraud signals captured verbatim into ``fraud_result`` — no train/serve
skew in *feature computation* because train and serve read the same keys). This
builder maps what the ``/v1/fraud/score`` request can honestly observe onto those
keys and marks everything else **native-missing** (never zero-filled).

Assembly gap — flagged, not fabricated
--------------------------------------
Five of the six fraud features are *cross-endpoint* outputs the Go ``scoring``
layer assembles from several ML responses, not signals this per-account endpoint
can see from a :class:`FraudScoreRequest` alone:

* ``clique_count`` / ``clique_membership_fraction`` come from ``/v1/pods/detect``
  (needs comment events, absent here);
* ``bot_comment_rate`` comes from ``/v1/comments/classify`` (needs comment text);
* ``fake_follower_rate`` / ``engagement_anomaly`` are ``fraud_result`` outputs of
  the assembled pipeline.

Only ``confidence`` — the per-account fraud confidence this endpoint itself
produces — is filled. The remaining keys are left missing (NaN at score time).
Closing this gap so the champion serves a *full* vector requires the caller to
pass the assembled feature vector; that is a composition-root change and is
flagged for the human (mirrors RETRAINING_ARCHITECTURE §1.2 "flag, do NOT
fabricate"). Until a champion is promoted this path is never taken in production
— cold start serves the heuristic score.
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
    # The one key this endpoint genuinely produces: the account-level fraud
    # confidence. In cold start this is exactly what fraud_result.confidence
    # stored, so filling it keeps train/serve semantics aligned for this key.
    observed["confidence"] = result.confidence
    return observed
