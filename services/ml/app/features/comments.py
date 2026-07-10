"""Rule-based comment-quality classification.

Assigns a coarse quality bucket (genuine / generic / emoji-only / duplicate) to
individual comment text. The commenter co-occurrence graph that coordination
detection needs is built separately, and sparsely, in :mod:`app.graph.cocomment`.
"""

from __future__ import annotations

import re
from collections import Counter

from app.schemas import CommentLabel

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
