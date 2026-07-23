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

FIREWALL: the comment classifier does not belong in this vector
---------------------------------------------------------------
STOP before you "just wire it in". Now that ``bot_comment_rate`` is gone there is
an obvious-looking hole, and an obvious-looking thing to plug it with: the output
of ``/v1/comments/classify``. DO NOT.

That endpoint is not a model. It is an 18-phrase English frozenset with an
unmeasured error rate that systematically mislabels Hinglish, Tamil and
Portuguese comment sections (see :mod:`app.features.comments`). And a high
generic-comment rate is not fraud in the first place — fan accounts and meme
pages earn oceans of genuine "🔥🔥🔥".

Its output therefore lives under its own quarantined key,
:data:`~app.features.comments.GENERIC_COMMENT_RATE_KEY`
(``generic_comment_rate_v1``), and may enter ``FEATURE_ORDER``, the 0-100
composite, or any weighted blend ONLY after its weight has been **fitted against
real fraud outcomes** (resolved dispute labels), which requires bumping
``FEATURE_ORDER_VERSION`` and retraining. Hand-picking a weight is the exact sin
that produced ``bot_comment_rate`` and the uncited engagement curve, both of which
this project has since had to delete.

:data:`_QUARANTINED_KEYS` enforces this at import time: if a comment-derived key
ever appears in ``FEATURE_ORDER``, the service refuses to start.
"""

from __future__ import annotations

from app.features.comments import GENERIC_COMMENT_RATE_KEY
from app.models.heuristics import FraudResult
from training.features import FEATURE_ORDER

#: Keys that must NEVER be positions in the fraud feature vector. Two of them are
#: comment-derived (unfitted, unevaluated, culturally biased); ``bot_comment_rate``
#: is the deleted fake, listed so it cannot be resurrected under its old name.
_QUARANTINED_KEYS = frozenset(
    {
        GENERIC_COMMENT_RATE_KEY,
        "bot_comment_rate",
        "low_quality_comment_rate",
    }
)

_leaked = _QUARANTINED_KEYS & set(FEATURE_ORDER)
if _leaked:
    raise RuntimeError(
        "FIREWALL: comment-derived signal(s) "
        f"{sorted(_leaked)} entered the fraud FEATURE_ORDER. The comment "
        "classifier is a rule set with an unmeasured error rate; its weight has "
        "never been fitted against real fraud outcomes, and a high "
        "generic-comment rate is not fraud. See app/features/comments.py."
    )


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
