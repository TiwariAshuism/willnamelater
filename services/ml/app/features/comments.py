"""Rule-based comment-quality classification. THIS IS NOT A MODEL. Read this.

What this actually is
---------------------
An 18-phrase **English** frozenset, an emoji regex, and an exact-duplicate check.
Nothing here was trained, and nothing here was evaluated against labels. It
assigns a coarse bucket (genuine / generic / emoji-only / duplicate) to one
comment's text. The commenter co-occurrence graph that coordination detection
needs is built separately, and sparsely, in :mod:`app.graph.cocomment`.

Consequences, all of which the caller must respect:

1. **It has a known, unmeasured, culturally skewed error rate.** "bahut sundar",
   "semma", "muito bom", "arre wah" are genuine, engaged comments and this code
   files them under ``genuine`` only by accident (they are not in the English
   phrase list) while "nice" from a real fan is filed under ``generic``. Hinglish,
   Tamil and Portuguese comment sections are systematically mislabelled. We do
   not know the error rate, so we may not print a percentage as though we did.

2. **A high generic-comment rate is NOT fraud.** Fan accounts, meme pages and
   large teen audiences produce oceans of genuine "🔥🔥🔥" and "so cute". Those are
   real humans, really engaging. This signal cannot distinguish them from a
   comment pod, and any customer-facing copy that implies otherwise is a lie.

3. **Rates are suppressed below** :data:`MIN_RATE_SAMPLE` **comments** and always
   carry their denominator (see :func:`summarize`). A ratio over 9 comments is
   noise with a decimal point on it. Nothing here may be extrapolated to the
   account: these are the comments we were handed, not a random sample of the
   account's lifetime comments.

FIREWALL — do not wire this into the fraud score
------------------------------------------------
The fraud feature vector (``training.features.FEATURE_ORDER``) once carried
``bot_comment_rate``. It was a **fake**: it was aliased to the clique membership
fraction, a bit-for-bit duplicate, and no comment's text was ever read. It has
been deleted. The obvious next move — fill the hole with *this* classifier's
output — is the trap, and it is forbidden.

This module's output gets its own key, :data:`GENERIC_COMMENT_RATE_KEY`, and that
key MUST NOT enter the fraud feature vector, the 0-100 composite, or any weighted
blend **until its weight has been fitted against real fraud outcomes** (resolved
dispute labels). Hand-picking a weight — "it feels like 0.15 of fraud" — invents a
customer-facing number out of a hunch; this project already deleted an uncited
engagement curve for exactly that sin. The guard that enforces this lives in
:mod:`app.features.fraud_vector`.
"""

from __future__ import annotations

import re
from collections import Counter
from dataclasses import dataclass

from app.schemas import CommentLabel

#: The key this classifier's rate is stored under, if it is ever stored at all.
#: It is deliberately NOT ``bot_comment_rate`` (the deleted fake) and deliberately
#: NOT any name in ``training.features.FEATURE_ORDER``. QUARANTINED: see the
#: firewall note above and ``app.features.fraud_vector``.
GENERIC_COMMENT_RATE_KEY = "generic_comment_rate_v1"

#: Below this many classified comments no rate is reported at all — raw counts
#: and an explicit "insufficient sample" marker are returned instead. A ratio
#: needs a denominator big enough to mean something; this is that floor.
MIN_RATE_SAMPLE = 50

# Broad emoji / pictograph / symbol ranges. A comment that is only these plus
# whitespace and punctuation carries no linguistic content.
_EMOJI_RE = re.compile(
    "[\U0001f000-\U0001faff\U00002600-\U000027bf\U0001f1e6-\U0001f1ff"
    "\U00002190-\U000021ff\U00002b00-\U00002bff️‍]+"
)
_NON_CONTENT_RE = re.compile(r"[\s\W_]+", re.UNICODE)

# Short, contentless phrases that appear identically across creators. Matched
# against the normalized (lowercased, punctuation-stripped) comment.
_GENERIC_PHRASES = frozenset(
    {
        "nice",
        "nice post",
        "nice pic",
        "cool",
        "wow",
        "love it",
        "love this",
        "so cute",
        "amazing",
        "awesome",
        "great",
        "great post",
        "beautiful",
        "perfect",
        "follow me",
        "check my profile",
        "dm me",
        "first",
    }
)

# A normalized comment at or below this word count with no distinctive tokens is
# treated as generic filler.
_GENERIC_MAX_WORDS = 3


def normalize(text: str) -> str:
    """Lowercase and collapse punctuation/whitespace to single spaces."""
    return _NON_CONTENT_RE.sub(" ", text).strip().lower()


def strip_emoji(text: str) -> str:
    return _EMOJI_RE.sub("", text)


def is_emoji_only(text: str) -> bool:
    """True when removing emoji and punctuation leaves nothing."""
    if not text.strip():
        return False
    residue = _NON_CONTENT_RE.sub("", strip_emoji(text))
    return residue == ""


def classify_comment(
    text: str, duplicate_norms: set[str]
) -> tuple[CommentLabel, float, list[str]]:
    """Assign a quality label to one comment via ordered heuristics.

    ``duplicate_norms`` holds normalized forms that occur more than once across
    the batch. Order matters: duplication is the strongest low-quality signal,
    then emoji-only, then generic filler; anything else is treated as genuine.
    Confidence reflects how cleanly the rule fired, and is deliberately capped
    below 1.0 because these are heuristics, not a validated classifier.
    """
    norm = normalize(text)
    fired: list[str] = []

    if norm and norm in duplicate_norms:
        fired.append("duplicate_text")
        return CommentLabel.duplicate, 0.7, fired

    if is_emoji_only(text):
        fired.append("emoji_only")
        return CommentLabel.emoji_only, 0.8, fired

    words = norm.split()
    if norm in _GENERIC_PHRASES:
        fired.append("generic_phrase")
        return CommentLabel.generic, 0.75, fired
    if 0 < len(words) <= _GENERIC_MAX_WORDS and all(
        w in _GENERIC_PHRASES or len(w) <= 2 for w in words
    ):
        fired.append("short_filler")
        return CommentLabel.generic, 0.55, fired

    return CommentLabel.genuine, 0.5, fired


def duplicate_norms(texts: list[str]) -> set[str]:
    """Normalized forms appearing more than once in the batch."""
    counts = Counter(normalize(t) for t in texts if t.strip())
    return {norm for norm, n in counts.items() if n > 1}


@dataclass(frozen=True)
class CommentSummary:
    """Batch-level counts, and a rate ONLY when the denominator earns one.

    ``low_quality_ratio`` is ``None`` — not 0.0 — whenever the sample is too small
    to support a percentage. Zero is a measurement ("we looked at 200 comments and
    none were generic"); ``None`` is an admission ("we saw 9 comments; we are not
    going to pretend that is a rate"). The raw ``counts`` and ``analyzed`` are
    always populated, so the caller can show something true either way.
    """

    analyzed: int
    counts: dict[str, int]
    low_quality_count: int
    low_quality_ratio: float | None
    sufficient_sample: bool
    detail: str


def summarize(
    labels: list[CommentLabel], posts_sampled: int | None = None
) -> CommentSummary:
    """Reduce a batch of labels to honest counts + a suppressed-or-denominated rate.

    ``posts_sampled`` is carried only so the detail string can name the scope of
    the sample ("212 comments sampled from 6 recent posts"). It is never used to
    extrapolate to the account, because the comments we were handed are not a
    random sample of the account's comments.
    """
    analyzed = len(labels)
    counts = {label.value: 0 for label in CommentLabel}
    for label in labels:
        counts[label.value] += 1
    low_quality = analyzed - counts[CommentLabel.genuine.value]

    scope = f"{analyzed} comments"
    if posts_sampled is not None:
        scope += f" sampled from {posts_sampled} recent posts"

    if analyzed < MIN_RATE_SAMPLE:
        return CommentSummary(
            analyzed=analyzed,
            counts=counts,
            low_quality_count=low_quality,
            # NOT 0.0. There is no rate here to report.
            low_quality_ratio=None,
            sufficient_sample=False,
            detail=(
                f"Insufficient sample: {low_quality} of {scope} matched a "
                f"low-quality rule. No rate is reported below "
                f"{MIN_RATE_SAMPLE} comments."
            ),
        )

    ratio = low_quality / analyzed
    return CommentSummary(
        analyzed=analyzed,
        counts=counts,
        low_quality_count=low_quality,
        low_quality_ratio=round(ratio, 6),
        sufficient_sample=True,
        detail=(
            f"{ratio:.0%} of {scope} matched a low-quality rule "
            f"({low_quality}/{analyzed}). Rule-based, not a trained classifier; "
            f"error rate unmeasured and biased against non-English comments. "
            f"Describes these comments only — not the account. A high rate is "
            f"not evidence of fraud."
        ),
    )
