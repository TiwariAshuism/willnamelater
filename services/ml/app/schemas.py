"""Wire contract between the Go ``internal/ml`` module and this service.

These pydantic v2 models are the single source of truth for request and
response shapes. They use strict typing and forbid unknown request fields so a
drift between the Go caller and this service surfaces as a 422 rather than a
silently ignored field.

Every scoring response carries three honesty markers that must never be
dropped: a ``confidence`` in [0, 1], a ``model_version`` string, and an
``estimate`` flag that is always true while the service is in its cold-start
(unsupervised) state.
"""

from __future__ import annotations

from datetime import datetime
from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field

# ---------------------------------------------------------------------------
# Shared enums / value objects
# ---------------------------------------------------------------------------


class Platform(StrEnum):
    """Source platform of the audited account."""

    instagram = "instagram"
    youtube = "youtube"
    tiktok = "tiktok"
    x = "x"
    facebook = "facebook"
    linkedin = "linkedin"


class SignalContribution(BaseModel):
    """One explainable signal and how much it moved the composite score.

    ``value`` is the raw signal strength in [0, 1]; ``weighted`` is that value
    multiplied by ``weight`` and is the amount actually contributed to the
    composite. Exposing both lets the caller show a per-signal breakdown
    instead of an opaque number.
    """

    model_config = ConfigDict(extra="forbid")

    name: str
    value: float = Field(ge=0.0, le=1.0)
    weight: float = Field(ge=0.0, le=1.0)
    weighted: float = Field(ge=0.0, le=1.0)
    detail: str


class _StrictRequest(BaseModel):
    """Base for inbound payloads: strict parsing, no unexpected fields."""

    model_config = ConfigDict(strict=True, extra="forbid")


# ---------------------------------------------------------------------------
# Fraud scoring
# ---------------------------------------------------------------------------


class FollowerPoint(_StrictRequest):
    """A single observation in an account's follower-count time series."""

    # Datetimes arrive as ISO strings over JSON, so strict parsing is relaxed
    # for them while numeric fields stay strict (no float/bool coercion to int).
    timestamp: datetime = Field(strict=False)
    count: int = Field(ge=0)


class PostMetrics(_StrictRequest):
    """Public engagement counters for one post.

    ``post_id`` is the join key: a :class:`CommentEvent` carries the same id, so
    comments can be attributed to the post they were left on. Without it no
    per-post coordination feature (co-commenter edges, per-video cliques) is
    recoverable — see ``product/research/fraud-detection-signals.md`` §6.
    """

    post_id: str = Field(min_length=1)
    timestamp: datetime = Field(strict=False)
    likes: int = Field(ge=0)
    comments: int = Field(ge=0)
    views: int | None = Field(default=None, ge=0)


class EngagementBenchmarkPoint(_StrictRequest):
    """One (follower-threshold, expected-rate) knot of a benchmark curve."""

    follower_threshold: int = Field(gt=0)
    expected_rate: float = Field(gt=0.0, le=1.0)


class EngagementBenchmark(_StrictRequest):
    """A sourced expected-engagement curve supplied by the caller.

    The ML service does not own engagement benchmarks: the Go ``scoring`` module
    reads them from the versioned ``benchmark`` table and passes them in with a
    provenance label. This exists precisely so this service never again ships an
    uncited vendor-blog curve as a customer-facing anchor (research §8). When no
    benchmark is supplied the engagement-deviation signal contributes nothing
    rather than inventing one.
    """

    curve: list[EngagementBenchmarkPoint] = Field(min_length=1)
    floor: float = Field(gt=0.0, le=1.0)
    source: str = Field(min_length=1)


class AccountSnapshot(_StrictRequest):
    """Point-in-time account totals."""

    handle: str = Field(min_length=1)
    platform: Platform = Field(strict=False)
    follower_count: int = Field(ge=0)
    following_count: int = Field(ge=0)


class FraudScoreRequest(_StrictRequest):
    """Everything the fraud scorer needs, drawn entirely from the request.

    No history is loaded from any store: the follower series and posts in this
    payload are the only data the per-call models see.
    """

    account: AccountSnapshot
    follower_series: list[FollowerPoint] = Field(default_factory=list)
    posts: list[PostMetrics] = Field(default_factory=list)
    # Optional sourced benchmark for the engagement-deviation signal. Absent =>
    # that signal is skipped (contributes 0), never anchored to a guessed curve.
    engagement_benchmark: EngagementBenchmark | None = None


class FraudScoreResponse(BaseModel):
    """Authenticity risk estimate for one account.

    ``score`` runs 0-100 where higher means *more likely inauthentic*. It is an
    estimate, never a measured fake-follower percentage.
    """

    model_config = ConfigDict(extra="forbid")

    score: float = Field(ge=0.0, le=100.0)
    confidence: float = Field(ge=0.0, le=1.0)
    model_version: str
    estimate: bool = True
    signals: list[SignalContribution]
    flags: list[str]
    generated_at: datetime


# ---------------------------------------------------------------------------
# Engagement-pod detection
# ---------------------------------------------------------------------------


class CommentEvent(_StrictRequest):
    """One commenter appearing on one post.

    ``text`` is carried so future *verified* text signals (near-duplicate
    detection, §4.3) can reach pod detection through the same payload. The
    current clique model does not read it — it joins purely on ``post_id`` and
    ``commenter`` — so the field is a wire-contract slot, not a live input.
    """

    post_id: str = Field(min_length=1)
    commenter: str = Field(min_length=1)
    text: str = ""
    timestamp: datetime = Field(strict=False)


class PodsDetectRequest(_StrictRequest):
    """Comment events plus the parameters of the co-commenter clique model.

    The co-commenter graph weights each edge by the *number of shared posts*
    between two commenters (arXiv 2311.05791), so no time window is used;
    ``min_shared_posts`` prunes weak edges and ``min_pod_size`` is the clique
    size that counts as coordination (the research signal is cliques of ≥ 5).
    """

    events: list[CommentEvent] = Field(default_factory=list)
    min_pod_size: int = Field(default=5, ge=2)
    min_shared_posts: int = Field(default=2, ge=1)


class Pod(BaseModel):
    """A maximal clique of commenters who co-comment on many shared posts."""

    model_config = ConfigDict(extra="forbid")

    members: list[str]
    size: int = Field(ge=2)
    cohesion: float = Field(ge=0.0, le=1.0)
    confidence: float = Field(ge=0.0, le=1.0)


class PodsDetectResponse(BaseModel):
    """Coordination estimate for one channel's commenter graph.

    ``clique_count`` (maximal cliques of size ≥ ``min_pod_size``) is the primary
    signal; ``clique_membership_fraction`` is secondary. ``partial`` is true when
    the graph had to be reduced to stay inside the compute budget, in which case
    ``clique_count`` is a lower bound. The honesty markers (``confidence``,
    ``model_version``, ``estimate``) and per-signal ``signals`` are always set.
    """

    model_config = ConfigDict(extra="forbid")

    pods: list[Pod]
    clique_count: int = Field(ge=0)
    clique_membership_fraction: float = Field(ge=0.0, le=1.0)
    commenters_analyzed: int = Field(ge=0)
    partial: bool = False
    signals: list[SignalContribution]
    confidence: float = Field(ge=0.0, le=1.0)
    model_version: str
    estimate: bool = True
    generated_at: datetime


# ---------------------------------------------------------------------------
# Comment-quality classification
# ---------------------------------------------------------------------------


class CommentLabel(StrEnum):
    """Heuristic quality bucket for a single comment."""

    genuine = "genuine"
    generic = "generic"
    emoji_only = "emoji_only"
    duplicate = "duplicate"


class CommentItem(_StrictRequest):
    id: str = Field(min_length=1)
    text: str


class CommentsClassifyRequest(_StrictRequest):
    comments: list[CommentItem] = Field(default_factory=list)


class CommentClassification(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: str
    label: CommentLabel
    confidence: float = Field(ge=0.0, le=1.0)
    signals: list[str]


class CommentsClassifyResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    classifications: list[CommentClassification]
    low_quality_ratio: float = Field(ge=0.0, le=1.0)
    confidence: float = Field(ge=0.0, le=1.0)
    model_version: str
    estimate: bool = True
    generated_at: datetime


# ---------------------------------------------------------------------------
# Health
# ---------------------------------------------------------------------------


class HealthResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    status: str
    model_version: str


class ErrorResponse(BaseModel):
    """Mirrors the Go ``errs`` envelope: a stable code plus a safe message."""

    model_config = ConfigDict(extra="forbid")

    code: str
    message: str
